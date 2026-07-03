package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/tools"
	"github.com/Lioooooo123/liora/internal/trace"
)

type Status string

const (
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusWaitingUser Status = "waiting_user"
)

type Result struct {
	Status  Status
	Summary string
	Diff    string
}

type Agent struct {
	workspace   *tools.Workspace
	recorder    trace.Recorder
	lifecycle   ToolLifecycleSink
	lifecycleMu sync.Mutex
	mcp         MCPExecutor
	skills      SkillReader
	shell       ShellExecutor
	checker     permission.Checker
	outputs     ToolOutputSink
	todos       TodoExecutor
	tasks       TaskExecutor
	traceSeq    int
	replay      CompletedToolLookup
}

type MCPExecutor interface {
	Call(ctx context.Context, server string, tool string, args map[string]any) (string, error)
}

type ShellExecutor interface {
	Run(ctx context.Context, workspace string, command string) (tools.ShellResult, error)
}

type SkillReader interface {
	ReadSkill(workspaceRoot string, name string, startLine int, lineCount int) (string, error)
}

type ToolOutputSink interface {
	PersistToolOutput(ctx context.Context, call llm.ToolCall, output string) (string, error)
}

type ToolLifecycleEvent struct {
	Phase          string
	Tool           string
	ToolCallID     string
	ToolResultID   string
	Input          string
	Output         string
	OutputPath     string
	Status         string
	BatchID        string
	BatchSize      int
	AccessMode     string
	AccessResource string
	AccessArgument string
	DurationMS     int64
	Truncated      bool
}

type ToolLifecycleSink interface {
	RecordToolLifecycle(event ToolLifecycleEvent)
}

type CompletedToolResult struct {
	Output string
}

type CompletedToolLookup func(ctx context.Context, toolCallID string) (CompletedToolResult, bool, error)

