package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/permission"
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
	permission permission.Policy
}

func NewRunner(repo *Repository, planner *llm.Planner) *Runner {
	return &Runner{repo: repo, planner: planner, permission: permission.Policy{Mode: permission.ModeAuto}}
}

func (r *Runner) SetSandbox(executor sandbox.Executor) {
	r.sandboxRun = executor
}

func (r *Runner) SetPatchMode(enabled bool) {
	r.patchMode = enabled
}

func (r *Runner) SetPermissionPolicy(policy permission.Policy) {
	r.permission = policy
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
	var permissionErr *permission.RequiredError
	if errors.As(err, &permissionErr) {
		if waitErr := r.waitForPermission(ctx, task.ID, permissionErr.Request); waitErr != nil {
			return waitErr
		}
		return nil
	}
	if isContextCancelled(ctx, err) && r.isTaskCancelled(task.ID) {
		return err
	}
	if strings.TrimSpace(result.plannedSteps) != "" {
		if !result.planReadyEmitted {
			_ = r.repo.AppendEvent(ctx, task.ID, EventPlanReady, EventPayload{Steps: result.plannedSteps})
		}
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
		if isContextCancelled(ctx, err) && r.isTaskCancelled(task.ID) {
			return err
		}
		_ = r.fail(ctx, task.ID, err)
		return err
	}
	if r.isTaskCancelled(task.ID) {
		return context.Canceled
	}
	if err := r.repo.UpdateStatus(ctx, task.ID, StatusCompleted); err != nil {
		return err
	}
	return r.repo.AppendEvent(ctx, task.ID, EventCompleted, EventPayload{Status: string(StatusCompleted)})
}

func (r *Runner) runTask(ctx context.Context, task Task) (runtimeResult, error) {
	mode := sandbox.WorkspaceModeDirect
	if r.patchMode {
		mode = sandbox.WorkspaceModeCopy
	}
	session, err := sandbox.PrepareWorkspace(task.Workspace, mode)
	if err != nil {
		return runtimeResult{}, err
	}
	defer session.Cleanup()
	_ = r.repo.AppendEvent(ctx, task.ID, EventSandboxWorkspace, EventPayload{Message: "workspace mode: " + string(session.Mode)})
	if task.Natural {
		turnRuntime, err := runtime.New(session.Root, r.planner)
		if err != nil {
			return runtimeResult{}, err
		}
		turnRuntime.SetSandbox(r.sandboxRun)
		turnRuntime.SetPermissionChecker(r.permissionChecker(task))
		recorder := newRepositoryRecorder(ctx, r, task.ID)
		planReadyEmitted := false
		result, err := turnRuntime.SubmitWithOptions(ctx, task.UserInput, runtime.SubmitOptions{
			Recorder: recorder,
			OnPlan: func(steps string) {
				if strings.TrimSpace(steps) == "" {
					return
				}
				planReadyEmitted = true
				_ = r.repo.AppendEvent(ctx, task.ID, EventPlanReady, EventPayload{Steps: steps})
			},
		})
		return runtimeResult{
			answer:           result.Answer,
			plannedSteps:     result.PlannedSteps,
			planReadyEmitted: planReadyEmitted,
			summary:          result.AgentResult.Summary,
			diff:             result.AgentResult.Diff,
		}, err
	}
	workspace, err := tools.NewWorkspace(session.Root)
	if err != nil {
		return runtimeResult{}, err
	}
	recorder := newRepositoryRecorder(ctx, r, task.ID)
	runner := agent.New(workspace, recorder)
	runner.SetPermissionChecker(r.permissionChecker(task))
	if r.sandboxRun != nil {
		runner.SetShellExecutor(r.sandboxRun)
	}
	result, err := runner.Run(ctx, task.UserInput)
	return runtimeResult{
		plannedSteps: task.UserInput,
		summary:      result.Summary,
		diff:         result.Diff,
	}, err
}

func (r *Runner) permissionChecker(task Task) permission.Checker {
	policy := r.permission
	policy.Approved = policy.Approved || task.ApprovalGranted
	policy.AllowWritesInPatchMode = r.patchMode
	return policy
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
	answer           string
	plannedSteps     string
	planReadyEmitted bool
	summary          string
	diff             string
	events           []trace.Event
}

type repositoryRecorder struct {
	ctx    context.Context
	runner *Runner
	taskID string
	once   sync.Once
}

func newRepositoryRecorder(ctx context.Context, runner *Runner, taskID string) *repositoryRecorder {
	return &repositoryRecorder{ctx: ctx, runner: runner, taskID: taskID}
}

func (r *repositoryRecorder) Record(event trace.Event) {
	r.once.Do(func() {
		_ = r.runner.repo.UpdateStatus(r.ctx, r.taskID, StatusRunning)
	})
	r.runner.appendTraceEvents(r.ctx, r.taskID, event)
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

func (r *Runner) waitForPermission(ctx context.Context, taskID string, request permission.Request) error {
	if updateErr := r.repo.UpdateStatus(ctx, taskID, StatusWaitingUser); updateErr != nil {
		return updateErr
	}
	_ = r.repo.AppendEvent(ctx, taskID, EventPermissionRequest, EventPayload{
		Message: "Approval required before continuing.",
		Tool:    request.Tool,
		Input:   request.Input,
		Status:  string(StatusWaitingUser),
		Risk:    request.Risk,
		Reason:  request.Reason,
	})
	return nil
}

func (r *Runner) isTaskCancelled(taskID string) bool {
	task, err := r.repo.Get(context.Background(), taskID)
	return err == nil && task.Status == StatusCancelled
}

func isContextCancelled(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled)
}
