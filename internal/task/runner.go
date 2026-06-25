package task

import (
	"context"
	"fmt"
	"strings"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/runtime"
	"github.com/Lioooooo123/liora/internal/tools"
	"github.com/Lioooooo123/liora/internal/trace"
)

type Runner struct {
	repo    *Repository
	planner *llm.Planner
}

func NewRunner(repo *Repository, planner *llm.Planner) *Runner {
	return &Runner{repo: repo, planner: planner}
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
	if task.Natural {
		turnRuntime, err := runtime.New(task.Workspace, r.planner)
		if err != nil {
			return runtimeResult{}, err
		}
		result, err := turnRuntime.Submit(ctx, task.UserInput)
		return runtimeResult{
			answer:       result.Answer,
			plannedSteps: result.PlannedSteps,
			summary:      result.AgentResult.Summary,
			diff:         result.AgentResult.Diff,
			events:       result.Events,
		}, err
	}
	workspace, err := tools.NewWorkspace(task.Workspace)
	if err != nil {
		return runtimeResult{}, err
	}
	recorder := trace.NewMemoryRecorder()
	result, err := agent.New(workspace, recorder).Run(ctx, task.UserInput)
	return runtimeResult{
		plannedSteps: task.UserInput,
		summary:      result.Summary,
		diff:         result.Diff,
		events:       recorder.Events(),
	}, err
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
