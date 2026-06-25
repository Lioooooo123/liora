package tuisession

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/daemonclient"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/trace"
	"github.com/Lioooooo123/liora/internal/tui"
)

type DaemonSubmitter struct {
	client        *daemonclient.Client
	workspace     string
	natural       bool
	mu            sync.Mutex
	currentTaskID string
	lastTaskID    string
	lastDiff      string
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
	s.setCurrentTask(created.Task.ID)
	defer s.clearCurrentTask(created.Task.ID)
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	stream, errs := s.client.StreamEvents(streamCtx, created.Task.ID)
	result := tui.TurnResult{AgentResult: agent.Result{Status: agent.StatusCompleted}}
	var runErr error
	terminalError := false
	for update := range stream {
		if onEvent != nil {
			onEvent(tui.StreamUpdate{
				Type:        string(update.Type),
				PayloadJSON: update.Event.Payload,
			})
		}
		if update.Type == taskpkg.EventDiff {
			if payload, err := eventPayload(update.Event); err == nil {
				s.rememberDiff(created.Task.ID, payload.Diff)
			}
		}
		mergeStreamEvent(&result, update)
		if update.Type == taskpkg.EventCompleted || update.Type == taskpkg.EventCancelled || update.Type == taskpkg.EventError {
			terminalError = update.Type == taskpkg.EventError
			cancelStream()
			break
		}
	}
	if err := <-errs; err != nil {
		runErr = err
	}
	if result.AgentResult.Status == agent.StatusFailed && runErr == nil && terminalError {
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

func (s *DaemonSubmitter) HandleCommand(ctx context.Context, line string) (string, bool, error) {
	line = strings.TrimSpace(line)
	switch line {
	case "/cancel":
		return s.cancelCurrent(ctx)
	case "/apply":
		return s.applyLast(ctx)
	case "/tasks":
		return s.listTasks(ctx)
	case "/last":
		return s.replayLastTask(ctx)
	default:
		if strings.HasPrefix(line, "/resume ") {
			return s.replayTask(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/resume ")))
		}
		if line == "/resume" {
			return "Usage: /resume <task_id>", true, nil
		}
		return "", false, nil
	}
}

func (s *DaemonSubmitter) cancelCurrent(ctx context.Context) (string, bool, error) {
	taskID := s.currentTask()
	if taskID == "" {
		return "No running daemon task.", true, nil
	}
	task, err := s.client.Cancel(ctx, taskID, "cancelled from TUI")
	if err != nil {
		return "", true, err
	}
	return "Cancelled task " + task.ID + ".", true, nil
}

func (s *DaemonSubmitter) applyLast(ctx context.Context) (string, bool, error) {
	taskID, diff := s.lastPatch()
	if taskID == "" {
		return "No daemon task to apply.", true, nil
	}
	if strings.TrimSpace(diff) == "" {
		fetched, err := s.client.Diff(ctx, taskID)
		if err != nil {
			return "", true, err
		}
		diff = fetched
	}
	if strings.TrimSpace(diff) == "" {
		return "No diff available for task " + taskID + ".", true, nil
	}
	if _, err := s.client.Apply(ctx, taskID, diff); err != nil {
		return "", true, err
	}
	return "Applied task " + taskID + ".", true, nil
}

func (s *DaemonSubmitter) listTasks(ctx context.Context) (string, bool, error) {
	tasks, err := s.client.ListTasks(ctx, 10)
	if err != nil {
		return "", true, err
	}
	if len(tasks) == 0 {
		return "No daemon tasks found.", true, nil
	}
	var lines []string
	for _, task := range tasks {
		lines = append(lines, fmt.Sprintf("- %s [%s] %s (%s)", task.ID, task.Status, task.Title, formatTaskTime(task.UpdatedAt)))
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) replayLastTask(ctx context.Context) (string, bool, error) {
	tasks, err := s.client.ListTasks(ctx, 1)
	if err != nil {
		return "", true, err
	}
	if len(tasks) == 0 {
		return "No daemon tasks found.", true, nil
	}
	return s.replayTask(ctx, tasks[0].ID)
}

func (s *DaemonSubmitter) replayTask(ctx context.Context, taskID string) (string, bool, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "Usage: /resume <task_id>", true, nil
	}
	task, err := s.client.GetTask(ctx, taskID)
	if err != nil {
		return "", true, err
	}
	events, err := s.client.Events(ctx, taskID)
	if err != nil {
		return "", true, err
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Task %s [%s]", task.ID, task.Status))
	lines = append(lines, "Title: "+task.Title)
	lines = append(lines, "Workspace: "+task.Workspace)
	if len(events) == 0 {
		lines = append(lines, "No events.")
	} else {
		lines = append(lines, "Events:")
	}
	var latestDiff string
	for _, event := range events {
		payload, _ := eventPayload(event)
		lines = append(lines, "- "+formatReplayEvent(event.Type, payload))
		if event.Type == taskpkg.EventDiff {
			latestDiff = payload.Diff
		}
	}
	s.rememberTask(task.ID)
	if strings.TrimSpace(latestDiff) != "" {
		s.rememberDiff(task.ID, latestDiff)
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) setCurrentTask(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentTaskID = taskID
	s.lastTaskID = taskID
	s.lastDiff = ""
}

func (s *DaemonSubmitter) rememberTask(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTaskID = taskID
}

func (s *DaemonSubmitter) clearCurrentTask(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentTaskID == taskID {
		s.currentTaskID = ""
	}
}

func (s *DaemonSubmitter) currentTask() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentTaskID
}

func (s *DaemonSubmitter) lastPatch() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTaskID, s.lastDiff
}

func (s *DaemonSubmitter) rememberDiff(taskID string, diff string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastTaskID == taskID {
		s.lastDiff = diff
	}
}

func formatReplayEvent(eventType taskpkg.EventType, payload taskpkg.EventPayload) string {
	switch eventType {
	case taskpkg.EventPlanReady:
		return string(eventType) + ": " + firstLine(payload.Steps)
	case taskpkg.EventToolCall, taskpkg.EventToolResult:
		return strings.TrimSpace(string(eventType) + ": " + payload.Tool + " " + payload.Input + " " + firstLine(payload.Output))
	case taskpkg.EventSummary:
		return string(eventType) + ": " + payload.Message
	case taskpkg.EventDiff:
		return string(eventType) + ": " + firstLine(payload.Diff)
	case taskpkg.EventCompleted, taskpkg.EventCancelled, taskpkg.EventError:
		if payload.Status != "" {
			return string(eventType) + ": " + payload.Status
		}
		if payload.Message != "" {
			return string(eventType) + ": " + payload.Message
		}
	}
	if payload.Message != "" {
		return string(eventType) + ": " + payload.Message
	}
	return string(eventType)
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	line, _, _ := strings.Cut(value, "\n")
	if len(line) > 120 {
		return line[:117] + "..."
	}
	return line
}

func formatTaskTime(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.Local().Format("2006-01-02 15:04")
}