type Todo struct {
	ID           string `json:"id,omitempty"`
	SourceTaskID string `json:"source_task_id,omitempty"`
	Content      string `json:"content"`
	Status       string `json:"status,omitempty"`
	Priority     string `json:"priority,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type TodoExecutor interface {
	WriteTodos(ctx context.Context, todos []Todo) ([]Todo, error)
	ReadTodos(ctx context.Context) ([]Todo, error)
}

type TaskToolScope struct {
	Paths           []string
	NetworkHosts    []string
	MCPServers      []string
	MCPTools        []string
	ApprovalActions []string
}

type TaskRequest struct {
	Prompt       string
	SubagentName string
	Role         string
	Scope        TaskToolScope
}

type TaskResult struct {
	TaskID string
	Status string
}

type TaskOutputRequest struct {
	TaskID           string
	WaitMilliseconds int
	Limit            int
}

type TaskOutputResult struct {
	TaskID string
	Status string
	Output string
}

type TaskStopRequest struct {
	TaskID string
	Reason string
}

type TaskStopResult struct {
	TaskID string
	Status string
}

type TaskExecutor interface {
	StartTask(ctx context.Context, request TaskRequest) (TaskResult, error)
	ReadTaskOutput(ctx context.Context, request TaskOutputRequest) (TaskOutputResult, error)
	StopTask(ctx context.Context, request TaskStopRequest) (TaskStopResult, error)
}

func New(workspace *tools.Workspace, recorder trace.Recorder) *Agent {
	return &Agent{workspace: workspace, recorder: recorder}
}

func (a *Agent) SetMCP(executor MCPExecutor) {
	a.mcp = executor
}

func (a *Agent) SetSkillReader(reader SkillReader) {
	a.skills = reader
}

func (a *Agent) SetShellExecutor(executor ShellExecutor) {
	a.shell = executor
}

func (a *Agent) SetPermissionChecker(checker permission.Checker) {
	a.checker = checker
}

func (a *Agent) SetToolOutputSink(sink ToolOutputSink) {
	a.outputs = sink
}

func (a *Agent) SetToolLifecycleSink(sink ToolLifecycleSink) {
	a.lifecycle = sink
}

func (a *Agent) SetTodoExecutor(executor TodoExecutor) {
	a.todos = executor
}

func (a *Agent) SetTaskExecutor(executor TaskExecutor) {
	a.tasks = executor
}

func (a *Agent) SetCompletedToolLookup(lookup CompletedToolLookup) {
	a.replay = lookup
}

func (a *Agent) Run(ctx context.Context, prompt string) (Result, error) {
	steps := parseSteps(prompt)
	if len(steps) == 0 {
		return Result{Status: StatusFailed}, fmt.Errorf("no executable steps found")
	}

	var latestDiff string
	baseTraceSeq := a.traceSeq
	for i, step := range steps {
		select {
		case <-ctx.Done():
			return Result{Status: StatusFailed}, ctx.Err()
		default:
		}
		stepTraceSeq := baseTraceSeq + i + 1
		toolCallID, toolResultID := fallbackToolIDs(stepTraceSeq)
		baseLifecycle := a.fallbackLifecycleEvent(step, toolCallID, toolResultID)
		a.recordToolLifecycle(withLifecyclePhase(baseLifecycle, "prepare", "pending"))
		if a.completed(ctx, toolCallID) {
			a.recordToolLifecycle(withLifecyclePhase(baseLifecycle, "finalize", "replayed"))
			a.rememberTraceSeq(stepTraceSeq)
			continue
		}
		if err := a.checkPermission(ctx, step, toolCallID); err != nil {
			a.recordToolLifecycle(withLifecyclePhase(baseLifecycle, "authorize", string(StatusWaitingUser)))
			return Result{
				Status:  StatusWaitingUser,
				Summary: fmt.Sprintf("waiting for approval at step %d/%d: %s", i+1, len(steps), step.Raw),
				Diff:    latestDiff,
			}, err
		}
		a.recordToolLifecycle(withLifecyclePhase(baseLifecycle, "authorize", "ok"))
		a.recordToolLifecycle(withLifecyclePhase(baseLifecycle, "execute", "running"))
		output, diff, err := a.execute(ctx, step)
		if diff != "" {
			latestDiff = diff
		}
		status := trace.StatusOK
		if err != nil {
			status = trace.StatusError
			output = err.Error() + "\n" + output
		}
		a.record(trace.Event{
			Tool:         step.Tool,
			Input:        strings.Join(step.Args, " "),
			Output:       output,
			Status:       status,
			ToolCallID:   toolCallID,
			ToolResultID: toolResultID,
		})
		finalizeStatus := string(status)
		finalize := withLifecyclePhase(baseLifecycle, "finalize", finalizeStatus)
		finalize.Output = output
		finalize.OutputPath = toolOutputPath(output)
		finalize.Truncated = finalize.OutputPath != ""
		a.recordToolLifecycle(finalize)
		a.rememberTraceSeq(stepTraceSeq)
		if err != nil {
			return Result{
				Status:  StatusFailed,
				Summary: fmt.Sprintf("failed at step %d/%d: %s", i+1, len(steps), step.Raw),
				Diff:    latestDiff,
			}, err
		}
	}

	if latestDiff == "" {
		diff, err := a.workspace.GitDiff()
		if err == nil {
			latestDiff = diff
		}
	}

	return Result{
		Status:  StatusCompleted,
		Summary: completionSummary(len(steps)),
		Diff:    latestDiff,
	}, nil
}

func (a *Agent) recordToolLifecycle(event ToolLifecycleEvent) {
	if a.lifecycle == nil {
		return
	}
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	a.lifecycle.RecordToolLifecycle(event)
}

func (a *Agent) fallbackLifecycleEvent(step Step, toolCallID string, toolResultID string) ToolLifecycleEvent {
	input := strings.Join(step.Args, " ")
	event := ToolLifecycleEvent{
		Tool:         step.Tool,
		ToolCallID:   toolCallID,
		ToolResultID: toolResultID,
		Input:        input,
		BatchID:      "fallback-step",
		BatchSize:    1,
	}
	return event.withAccess(fallbackStepAccess(step))
}

func withLifecyclePhase(event ToolLifecycleEvent, phase string, status string) ToolLifecycleEvent {
	event.Phase = phase
	event.Status = status
	return event
}

func (a *Agent) checkPermission(ctx context.Context, step Step, toolCallID string) error {
	if a.checker == nil {
		return nil
	}
	return a.checker.Check(ctx, permission.Request{
		Tool:       step.Tool,
		ToolCallID: toolCallID,
		Input:      strings.Join(step.Args, " "),
	})
}

func (a *Agent) completed(ctx context.Context, toolCallID string) bool {
	if a.replay == nil {
		return false
	}
	_, ok, err := a.replay(ctx, toolCallID)
	return err == nil && ok
}

func (a *Agent) execute(ctx context.Context, step Step) (output string, diff string, err error) {
	switch step.Tool {
	case "list":
		path := "."
		if len(step.Args) > 1 {
			return "", "", fmt.Errorf("list expects at most 1 argument")
		}
		if len(step.Args) == 1 {
			path = step.Args[0]
		}
		entries, err := a.workspace.List(path)
		if err != nil {
			return "", "", err
		}
		return strings.Join(entries, "\n"), "", nil
	case "tree":
		path := "."
		depth := 2
		if len(step.Args) > 0 {
			path = step.Args[0]
		}
		if len(step.Args) > 1 {
			parsed, err := strconv.Atoi(step.Args[1])
			if err != nil {
				return "", "", fmt.Errorf("tree depth must be a number")
			}
			depth = parsed
		}
		entries, err := a.workspace.Tree(path, depth)
		if err != nil {
			return "", "", err
		}
		return strings.Join(entries, "\n"), "", nil
	case "glob":
		if len(step.Args) < 1 {
			return "", "", fmt.Errorf("glob expects a pattern")
		}
		root := "."
		if len(step.Args) > 1 {
			root = step.Args[1]
		}
		matches, err := a.workspace.Glob(step.Args[0], root, true)
		if err != nil {
			return "", "", err
		}
		return strings.Join(matches, "\n"), "", nil
	case "stat":
		if len(step.Args) != 1 {
			return "", "", fmt.Errorf("stat expects 1 argument")
		}
		info, err := a.workspace.Stat(step.Args[0])
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("%s size=%d mode=%s dir=%t", info.Path, info.Size, info.Mode, info.IsDir), "", nil
	case "read":
		if len(step.Args) < 1 || len(step.Args) > 3 {
			return "", "", fmt.Errorf("read expects path [start_line] [line_count]")
		}
		startLine := 1
		lineCount := 1000
		var err error
		if len(step.Args) > 1 {
			startLine, err = strconv.Atoi(step.Args[1])
			if err != nil {
				return "", "", fmt.Errorf("read start_line must be a number")
			}
		}
		if len(step.Args) > 2 {
			lineCount, err = strconv.Atoi(step.Args[2])
			if err != nil {
				return "", "", fmt.Errorf("read line_count must be a number")
			}
		}
		content, err := a.workspace.ReadFileRange(step.Args[0], startLine, lineCount)
		return content, "", err
	case "document":
		if len(step.Args) < 1 || len(step.Args) > 3 {
			return "", "", fmt.Errorf("document expects path [start_line] [line_count]")
		}
		startLine := 1
		lineCount := 1000
		var err error
		if len(step.Args) > 1 {
			startLine, err = strconv.Atoi(step.Args[1])
			if err != nil {
				return "", "", fmt.Errorf("document start_line must be a number")
			}
		}
		if len(step.Args) > 2 {
			lineCount, err = strconv.Atoi(step.Args[2])
			if err != nil {
				return "", "", fmt.Errorf("document line_count must be a number")
			}
		}
		content, err := a.workspace.ReadDocumentRange(step.Args[0], startLine, lineCount)
		return content, "", err
	case "skill":
		if a.skills == nil {
			return "", "", fmt.Errorf("no skill reader configured")
		}
		if len(step.Args) < 1 || len(step.Args) > 3 {
			return "", "", fmt.Errorf("skill expects name [start_line] [line_count]")
		}
		startLine := 1
		lineCount := 1000
		var err error
		if len(step.Args) > 1 {
			startLine, err = strconv.Atoi(step.Args[1])
			if err != nil {
				return "", "", fmt.Errorf("skill start_line must be a number")
			}
		}
		if len(step.Args) > 2 {
			lineCount, err = strconv.Atoi(step.Args[2])
			if err != nil {
				return "", "", fmt.Errorf("skill line_count must be a number")
			}
		}
		content, err := a.skills.ReadSkill(a.workspace.Root(), step.Args[0], startLine, lineCount)
		return content, "", err
	case "search":
		if len(step.Args) < 1 {
			return "", "", fmt.Errorf("search expects a query")
		}
		matches, err := a.workspace.Search(strings.Join(step.Args, " "))
		if err != nil {
			return "", "", err
		}
		var builder strings.Builder
		for _, match := range matches {
			builder.WriteString(match.Path)
			builder.WriteString(":")
			builder.WriteString(strconv.Itoa(match.Line))
			builder.WriteString(": ")
			builder.WriteString(match.Content)
			builder.WriteString("\n")
		}
		return builder.String(), "", nil
	case "write":
		if len(step.Args) < 2 {
			return "", "", fmt.Errorf("write expects a path and content")
		}
		err := a.workspace.WriteFile(step.Args[0], strings.Join(step.Args[1:], " ")+"\n")
		return "written " + step.Args[0], "", err
	case "append":
		if len(step.Args) < 2 {
			return "", "", fmt.Errorf("append expects a path and content")
		}
		err := a.workspace.AppendFile(step.Args[0], strings.Join(step.Args[1:], " ")+"\n")
		return "appended " + step.Args[0], "", err
	case "mkdir":
		if len(step.Args) != 1 {
			return "", "", fmt.Errorf("mkdir expects 1 argument")
		}
		err := a.workspace.Mkdir(step.Args[0])
		return "created directory " + step.Args[0], "", err
	case "delete":
		if len(step.Args) != 1 {
			return "", "", fmt.Errorf("delete expects 1 argument")
		}
		err := a.workspace.Delete(step.Args[0])
		return "deleted " + step.Args[0], "", err
	case "edit":
		if len(step.Args) < 3 {
			return "", "", fmt.Errorf("edit expects a path, old text and new text")
		}
		replaceAll := len(step.Args) > 3 && step.Args[len(step.Args)-1] == "all"
		newArgsEnd := len(step.Args)
		if replaceAll {
			newArgsEnd--
		}
		err := a.workspace.Edit(step.Args[0], step.Args[1], strings.Join(step.Args[2:newArgsEnd], " "), replaceAll)
		diff, _ := a.workspace.GitDiff()
		return "edited " + step.Args[0], diff, err
	case "replace":
		if len(step.Args) < 3 {
			return "", "", fmt.Errorf("replace expects a path, old text and new text")
		}
		err := a.workspace.Replace(step.Args[0], step.Args[1], strings.Join(step.Args[2:], " "))
		diff, _ := a.workspace.GitDiff()
		return "replaced " + step.Args[0], diff, err
	case "run":
		if len(step.Args) < 1 {
			return "", "", fmt.Errorf("run expects a shell command")
		}
		command := strings.Join(step.Args, " ")
		result, err := a.runShell(ctx, command)
		output := result.Stdout + result.Stderr
		if err != nil {
			return output, "", err
		}
		return output, "", nil
	case "diff":
		diff, err := a.workspace.GitDiff()
		return diff, diff, err
	case "mcp":
		if a.mcp == nil {
			return "", "", fmt.Errorf("no MCP servers configured")
		}
		if len(step.Args) < 2 {
			return "", "", fmt.Errorf("mcp expects a server and tool name")
		}
		args := map[string]any{}
		if len(step.Args) > 2 {
			if err := json.Unmarshal([]byte(strings.Join(step.Args[2:], " ")), &args); err != nil {
				return "", "", fmt.Errorf("invalid MCP arguments JSON: %w", err)
			}
		}
		output, err := a.mcp.Call(ctx, step.Args[0], step.Args[1], args)
		return output, "", err
	case "todo_write":
		args := map[string]any{}
		if len(step.Args) > 0 {
			if err := json.Unmarshal([]byte(strings.Join(step.Args, " ")), &args); err != nil {
				return "", "", fmt.Errorf("invalid todo_write JSON: %w", err)
			}
		}
		output, err := a.executeTodoWrite(ctx, args)
		return output, "", err
	case "todo_read":
		if len(step.Args) > 0 {
			return "", "", fmt.Errorf("todo_read expects no arguments")
		}
		output, err := a.executeTodoRead(ctx)
		return output, "", err
	default:
		return "", "", fmt.Errorf("unknown tool %q", step.Tool)
	}
}

func (a *Agent) nextFallbackToolIDs() (string, string) {
	a.traceSeq++
	return fallbackToolIDs(a.traceSeq)
}

func (a *Agent) rememberTraceSeq(seq int) {
	if seq > a.traceSeq {
		a.traceSeq = seq
	}
}

func fallbackToolIDs(step int) (string, string) {
	callID := fmt.Sprintf("fallback-step-%d", step)
	return callID, callID + "-result"
}

func (a *Agent) runShell(ctx context.Context, command string) (tools.ShellResult, error) {
	if a.shell != nil {
		return a.shell.Run(ctx, a.workspace.Root(), command)
	}
	return a.workspace.RunShell(command)
}

func (a *Agent) record(event trace.Event) {
	if a.recorder != nil {
		a.recorder.Record(event)
	}
}

func completionSummary(stepCount int) string {
	if stepCount == 1 {
		return "completed 1 step"
	}
	return fmt.Sprintf("completed %d steps", stepCount)
}
