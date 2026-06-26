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
	sessionID     string
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
		SessionID: s.currentSessionID(),
		Natural:   s.natural,
		RunAsync:  true,
	})
	if err != nil {
		return tui.TurnResult{}, err
	}
	s.rememberSession(created.Task.SessionID)
	return s.streamTask(ctx, created.Task.ID, onEvent)
}

func (s *DaemonSubmitter) streamTask(ctx context.Context, taskID string, onEvent func(tui.StreamUpdate)) (tui.TurnResult, error) {
	s.setCurrentTask(taskID)
	defer s.clearCurrentTask(taskID)
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	stream, errs := s.client.StreamEvents(streamCtx, taskID)
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
				s.rememberDiff(taskID, payload.Diff)
			}
		}
		mergeStreamEvent(&result, update)
		if update.Type == taskpkg.EventCompleted || update.Type == taskpkg.EventCancelled || update.Type == taskpkg.EventError || update.Type == taskpkg.EventPermissionRequest {
			terminalError = update.Type == taskpkg.EventError
			cancelStream()
			break
		}
	}
	if err := <-errs; err != nil {
		runErr = err
	}
	if result.AgentResult.Status == agent.StatusFailed && runErr == nil && terminalError {
		summary := strings.TrimSpace(result.AgentResult.Summary)
		if summary == "" {
			summary = "daemon task failed"
		}
		runErr = fmt.Errorf("%s", summary)
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
	case taskpkg.EventPermissionRequest:
		result.AgentResult.Status = agent.StatusWaitingUser
		if strings.TrimSpace(result.AgentResult.Summary) == "" {
			result.AgentResult.Summary = "waiting for approval"
		}
	case taskpkg.EventCompleted:
		result.AgentResult.Status = agent.StatusCompleted
	}
}

