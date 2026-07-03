package tuisession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/daemonclient"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/trace"
	"github.com/Lioooooo123/liora/internal/tui"
)

const defaultArtifactCommandPageSize = 40

type DaemonSubmitter struct {
	client          *daemonclient.Client
	workspace       string
	natural         bool
	mu              sync.Mutex
	sessionID       string
	explicitSession bool
	currentTaskID   string
	awaitingInputID string
	lastTaskID      string
	lastDiff        string
	newSession      bool
}

func NewDaemonSubmitter(client *daemonclient.Client, workspace string, natural bool, sessionID string, startFresh bool) *DaemonSubmitter {
	submitter := &DaemonSubmitter{
		client:     client,
		workspace:  workspace,
		natural:    natural,
		newSession: startFresh,
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		submitter.rememberSession(sessionID)
		submitter.explicitSession = true
		submitter.newSession = false
	}
	return submitter
}

func (s *DaemonSubmitter) Submit(ctx context.Context, input string) (tui.TurnResult, error) {
	return s.SubmitStream(ctx, input, nil)
}

func (s *DaemonSubmitter) SubmitStream(ctx context.Context, input string, onEvent func(tui.StreamUpdate)) (tui.TurnResult, error) {
	if s.client == nil {
		return tui.TurnResult{}, fmt.Errorf("daemon client is required")
	}
	if taskID := s.awaitingInputTask(); taskID != "" {
		if _, err := s.client.SendInput(ctx, taskID, input); err != nil {
			return tui.TurnResult{}, err
		}
		s.clearAwaitingInput(taskID)
		return s.streamTaskAfterInput(ctx, taskID, onEvent)
	}
	created, err := s.createTask(ctx, input)
	if err != nil {
		return tui.TurnResult{}, err
	}
	return s.streamTask(ctx, created.Task.ID, onEvent)
}

func (s *DaemonSubmitter) createTask(ctx context.Context, input string) (taskpkg.CreateResponse, error) {
	return s.createTaskWithQueue(ctx, input, true)
}

func (s *DaemonSubmitter) createTaskWithQueue(ctx context.Context, input string, queue bool) (taskpkg.CreateResponse, error) {
	sessionID := s.currentSessionID()
	if sessionID == "" {
		resumed, ok, err := s.resumeLatestWorkspaceSession(ctx)
		if err != nil {
			return taskpkg.CreateResponse{}, err
		}
		if ok {
			sessionID = resumed.ID
		}
	}
	request := taskpkg.CreateRequest{
		Workspace: s.workspace,
		Prompt:    input,
		SessionID: sessionID,
		Natural:   s.natural,
		RunAsync:  true,
		Queue:     queue,
	}
	if threadID := s.currentConversationThreadID(ctx, sessionID); threadID != "" {
		request.ThreadID = &threadID
	}
	created, err := s.client.CreateTask(ctx, request)
	if err != nil {
		return taskpkg.CreateResponse{}, err
	}
	s.rememberSession(created.Task.SessionID)
	s.rememberTask(created.Task.ID)
	return created, nil
}

func (s *DaemonSubmitter) currentConversationThreadID(ctx context.Context, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || s.client == nil {
		return ""
	}
	thread, err := s.client.GetConversationThread(ctx, sessionID)
	if err != nil || thread.Workspace != s.workspace || thread.ArchivedAt != nil {
		return ""
	}
	return thread.ID
}

func (s *DaemonSubmitter) streamTask(ctx context.Context, taskID string, onEvent func(tui.StreamUpdate)) (tui.TurnResult, error) {
	return s.streamTaskUpdates(ctx, taskID, onEvent, false)
}

func (s *DaemonSubmitter) streamTaskAfterInput(ctx context.Context, taskID string, onEvent func(tui.StreamUpdate)) (tui.TurnResult, error) {
	return s.streamTaskUpdates(ctx, taskID, onEvent, true)
}

