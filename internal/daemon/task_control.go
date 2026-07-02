package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

const (
	defaultTaskOutputLimit = 4000
	maxTaskOutputWait      = 60 * time.Second
)

func (s *server) CreateChildTask(ctx context.Context, parent taskpkg.Task, request taskpkg.ChildTaskRequest) (taskpkg.Task, error) {
	if s.runner == nil {
		return taskpkg.Task{}, errors.New("runner is not configured")
	}
	if err := s.ensureBackgroundCapacity(ctx); err != nil {
		return taskpkg.Task{}, err
	}
	child, err := s.repo.Create(ctx, taskpkg.CreateRequest{
		Workspace:    parent.Workspace,
		Prompt:       request.Prompt,
		SessionID:    parent.SessionID,
		Natural:      true,
		RunAsync:     true,
		Origin:       taskpkg.OriginSubagent,
		ParentTaskID: parent.ID,
		SubagentName: request.SubagentName,
		Role:         request.Role,
		Scope:        request.Scope,
		Automation: taskpkg.AutomationMetadata{
			Kind:    taskpkg.AutomationKindSubagent,
			Risk:    taskpkg.AutomationRiskSafe,
			Source:  "model_tool",
			Trigger: "Task",
		},
	})
	if err != nil {
		return taskpkg.Task{}, err
	}
	_ = s.repo.AppendEvent(ctx, child.ID, taskpkg.EventTaskCreated, taskpkg.EventPayload{
		Message:      child.UserInput,
		Status:       string(child.Status),
		Origin:       string(child.Origin),
		Kind:         string(child.Automation.Kind),
		Risk:         string(child.Automation.Risk),
		Source:       child.Automation.Source,
		Trigger:      child.Automation.Trigger,
		ParentTaskID: parent.ID,
		SubagentName: child.SubagentName,
		Role:         child.Role,
	})
	_ = s.repo.AppendEvent(ctx, parent.ID, taskpkg.EventSubagentStarted, taskpkg.EventPayload{
		ID:           child.ID,
		Message:      child.UserInput,
		Status:       string(child.Status),
		ParentTaskID: parent.ID,
		SubagentName: child.SubagentName,
		Role:         child.Role,
	})
	counts, err := s.repo.CountBackgroundTasks(ctx)
	if err != nil {
		return taskpkg.Task{}, err
	}
	if counts.Running >= s.background.MaxConcurrent {
		if err := s.repo.Queue(ctx, child.ID); err != nil {
			return taskpkg.Task{}, err
		}
		_ = s.repo.AppendEvent(ctx, child.ID, taskpkg.EventTaskQueued, taskpkg.EventPayload{
			Message: "Child task queued by the subagent concurrency limit.",
			Status:  string(taskpkg.StatusQueued),
			Origin:  string(child.Origin),
			Kind:    string(child.Automation.Kind),
		})
		return s.repo.Get(ctx, child.ID)
	}
	if err := s.startTaskAsync(child.ID); err != nil {
		return taskpkg.Task{}, err
	}
	return s.repo.Get(ctx, child.ID)
}

func (s *server) ReadChildTaskOutput(ctx context.Context, parent taskpkg.Task, request taskpkg.ChildTaskOutputRequest) (taskpkg.Task, string, error) {
	child, err := s.childForParent(ctx, parent, request.TaskID)
	if err != nil {
		return taskpkg.Task{}, "", err
	}
	wait := request.Wait
	if wait < 0 {
		wait = 0
	}
	if wait > maxTaskOutputWait {
		wait = maxTaskOutputWait
	}
	if wait > 0 && !taskStatusTerminal(child.Status) {
		notification, unsubscribe := s.repo.SubscribeEvents(ctx, child.ID)
		defer unsubscribe()
		timer := time.NewTimer(wait)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return taskpkg.Task{}, "", ctx.Err()
			case <-notification:
				child, err = s.childForParent(ctx, parent, request.TaskID)
				if err != nil {
					return taskpkg.Task{}, "", err
				}
				if taskStatusTerminal(child.Status) {
					goto readOutput
				}
			case <-timer.C:
				goto readOutput
			}
		}
	}

readOutput:
	child, err = s.childForParent(ctx, parent, request.TaskID)
	if err != nil {
		return taskpkg.Task{}, "", err
	}
	events, err := s.repo.Events(ctx, child.ID, 1000)
	if err != nil {
		return taskpkg.Task{}, "", err
	}
	return child, renderChildTaskOutput(events, request.Limit), nil
}

func (s *server) StopChildTask(ctx context.Context, parent taskpkg.Task, request taskpkg.ChildTaskStopRequest) (taskpkg.Task, error) {
	child, err := s.childForParent(ctx, parent, request.TaskID)
	if err != nil {
		return taskpkg.Task{}, err
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" {
		reason = "stopped by parent TaskStop"
	}
	if err := s.repo.Cancel(ctx, child.ID, reason); err != nil {
		return taskpkg.Task{}, err
	}
	s.cancelRunning(child.ID)
	s.startNextQueuedAfter(child.ID)
	s.startNextForegroundQueued()
	s.startNextBackgroundQueued()
	child, err = s.repo.Get(ctx, child.ID)
	if err != nil {
		return taskpkg.Task{}, err
	}
	_ = s.repo.AppendEvent(ctx, parent.ID, taskpkg.EventSubagentCompleted, taskpkg.EventPayload{
		ID:           child.ID,
		Message:      reason,
		Status:       string(child.Status),
		ParentTaskID: parent.ID,
		SubagentName: child.SubagentName,
		Role:         child.Role,
		StopReason:   reason,
	})
	return child, nil
}

func (s *server) childForParent(ctx context.Context, parent taskpkg.Task, childID string) (taskpkg.Task, error) {
	childID = strings.TrimSpace(childID)
	if childID == "" {
		return taskpkg.Task{}, errors.New("task id is required")
	}
	child, err := s.repo.Get(ctx, childID)
	if err != nil {
		return taskpkg.Task{}, err
	}
	if child.ParentTaskID != parent.ID {
		return taskpkg.Task{}, fmt.Errorf("task %q is not a child of parent task %q", childID, parent.ID)
	}
	return child, nil
}

func taskStatusTerminal(status taskpkg.Status) bool {
	switch status {
	case taskpkg.StatusCompleted, taskpkg.StatusFailed, taskpkg.StatusCancelled, taskpkg.StatusStale:
		return true
	default:
		return false
	}
}

func renderChildTaskOutput(events []taskpkg.Event, limit int) string {
	if limit <= 0 {
		limit = defaultTaskOutputLimit
	}
	var builder strings.Builder
	for _, event := range events {
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			continue
		}
		switch event.Type {
		case taskpkg.EventSummary:
			appendOutputLine(&builder, payload.Message)
		case taskpkg.EventToolResult:
			appendOutputLine(&builder, payload.Output)
		case taskpkg.EventDiff:
			appendOutputLine(&builder, payload.Diff)
		case taskpkg.EventCompleted, taskpkg.EventCancelled, taskpkg.EventError, taskpkg.EventPermissionRequest, taskpkg.EventUserInputRequest:
			appendOutputLine(&builder, firstNonEmpty(payload.Message, payload.Reason, payload.Status))
		}
	}
	output := strings.TrimSpace(builder.String())
	if len(output) <= limit {
		return output
	}
	return output[:limit] + "\n[...truncated]"
}

func appendOutputLine(builder *strings.Builder, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if builder.Len() > 0 {
		builder.WriteString("\n")
	}
	builder.WriteString(text)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
