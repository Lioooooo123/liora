package tuisession

import (
	"context"
	"fmt"
	"strings"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/daemonclient"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/trace"
	"github.com/Lioooooo123/liora/internal/tui"
)

type DaemonSubmitter struct {
	client    *daemonclient.Client
	workspace string
	natural   bool
}

func NewDaemonSubmitter(client *daemonclient.Client, workspace string, natural bool) *DaemonSubmitter {
	return &DaemonSubmitter{client: client, workspace: workspace, natural: natural}
}

func (s *DaemonSubmitter) Submit(ctx context.Context, input string) (tui.TurnResult, error) {
	return s.SubmitStream(ctx, input, nil)
}

func (s *DaemonSubmitter) SubmitStream(ctx context.Context, input string, onEvent func(tui.StreamUpdate)) (tui.TurnResult, error) {
	if s.client == nil {
		return tui.TurnResult{}, fmt.Errorf("daemon client is required")
	}
	created, err := s.client.CreateTask(ctx, taskpkg.CreateRequest{
		Workspace: s.workspace,
		Prompt:    input,
		Natural:   s.natural,
		RunAsync:  true,
	})
	if err != nil {
		return tui.TurnResult{}, err
	}
	stream, errs := s.client.StreamEvents(ctx, created.Task.ID)
	result := tui.TurnResult{AgentResult: agent.Result{Status: agent.StatusCompleted}}
	var runErr error
	for update := range stream {
		if onEvent != nil {
			onEvent(tui.StreamUpdate{
				Type:        string(update.Type),
				PayloadJSON: update.Event.Payload,
			})
		}
		mergeStreamEvent(&result, update)
		if update.Type == taskpkg.EventCompleted || update.Type == taskpkg.EventCancelled || update.Type == taskpkg.EventError {
			break
		}
	}
	if err := <-errs; err != nil {
		runErr = err
	}
	if result.AgentResult.Status == agent.StatusFailed && runErr == nil {
		runErr = fmt.Errorf("daemon task failed")
	}
	return result, runErr
}

func mergeStreamEvent(result *tui.TurnResult, update daemonclient.StreamEvent) {
	payload, err := eventPayload(update.Event)
	if err != nil {
		result.AgentResult.Status = agent.StatusFailed
		result.AgentResult.Summary = err.Error()
		return
	}
	switch update.Type {
	case taskpkg.EventPlanReady:
		result.PlannedSteps = payload.Steps
	case taskpkg.EventToolResult:
		status := trace.StatusOK
		if payload.Status != "" {
			status = trace.Status(payload.Status)
		}
		result.Events = append(result.Events, trace.Event{
			Tool:   payload.Tool,
			Input:  payload.Input,
			Output: payload.Output,
			Status: status,
		})
	case taskpkg.EventSummary:
		if strings.TrimSpace(result.PlannedSteps) == "" && len(result.Events) == 0 {
			result.Answer = payload.Message
		}
		result.AgentResult.Summary = payload.Message
	case taskpkg.EventDiff:
		result.AgentResult.Diff = payload.Diff
	case taskpkg.EventError:
		result.AgentResult.Status = agent.StatusFailed
		if strings.TrimSpace(result.AgentResult.Summary) == "" {
			result.AgentResult.Summary = strings.TrimSpace(payload.Message + "\n" + payload.Output)
		}
	case taskpkg.EventCancelled:
		result.AgentResult.Status = agent.StatusFailed
		result.AgentResult.Summary = "cancelled"
	case taskpkg.EventCompleted:
		result.AgentResult.Status = agent.StatusCompleted
	}
}