func (s *DaemonSubmitter) streamTaskUpdates(ctx context.Context, taskID string, onEvent func(tui.StreamUpdate), skipAnsweredInputRequest bool) (tui.TurnResult, error) {
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
		if update.Type == taskpkg.EventUserInputRequest {
			if skipAnsweredInputRequest {
				continue
			}
			s.setAwaitingInput(taskID)
		}
		if update.Type == taskpkg.EventUserInputReceived {
			skipAnsweredInputRequest = false
		}
		mergeStreamEvent(&result, update)
		if update.Type == taskpkg.EventCompleted || update.Type == taskpkg.EventCancelled || update.Type == taskpkg.EventError {
			s.clearAwaitingInput(taskID)
		}
		if update.Type == taskpkg.EventCompleted || update.Type == taskpkg.EventCancelled || update.Type == taskpkg.EventError || update.Type == taskpkg.EventPermissionRequest || update.Type == taskpkg.EventUserInputRequest {
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
	case taskpkg.EventUserInputRequest:
		result.AgentResult.Status = agent.StatusWaitingUser
		if strings.TrimSpace(result.AgentResult.Summary) == "" {
			result.AgentResult.Summary = payload.Message
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
	case "/clear":
		return s.newSessionCommand()
	case "/cancel":
		return s.cancelTask(ctx, "")
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
	case "/threads":
		return s.listThreads(ctx, false)
	case "/model":
		return s.showCurrentModel(ctx)
	case "/thread-new":
		return "Usage: /thread-new <title>", true, nil
	case "/thread-send":
		return "Usage: /thread-send <thread_id> <message>", true, nil
	case "/thread-inbox":
		return s.threadInbox(ctx, "")
	case "/workbench", "/status":
		return s.showWorkbench(ctx)
	case "/watch":
		return s.watchTasks(ctx, "")
	case "/spawn":
		return "Usage: /spawn <request>", true, nil
	case "/session":
		return s.showSession(ctx)
	case "/resume-latest", "/continue":
		return s.resumeLatest(ctx)
	case "/new-session":
		return s.newSessionCommand()
	case "/timeline":
		return s.showTimeline(ctx, "")
	case "/transcript":
		return s.showTranscript(ctx, "")
	case "/context":
		return s.showContext(ctx, "")
	case "/prompt-context":
		return s.showPromptContext(ctx, "")
	case "/compact":
		return s.compactSession(ctx, "")
	case "/todo":
		return s.showTodos(ctx)
	case "/artifact":
		return "Usage: /artifact <artifact://...> [page] [page_size]", true, nil
	case "/history", "/search-history":
		return "Usage: /history <query>", true, nil
	case "/memory":
		return s.handleMemory(ctx, "list")
	case "/schedule":
		return s.handleSchedule(ctx, "list")
	case "/permissions", "/permission-rules":
		return s.listPermissionRules(ctx)
	case "/permission-rule":
		return "Usage: /permission-rule add <always_allow|always_deny|always_ask> [workspace=current|global|<path>] [session=current|global|<id>] [tool=<tool>] [risk=<risk>] [reason=<text>] or /permission-rule delete <id>", true, nil
	case "/last":
		return s.replayLastTask(ctx)
	default:
		if line == "/tail" || strings.HasPrefix(line, "/tail ") || line == "/log" || strings.HasPrefix(line, "/log ") {
			command := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "/tail"), "/log"))
			return s.tailTask(ctx, command)
		}
		if strings.HasPrefix(line, "/cancel ") {
			return s.cancelTask(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/cancel ")))
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
		if strings.HasPrefix(line, "/timeline ") {
			return s.showTimeline(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/timeline ")))
		}
		if strings.HasPrefix(line, "/transcript ") {
			return s.showTranscript(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/transcript ")))
		}
		if strings.HasPrefix(line, "/context ") {
			args := strings.TrimSpace(strings.TrimPrefix(line, "/context "))
			if args == "sources" || strings.HasPrefix(args, "sources ") {
				return s.showPromptContext(ctx, strings.TrimSpace(strings.TrimPrefix(args, "sources")))
			}
			return s.showContext(ctx, args)
		}
		if strings.HasPrefix(line, "/prompt-context ") {
			return s.showPromptContext(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/prompt-context ")))
		}
		if strings.HasPrefix(line, "/compact ") {
			return s.compactSession(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/compact ")))
		}
		if strings.HasPrefix(line, "/todo ") {
			return "Usage: /todo", true, nil
		}
		if strings.HasPrefix(line, "/artifact ") {
			return s.showArtifact(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/artifact ")))
		}
		if strings.HasPrefix(line, "/history ") {
			return s.searchHistory(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/history ")))
		}
		if strings.HasPrefix(line, "/search-history ") {
			return s.searchHistory(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/search-history ")))
		}
		if strings.HasPrefix(line, "/memory ") {
			return s.handleMemory(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/memory ")))
		}
		if strings.HasPrefix(line, "/schedule ") {
			return s.handleSchedule(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/schedule ")))
		}
		if strings.HasPrefix(line, "/permission-rule ") {
			return s.handlePermissionRule(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/permission-rule ")))
		}
		if strings.HasPrefix(line, "/model ") {
			return s.handleModel(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/model ")))
		}
		if strings.HasPrefix(line, "/watch ") {
			return s.watchTasks(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/watch ")))
		}
		if strings.HasPrefix(line, "/spawn ") {
			return s.spawnTask(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/spawn ")))
		}
		if strings.HasPrefix(line, "/resume-session ") {
			return s.resumeSession(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/resume-session ")))
		}
		if strings.HasPrefix(line, "/thread-new ") {
			return s.createThread(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/thread-new ")))
		}
		if strings.HasPrefix(line, "/thread-rename ") {
			return s.renameThread(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/thread-rename ")))
		}
		if strings.HasPrefix(line, "/thread-archive ") {
			return s.archiveThread(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/thread-archive ")), true)
		}
		if strings.HasPrefix(line, "/thread-unarchive ") {
			return s.archiveThread(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/thread-unarchive ")), false)
		}
		if strings.HasPrefix(line, "/thread-send ") {
			return s.threadSend(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/thread-send ")))
		}
		if strings.HasPrefix(line, "/thread-inbox ") {
			return s.threadInbox(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/thread-inbox ")))
		}
		if strings.HasPrefix(line, "/thread ") {
			return s.switchThread(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/thread ")))
		}
		if line == "/thread" {
			return "Usage: /thread <thread_id>", true, nil
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

func (s *DaemonSubmitter) currentThread(ctx context.Context) (store.ConversationThread, bool, error) {
	threadID := s.currentSessionID()
	if threadID == "" {
		return store.ConversationThread{}, false, nil
	}
	thread, err := s.client.GetConversationThread(ctx, threadID)
	if err != nil {
		return store.ConversationThread{}, false, err
	}
	if thread.Workspace != s.workspace || thread.ArchivedAt != nil {
		return store.ConversationThread{}, false, nil
	}
	return thread, true, nil
}

func (s *DaemonSubmitter) handleMemory(ctx context.Context, args string) (string, bool, error) {
	command, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	switch strings.TrimSpace(command) {
	case "", "list":
		memories, err := s.client.ListMemoriesWithOptions(ctx, store.MemoryListOptions{Limit: 10, IncludeDisabled: strings.TrimSpace(rest) == "all"})
		if err != nil {
			return "", true, err
		}
		return formatMemories(memories), true, nil
	case "add":
		request := memoryCreateRequest(rest)
		memory, err := s.client.CreateMemory(ctx, request)
		if err != nil {
			return "", true, err
		}
		return "Memory saved: " + formatMemory(memory), true, nil
	case "search":
		memories, err := s.client.ListMemoriesWithOptions(ctx, store.MemoryListOptions{Query: rest, Limit: 10, IncludeDisabled: false})
		if err != nil {
			return "", true, err
		}
		return formatMemories(memories), true, nil
	case "edit":
		id, text, ok := strings.Cut(strings.TrimSpace(rest), " ")
		if !ok || strings.TrimSpace(id) == "" || strings.TrimSpace(text) == "" {
			return "Usage: /memory edit <id> <text>", true, nil
		}
		memory, err := s.client.UpdateMemory(ctx, id, store.UpdateMemoryRequest{Text: &text})
		if err != nil {
			return "", true, err
		}
		return "Memory updated: " + formatMemory(memory), true, nil
	case "disable", "enable":
		id := strings.TrimSpace(rest)
		if id == "" {
			return "Usage: /memory " + command + " <id>", true, nil
		}
		memory, err := s.client.SetMemoryEnabled(ctx, id, command == "enable")
		if err != nil {
			return "", true, err
		}
		return "Memory " + command + "d: " + formatMemory(memory), true, nil
	default:
		return "Usage: /memory list [all] | /memory add [note|preference|rule|automation] <text> | /memory search <query> | /memory edit <id> <text> | /memory disable <id> | /memory enable <id>", true, nil
	}
}

func memoryCreateRequest(input string) store.CreateMemoryRequest {
	input = strings.TrimSpace(input)
	kind, text, ok := strings.Cut(input, " ")
	if !ok {
		return store.CreateMemoryRequest{Text: input}
	}
	switch kind {
	case "note", "preference", "rule", "automation":
		return store.CreateMemoryRequest{Kind: kind, Text: text, Source: "manual"}
	default:
		return store.CreateMemoryRequest{Text: input}
	}
}

func formatMemories(memories []store.Memory) string {
	if len(memories) == 0 {
		return "No memories found."
	}
	lines := make([]string, 0, len(memories))
	for _, memory := range memories {
		lines = append(lines, "- "+formatMemory(memory))
	}
	return strings.Join(lines, "\n")
}

func formatMemory(memory store.Memory) string {
	status := "disabled"
	if memory.Enabled {
		status = "enabled"
	}
	source := memory.Source
	if source == "" {
		source = "unknown"
	}
	return fmt.Sprintf("%s [%s source=%s %s] %s", memory.ID, memory.Kind, source, status, memory.Text)
}

func (s *DaemonSubmitter) handleSchedule(ctx context.Context, args string) (string, bool, error) {
	command, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	switch strings.TrimSpace(command) {
	case "", "list":
		schedules, err := s.client.ListSchedules(ctx, store.ScheduleListOptions{
			Workspace:       s.workspace,
			Limit:           50,
			IncludeDisabled: strings.TrimSpace(rest) == "all",
		})
		if err != nil {
			return "", true, err
		}
		return formatSchedules(schedules), true, nil
	case "add":
		request, err := s.scheduleCreateRequest(rest)
		if err != nil {
			return err.Error(), true, nil
		}
		schedule, err := s.client.CreateSchedule(ctx, request)
		if err != nil {
			return "", true, err
		}
		return "Schedule saved: " + formatSchedule(schedule), true, nil
	case "pause", "resume":
		id := strings.TrimSpace(rest)
		if id == "" {
			return "Usage: /schedule " + command + " <id>", true, nil
		}
		schedule, err := s.client.SetScheduleEnabled(ctx, id, command == "resume")
		if err != nil {
			return "", true, err
		}
		verb := "paused"
		if command == "resume" {
			verb = "resumed"
		}
		return "Schedule " + verb + ": " + formatSchedule(schedule), true, nil
	case "delete":
		id := strings.TrimSpace(rest)
		if id == "" {
			return "Usage: /schedule delete <id>", true, nil
		}
		if err := s.client.DeleteSchedule(ctx, id); err != nil {
			return "", true, err
		}
		return "Schedule deleted: " + id, true, nil
	default:
		return scheduleUsage(), true, nil
	}
}

func (s *DaemonSubmitter) scheduleCreateRequest(input string) (store.CreateScheduleRequest, error) {
	left, prompt, ok := strings.Cut(input, " -- ")
	if !ok || strings.TrimSpace(prompt) == "" {
		return store.CreateScheduleRequest{}, errors.New(scheduleUsage())
	}
	fields := strings.Fields(left)
	if len(fields) < 3 {
		return store.CreateScheduleRequest{}, errors.New(scheduleUsage())
	}
	kind, err := scheduleTriggerKind(fields[1])
	if err != nil {
		return store.CreateScheduleRequest{}, err
	}
	triggerFields := fields[2:]
	trigger := ""
	switch kind {
	case store.ScheduleTriggerCron:
		if len(triggerFields) != 5 {
			return store.CreateScheduleRequest{}, fmt.Errorf("cron-like trigger must have 5 fields, got %d", len(triggerFields))
		}
		trigger = strings.Join(triggerFields, " ")
	default:
		if len(triggerFields) != 1 {
			return store.CreateScheduleRequest{}, errors.New(scheduleUsage())
		}
		trigger = triggerFields[0]
	}
	return store.CreateScheduleRequest{
		ID:          fields[0],
		Workspace:   s.workspace,
		TriggerKind: kind,
		Trigger:     trigger,
		Prompt:      strings.TrimSpace(prompt),
	}, nil
}

func scheduleTriggerKind(value string) (store.ScheduleTriggerKind, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "one-shot", "one_shot":
		return store.ScheduleTriggerOneShot, nil
	case "interval":
		return store.ScheduleTriggerInterval, nil
	case "cron":
		return store.ScheduleTriggerCron, nil
	default:
		return "", fmt.Errorf("unknown schedule trigger kind %q", value)
	}
}

func formatSchedules(schedules []store.ScheduleSpec) string {
	if len(schedules) == 0 {
		return "No schedules found."
	}
	lines := []string{"Schedules"}
	for _, schedule := range schedules {
		lines = append(lines, "- "+formatSchedule(schedule))
	}
	return strings.Join(lines, "\n")
}

func formatSchedule(schedule store.ScheduleSpec) string {
	status := "disabled"
	if schedule.Enabled {
		status = "enabled"
	}
	scope := "global"
	if strings.TrimSpace(schedule.Workspace) != "" {
		scope = "workspace=" + schedule.Workspace
	}
	return fmt.Sprintf("%s [%s %s %s] %s -> %s", schedule.ID, schedule.TriggerKind, status, scope, schedule.Trigger, schedule.Prompt)
}

func scheduleUsage() string {
	return "Usage: /schedule add <id> <one-shot|interval|cron> <trigger> -- <prompt> | /schedule list [all] | /schedule pause <id> | /schedule resume <id> | /schedule delete <id>"
}

func (s *DaemonSubmitter) listPermissionRules(ctx context.Context) (string, bool, error) {
	rules, err := s.client.ListPermissionRules(ctx, store.PermissionRuleListOptions{
		Workspace: s.workspace,
		SessionID: s.currentSessionID(),
		Limit:     50,
	})
	if err != nil {
		return "", true, err
	}
	if len(rules) == 0 {
		return "No permission rules found.", true, nil
	}
	lines := []string{"Permission rules"}
	for _, rule := range rules {
		lines = append(lines, "- "+formatPermissionRule(rule))
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) handlePermissionRule(ctx context.Context, args string) (string, bool, error) {
	command, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	switch command {
	case "add":
		request, err := s.permissionRuleCreateRequest(rest)
		if err != nil {
			return err.Error(), true, nil
		}
		rule, err := s.client.CreatePermissionRule(ctx, request)
		if err != nil {
			return "", true, err
		}
		return "Permission rule saved: " + formatPermissionRule(rule), true, nil
	case "delete":
		id := strings.TrimSpace(rest)
		if id == "" {
			return "Usage: /permission-rule delete <id>", true, nil
		}
		if err := s.client.DeletePermissionRule(ctx, id); err != nil {
			return "", true, err
		}
		return "Permission rule deleted: " + id, true, nil
	default:
		return "Usage: /permission-rule add <always_allow|always_deny|always_ask> [workspace=current|global|<path>] [session=current|global|<id>] [tool=<tool>] [risk=<risk>] [reason=<text>] or /permission-rule delete <id>", true, nil
	}
}

func (s *DaemonSubmitter) permissionRuleCreateRequest(input string) (store.CreatePermissionRuleRequest, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return store.CreatePermissionRuleRequest{}, fmt.Errorf("Usage: /permission-rule add <always_allow|always_deny|always_ask> [workspace=current|global|<path>] [session=current|global|<id>] [tool=<tool>] [risk=<risk>] [reason=<text>]")
	}
	request := store.CreatePermissionRuleRequest{Action: store.PermissionRuleAction(fields[0])}
	hasWorkspaceOrSession := false
	for _, field := range fields[1:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return store.CreatePermissionRuleRequest{}, fmt.Errorf("unknown permission rule argument %q", field)
		}
		switch strings.TrimSpace(key) {
		case "workspace":
			hasWorkspaceOrSession = true
			switch strings.TrimSpace(value) {
			case "current":
				request.Workspace = s.workspace
			case "global":
				request.Workspace = ""
			default:
				request.Workspace = value
			}
		case "session":
			hasWorkspaceOrSession = true
			switch strings.TrimSpace(value) {
			case "current":
				sessionID := s.currentSessionID()
				if sessionID == "" {
					return store.CreatePermissionRuleRequest{}, fmt.Errorf("no current session for session=current")
				}
				request.SessionID = sessionID
			case "global":
				request.SessionID = ""
			default:
				request.SessionID = value
			}
		case "tool":
			request.Tool = value
		case "risk":
			request.Risk = value
		case "reason":
			request.Reason = value
		default:
			return store.CreatePermissionRuleRequest{}, fmt.Errorf("unknown permission rule argument %q", field)
		}
	}
	if !hasWorkspaceOrSession {
		request.Workspace = s.workspace
	}
	return request, nil
}

func formatPermissionRule(rule store.PermissionRule) string {
	status := "disabled"
	if rule.Enabled {
		status = "enabled"
	}
	scope := []string{}
	if strings.TrimSpace(rule.Workspace) != "" {
		scope = append(scope, "workspace="+rule.Workspace)
	}
	if strings.TrimSpace(rule.SessionID) != "" {
		scope = append(scope, "session="+rule.SessionID)
	}
	if strings.TrimSpace(rule.Tool) != "" {
		scope = append(scope, "tool="+rule.Tool)
	}
	if strings.TrimSpace(rule.Risk) != "" {
		scope = append(scope, "risk="+rule.Risk)
	}
	if len(scope) == 0 {
		scope = append(scope, "global")
	}
	line := fmt.Sprintf("%s [%s %s] %s", rule.ID, rule.Action, status, strings.Join(scope, " "))
	if strings.TrimSpace(rule.Reason) != "" {
		line += " reason=" + rule.Reason
	}
	return line
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
	var artifactReferences []string
	for _, event := range events {
		lines := tui.FormatDaemonEventTail(string(event.Type), event.Payload)
		transcript = append(transcript, lines...)
		if event.Type == taskpkg.EventArtifactReference {
			artifactReferences = append(artifactReferences, lines...)
		}
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
	for _, line := range artifactReferences {
		if !containsLine(transcript, line) {
			transcript = append(transcript, line)
		}
	}
	lines := []string{fmt.Sprintf("Tail %s last %d lines", taskID, lineCount)}
	lines = append(lines, transcript...)
	return strings.Join(lines, "\n"), true, nil
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
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

func (s *DaemonSubmitter) showTimeline(ctx context.Context, args string) (string, bool, error) {
	sessionID, ok, err := s.currentOrLatestSessionID(ctx)
	if err != nil {
		return "", true, err
	}
	if !ok {
		return "No current daemon session.", true, nil
	}
	limit := timelineLimit(args, 50, 200)
	timeline, err := s.client.SessionTimeline(ctx, sessionID, limit)
	if err != nil {
		return "", true, err
	}
	if len(timeline) == 0 {
		return "No timeline items found.", true, nil
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Timeline %s last %d items", sessionID, limit))
	for _, item := range timeline {
		line := formatTimelineItem(item)
		if line != "" {
			lines = append(lines, "- "+line)
		}
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) showTranscript(ctx context.Context, args string) (string, bool, error) {
	sessionID, ok, err := s.currentOrLatestSessionID(ctx)
	if err != nil {
		return "", true, err
	}
	if !ok {
		return "No current daemon session.", true, nil
	}
	limit := timelineLimit(args, 100, 300)
	timeline, err := s.client.SessionTimeline(ctx, sessionID, limit)
	if err != nil {
		return "", true, err
	}
	if len(timeline) == 0 {
		return "No transcript items found.", true, nil
	}
	lines := []string{fmt.Sprintf("Transcript %s last %d items", sessionID, limit)}
	for _, item := range timeline {
		formatted := formatTranscriptItem(item)
		if len(formatted) > 0 {
			lines = append(lines, formatted...)
		}
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) showContext(ctx context.Context, args string) (string, bool, error) {
	sessionID, ok, err := s.currentOrLatestSessionID(ctx)
	if err != nil {
		return "", true, err
	}
	if !ok {
		return "No current daemon session.", true, nil
	}
	request := parseContextRequest(args)
	envelope, err := s.client.SessionContext(ctx, sessionID, request)
	if err != nil {
		return "", true, err
	}
	lines := []string{
		fmt.Sprintf("Context %s", envelope.Session.ID),
		fmt.Sprintf("Budget: %d/%d estimated tokens, %d item limit, truncated=%t", envelope.Budget.EstimatedTokens, envelope.Budget.MaxTokens, envelope.Budget.ItemLimit, envelope.Budget.Truncated),
		fmt.Sprintf("Transcript items: %d", len(envelope.Transcript)),
		fmt.Sprintf("Summaries: %d", len(envelope.Summaries)),
		fmt.Sprintf("Artifacts: %d", len(envelope.ArtifactRefs)),
		fmt.Sprintf("Compact boundaries: %d", len(envelope.CompactBoundaries)),
	}
	if len(envelope.CompactBoundaries) > 0 {
		lines = append(lines, "Latest compact boundary: "+firstLine(envelope.CompactBoundaries[len(envelope.CompactBoundaries)-1].Summary))
	}
	if len(envelope.ArtifactRefs) > 0 {
		lines = append(lines, "Artifact refs:")
		for _, ref := range envelope.ArtifactRefs {
			label := strings.TrimSpace(ref.Path)
			if label == "" {
				label = ref.Tool
			}
			lines = append(lines, "- "+strings.TrimSpace(label+" "+firstLine(ref.Summary)))
		}
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) showPromptContext(ctx context.Context, args string) (string, bool, error) {
	sessionID, ok, err := s.currentOrLatestSessionID(ctx)
	if err != nil {
		return "", true, err
	}
	if !ok {
		return "No current daemon session.", true, nil
	}
	if promptContextLastRequested(args) {
		output, err := s.showLastPromptContextSnapshot(ctx, sessionID)
		return output, true, err
	}
	request := parseContextRequest(args)
	envelope, err := s.client.SessionContext(ctx, sessionID, request)
	if err != nil {
		return "", true, err
	}
	return formatPromptContextSummary(envelope), true, nil
}

func promptContextLastRequested(args string) bool {
	for _, field := range strings.Fields(args) {
		if field == "--last" {
			return true
		}
	}
	return false
}

func (s *DaemonSubmitter) showLastPromptContextSnapshot(ctx context.Context, sessionID string) (string, error) {
	tasks, err := s.client.SessionTasks(ctx, sessionID, 1)
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "No tasks found for current daemon session.", nil
	}
	task := tasks[0]
	events, err := s.client.Events(ctx, task.ID)
	if err != nil {
		return "", err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != taskpkg.EventPromptContextSnapshot {
			continue
		}
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(events[i].Payload), &payload); err != nil {
			return "", err
		}
		output := strings.TrimSpace(payload.Output)
		if output == "" {
			output = strings.TrimSpace(payload.Message)
		}
		lines := []string{fmt.Sprintf("Actual prompt context snapshot for %s", task.ID)}
		if strings.TrimSpace(payload.Target) != "" && !strings.Contains(output, payload.Target) {
			lines = append(lines, "Hash: "+strings.TrimSpace(payload.Target))
		}
		if strings.TrimSpace(output) != "" {
			lines = append(lines, output)
		}
		return strings.Join(lines, "\n"), nil
	}
	return fmt.Sprintf("No prompt context snapshot recorded for %s.", task.ID), nil
}

func formatPromptContextSummary(envelope taskpkg.ContextEnvelope) string {
	lines := []string{
		fmt.Sprintf("Prompt context %s", envelope.Session.ID),
		fmt.Sprintf("Budget: %d/%d estimated tokens, %d item limit, truncated=%t", envelope.Budget.EstimatedTokens, envelope.Budget.MaxTokens, envelope.Budget.ItemLimit, envelope.Budget.Truncated),
	}
	if len(envelope.Pack.Sources) == 0 {
		lines = append(lines, "Sources: none")
	} else {
		lines = append(lines, "Sources:")
		for _, source := range envelope.Pack.Sources {
			lines = append(lines, "- "+formatPromptContextSource(source))
		}
	}
	if len(envelope.Diagnostics) == 0 {
		lines = append(lines, "Diagnostics: none")
	} else {
		lines = append(lines, "Diagnostics:")
		for _, diagnostic := range envelope.Diagnostics {
			lines = append(lines, "- "+formatPromptContextDiagnostic(diagnostic))
		}
	}
	return strings.Join(lines, "\n")
}

func formatPromptContextSource(source taskpkg.ContextPackSource) string {
	name := strings.TrimSpace(source.Name)
	if name == "" {
		name = "unknown"
	}
	return fmt.Sprintf("%s: selected=%d/%d tokens=%d truncated=%t", name, source.Selected, source.Available, source.EstimatedTokens, source.Truncated)
}

func formatPromptContextDiagnostic(diagnostic taskpkg.ContextDiagnostic) string {
	parts := []string{strings.TrimSpace(diagnostic.Source)}
	if parts[0] == "" {
		parts[0] = "unknown"
	}
	if strings.TrimSpace(diagnostic.ItemID) != "" {
		parts = append(parts, "id="+diagnostic.ItemID)
	}
	if strings.TrimSpace(diagnostic.ItemKind) != "" {
		parts = append(parts, "kind="+diagnostic.ItemKind)
	}
	parts = append(parts, fmt.Sprintf("tokens=%d", diagnostic.EstimatedTokens))
	if strings.TrimSpace(diagnostic.Reason) != "" {
		parts = append(parts, "reason="+firstLine(diagnostic.Reason))
	}
	return strings.Join(parts, " ")
}

func (s *DaemonSubmitter) compactSession(ctx context.Context, args string) (string, bool, error) {
	sessionID, ok, err := s.currentOrLatestSessionID(ctx)
	if err != nil {
		return "", true, err
	}
	if !ok {
		return "No current daemon session.", true, nil
	}
	request := parseCompactRequest(args)
	result, err := s.client.CompactSession(ctx, sessionID, request)
	if err != nil {
		return "", true, err
	}
	lines := []string{
		fmt.Sprintf("Compact %s", result.Session.ID),
		fmt.Sprintf("Mode: %s", result.Mode),
		fmt.Sprintf("Compacted: %t", result.Compacted),
		fmt.Sprintf("Budget: %d/%d estimated tokens before, %d after", result.BeforeEstimatedTokens, result.TokenBudget, result.AfterEstimatedTokens),
		fmt.Sprintf("Transcript items: %d", result.TranscriptItems),
	}
	if result.SkippedReason != "" {
		lines = append(lines, "Skipped: "+result.SkippedReason)
	}
	if result.Boundary != nil {
		lines = append(lines, "Boundary: "+firstLine(result.Boundary.Summary))
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) showTodos(ctx context.Context) (string, bool, error) {
	sessionID, ok, err := s.currentOrLatestSessionID(ctx)
	if err != nil {
		return "", true, err
	}
	if !ok {
		return "No current daemon session.", true, nil
	}
	todos, err := s.client.SessionTodos(ctx, sessionID)
	if err != nil {
		return "", true, err
	}
	if len(todos) == 0 {
		return "No todos found for session " + sessionID + ".", true, nil
	}
	lines := []string{fmt.Sprintf("Todos %s", sessionID)}
	for _, todo := range todos {
		lines = append(lines, fmt.Sprintf("- id=%s status=%s priority=%s source_task_id=%s updated_at=%s content=%s", todo.ID, todo.Status, todo.Priority, todo.SourceTaskID, todo.UpdatedAt.Format(time.RFC3339Nano), firstLine(todo.Content)))
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) showArtifact(ctx context.Context, args string) (string, bool, error) {
	request, err := parseArtifactPageRequest(args)
	if err != nil {
		return "", true, err
	}
	artifact, err := s.client.ArtifactPage(ctx, request)
	if err != nil {
		return "", true, err
	}
	lines := []string{
		fmt.Sprintf("Artifact %s", artifact.URI),
		fmt.Sprintf("Page %d/%d, lines %d, page_size=%d, has_prev=%t, has_next=%t", artifact.Page, artifact.TotalPages, artifact.TotalLines, artifact.PageSize, artifact.HasPrev, artifact.HasNext),
	}
	if artifact.Tail {
		lines[1] = "Tail " + lines[1]
	}
	lines = append(lines, artifact.Lines...)
	return strings.Join(lines, "\n"), true, nil
}

func parseArtifactPageRequest(args string) (taskpkg.ArtifactPageRequest, error) {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return taskpkg.ArtifactPageRequest{}, fmt.Errorf("usage: /artifact <artifact://...> [page] [page_size] or /artifact <artifact://...> tail [page_size]")
	}
	request := taskpkg.ArtifactPageRequest{URI: fields[0], Page: 1, PageSize: defaultArtifactCommandPageSize}
	if len(fields) > 1 {
		if strings.EqualFold(fields[1], "tail") {
			request.Tail = true
			if len(fields) > 2 {
				parsed, ok := parsePositiveInt(fields[2])
				if !ok {
					return taskpkg.ArtifactPageRequest{}, fmt.Errorf("page_size must be a positive integer")
				}
				request.PageSize = parsed
			}
			if len(fields) > 3 {
				return taskpkg.ArtifactPageRequest{}, fmt.Errorf("usage: /artifact <artifact://...> [page] [page_size] or /artifact <artifact://...> tail [page_size]")
			}
			return request, nil
		}
		parsed, ok := parsePositiveInt(fields[1])
		if !ok {
			return taskpkg.ArtifactPageRequest{}, fmt.Errorf("page must be a positive integer")
		}
		request.Page = parsed
	}
	if len(fields) > 2 {
		parsed, ok := parsePositiveInt(fields[2])
		if !ok {
			return taskpkg.ArtifactPageRequest{}, fmt.Errorf("page_size must be a positive integer")
		}
		request.PageSize = parsed
	}
	if len(fields) > 3 {
		return taskpkg.ArtifactPageRequest{}, fmt.Errorf("usage: /artifact <artifact://...> [page] [page_size] or /artifact <artifact://...> tail [page_size]")
	}
	return request, nil
}

func (s *DaemonSubmitter) searchHistory(ctx context.Context, query string) (string, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "Usage: /history <query>", true, nil
	}
	items, err := s.client.SearchTimeline(ctx, s.workspace, query, 30)
	if err != nil {
		return "", true, err
	}
	if len(items) == 0 {
		return "No history matches for " + query + ".", true, nil
	}
	lines := []string{fmt.Sprintf("History %q", query)}
	for _, item := range items {
		line := formatTimelineItem(item)
		if line == "" {
			continue
		}
		prefix := item.SessionID
		if item.TaskID != "" {
			prefix += " " + item.TaskID
		}
		lines = append(lines, "- "+strings.TrimSpace(prefix+": "+line))
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) currentOrLatestSessionID(ctx context.Context) (string, bool, error) {
	sessionID := s.currentSessionID()
	if sessionID == "" {
		session, ok, err := s.resumeLatestWorkspaceSession(ctx)
		if err != nil {
			return "", false, err
		}
		if !ok {
			return "", false, nil
		}
		sessionID = session.ID
	}
	return sessionID, true, nil
}

func formatTimelineItem(item taskpkg.TimelineItem) string {
	modelSuffix := timelineModelSuffix(item)
	switch item.Kind {
	case "message":
		role := item.Role
		if role == "" {
			role = "message"
		}
		return role + modelSuffix + ": " + firstLine(item.Content)
	case "tool_call":
		return strings.TrimSpace("tool.call" + modelSuffix + ": " + item.Tool + " " + item.Input)
	case "tool_result":
		status := item.Status
		if status == "" {
			status = "ok"
		}
		return strings.TrimSpace("tool.result[" + status + "]" + modelSuffix + ": " + item.Tool + " " + item.Input + " " + firstLine(item.Output))
	case "diff":
		return "diff: " + firstLine(item.Diff)
	case "approval":
		return strings.TrimSpace("approval: " + item.Tool + " " + item.Input + " " + item.Status + " " + item.Risk + " " + item.Reason + " " + firstLine(item.Content))
	case "status":
		status := item.Status
		if status == "" {
			status = item.Type
		}
		return "status" + modelSuffix + ": " + strings.TrimSpace(status+" "+firstLine(item.Content))
	default:
		return strings.TrimSpace(item.Kind + modelSuffix + ": " + firstLine(item.Content))
	}
}

func formatTranscriptItem(item taskpkg.TimelineItem) []string {
	taskSuffix := ""
	if item.TaskID != "" {
		taskSuffix = " (" + item.TaskID + ")"
	}
	modelSuffix := timelineModelSuffix(item)
	switch item.Kind {
	case "message":
		role := item.Role
		if role == "" {
			role = "message"
		}
		title := titleWord(role) + modelSuffix + taskSuffix
		return append([]string{title}, indentLines(item.Content)...)
	case "tool_call":
		return []string{strings.TrimSpace("Tool call" + modelSuffix + taskSuffix + ": " + item.Tool + " " + item.Input)}
	case "tool_result":
		status := item.Status
		if status == "" {
			status = "ok"
		}
		lines := []string{strings.TrimSpace("Tool result [" + status + "]" + modelSuffix + taskSuffix + ": " + item.Tool + " " + item.Input)}
		return append(lines, indentLines(item.Output)...)
	case "diff":
		lines := []string{"Diff" + taskSuffix}
		return append(lines, previewLines(item.Diff, 80)...)
	case "approval":
		header := strings.TrimSpace("Approval" + taskSuffix + ": " + item.Tool + " " + item.Input + " " + item.Status + " " + item.Risk + " " + item.Reason)
		return appendPrefixedLines(header, item.Content)
	case "status":
		header := strings.TrimSpace("Status" + modelSuffix + taskSuffix + ": " + item.Status)
		return appendPrefixedLines(header, strings.TrimSpace(item.Content+" "+item.Reason))
	default:
		return appendPrefixedLines(strings.TrimSpace(item.Kind+modelSuffix+taskSuffix), item.Content)
	}
}

func timelineModelSuffix(item taskpkg.TimelineItem) string {
	parts := []string{}
	if strings.TrimSpace(item.Provider) != "" || strings.TrimSpace(item.Model) != "" {
		parts = append(parts, "model="+strings.Trim(strings.TrimSpace(item.Provider)+"/"+strings.TrimSpace(item.Model), "/"))
	}
	if strings.TrimSpace(item.Profile) != "" {
		parts = append(parts, "profile="+item.Profile)
	}
	if len(parts) == 0 {
		return ""
	}
	return " [" + strings.Join(parts, " ") + "]"
}

func titleWord(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}

func timelineLimit(args string, defaultLimit int, maxLimit int) int {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return defaultLimit
	}
	parsed, ok := parsePositiveInt(fields[0])
	if !ok {
		return defaultLimit
	}
	if parsed > maxLimit {
		return maxLimit
	}
	return parsed
}

func parseContextRequest(args string) taskpkg.ContextRequest {
	fields := strings.Fields(args)
	request := taskpkg.ContextRequest{}
	if len(fields) > 0 {
		if parsed, ok := parsePositiveInt(fields[0]); ok {
			request.ItemLimit = parsed
		}
	}
	if len(fields) > 1 {
		if parsed, ok := parsePositiveInt(fields[1]); ok {
			request.TokenBudget = parsed
		}
	}
	return request
}

func parseCompactRequest(args string) taskpkg.CompactRequest {
	fields := strings.Fields(args)
	request := taskpkg.CompactRequest{Mode: taskpkg.CompactModeManual}
	if len(fields) > 0 {
		switch fields[0] {
		case string(taskpkg.CompactModeManual), string(taskpkg.CompactModeAuto):
			request.Mode = taskpkg.CompactMode(fields[0])
			fields = fields[1:]
		}
	}
	contextRequest := parseContextRequest(strings.Join(fields, " "))
	request.ItemLimit = contextRequest.ItemLimit
	request.TokenBudget = contextRequest.TokenBudget
	return request
}

func (s *DaemonSubmitter) listApprovals(ctx context.Context) (string, bool, error) {
	if s.client == nil {
		return "No pending approvals.", true, nil
	}
	workbench, err := s.client.Workbench(ctx, s.workspace, 50)
	if err != nil {
		return "", true, err
	}
	if len(workbench.PendingApprovals) == 0 {
		return "No pending approvals.", true, nil
	}
	var lines []string
	lines = append(lines, "Pending approvals")
	for _, approval := range workbench.PendingApprovals {
		if len(lines) == 1 {
			s.rememberTask(approval.Task.ID)
		}
		lines = append(lines, fmt.Sprintf("- %s [%s] %s", approval.Task.ID, approval.Task.Status, approval.Task.Title))
		if strings.TrimSpace(approval.Item.ID) != "" {
			lines = append(lines, "  item: "+approval.Item.ID)
		}
		if strings.TrimSpace(approval.Item.ToolCallID) != "" {
			lines = append(lines, "  tool_call_id: "+approval.Item.ToolCallID)
		}
		request := strings.TrimSpace(firstNonEmptyString(approval.Item.ToolName, approval.Request.Tool) + " " + firstNonEmptyString(approval.Item.ArgsPreview, approval.Request.Input))
		if request != "" {
			lines = append(lines, "  request: "+request)
		}
		if strings.TrimSpace(approval.Item.Risk) != "" {
			lines = append(lines, "  risk: "+approval.Item.Risk)
		} else if strings.TrimSpace(approval.Request.Risk) != "" {
			lines = append(lines, "  risk: "+approval.Request.Risk)
		}
		if strings.TrimSpace(approval.Item.CommandPreview) != "" {
			lines = append(lines, "  command: "+approval.Item.CommandPreview)
		}
		if strings.TrimSpace(approval.Item.DiffPreview) != "" {
			lines = append(lines, "  diff: "+approval.Item.DiffPreview)
		}
		if strings.TrimSpace(approval.Item.Decision) != "" {
			lines = append(lines, "  decision: "+approval.Item.Decision)
		}
		if strings.TrimSpace(approval.Item.Reason) != "" {
			lines = append(lines, "  reason: "+approval.Item.Reason)
		} else if strings.TrimSpace(approval.Request.Reason) != "" {
			lines = append(lines, "  reason: "+approval.Request.Reason)
		}
		lines = append(lines, "  commands: /approve "+approval.Task.ID+"  /deny "+approval.Task.ID)
	}
	return strings.Join(lines, "\n"), true, nil
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
	sessions, err := s.client.ListSessionsForWorkspace(ctx, s.workspace, 10)
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

func (s *DaemonSubmitter) listThreads(ctx context.Context, includeArchived bool) (string, bool, error) {
	threads, err := s.client.ListConversationThreadsWithOptions(ctx, store.ConversationThreadListOptions{
		Workspace:       s.workspace,
		Limit:           20,
		IncludeArchived: includeArchived,
	})
	if err != nil {
		return "", true, err
	}
	childThreadOwners, err := s.childThreadOwners(ctx)
	if err != nil {
		return "", true, err
	}
	if len(threads) == 0 {
		return "No conversation threads found.", true, nil
	}
	current := s.currentSessionID()
	lines := []string{"Threads:"}
	for _, thread := range threads {
		marker := " "
		if thread.ID == current {
			marker = "*"
		}
		status := "active"
		if thread.ArchivedAt != nil {
			status = "archived"
		}
		lines = append(lines, fmt.Sprintf("- %s %s [%s] %s last=%s%s%s (%s)", marker, thread.ID, status, thread.Title, emptyDash(thread.LastTaskID), threadModelSuffix(thread.ModelConfig), childThreadOwnerSuffix(childThreadOwners[thread.ID]), formatTaskTime(thread.UpdatedAt)))
	}
	return strings.Join(lines, "\n"), true, nil
}

type childThreadOwner struct {
	TaskID       string
	ParentTaskID string
	SubagentName string
	Role         string
}

func (s *DaemonSubmitter) childThreadOwners(ctx context.Context) (map[string][]childThreadOwner, error) {
	owners := map[string][]childThreadOwner{}
	if s.client == nil {
		return owners, nil
	}
	tasks, err := s.client.ListTasksForWorkspace(ctx, s.workspace, 100)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if strings.TrimSpace(task.ChildThreadID) == "" {
			continue
		}
		owners[task.ChildThreadID] = append(owners[task.ChildThreadID], childThreadOwner{
			TaskID:       task.ID,
			ParentTaskID: task.ParentTaskID,
			SubagentName: task.SubagentName,
			Role:         task.Role,
		})
	}
	return owners, nil
}

func childThreadOwnerSuffix(owners []childThreadOwner) string {
	if len(owners) == 0 {
		return ""
	}
	parts := make([]string, 0, len(owners))
	for _, owner := range owners {
		fields := []string{"via=" + owner.TaskID}
		if strings.TrimSpace(owner.ParentTaskID) != "" {
			fields = append(fields, "parent="+owner.ParentTaskID)
		}
		if strings.TrimSpace(owner.SubagentName) != "" {
			fields = append(fields, "subagent="+owner.SubagentName)
		}
		if strings.TrimSpace(owner.Role) != "" {
			fields = append(fields, "role="+owner.Role)
		}
		parts = append(parts, strings.Join(fields, ","))
	}
	return " child_of=" + strings.Join(parts, ";")
}

func (s *DaemonSubmitter) createThread(ctx context.Context, title string) (string, bool, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Usage: /thread-new <title>", true, nil
	}
	thread, err := s.client.CreateConversationThread(ctx, store.CreateConversationThreadRequest{
		Workspace: s.workspace,
		Title:     title,
	})
	if err != nil {
		return "", true, err
	}
	s.rememberSession(thread.ID)
	lines := []string{
		"Created thread " + thread.ID + ".",
		"Title: " + thread.Title,
		"Next: type a request, /spawn a background turn, or use /threads to switch.",
	}
	return strings.Join(lines, "\n"), true, nil
}

func threadModelSuffix(config *store.ThreadModelConfig) string {
	if config == nil {
		return ""
	}
	return formatThreadModelSuffix(config.Provider, config.Model, config.BaseURL, config.Profile, config.InheritedFromThreadID)
}

func taskThreadModelSuffix(config *taskpkg.ThreadModelConfig) string {
	if config == nil {
		return ""
	}
	return formatThreadModelSuffix(config.Provider, config.Model, config.BaseURL, config.Profile, config.InheritedFromThreadID)
}

func formatThreadModelDetails(config *store.ThreadModelConfig) []string {
	if config == nil {
		return []string{"Model: default"}
	}
	lines := []string{
		"Provider: " + emptyDash(config.Provider),
		"Model: " + emptyDash(config.Model),
	}
	if strings.TrimSpace(config.Profile) != "" {
		lines = append(lines, "Profile: "+config.Profile)
	}
	if strings.TrimSpace(config.BaseURL) != "" {
		lines = append(lines, "Base URL: "+config.BaseURL)
	}
	if strings.TrimSpace(config.InheritedFromThreadID) != "" {
		lines = append(lines, "Inherits: "+config.InheritedFromThreadID)
	}
	return lines
}

func formatThreadModelSuffix(provider string, model string, baseURL string, profile string, inheritedFromThreadID string) string {
	parts := []string{}
	if strings.TrimSpace(inheritedFromThreadID) != "" {
		parts = append(parts, "inherits="+inheritedFromThreadID)
	}
	if strings.TrimSpace(provider) != "" || strings.TrimSpace(model) != "" {
		parts = append(parts, "model="+strings.Trim(strings.TrimSpace(provider)+"/"+strings.TrimSpace(model), "/"))
	}
	if strings.TrimSpace(profile) != "" {
		parts = append(parts, "profile="+profile)
	}
	if strings.TrimSpace(baseURL) != "" {
		parts = append(parts, "base_url="+baseURL)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func (s *DaemonSubmitter) switchThread(ctx context.Context, threadID string) (string, bool, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "Usage: /thread <thread_id>", true, nil
	}
	thread, err := s.client.GetConversationThread(ctx, threadID)
	if err != nil {
		return "", true, err
	}
	if thread.Workspace != s.workspace {
		return fmt.Sprintf("Thread %s belongs to workspace %s.", thread.ID, thread.Workspace), true, nil
	}
	if thread.ArchivedAt != nil {
		return fmt.Sprintf("Thread %s is archived. Use /thread-unarchive %s before switching.", thread.ID, thread.ID), true, nil
	}
	s.rememberSession(thread.ID)
	if thread.LastTaskID != "" {
		s.rememberTask(thread.LastTaskID)
	}
	lines := []string{
		"Switched thread " + thread.ID + ".",
		"Title: " + thread.Title,
	}
	if suffix := strings.TrimSpace(threadModelSuffix(thread.ModelConfig)); suffix != "" {
		lines = append(lines, suffix)
	}
	lines = append(lines, "Next: type a request, or use /timeline to inspect this thread.")
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) threadSend(ctx context.Context, args string) (string, bool, error) {
	targetID, message, ok := strings.Cut(strings.TrimSpace(args), " ")
	targetID = strings.TrimSpace(targetID)
	message = strings.TrimSpace(message)
	if !ok || targetID == "" || message == "" {
		return "Usage: /thread-send <thread_id> <message>", true, nil
	}
	fromID := s.currentSessionID()
	if fromID == "" {
		return "No current thread. Use /thread <thread_id> first.", true, nil
	}
	fromThread, err := s.client.GetConversationThread(ctx, fromID)
	if err != nil {
		return "", true, err
	}
	if fromThread.Workspace != s.workspace {
		return fmt.Sprintf("Current thread %s belongs to workspace %s.", fromThread.ID, fromThread.Workspace), true, nil
	}
	targetThread, err := s.client.GetConversationThread(ctx, targetID)
	if err != nil {
		return "", true, err
	}
	if targetThread.Workspace != s.workspace {
		return fmt.Sprintf("Thread %s belongs to workspace %s; /thread-send only sends within the current workspace.", targetThread.ID, targetThread.Workspace), true, nil
	}
	summary := summarizeThreadMessage(message)
	request := store.CreateCrossThreadMessageRequest{
		FromThreadID:    fromThread.ID,
		ToThreadID:      targetThread.ID,
		TaskID:          s.lastTask(),
		Summary:         summary,
		ExplicitContent: message,
	}
	created, err := s.client.CreateCrossThreadMessage(ctx, targetThread.ID, request)
	if err != nil {
		return "", true, err
	}
	return strings.Join([]string{
		"Sent thread message " + created.ID + ".",
		"From: " + created.FromThreadID,
		"To: " + created.ToThreadID,
		"Summary: " + created.Summary,
		"Next: use /thread-inbox " + created.ToThreadID + " to inspect the handoff.",
	}, "\n"), true, nil
}

func (s *DaemonSubmitter) threadInbox(ctx context.Context, threadID string) (string, bool, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		threadID = s.currentSessionID()
	}
	if threadID == "" {
		return "Usage: /thread-inbox [thread_id]", true, nil
	}
	thread, err := s.client.GetConversationThread(ctx, threadID)
	if err != nil {
		return "", true, err
	}
	if thread.Workspace != s.workspace {
		return fmt.Sprintf("Thread %s belongs to workspace %s.", thread.ID, thread.Workspace), true, nil
	}
	messages, err := s.client.ListCrossThreadMessages(ctx, thread.ID, 20)
	if err != nil {
		return "", true, err
	}
	if len(messages) == 0 {
		return "Thread inbox " + thread.ID + ": empty.", true, nil
	}
	lines := []string{"Thread inbox " + thread.ID + ":"}
	for _, message := range messages {
		summary := strings.TrimSpace(message.Summary)
		if summary == "" {
			summary = summarizeThreadMessage(message.Content)
		}
		lines = append(lines, fmt.Sprintf("- %s from=%s task=%s %s (%s)", message.ID, message.FromThreadID, emptyDash(message.TaskID), summary, formatTaskTime(message.CreatedAt)))
	}
	return strings.Join(lines, "\n"), true, nil
}

func summarizeThreadMessage(message string) string {
	words := strings.Fields(strings.TrimSpace(message))
	summary := strings.Join(words, " ")
	if summary == "" {
		return ""
	}
	runes := []rune(summary)
	if len(runes) > 96 {
		return string(runes[:93]) + "..."
	}
	return summary
}

func (s *DaemonSubmitter) renameThread(ctx context.Context, args string) (string, bool, error) {
	threadID, title, ok := strings.Cut(strings.TrimSpace(args), " ")
	if !ok || strings.TrimSpace(threadID) == "" || strings.TrimSpace(title) == "" {
		return "Usage: /thread-rename <thread_id> <title>", true, nil
	}
	thread, err := s.client.UpdateConversationThread(ctx, threadID, store.UpdateConversationThreadRequest{Title: &title})
	if err != nil {
		return "", true, err
	}
	return "Renamed thread " + thread.ID + ": " + thread.Title, true, nil
}

func (s *DaemonSubmitter) archiveThread(ctx context.Context, threadID string, archived bool) (string, bool, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		if archived {
			return "Usage: /thread-archive <thread_id>", true, nil
		}
		return "Usage: /thread-unarchive <thread_id>", true, nil
	}
	thread, err := s.client.UpdateConversationThread(ctx, threadID, store.UpdateConversationThreadRequest{Archived: &archived})
	if err != nil {
		return "", true, err
	}
	if archived && s.currentSessionID() == thread.ID {
		s.startNewSession()
	}
	action := "Unarchived"
	if archived {
		action = "Archived"
	}
	return fmt.Sprintf("%s thread %s: %s", action, thread.ID, thread.Title), true, nil
}

func (s *DaemonSubmitter) showWorkbench(ctx context.Context) (string, bool, error) {
	if s.client == nil {
		return "No daemon client.", true, nil
	}
	workbench, err := s.client.Workbench(ctx, s.workspace, 50)
	if err != nil {
		return "", true, err
	}
	current := s.currentSessionID()
	lines := []string{"Workbench " + s.workspace}
	lines = append(lines, "Threads:")
	if len(workbench.Threads) == 0 {
		lines = append(lines, "- none")
	} else {
		for _, thread := range workbench.Threads {
			marker := " "
			if thread.ID == current {
				marker = "*"
			}
			lines = append(lines, fmt.Sprintf("- %s %s [%s] %s last=%s%s", marker, thread.ID, thread.Lifecycle, thread.Title, emptyDash(thread.LastTaskID), taskThreadModelSuffix(thread.ModelConfig)))
		}
	}
	lines = append(lines, "Sessions:")
	if len(workbench.Sessions) == 0 {
		lines = append(lines, "- none")
	} else {
		for _, session := range workbench.Sessions {
			marker := " "
			if session.ID == current {
				marker = "*"
			}
			lines = append(lines, fmt.Sprintf("- %s %s %s last=%s (%s)", marker, session.ID, session.Title, emptyDash(session.LastTaskID), formatTaskTime(session.UpdatedAt)))
		}
	}
	lines = append(lines, "Active tasks:")
	if len(workbench.ActiveTasks) == 0 {
		lines = append(lines, "- none")
	} else {
		for _, task := range workbench.ActiveTasks {
			lines = append(lines, fmt.Sprintf("- %s [%s] %s session=%s", task.ID, task.Status, task.Title, emptyDash(task.SessionID)))
		}
	}
	lines = append(lines, "Pending approvals:")
	if len(workbench.PendingApprovals) == 0 {
		lines = append(lines, "- none")
	} else {
		for _, approval := range workbench.PendingApprovals {
			request := strings.TrimSpace(firstNonEmptyString(approval.Item.ToolName, approval.Request.Tool) + " " + firstNonEmptyString(approval.Item.ArgsPreview, approval.Request.Input))
			if request == "" {
				request = approval.Task.Title
			}
			lines = append(lines, fmt.Sprintf("- %s %s", approval.Task.ID, request))
		}
	}
	lines = append(lines, "Pending user input:")
	if len(workbench.PendingUserInputs) == 0 {
		lines = append(lines, "- none")
	} else {
		for _, input := range workbench.PendingUserInputs {
			request := strings.TrimSpace(input.Request.Message)
			if request == "" {
				request = input.Task.Title
			}
			lines = append(lines, fmt.Sprintf("- %s %s", input.Task.ID, request))
		}
	}
	lines = append(lines, "Queued tasks:")
	if len(workbench.QueuedTasks) == 0 {
		lines = append(lines, "- none")
	} else {
		for _, task := range workbench.QueuedTasks {
			lines = append(lines, fmt.Sprintf("- %s %s session=%s", task.ID, task.Title, emptyDash(task.SessionID)))
		}
	}
	lines = append(lines, "Background unfinished:")
	appendWorkbenchTaskList(&lines, workbench.BackgroundUnfinishedTasks)
	lines = append(lines, "Background lost:")
	appendWorkbenchTaskList(&lines, workbench.BackgroundLostTasks)
	lines = append(lines, "Background completed:")
	appendWorkbenchTaskList(&lines, workbench.BackgroundCompletedTasks)
	lines = append(lines, "Background outputs:")
	if len(workbench.BackgroundOutputs) == 0 {
		lines = append(lines, "- none")
	} else {
		for _, output := range workbench.BackgroundOutputs {
			preview := strings.TrimSpace(output.Output)
			if preview == "" {
				preview = "(no output yet)"
			}
			lines = append(lines, fmt.Sprintf("- %s [%s] %s", output.TaskID, output.Status, firstLine(preview)))
			if strings.TrimSpace(output.ArtifactTailHint) != "" {
				lines = append(lines, "  "+output.ArtifactTailHint)
			}
		}
	}
	lines = append(lines, "Recent tasks:")
	if len(workbench.RecentTasks) == 0 {
		lines = append(lines, "- none")
	} else {
		limit := len(workbench.RecentTasks)
		if limit > 8 {
			limit = 8
		}
		for _, task := range workbench.RecentTasks[:limit] {
			lines = append(lines, fmt.Sprintf("- %s [%s] %s (%s)", task.ID, task.Status, task.Title, formatTaskTime(task.UpdatedAt)))
		}
	}
	return strings.Join(lines, "\n"), true, nil
}

func appendWorkbenchTaskList(lines *[]string, tasks []taskpkg.Task) {
	if len(tasks) == 0 {
		*lines = append(*lines, "- none")
		return
	}
	for _, task := range tasks {
		*lines = append(*lines, fmt.Sprintf("- %s [%s] %s", task.ID, task.Status, task.Title))
	}
}

func (s *DaemonSubmitter) watchTasks(ctx context.Context, args string) (string, bool, error) {
	if s.client == nil {
		return "No daemon client.", true, nil
	}
	selection, err := s.watchTaskIDs(ctx, args)
	if err != nil {
		return "", true, err
	}
	if len(selection.TaskIDs) == 0 {
		return selection.EmptyMessage, true, nil
	}
	stream, errs := s.client.StreamTaskEvents(ctx, selection.TaskIDs)
	lines := []string{selection.Title}
	for _, taskID := range selection.TaskIDs {
		lines = append(lines, "- "+taskID)
	}
	eventCount := 0
	const maxWatchEvents = 120
	for update := range stream {
		payload, _ := eventPayload(update.Event)
		if update.Type == taskpkg.EventDiff {
			s.rememberDiff(update.TaskID, payload.Diff)
		}
		s.rememberTask(update.TaskID)
		if eventCount < maxWatchEvents {
			lines = append(lines, tui.FormatDaemonEventWatch(update.TaskID, string(update.Type), update.Event.Payload))
		}
		eventCount++
	}
	if err := <-errs; err != nil {
		if len(lines) > 1 {
			lines = append(lines, "Error: "+err.Error())
			return strings.Join(lines, "\n"), true, nil
		}
		return "", true, err
	}
	if eventCount == 0 {
		lines = append(lines, "No events received.")
	} else if eventCount > maxWatchEvents {
		lines = append(lines, fmt.Sprintf("... %d more events omitted; use /tail <task_id> to inspect history.", eventCount-maxWatchEvents))
	}
	return strings.Join(lines, "\n"), true, nil
}

type watchTaskSelection struct {
	Title        string
	EmptyMessage string
	TaskIDs      []string
}

func (s *DaemonSubmitter) watchTaskIDs(ctx context.Context, args string) (watchTaskSelection, error) {
	fields := strings.Fields(args)
	if len(fields) > 0 && fields[0] == "children" {
		if len(fields) != 2 {
			return watchTaskSelection{}, fmt.Errorf("usage: /watch children <parent_task_id>")
		}
		return s.watchChildTaskIDs(ctx, fields[1])
	}
	if len(fields) > 0 && fields[0] != "active" {
		return watchTaskSelection{
			Title:        "Watch tasks",
			EmptyMessage: "No daemon tasks selected.",
			TaskIDs:      uniqueStrings(fields),
		}, nil
	}
	tasks, err := s.client.ListTasksForWorkspace(ctx, s.workspace, 50)
	if err != nil {
		return watchTaskSelection{}, err
	}
	active := filterActiveTasks(tasks)
	taskIDs := make([]string, 0, len(active))
	for _, task := range active {
		taskIDs = append(taskIDs, task.ID)
	}
	return watchTaskSelection{
		Title:        "Watch tasks",
		EmptyMessage: "No active daemon tasks for this workspace.",
		TaskIDs:      taskIDs,
	}, nil
}

func (s *DaemonSubmitter) watchChildTaskIDs(ctx context.Context, parentTaskID string) (watchTaskSelection, error) {
	parentTaskID = strings.TrimSpace(parentTaskID)
	if parentTaskID == "" {
		return watchTaskSelection{}, fmt.Errorf("usage: /watch children <parent_task_id>")
	}
	tasks, err := s.client.ListTasksForWorkspace(ctx, s.workspace, 100)
	if err != nil {
		return watchTaskSelection{}, err
	}
	var parentFound bool
	taskIDs := []string{parentTaskID}
	for _, task := range tasks {
		if task.ID == parentTaskID {
			parentFound = true
			continue
		}
		if task.ParentTaskID == parentTaskID {
			taskIDs = append(taskIDs, task.ID)
		}
	}
	if !parentFound {
		return watchTaskSelection{}, fmt.Errorf("parent task %s was not found in this workspace", parentTaskID)
	}
	if len(taskIDs) == 1 {
		return watchTaskSelection{
			Title:        "Watch child tasks for " + parentTaskID,
			EmptyMessage: "No child daemon tasks for parent " + parentTaskID + ".",
		}, nil
	}
	return watchTaskSelection{
		Title:        "Watch child tasks for " + parentTaskID,
		EmptyMessage: "No child daemon tasks for parent " + parentTaskID + ".",
		TaskIDs:      uniqueStrings(taskIDs),
	}, nil
}

func (s *DaemonSubmitter) spawnTask(ctx context.Context, input string) (string, bool, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "Usage: /spawn <request>", true, nil
	}
	if s.client == nil {
		return "No daemon client.", true, nil
	}
	created, err := s.createTaskWithQueue(ctx, input, false)
	if err != nil {
		return "", true, err
	}
	lines := []string{
		"Spawned task " + created.Task.ID + ".",
		"Status: " + string(created.Task.Status),
		"Session: " + emptyDash(created.Task.SessionID),
		"Next: use /watch " + created.Task.ID + " to follow events, or /tail " + created.Task.ID + " after it finishes.",
	}
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) showSession(ctx context.Context) (string, bool, error) {
	sessionID := s.currentSessionID()
	if sessionID == "" {
		session, ok, err := s.resumeLatestWorkspaceSession(ctx)
		if err != nil {
			return "", true, err
		}
		if !ok {
			return "No current daemon session.", true, nil
		}
		sessionID = session.ID
	}
	return s.resumeSession(ctx, sessionID)
}

func (s *DaemonSubmitter) resumeLatest(ctx context.Context) (string, bool, error) {
	session, ok, err := s.resumeLatestWorkspaceSession(ctx)
	if err != nil {
		return "", true, err
	}
	if !ok {
		return "No previous daemon session for this workspace.", true, nil
	}
	output, err := s.sessionResumeOutput(ctx, session, "Resumed session "+session.ID+".")
	if err != nil {
		return "", true, err
	}
	return output, true, nil
}

func (s *DaemonSubmitter) newSessionCommand() (string, bool, error) {
	s.startNewSession()
	return strings.Join([]string{
		"New session will be created for the next task.",
		"Next: type a request, or use /resume-latest to reattach the latest workspace session.",
	}, "\n"), true, nil
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
	output, err := s.sessionResumeOutput(ctx, session, fmt.Sprintf("Session %s", session.ID))
	if err != nil {
		return "", true, err
	}
	return output, true, nil
}

func (s *DaemonSubmitter) sessionResumeOutput(ctx context.Context, session taskpkg.Session, heading string) (string, error) {
	envelope, err := s.client.SessionContext(ctx, session.ID, taskpkg.ContextRequest{ItemLimit: 20, TokenBudget: 4096})
	if err != nil {
		return "", err
	}
	tasks, err := s.client.SessionTasks(ctx, session.ID, 10)
	if err != nil {
		return "", err
	}
	s.rememberSession(session.ID)
	if session.LastTaskID != "" {
		s.rememberTask(session.LastTaskID)
	}
	var lines []string
	lines = append(lines, heading)
	lines = append(lines, "Title: "+session.Title)
	lines = append(lines, "Workspace: "+session.Workspace)
	lines = append(lines, fmt.Sprintf("Context: transcript_items=%d artifacts=%d compact_boundaries=%d truncated=%t", len(envelope.Transcript), len(envelope.ArtifactRefs), len(envelope.CompactBoundaries), envelope.Budget.Truncated))
	if len(envelope.Transcript) == 0 {
		lines = append(lines, "Transcript: none")
	} else {
		lines = append(lines, "Transcript:")
		for _, item := range envelope.Transcript {
			formatted := formatTranscriptItem(item)
			for _, line := range formatted {
				lines = append(lines, "- "+line)
			}
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
	lines = append(lines, "Next: use /transcript or continue typing a task.")
	return strings.Join(lines, "\n"), nil
}

func (s *DaemonSubmitter) cancelTask(ctx context.Context, taskID string) (string, bool, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		taskID = s.waitCurrentTask(ctx, 500*time.Millisecond)
	}
	if taskID == "" {
		return "No running daemon task.", true, nil
	}
	task, err := s.client.Cancel(ctx, taskID, "cancelled from TUI")
	if err != nil {
		return "", true, err
	}
	s.rememberTask(task.ID)
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
	lines = append(lines, tui.PatchReadyReply(diff))
	lines = append(lines, tui.PatchReviewPreview(diff, 180))
	lines = append(lines, "Next:")
	lines = append(lines, tui.PatchReadyNextAction())
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
	lines := []string{"已应用变更，真实工作区已更新。"}
	if len(result.Files) > 0 {
		lines = append(lines, "文件:")
		for _, file := range result.Files {
			lines = append(lines, "- "+file)
		}
	}
	lines = append(lines, "你可以继续让我运行测试、解释变更，或用 /timeline 查看记录。")
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) listTasks(ctx context.Context) (string, bool, error) {
	tasks, err := s.client.ListTasksForWorkspace(ctx, s.workspace, 10)
	if err != nil {
		return "", true, err
	}
	if len(tasks) == 0 {
		return "No daemon tasks found.", true, nil
	}
	var lines []string
	for _, task := range tasks {
		lines = append(lines, fmt.Sprintf("- %s [%s] %s%s (%s)", task.ID, task.Status, task.Title, taskRelationSuffix(task), formatTaskTime(task.UpdatedAt)))
	}
	return strings.Join(lines, "\n"), true, nil
}

func taskRelationSuffix(task taskpkg.Task) string {
	fields := []string{}
	if strings.TrimSpace(task.ParentTaskID) != "" {
		fields = append(fields, "parent="+task.ParentTaskID)
	}
	if strings.TrimSpace(task.ParentThreadID) != "" {
		fields = append(fields, "parent_thread="+task.ParentThreadID)
	}
	if strings.TrimSpace(task.ChildThreadID) != "" {
		fields = append(fields, "child_thread="+task.ChildThreadID)
	}
	if strings.TrimSpace(task.SubagentName) != "" {
		fields = append(fields, "subagent="+task.SubagentName)
	}
	if strings.TrimSpace(task.Role) != "" {
		fields = append(fields, "role="+task.Role)
	}
	if len(fields) == 0 {
		return ""
	}
	return " " + strings.Join(fields, " ")
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
		lines = append(lines, "- "+tui.FormatDaemonEventReplay(string(event.Type), event.Payload))
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
	s.newSession = false
}

func (s *DaemonSubmitter) currentSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

func (s *DaemonSubmitter) startNewSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = ""
	s.lastTaskID = ""
	s.lastDiff = ""
	s.awaitingInputID = ""
	s.newSession = true
}

func (s *DaemonSubmitter) shouldAutoResume() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.newSession && !s.explicitSession
}

func (s *DaemonSubmitter) resumeLatestWorkspaceSession(ctx context.Context) (taskpkg.Session, bool, error) {
	if s.client == nil || !s.shouldAutoResume() {
		return taskpkg.Session{}, false, nil
	}
	sessions, err := s.client.ListSessionsForWorkspace(ctx, s.workspace, 50)
	if err != nil {
		return taskpkg.Session{}, false, err
	}
	if len(sessions) > 0 {
		session := sessions[0]
		s.rememberSession(session.ID)
		if session.LastTaskID != "" {
			s.rememberTask(session.LastTaskID)
		}
		return session, true, nil
	}
	return taskpkg.Session{}, false, nil
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

func (s *DaemonSubmitter) setAwaitingInput(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.awaitingInputID = taskID
}

func (s *DaemonSubmitter) awaitingInputTask() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.awaitingInputID
}

func (s *DaemonSubmitter) clearAwaitingInput(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.awaitingInputID == taskID {
		s.awaitingInputID = ""
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

func filterActiveTasks(tasks []taskpkg.Task) []taskpkg.Task {
	var active []taskpkg.Task
	for _, task := range tasks {
		switch task.Status {
		case taskpkg.StatusDraft, taskpkg.StatusQueued, taskpkg.StatusPlanning, taskpkg.StatusRunning, taskpkg.StatusWaitingUser:
			active = append(active, task)
		}
	}
	return active
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func emptyDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
