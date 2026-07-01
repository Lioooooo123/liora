package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/trace"
)

const (
	defaultMaxTurns        = 25
	maxReadOnlyConcurrency = 10
)

// ToolLoop runs the observe→act→observe execution model: it calls the model with
// native structured tool schemas, dispatches the requested tools, feeds each
// tool result back as a message, and repeats until the model stops requesting
// tools. Termination is driven by "did the model request tools" rather than the
// provider stop_reason, matching the Claude Code and Kimi Code reference loops.
type ToolLoop struct {
	agent     *Agent
	generator llm.ToolCaller
	maxTurns  int
	onPlan    func(steps string)
	onReplan  func(attempt int, reason string)
}

// LoopOptions configures a ToolLoop run. The callbacks mirror the planner path
// so the task runner keeps emitting the same plan_ready / replanning events.
type LoopOptions struct {
	MaxTurns int
	OnPlan   func(steps string)
	OnReplan func(attempt int, reason string)
}

// NewToolLoop wraps a configured Agent (workspace, recorder, sandbox, permission
// checker and MCP executor already set) so the loop reuses the same tool
// semantics as the planner path.
func NewToolLoop(a *Agent, generator llm.ToolCaller, options LoopOptions) *ToolLoop {
	maxTurns := options.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}
	return &ToolLoop{
		agent:     a,
		generator: generator,
		maxTurns:  maxTurns,
		onPlan:    options.OnPlan,
		onReplan:  options.OnReplan,
	}
}

type toolOutcome struct {
	call    llm.ToolCall
	output  string
	isError bool
}

func (l *ToolLoop) Run(ctx context.Context, prompt string) (Result, error) {
	if strings.TrimSpace(prompt) == "" {
		return Result{Status: StatusFailed}, fmt.Errorf("prompt is required")
	}
	schemas := loopToolSchemas()
	messages := []llm.Message{
		{Role: "system", Content: loopSystemPrompt()},
		{Role: "user", Content: prompt},
	}

	executed := 0
	replanAttempts := 0
	for turn := 0; ; turn++ {
		select {
		case <-ctx.Done():
			return Result{Status: StatusFailed, Diff: l.currentDiff()}, ctx.Err()
		default:
		}
		if turn >= l.maxTurns {
			return Result{
				Status:  StatusFailed,
				Summary: fmt.Sprintf("stopped after %d turns without completing", l.maxTurns),
				Diff:    l.currentDiff(),
			}, fmt.Errorf("tool loop exceeded %d turns", l.maxTurns)
		}

		completion, err := l.generator.GenerateWithTools(ctx, messages, schemas)
		if err != nil {
			return Result{Status: StatusFailed, Diff: l.currentDiff()}, err
		}
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   completion.Content,
			ToolCalls: completion.ToolCalls,
		})

		if len(completion.ToolCalls) == 0 {
			return Result{
				Status:  StatusCompleted,
				Summary: completionSummaryForLoop(completion.Content, executed),
				Diff:    l.currentDiff(),
			}, nil
		}

		if turn == 0 && l.onPlan != nil {
			l.onPlan(renderToolCalls(completion.ToolCalls))
		}

		if waiting, err := l.checkTurnPermissions(ctx, completion.ToolCalls); err != nil {
			return waiting, err
		}

		outcomes := l.dispatch(ctx, completion.ToolCalls)
		executed += len(outcomes)

		anyError := false
		var firstErrReason string
		for _, outcome := range outcomes {
			status := trace.StatusOK
			if outcome.isError {
				status = trace.StatusError
				if !anyError {
					firstErrReason = outcome.call.Name + ": " + firstLine(outcome.output)
				}
				anyError = true
			}
			l.agent.record(trace.Event{
				Tool:   outcome.call.Name,
				Input:  toolInput(outcome.call),
				Output: outcome.output,
				Status: status,
			})
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    outcome.output,
				ToolCallID: outcome.call.ID,
				ToolError:  outcome.isError,
			})
		}

		if anyError && l.onReplan != nil {
			replanAttempts++
			l.onReplan(replanAttempts, firstErrReason)
		}
	}
}