func (s *DaemonSubmitter) HandleCommand(ctx context.Context, line string) (string, bool, error) {
	line = strings.TrimSpace(line)
	switch line {
	case "/tools":
		return s.showTools(ctx)
	case "/cancel":
		return s.cancelCurrent(ctx)
	case "/approve":
		return s.approveTask(ctx, "")
	case "/deny":
		return s.denyTask(ctx, "")
	case "/approvals", "/pending":
		return s.listApprovals(ctx)
	case "/diff":
		return s.showDiff(ctx, "")
	case "/apply":
		return s.applyLast(ctx)
	case "/tasks":
		return s.listTasks(ctx)
	case "/sessions":
		return s.listSessions(ctx)
	case "/session":
		return s.showSession(ctx)
	case "/timeline", "/transcript":
		return s.showTimeline(ctx)
	case "/last":
		return s.replayLastTask(ctx)
	default:
		if line == "/tail" || strings.HasPrefix(line, "/tail ") || line == "/log" || strings.HasPrefix(line, "/log ") {
			command := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "/tail"), "/log"))
			return s.tailTask(ctx, command)
		}
		if strings.HasPrefix(line, "/diff ") {
			return s.showDiff(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/diff ")))
		}
		if strings.HasPrefix(line, "/approve ") {
			return s.approveTask(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/approve ")))
		}
		if strings.HasPrefix(line, "/deny ") {
			return s.denyTask(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/deny ")))
		}
		if strings.HasPrefix(line, "/resume-session ") {
			return s.resumeSession(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/resume-session ")))
		}
		if line == "/resume-session" {
			return "Usage: /resume-session <session_id>", true, nil
		}
		if strings.HasPrefix(line, "/resume ") {
			return s.replayTask(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/resume ")))
		}
		if line == "/resume" {
			return "Usage: /resume <task_id>", true, nil
		}
		return "", false, nil
	}
}

func (s *DaemonSubmitter) tailTask(ctx context.Context, args string) (string, bool, error) {
	taskID := s.lastTask()
	lineCount := 40
	fields := strings.Fields(args)
	if len(fields) > 0 {
		if parsed, ok := parsePositiveInt(fields[0]); ok {
			lineCount = parsed
		} else {
			taskID = fields[0]
		}
	}
	if len(fields) > 1 {
		if parsed, ok := parsePositiveInt(fields[1]); ok {
			lineCount = parsed
		}
	}
	if taskID == "" {
		tasks, err := s.client.ListTasks(ctx, 1)
		if err != nil {
			return "", true, err
		}
		if len(tasks) == 0 {
			return "No daemon tasks found.", true, nil
		}
		taskID = tasks[0].ID
	}
	events, err := s.client.Events(ctx, taskID)
	if err != nil {
		return "", true, err
	}
	var transcript []string
	for _, event := range events {
		payload, _ := eventPayload(event)
		transcript = append(transcript, formatTailEvent(event.Type, payload)...)
	}
	if len(transcript) == 0 {
		return "No event output for task " + taskID + ".", true, nil
	}
	if lineCount > 200 {
		lineCount = 200
	}
	if len(transcript) > lineCount {
		transcript = transcript[len(transcript)-lineCount:]
	}
	lines := []string{fmt.Sprintf("Tail %s last %d lines", taskID, lineCount)}
	lines = append(lines, transcript...)
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) showTools(ctx context.Context) (string, bool, error) {
	capabilities, err := s.client.Capabilities(ctx)
	if err != nil {
		return "", true, err
	}
	var lines []string
	if len(capabilities.Tools) > 0 {
		lines = append(lines, "Built-in tools")
		for _, tool := range capabilities.Tools {
			line := "- " + tool.Usage + " [" + string(tool.Kind) + "]"
			if strings.TrimSpace(tool.Description) != "" {
				line += " - " + tool.Description
			}
			lines = append(lines, line)
		}
	}
	if len(capabilities.MCPTools) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "MCP tools")
		for _, tool := range capabilities.MCPTools {
			line := "- " + tool.Usage + " [" + string(tool.Kind) + "]"
			if strings.TrimSpace(tool.Description) != "" {
				line += " - " + tool.Description
			}
			lines = append(lines, line)
		}
	}
	if strings.TrimSpace(capabilities.MCPError) != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "MCP error: "+capabilities.MCPError)
	}
	if len(lines) == 0 {
		return "No tools available.", true, nil
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) showTimeline(ctx context.Context) (string, bool, error) {
	sessionID := s.currentSessionID()
	if sessionID == "" {
		return "No current daemon session.", true, nil
	}
	timeline, err := s.client.SessionTimeline(ctx, sessionID, 50)
	if err != nil {
		return "", true, err
	}
	if len(timeline) == 0 {
		return "No timeline items found.", true, nil
	}
	var lines []string
	lines = append(lines, "Timeline "+sessionID)
	for _, item := range timeline {
		line := formatTimelineItem(item)
		if line != "" {
			lines = append(lines, "- "+line)
		}
	}
	return strings.Join(lines, "\n"), true, nil
}

func formatTimelineItem(item taskpkg.TimelineItem) string {
	switch item.Kind {
	case "message":
		role := item.Role
		if role == "" {
			role = "message"
		}
		return role + ": " + firstLine(item.Content)
	case "tool_call":
		return strings.TrimSpace("tool.call: " + item.Tool + " " + item.Input)
	case "tool_result":
		status := item.Status
		if status == "" {
			status = "ok"
		}
		return strings.TrimSpace("tool.result[" + status + "]: " + item.Tool + " " + item.Input + " " + firstLine(item.Output))
	case "diff":
		return "diff: " + firstLine(item.Diff)
	case "approval":
		return strings.TrimSpace("approval: " + item.Tool + " " + item.Input + " " + item.Status + " " + item.Risk + " " + item.Reason + " " + firstLine(item.Content))
	case "status":
		status := item.Status
		if status == "" {
			status = item.Type
		}
		return "status: " + strings.TrimSpace(status+" "+firstLine(item.Content))
	default:
		return strings.TrimSpace(item.Kind + ": " + firstLine(item.Content))
	}
}

