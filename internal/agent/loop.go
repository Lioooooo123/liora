package agent

import (
	"context"
	"fmt"
	"strings"

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
	onDelta   llm.DeltaHandler
}

// LoopOptions configures a ToolLoop run. The callbacks mirror the planner path
// so the task runner keeps emitting the same plan_ready / replanning events.
type LoopOptions struct {
	MaxTurns         int
	OnPlan           func(steps string)
	OnReplan         func(attempt int, reason string)
	OnAssistantDelta llm.DeltaHandler
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
		onDelta:   options.OnAssistantDelta,
	}
}

type toolOutcome struct {
	call          llm.ToolCall
	output        string
	failureOutput string
	isError       bool
	replayed      bool
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
	seenFailures := map[toolFailureSignature]bool{}
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

		completion, err := l.generate(ctx, messages, schemas)
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
		var repeatedFailure string
		for _, outcome := range outcomes {
			status := trace.StatusOK
			if outcome.isError {
				status = trace.StatusError
				if !anyError {
					firstErrReason = outcome.call.Name + ": " + firstLine(outcome.output)
				}
				signature := newToolFailureSignature(outcome.call, outcome.failureOutput)
				if seenFailures[signature] && repeatedFailure == "" {
					repeatedFailure = renderRepeatedFailure(signature)
				}
				seenFailures[signature] = true
				anyError = true
			}
			if !outcome.replayed {
				l.agent.record(trace.Event{
					Tool:         outcome.call.Name,
					Input:        toolInput(outcome.call),
					Output:       outcome.output,
					Status:       status,
					ToolCallID:   outcome.call.ID,
					ToolResultID: toolResultID(outcome.call),
				})
			}
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    outcome.output,
				ToolCallID: outcome.call.ID,
				ToolError:  outcome.isError,
			})
		}

		if repeatedFailure != "" {
			return Result{
				Status:  StatusFailed,
				Summary: repeatedFailure,
				Diff:    l.currentDiff(),
			}, fmt.Errorf("%s", repeatedFailure)
		}

		if anyError && l.onReplan != nil {
			replanAttempts++
			l.onReplan(replanAttempts, firstErrReason)
		}
	}
}

func (l *ToolLoop) generate(ctx context.Context, messages []llm.Message, schemas []llm.ToolSchema) (llm.Completion, error) {
	if l.onDelta != nil {
		if streamer, ok := l.generator.(llm.ToolStreamCaller); ok {
			return streamer.GenerateWithToolsStream(ctx, messages, schemas, l.onDelta)
		}
	}
	return l.generator.GenerateWithTools(ctx, messages, schemas)
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
			Tool:       call.Name,
			ToolCallID: call.ID,
			Input:      toolInput(call),
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

func (l *ToolLoop) dispatch(ctx context.Context, calls []llm.ToolCall) []toolOutcome {
	outcomes := make([]toolOutcome, len(calls))
	for _, batch := range scheduleToolBatches(calls) {
		l.dispatchBatch(ctx, batch, outcomes)
	}
	return outcomes
}

func (l *ToolLoop) dispatchOne(ctx context.Context, call llm.ToolCall) toolOutcome {
	if l.agent.replay != nil {
		if replay, ok, err := l.agent.replay(ctx, call.ID); err == nil && ok {
			return toolOutcome{call: call, output: replay.Output, replayed: true}
		}
	}
	args, err := parseToolArgs(call.Arguments)
	if err != nil {
		message := fmt.Sprintf("invalid arguments JSON: %v", err)
		return toolOutcome{call: call, output: message, failureOutput: message, isError: true}
	}
	output, err := l.executeToolCall(ctx, call.Name, args)
	if err != nil {
		message := err.Error()
		if output != "" {
			message += "\n" + output
		}
		return toolOutcome{call: call, output: l.budgetToolOutput(ctx, call, message), failureOutput: message, isError: true}
	}
	return toolOutcome{call: call, output: l.budgetToolOutput(ctx, call, output)}
}

func toolResultID(call llm.ToolCall) string {
	if strings.TrimSpace(call.ID) == "" {
		return "tool-result"
	}
	return call.ID + "-result"
}
