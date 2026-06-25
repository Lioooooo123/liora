package task

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/runtime"
	"github.com/Lioooooo123/liora/internal/sandbox"
	"github.com/Lioooooo123/liora/internal/tools"
	"github.com/Lioooooo123/liora/internal/trace"
)

type Runner struct {
	repo       *Repository
	planner    *llm.Planner
	sandboxRun sandbox.Executor
	patchMode  bool
}

func NewRunner(repo *Repository, planner *llm.Planner) *Runner {
	return &Runner{repo: repo, planner: planner}
}

func (r *Runner) SetSandbox(executor sandbox.Executor) {
	r.sandboxRun = executor
}

func (r *Runner) SetPatchMode(enabled bool) {
	r.patchMode = enabled
}

func (r *Runner) Run(ctx context.Context, taskID string) error {
	task, err := r.repo.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if err := r.repo.UpdateStatus(ctx, task.ID, StatusPlanning); err != nil {
		return err
	}
	_ = r.repo.AppendEvent(ctx, task.ID, EventPlanning, EventPayload{Message: "Planning task"})

	result, err := r.runTask(ctx, task)
	if strings.TrimSpace(result.plannedSteps) != "" {
		_ = r.repo.AppendEvent(ctx, task.ID, EventPlanReady, EventPayload{Steps: result.plannedSteps})
	}
	if r.sandboxRun != nil && containsRunStep(result.plannedSteps) {
		_ = r.repo.AppendEvent(ctx, task.ID, EventSandboxRun, EventPayload{Message: "shell executor: " + sandbox.Label(r.sandboxRun)})
	}
	if strings.TrimSpace(result.answer) != "" {
		_ = r.repo.AppendEvent(ctx, task.ID, EventSummary, EventPayload{Message: result.answer})
	}
	if len(result.events) > 0 {
		if updateErr := r.repo.UpdateStatus(ctx, task.ID, StatusRunning); updateErr != nil && err == nil {
			err = updateErr
		}
		for _, event := range result.events {
			r.appendTraceEvents(ctx, task.ID, event)
		}
	}
	if strings.TrimSpace(result.summary) != "" {
		_ = r.repo.AppendEvent(ctx, task.ID, EventSummary, EventPayload{Message: result.summary})
	}
	if strings.TrimSpace(result.diff) != "" {
		_ = r.repo.AppendEvent(ctx, task.ID, EventDiff, EventPayload{Diff: result.diff})
	}
	if err != nil {
		_ = r.fail(ctx, task.ID, err)
		return err
	}
	if err := r.repo.UpdateStatus(ctx, task.ID, StatusCompleted); err != nil {
		return err
	}
	return r.repo.AppendEvent(ctx, task.ID, EventCompleted, EventPayload{Status: string(StatusCompleted)})
}

func (r *Runner) runTask(ctx context.Context, task Task) (runtimeResult, error) {
	workspaceRoot := task.Workspace
	if r.patchMode {
		tempRoot, cleanup, err := copyWorkspace(task.Workspace)
		if err != nil {
			return runtimeResult{}, err
		}
		defer cleanup()
		workspaceRoot = tempRoot
	}
	if task.Natural {
		turnRuntime, err := runtime.New(workspaceRoot, r.planner)
		if err != nil {
			return runtimeResult{}, err
		}
		turnRuntime.SetSandbox(r.sandboxRun)
		result, err := turnRuntime.Submit(ctx, task.UserInput)
		return runtimeResult{
			answer:       result.Answer,
			plannedSteps: result.PlannedSteps,
			summary:      result.AgentResult.Summary,
			diff:         result.AgentResult.Diff,
			events:       result.Events,
		}, err
	}
	workspace, err := tools.NewWorkspace(workspaceRoot)
	if err != nil {
		return runtimeResult{}, err
	}
	recorder := trace.NewMemoryRecorder()
	runner := agent.New(workspace, recorder)
	if r.sandboxRun != nil {
		runner.SetShellExecutor(r.sandboxRun)
	}
	result, err := runner.Run(ctx, task.UserInput)
	return runtimeResult{
		plannedSteps: task.UserInput,
		summary:      result.Summary,
		diff:         result.Diff,
		events:       recorder.Events(),
	}, err
}

func copyWorkspace(source string) (string, func(), error) {
	tempRoot, err := os.MkdirTemp("", "liora-task-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tempRoot) }
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		if shouldSkipCopy(entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(tempRoot, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return tempRoot, cleanup, nil
}

func shouldSkipCopy(entry os.DirEntry) bool {
	name := entry.Name()
	if name == ".git" || name == "node_modules" || name == "vendor" {
		return true
	}
	return strings.HasPrefix(name, ".liora-task-")
}

func containsRunStep(steps string) bool {
	for _, line := range strings.Split(steps, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "run ") {
			return true
		}
	}
	return false
}

type runtimeResult struct {
	answer       string
	plannedSteps string
	summary      string
	diff         string
	events       []trace.Event
}

func (r *Runner) appendTraceEvents(ctx context.Context, taskID string, event trace.Event) {
	_ = r.repo.AppendEvent(ctx, taskID, EventToolCall, EventPayload{
		Tool:  event.Tool,
		Input: event.Input,
	})
	eventType := EventToolResult
	if event.Status == trace.StatusError {
		eventType = EventError
	}
	_ = r.repo.AppendEvent(ctx, taskID, eventType, EventPayload{
		Tool:   event.Tool,
		Input:  event.Input,
		Output: event.Output,
		Status: string(event.Status),
	})
}

func (r *Runner) fail(ctx context.Context, taskID string, err error) error {
	if updateErr := r.repo.UpdateStatus(ctx, taskID, StatusFailed); updateErr != nil {
		return fmt.Errorf("%w; also failed updating task status: %v", err, updateErr)
	}
	_ = r.repo.AppendEvent(ctx, taskID, EventError, EventPayload{Message: err.Error(), Status: string(StatusFailed)})
	return nil
}