func (s *DaemonSubmitter) listApprovals(ctx context.Context) (string, bool, error) {
	if s.client == nil {
		return "No pending approvals.", true, nil
	}
	tasks, err := s.client.ListTasks(ctx, 50)
	if err != nil {
		return "", true, err
	}
	var lines []string
	lines = append(lines, "Pending approvals")
	for _, task := range tasks {
		if task.Status != taskpkg.StatusWaitingUser {
			continue
		}
		payload, err := s.latestPermissionRequest(ctx, task.ID)
		if err != nil {
			return "", true, err
		}
		if len(lines) == 1 {
			s.rememberTask(task.ID)
		}
		lines = append(lines, fmt.Sprintf("- %s [%s] %s", task.ID, task.Status, task.Title))
		if strings.TrimSpace(payload.Tool+payload.Input) != "" {
			lines = append(lines, "  request: "+strings.TrimSpace(payload.Tool+" "+payload.Input))
		}
		if strings.TrimSpace(payload.Risk) != "" {
			lines = append(lines, "  risk: "+payload.Risk)
		}
		if strings.TrimSpace(payload.Reason) != "" {
			lines = append(lines, "  reason: "+payload.Reason)
		}
		lines = append(lines, "  commands: /approve "+task.ID+"  /deny "+task.ID)
	}
	if len(lines) == 1 {
		return "No pending approvals.", true, nil
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) latestPermissionRequest(ctx context.Context, taskID string) (taskpkg.EventPayload, error) {
	events, err := s.client.Events(ctx, taskID)
	if err != nil {
		return taskpkg.EventPayload{}, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != taskpkg.EventPermissionRequest {
			continue
		}
		payload, err := eventPayload(events[i])
		if err != nil {
			return taskpkg.EventPayload{}, err
		}
		return payload, nil
	}
	return taskpkg.EventPayload{}, nil
}

func (s *DaemonSubmitter) approveTask(ctx context.Context, taskID string) (string, bool, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		taskID = s.lastTask()
	}
	if taskID == "" {
		return "No daemon task to approve.", true, nil
	}
	task, err := s.client.Approve(ctx, taskID)
	if err != nil {
		return "", true, err
	}
	s.rememberTask(task.ID)
	return strings.Join([]string{
		"Approved task " + task.ID + ".",
		"Status: " + string(task.Status),
		"Next: use /last or /timeline to inspect the continued run.",
	}, "\n"), true, nil
}

func (s *DaemonSubmitter) denyTask(ctx context.Context, taskID string) (string, bool, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		taskID = s.lastTask()
	}
	if taskID == "" {
		return "No daemon task to deny.", true, nil
	}
	task, err := s.client.Deny(ctx, taskID, "denied from TUI")
	if err != nil {
		return "", true, err
	}
	s.rememberTask(task.ID)
	return strings.Join([]string{
		"Denied task " + task.ID + ".",
		"Status: " + string(task.Status),
		"Next: use /timeline to review the decision history.",
	}, "\n"), true, nil
}

