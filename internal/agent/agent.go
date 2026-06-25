package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Lioooooo123/liora/internal/tools"
	"github.com/Lioooooo123/liora/internal/trace"
)

type Status string

const (
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

type Result struct {
	Status  Status
	Summary string
	Diff    string
}

type Agent struct {
	workspace *tools.Workspace
	recorder  trace.Recorder
	mcp       MCPExecutor
}

type MCPExecutor interface {
	Call(ctx context.Context, server string, tool string, args map[string]any) (string, error)
}

func New(workspace *tools.Workspace, recorder trace.Recorder) *Agent {
	return &Agent{workspace: workspace, recorder: recorder}
}

func (a *Agent) SetMCP(executor MCPExecutor) {
	a.mcp = executor
}

func (a *Agent) Run(ctx context.Context, prompt string) (Result, error) {
	steps := parseSteps(prompt)
	if len(steps) == 0 {
		return Result{Status: StatusFailed}, fmt.Errorf("no executable steps found")
	}

	var latestDiff string
	for i, step := range steps {
		select {
		case <-ctx.Done():
			return Result{Status: StatusFailed}, ctx.Err()
		default:
		}
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
			Tool:   step.Tool,
			Input:  strings.Join(step.Args, " "),
			Output: output,
			Status: status,
		})
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
	case "read":
		if len(step.Args) != 1 {
			return "", "", fmt.Errorf("read expects 1 argument")
		}
		content, err := a.workspace.ReadFile(step.Args[0])
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
		result, err := a.workspace.RunShell(strings.Join(step.Args, " "))
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
	default:
		return "", "", fmt.Errorf("unknown tool %q", step.Tool)
	}
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