// checkTurnPermissions validates every requested tool before any of them run. A
// RequiredError bubbles up as StatusWaitingUser without executing tools, so the
// approval flow matches the planner path.
func (l *ToolLoop) checkTurnPermissions(ctx context.Context, calls []llm.ToolCall) (Result, error) {
	if l.agent.checker == nil {
		return Result{}, nil
	}
	for _, call := range calls {
		err := l.agent.checker.Check(ctx, permission.Request{
			Tool:  call.Name,
			Input: toolInput(call),
		})
		if err != nil {
			return Result{
				Status:  StatusWaitingUser,
				Summary: fmt.Sprintf("waiting for approval: %s %s", call.Name, toolInput(call)),
				Diff:    l.currentDiff(),
			}, err
		}
	}
	return Result{}, nil
}

// dispatch runs read-only tools in concurrent batches (cap 10) while keeping
// write/shell/external tools serial, preserving the original call order in the
// returned outcomes.
func (l *ToolLoop) dispatch(ctx context.Context, calls []llm.ToolCall) []toolOutcome {
	outcomes := make([]toolOutcome, len(calls))
	i := 0
	for i < len(calls) {
		if !isReadOnlyTool(calls[i].Name) {
			outcomes[i] = l.dispatchOne(ctx, calls[i])
			i++
			continue
		}
		j := i
		for j < len(calls) && isReadOnlyTool(calls[j].Name) {
			j++
		}
		l.dispatchParallel(ctx, calls[i:j], outcomes[i:j])
		i = j
	}
	return outcomes
}

func (l *ToolLoop) dispatchParallel(ctx context.Context, calls []llm.ToolCall, out []toolOutcome) {
	if len(calls) == 1 {
		out[0] = l.dispatchOne(ctx, calls[0])
		return
	}
	sem := make(chan struct{}, maxReadOnlyConcurrency)
	var wg sync.WaitGroup
	for k := range calls {
		wg.Add(1)
		sem <- struct{}{}
		go func(k int) {
			defer wg.Done()
			defer func() { <-sem }()
			out[k] = l.dispatchOne(ctx, calls[k])
		}(k)
	}
	wg.Wait()
}

func (l *ToolLoop) dispatchOne(ctx context.Context, call llm.ToolCall) toolOutcome {
	args, err := parseToolArgs(call.Arguments)
	if err != nil {
		return toolOutcome{call: call, output: fmt.Sprintf("invalid arguments JSON: %v", err), isError: true}
	}
	output, err := l.executeToolCall(ctx, call.Name, args)
	if err != nil {
		message := err.Error()
		if output != "" {
			message += "\n" + output
		}
		return toolOutcome{call: call, output: l.budgetToolOutput(call, message), isError: true}
	}
	return toolOutcome{call: call, output: l.budgetToolOutput(call, output)}
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	return text
}

func loopSystemPrompt() string {
	return `You are Liora, a local-first coding agent working inside a single workspace.

Use the provided tools to inspect and modify the workspace, then reply with a short summary when the task is done.

Rules:
- Use relative paths only.
- Observe before acting: prefer list, tree, glob, search and read to understand the workspace before editing.
- Use document for .pdf and .docx files; use read for plain text and source code.
- Use skill to read installed Liora skills only when the listed skill metadata is relevant to the task.
- Prefer edit for precise replacements; use write only for new files or full-file rewrites.
- Prefer built-in file tools over shell commands when possible.
- Use mcp only when the request explicitly needs a configured MCP server.
- When a tool fails, read the error, adjust, and try a corrected tool call instead of repeating the same failing call.
- When no further tool calls are needed, stop calling tools and reply with a concise natural-language summary of what you did.
- For greetings or questions that need no tools, reply directly without calling any tool.`
}