func (s *DaemonSubmitter) listSessions(ctx context.Context) (string, bool, error) {
	sessions, err := s.client.ListSessions(ctx, 10)
	if err != nil {
		return "", true, err
	}
	if len(sessions) == 0 {
		return "No daemon sessions found.", true, nil
	}
	current := s.currentSessionID()
	var lines []string
	for _, session := range sessions {
		marker := " "
		if session.ID == current {
			marker = "*"
		}
		lines = append(lines, fmt.Sprintf("%s %s %s (%s)", marker, session.ID, session.Title, formatTaskTime(session.UpdatedAt)))
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) showSession(ctx context.Context) (string, bool, error) {
	sessionID := s.currentSessionID()
	if sessionID == "" {
		return "No current daemon session.", true, nil
	}
	return s.resumeSession(ctx, sessionID)
}

func (s *DaemonSubmitter) resumeSession(ctx context.Context, sessionID string) (string, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "Usage: /resume-session <session_id>", true, nil
	}
	session, err := s.client.GetSession(ctx, sessionID)
	if err != nil {
		return "", true, err
	}
	messages, err := s.client.SessionMessages(ctx, sessionID, 20)
	if err != nil {
		return "", true, err
	}
	tasks, err := s.client.SessionTasks(ctx, sessionID, 10)
	if err != nil {
		return "", true, err
	}
	s.rememberSession(session.ID)
	if session.LastTaskID != "" {
		s.rememberTask(session.LastTaskID)
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Session %s", session.ID))
	lines = append(lines, "Title: "+session.Title)
	lines = append(lines, "Workspace: "+session.Workspace)
	if len(messages) == 0 {
		lines = append(lines, "Messages: none")
	} else {
		lines = append(lines, "Messages:")
		for _, message := range messages {
			lines = append(lines, fmt.Sprintf("- %s: %s", message.Role, firstLine(message.Content)))
		}
	}
	if len(tasks) == 0 {
		lines = append(lines, "Tasks: none")
	} else {
		lines = append(lines, "Tasks:")
		for _, task := range tasks {
			lines = append(lines, fmt.Sprintf("- %s [%s] %s", task.ID, task.Status, task.Title))
		}
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) cancelCurrent(ctx context.Context) (string, bool, error) {
	taskID := s.waitCurrentTask(ctx, 500*time.Millisecond)
	if taskID == "" {
		return "No running daemon task.", true, nil
	}
	task, err := s.client.Cancel(ctx, taskID, "cancelled from TUI")
	if err != nil {
		return "", true, err
	}
	return "Cancelled task " + task.ID + ".", true, nil
}

func (s *DaemonSubmitter) showDiff(ctx context.Context, taskID string) (string, bool, error) {
	taskID = strings.TrimSpace(taskID)
	lastTaskID, diff := s.lastPatch()
	if taskID == "" {
		taskID = lastTaskID
	}
	if taskID == "" {
		if s.client == nil {
			return "No daemon task to diff.", true, nil
		}
		tasks, err := s.client.ListTasks(ctx, 1)
		if err != nil {
			return "", true, err
		}
		if len(tasks) == 0 {
			return "No daemon task to diff.", true, nil
		}
		taskID = tasks[0].ID
	}
	if taskID != lastTaskID || strings.TrimSpace(diff) == "" {
		fetched, err := s.client.Diff(ctx, taskID)
		if err != nil {
			return "", true, err
		}
		diff = fetched
	}
	if strings.TrimSpace(diff) == "" {
		return "No diff available for task " + taskID + ".", true, nil
	}
	s.rememberTask(taskID)
	s.rememberDiff(taskID, diff)
	lines := []string{"Diff " + taskID}
	lines = append(lines, previewLines(diff, 180)...)
	lines = append(lines, "Next: review this diff, then use /apply to write it to the workspace.")
	return strings.Join(lines, "\n"), true, nil
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
	result, err := s.client.Apply(ctx, taskID, diff)
	if err != nil {
		return "", true, err
	}
	lines := []string{"Applied task " + taskID + "."}
	if len(result.Files) > 0 {
		lines = append(lines, "Files:")
		for _, file := range result.Files {
			lines = append(lines, "- "+file)
		}
	}
	lines = append(lines, "Next: use /timeline to review the applied patch event.")
	return strings.Join(lines, "\n"), true, nil
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

func (s *DaemonSubmitter) rememberSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = sessionID
}

func (s *DaemonSubmitter) currentSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
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

func (s *DaemonSubmitter) waitCurrentTask(ctx context.Context, timeout time.Duration) string {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if taskID := s.currentTask(); taskID != "" {
			return taskID
		}
		select {
		case <-ctx.Done():
			return ""
		case <-deadline.C:
			return ""
		case <-ticker.C:
		}
	}
}

func (s *DaemonSubmitter) lastPatch() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTaskID, s.lastDiff
}

func (s *DaemonSubmitter) lastTask() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTaskID
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
		status := payload.Status
		if status != "" {
			status = "[" + status + "] "
		}
		return strings.TrimSpace(string(eventType) + ": " + status + payload.Tool + " " + payload.Input + " " + firstLine(payload.Output))
	case taskpkg.EventSummary:
		return string(eventType) + ": " + payload.Message
	case taskpkg.EventDiff:
		return string(eventType) + ": " + firstLine(payload.Diff)
	case taskpkg.EventCompleted, taskpkg.EventCancelled, taskpkg.EventError:
		return strings.TrimSpace(string(eventType) + ": " + payload.Status + " " + firstLine(payload.Message))
	case taskpkg.EventPermissionRequest:
		return strings.TrimSpace(string(eventType) + ": " + payload.Tool + " " + payload.Input + " " + payload.Risk + " " + payload.Reason)
	case taskpkg.EventPermissionApproved, taskpkg.EventPermissionDenied:
		return string(eventType) + ": " + payload.Message
	}
	if payload.Message != "" {
		return string(eventType) + ": " + payload.Message
	}
	return string(eventType)
}

func formatTailEvent(eventType taskpkg.EventType, payload taskpkg.EventPayload) []string {
	header := strings.TrimSpace(string(eventType))
	switch eventType {
	case taskpkg.EventPlanReady:
		return appendPrefixedLines(header, payload.Steps)
	case taskpkg.EventToolCall:
		return []string{strings.TrimSpace(header + ": " + payload.Tool + " " + payload.Input)}
	case taskpkg.EventToolResult:
		status := payload.Status
		if status == "" {
			status = string(trace.StatusOK)
		}
		lines := []string{strings.TrimSpace(header + " [" + status + "]: " + payload.Tool + " " + payload.Input)}
		return append(lines, indentLines(payload.Output)...)
	case taskpkg.EventSummary:
		return appendPrefixedLines(header, payload.Message)
	case taskpkg.EventDiff:
		return appendPrefixedLines(header, payload.Diff)
	case taskpkg.EventPermissionRequest:
		return []string{strings.TrimSpace(header + ": " + payload.Tool + " " + payload.Input + " " + payload.Risk + " " + payload.Reason)}
	case taskpkg.EventCompleted, taskpkg.EventCancelled, taskpkg.EventError, taskpkg.EventReplanning:
		return appendPrefixedLines(strings.TrimSpace(header+" "+payload.Status), payload.Message)
	default:
		return appendPrefixedLines(header, payload.Message)
	}
}

func appendPrefixedLines(prefix string, value string) []string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return []string{prefix}
	}
	lines := []string{prefix + ":"}
	return append(lines, indentLines(value)...)
}

func indentLines(value string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(value, "\n"), "\n") {
		if len(line) > 180 {
			line = line[:177] + "..."
		}
		lines = append(lines, "  "+line)
	}
	return lines
}

func parsePositiveInt(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	parsed := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
		parsed = parsed*10 + int(r-'0')
	}
	if parsed <= 0 {
		return 0, false
	}
	return parsed, true
}

func previewLines(value string, maxLines int) []string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return nil
	}
	lines := strings.Split(value, "\n")
	if maxLines > 0 && len(lines) > maxLines {
		omitted := len(lines) - maxLines
		lines = append(lines[:maxLines], fmt.Sprintf("... %d more lines omitted", omitted))
	}
	for i, line := range lines {
		if len(line) > 220 {
			lines[i] = line[:217] + "..."
		}
	}
	return lines
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
