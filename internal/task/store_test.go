package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/store"
)

func TestRepositoryCreatesListsAndReadsTaskEvents(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "看看目录",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Status != StatusDraft || !strings.Contains(created.Title, "看看目录") {
		t.Fatalf("unexpected created task %#v", created)
	}
	if created.SessionID == "" {
		t.Fatalf("expected task session id, got %#v", created)
	}

	if err := repo.AppendEvent(t.Context(), created.ID, EventPlanReady, EventPayload{Steps: "list ."}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), created.ID, StatusRunning); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), created.ID, StatusCompleted); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted || got.CompletedAt == nil {
		t.Fatalf("unexpected task after status update %#v", got)
	}

	tasks, err := repo.List(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != created.ID {
		t.Fatalf("unexpected task list %#v", tasks)
	}

	events, err := repo.Events(t.Context(), created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != EventPlanReady || !strings.Contains(events[0].Payload, "list .") {
		t.Fatalf("unexpected events %#v", events)
	}
}

func TestRepositoryPersistsTaskThreadRelationMetadata(t *testing.T) {
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	workspace := t.TempDir()
	parentThread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{
		Workspace: workspace,
		Title:     "Parent thread",
	})
	if err != nil {
		t.Fatal(err)
	}
	childThread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{
		Workspace: workspace,
		Title:     "Child thread",
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "parent task",
		Scope: TaskScope{
			Paths:           []string{workspace},
			NetworkHosts:    []string{"api.internal"},
			MCPServers:      []string{"filesystem"},
			MCPTools:        []string{"filesystem.read"},
			ApprovalActions: []string{"apply_patch"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	child, err := repo.Create(t.Context(), CreateRequest{
		Workspace:      workspace,
		Prompt:         "child task",
		ParentTaskID:   parent.ID,
		ParentThreadID: " " + parentThread.ID + " ",
		ChildThreadID:  " " + childThread.ID + " ",
		SubagentName:   " review-worker ",
		Role:           " reviewer ",
		Scope:          TaskScope{Paths: []string{workspace + "/src"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentThreadID != parentThread.ID || child.ChildThreadID != childThread.ID {
		t.Fatalf("expected normalized thread relation ids, got %#v", child)
	}
	if child.SubagentName != "review-worker" || child.Role != "reviewer" {
		t.Fatalf("expected normalized subagent metadata, got %#v", child)
	}

	got, err := repo.Get(t.Context(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ParentThreadID != parentThread.ID || got.ChildThreadID != childThread.ID || got.SubagentName != "review-worker" || got.Role != "reviewer" {
		t.Fatalf("expected get to round-trip task relation metadata, got %#v", got)
	}
	listed, err := repo.ListByWorkspace(t.Context(), workspace, 10)
	if err != nil {
		t.Fatal(err)
	}
	var listedChild Task
	for _, item := range listed {
		if item.ID == child.ID {
			listedChild = item
			break
		}
	}
	if listedChild.ID == "" || listedChild.ParentThreadID != parentThread.ID || listedChild.ChildThreadID != childThread.ID {
		t.Fatalf("expected list to round-trip task relation metadata, got %#v", listed)
	}
	var parentThreadID, childThreadID, subagentName, role string
	if err := db.QueryRow(`
		SELECT parent_thread_id, child_thread_id, subagent_name, role
		FROM tasks
		WHERE id = ?
	`, child.ID).Scan(&parentThreadID, &childThreadID, &subagentName, &role); err != nil {
		t.Fatal(err)
	}
	if parentThreadID != parentThread.ID || childThreadID != childThread.ID || subagentName != "review-worker" || role != "reviewer" {
		t.Fatalf("unexpected raw relation row parent=%q child=%q subagent=%q role=%q", parentThreadID, childThreadID, subagentName, role)
	}
}

func TestRepositoryRejectsInvalidTaskThreadRelations(t *testing.T) {
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	workspace := t.TempDir()
	parentThread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{
		Workspace: workspace,
		Title:     "Parent thread",
	})
	if err != nil {
		t.Fatal(err)
	}
	otherThread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{
		Workspace: t.TempDir(),
		Title:     "Other thread",
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "parent scope",
		Scope:     TaskScope{NetworkHosts: []string{"api.internal"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := repo.ListByWorkspace(t.Context(), workspace, 20)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		request CreateRequest
		want    string
	}{
		{
			name: "missing parent thread",
			request: CreateRequest{
				Workspace:      workspace,
				Prompt:         "missing parent thread",
				ParentTaskID:   parent.ID,
				ParentThreadID: "thread_missing",
			},
			want: "parent_thread_id",
		},
		{
			name: "cross workspace child thread",
			request: CreateRequest{
				Workspace:      workspace,
				Prompt:         "cross child thread",
				ParentTaskID:   parent.ID,
				ParentThreadID: parentThread.ID,
				ChildThreadID:  otherThread.ID,
			},
			want: "child_thread_id",
		},
		{
			name: "oversized subagent name",
			request: CreateRequest{
				Workspace:    workspace,
				Prompt:       "oversized subagent",
				ParentTaskID: parent.ID,
				SubagentName: strings.Repeat("x", 65),
			},
			want: "subagent_name",
		},
		{
			name: "scope outside parent",
			request: CreateRequest{
				Workspace:    workspace,
				Prompt:       "outside scope",
				ParentTaskID: parent.ID,
				Scope:        TaskScope{NetworkHosts: []string{"public.example.com"}},
			},
			want: "outside parent scope",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repo.Create(t.Context(), tc.request)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
	after, err := repo.ListByWorkspace(t.Context(), workspace, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("invalid relation requests should not insert tasks, before=%d after=%d", len(before), len(after))
	}
}

func TestEventCatalogCoversFirstClassEventFamilies(t *testing.T) {
	definitions := EventDefinitions()
	families := map[EventFamily]bool{}
	for _, definition := range definitions {
		if definition.Type == "" {
			t.Fatalf("event definition has empty type: %#v", definition)
		}
		families[definition.Family] = true
		if _, ok := EventDefinitionFor(definition.Type); !ok {
			t.Fatalf("missing lookup for event definition %#v", definition)
		}
	}
	for _, want := range []EventFamily{
		EventFamilyTool,
		EventFamilyTodo,
		EventFamilyTranscript,
		EventFamilyArtifact,
		EventFamilyContext,
		EventFamilyApproval,
		EventFamilyHook,
		EventFamilySchedule,
		EventFamilySubagent,
	} {
		if !families[want] {
			t.Fatalf("expected event catalog to include family %q in %#v", want, definitions)
		}
	}
}

func TestRepositoryRejectsUnknownOrMalformedFirstClassEvents(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "event validation",
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		taskID    string
		eventType EventType
		payload   EventPayload
		want      string
	}{
		{name: "blank task", taskID: "", eventType: EventSummary, payload: EventPayload{Message: "ok"}, want: "task id is required"},
		{name: "unknown type", taskID: created.ID, eventType: EventType("custom.shadow"), payload: EventPayload{Message: "ok"}, want: "unknown event type"},
		{name: "tool missing name", taskID: created.ID, eventType: EventToolResult, payload: EventPayload{Output: "ok"}, want: "payload.tool"},
		{name: "first class missing narrative", taskID: created.ID, eventType: EventHookRun, payload: EventPayload{}, want: "requires payload.message"},
		{name: "compact missing narrative", taskID: created.ID, eventType: EventCompactBoundary, payload: EventPayload{}, want: "requires payload.message"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := repo.AppendEvent(t.Context(), tc.taskID, tc.eventType, tc.payload)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestRepositoryPersistsFirstClassEventsAndProjectsTimeline(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "event projection",
	})
	if err != nil {
		t.Fatal(err)
	}
	records := []struct {
		eventType EventType
		payload   EventPayload
		kind      string
		content   string
	}{
		{EventTodoUpdated, EventPayload{ID: "todo_1", Action: "complete", Message: "write tests", Status: "done"}, "todo", "write tests"},
		{EventTranscriptEntry, EventPayload{Kind: "assistant", Message: "summary persisted"}, "transcript", "summary persisted"},
		{EventArtifactReference, EventPayload{Path: ".liora/tool-results/result.txt", Tool: "shell", Message: "full shell output"}, "artifact", "full shell output"},
		{EventCompactBoundary, EventPayload{Message: "compact after long tool output", TokenEstimate: 4000}, "compact_boundary", "compact after long tool output"},
		{EventHookRun, EventPayload{Action: "PreToolUse", Message: "checked command", Status: "ok"}, "hook", "checked command"},
		{EventScheduleTriggered, EventPayload{ID: "schedule_1", Message: "nightly audit", Trigger: "0 2 * * *"}, "schedule", "nightly audit"},
		{EventSubagentStarted, EventPayload{ID: "agent_1", ParentTaskID: created.ID, Message: "review started"}, "subagent", "review started"},
	}
	for _, record := range records {
		if err := repo.AppendEvent(t.Context(), created.ID, record.eventType, record.payload); err != nil {
			t.Fatalf("append %s: %v", record.eventType, err)
		}
	}
	events, err := repo.Events(t.Context(), created.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != len(records) {
		t.Fatalf("expected %d events, got %#v", len(records), events)
	}
	timeline, err := repo.Timeline(t.Context(), created.SessionID, 20)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	var combined strings.Builder
	for _, item := range timeline {
		kinds[item.Kind] = true
		combined.WriteString(item.Content)
		combined.WriteByte('\n')
	}
	for _, record := range records {
		if !kinds[record.kind] {
			t.Fatalf("expected timeline kind %q in %#v", record.kind, timeline)
		}
		if !strings.Contains(combined.String(), record.content) {
			t.Fatalf("expected timeline content %q in %#v", record.content, timeline)
		}
	}
}

func TestRepositoryMaterializesTranscriptEntriesForFirstClassTimelineKinds(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "materialize transcript",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendMessage(t.Context(), created.SessionID, "assistant", "assistant summary", created.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolCall, EventPayload{Tool: "shell", ToolCallID: "call-materialized-1", Input: "go test"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{Tool: "shell", ToolCallID: "call-materialized-1", ToolResultID: "call-materialized-1-result", Input: "go test", Output: "ok", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.WriteTodos(t.Context(), TodoWriteRequest{
		SessionID:    created.SessionID,
		SourceTaskID: created.ID,
		Todos:        []TodoWriteItem{{ID: "todo_materialized", Content: "write transcript test", Status: TodoStatusInProgress, Priority: TodoPriorityHigh}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, record := range []struct {
		eventType EventType
		payload   EventPayload
	}{
		{EventDiff, EventPayload{Diff: "diff --git a/file b/file"}},
		{EventPermissionRequest, EventPayload{Tool: "shell", Message: "approval needed", Status: string(StatusWaitingUser), Risk: "write", Reason: "edits workspace"}},
		{EventHookRun, EventPayload{Action: "PostToolUse", Message: "hook ok", Status: "ok"}},
		{EventScheduleTriggered, EventPayload{ID: "schedule_materialized", Message: "nightly audit", Trigger: "0 2 * * *", Status: "triggered"}},
		{EventCompactBoundary, EventPayload{Message: "compacted for restart", TokenBudget: 128, TokenEstimate: 512}},
	} {
		if err := repo.AppendEvent(t.Context(), created.ID, record.eventType, record.payload); err != nil {
			t.Fatalf("append %s: %v", record.eventType, err)
		}
	}

	entries, err := repo.TranscriptEntries(t.Context(), created.SessionID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var sawUser, sawAssistant bool
	var toolCall, toolResult TimelineItem
	kinds := map[string]bool{}
	var combined strings.Builder
	for _, entry := range entries {
		if entry.Kind == "message" && entry.Role == "user" && strings.Contains(entry.Content, "materialize transcript") {
			sawUser = true
		}
		if entry.Kind == "message" && entry.Role == "assistant" && strings.Contains(entry.Content, "assistant summary") {
			sawAssistant = true
		}
		if entry.Kind == "tool_call" {
			toolCall = entry
		}
		if entry.Kind == "tool_result" {
			toolResult = entry
		}
		kinds[entry.Kind] = true
		combined.WriteString(entry.Content)
		combined.WriteString(entry.Input)
		combined.WriteString(entry.Output)
		combined.WriteString(entry.Diff)
		combined.WriteString(entry.Reason)
		combined.WriteByte('\n')
	}
	if !sawUser || !sawAssistant {
		t.Fatalf("expected user and assistant transcript entries, got %#v", entries)
	}
	if toolCall.ToolCallID != "call-materialized-1" || toolResult.ToolCallID != toolCall.ToolCallID || toolResult.ToolResultID != "call-materialized-1-result" {
		t.Fatalf("expected materialized tool pair ids to survive transcript projection, call=%#v result=%#v", toolCall, toolResult)
	}
	for _, want := range []string{"tool_call", "tool_result", "todo", "diff", "approval", "hook", "schedule", "compact_boundary"} {
		if !kinds[want] {
			t.Fatalf("expected materialized transcript kind %q in %#v", want, entries)
		}
	}
	for _, want := range []string{"go test", "ok", "write transcript test", "diff --git", "approval needed", "hook ok", "nightly audit", "compacted for restart"} {
		if !strings.Contains(combined.String(), want) {
			t.Fatalf("expected materialized content %q in %#v", want, entries)
		}
	}
	timeline, err := repo.Timeline(t.Context(), created.SessionID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != len(entries) {
		t.Fatalf("expected timeline to use materialized entries, got timeline=%d entries=%d", len(timeline), len(entries))
	}
}

func TestRepositoryRejectsMalformedTranscriptMaterializationWithoutPartialRows(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "transcript rollback",
	})
	if err != nil {
		t.Fatal(err)
	}
	before := transcriptEntryCount(t, db, created.SessionID)
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{Output: "missing tool"}); err == nil {
		t.Fatalf("expected malformed tool result to fail")
	}
	after := transcriptEntryCount(t, db, created.SessionID)
	if after != before {
		t.Fatalf("expected no partial transcript rows, before=%d after=%d", before, after)
	}
	empty, err := repo.TranscriptEntries(t.Context(), "missing_session", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected missing session to have no entries, got %#v", empty)
	}
}

func TestRepositoryTranscriptProjectionDrivesContextSearchAndExportWhileEventsRemainFacts(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	workspace := t.TempDir()
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "stable transcript projection",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{
		Tool:   "shell",
		Input:  "generate stable projection",
		Output: "stable projection output for resume search export",
		Status: "ok",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventCompactBoundary, EventPayload{Message: "stable compact boundary", TokenEstimate: 32}); err != nil {
		t.Fatal(err)
	}

	events, err := repo.Events(t.Context(), created.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !containsEventType(eventTypes(events), EventToolResult) || !containsEventType(eventTypes(events), EventCompactBoundary) {
		t.Fatalf("expected raw task_events facts to remain, got %#v", events)
	}
	if _, err := db.Exec(`UPDATE task_events SET payload_json = '{malformed raw event payload}' WHERE task_id = ?`, created.ID); err != nil {
		t.Fatal(err)
	}
	rawEvents, err := repo.Events(t.Context(), created.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rawEvents[0].Payload+rawEvents[len(rawEvents)-1].Payload, "malformed raw event payload") {
		t.Fatalf("expected raw task_events to keep mutated fact payloads, got %#v", rawEvents)
	}

	entries, err := repo.TranscriptEntries(t.Context(), created.SessionID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := timelineItemsText(entries); !strings.Contains(got, "stable projection output") || strings.Contains(got, "malformed raw event payload") {
		t.Fatalf("expected transcript export projection without raw payload leak, got %q", got)
	}
	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 20, TokenBudget: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if got := timelineItemsText(envelope.Transcript); !strings.Contains(got, "stable projection output") || !strings.Contains(got, "stable compact boundary") || strings.Contains(got, "malformed raw event payload") {
		t.Fatalf("expected context to use transcript projection, got %q", got)
	}
	matches, err := repo.SearchTimeline(t.Context(), workspace, "stable projection output", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected search to find materialized transcript projection")
	}
	rawMatches, err := repo.SearchTimeline(t.Context(), workspace, "malformed raw event payload", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rawMatches) != 0 {
		t.Fatalf("expected search to ignore corrupted raw task_events payloads, got %#v", rawMatches)
	}
}

func TestRepositoryMaterializesAndResolvesApprovalItems(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{Workspace: t.TempDir(), Prompt: "approval item", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), created.ID, StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventPermissionRequest, EventPayload{
		Tool:       "run",
		ToolCallID: "toolcall-approval-1",
		Input:      "rm -rf build",
		Diff:       "+dangerous edit",
		Risk:       "dangerous_shell",
		Reason:     "Command contains rm -rf.",
		Status:     string(StatusWaitingUser),
	}); err != nil {
		t.Fatal(err)
	}
	item, ok, err := repo.ApprovalItemForTask(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected approval item")
	}
	if item.TaskID != created.ID || item.ToolCallID != "toolcall-approval-1" || item.ToolName != "run" || item.ArgsPreview != "rm -rf build" || item.CommandPreview != "rm -rf build" || item.DiffPreview != "+dangerous edit" || item.Risk != "dangerous_shell" || item.Status != "pending" || item.Decision != "" || item.ResolvedAt != nil {
		t.Fatalf("unexpected pending approval item %#v", item)
	}
	if err := repo.GrantApproval(t.Context(), created.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	resolved, ok, err := repo.ApprovalItemForTask(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || resolved.Status != "resolved" || resolved.Decision != "approved" || resolved.DecidedBy != "tester" || resolved.ResolvedAt == nil {
		t.Fatalf("unexpected resolved approval item %#v ok=%v", resolved, ok)
	}
}

func TestRepositorySearchTimelineUsesMaterializedTranscriptStructuredColumns(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	workspace := t.TempDir()
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "projection search structured",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendMessage(t.Context(), created.SessionID, "assistant", "assistant structured search summary", created.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolCall, EventPayload{Tool: "shell", Input: "structured search tool call"}); err != nil {
		t.Fatal(err)
	}
	artifactURI := "artifact://search/session/" + created.SessionID + "/tool-result.log"
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{Tool: "shell", Input: "structured search tool result", Output: "structured result output " + artifactURI, Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventDiff, EventPayload{Diff: "diff --git a/search b/search\n+structured diff search"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventPermissionRequest, EventPayload{Tool: "shell", Risk: "write", Message: "structured approval search"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventArtifactReference, EventPayload{Tool: "shell", Path: artifactURI, Message: "structured artifact search preview"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.WriteTodos(t.Context(), TodoWriteRequest{
		SessionID:    created.SessionID,
		SourceTaskID: created.ID,
		Todos: []TodoWriteItem{{
			ID:       "structured-todo",
			Content:  "structured todo search",
			Status:   TodoStatusInProgress,
			Priority: TodoPriorityHigh,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	assertSearchMatch := func(query string, wantKind string) {
		t.Helper()
		matches, err := repo.SearchTimeline(t.Context(), workspace, query, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range matches {
			if match.Kind == wantKind {
				return
			}
		}
		t.Fatalf("expected query %q to match kind %q in %#v", query, wantKind, matches)
	}
	assertSearchMatch("assistant structured search summary", "message")
	assertSearchMatch("structured search tool call", "tool_call")
	assertSearchMatch("structured result output", "tool_result")
	assertSearchMatch("structured diff search", "diff")
	assertSearchMatch("structured approval search", "approval")
	assertSearchMatch(artifactURI, "artifact")
	assertSearchMatch("structured todo search", "todo")

	escapedPrompt, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "literal 100%_needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	escapedMatches, err := repo.SearchTimeline(t.Context(), workspace, "100%_needle", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(escapedMatches) != 1 || escapedMatches[0].TaskID != escapedPrompt.ID {
		t.Fatalf("expected escaped LIKE query to match only literal prompt task %s, got %#v", escapedPrompt.ID, escapedMatches)
	}
}

func TestRepositorySearchTimelineIgnoresCorruptedRawEventsAndKeepsSessionIsolation(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	workspace := t.TempDir()
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "projection search raw event isolation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{
		Tool:   "shell",
		Input:  "generate projection-only result",
		Output: "projection-only searchable output",
		Status: "ok",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE task_events SET payload_json = '{malformed raw search payload}' WHERE task_id = ?`, created.ID); err != nil {
		t.Fatal(err)
	}

	matches, err := repo.SearchTimeline(t.Context(), workspace, "projection-only searchable output", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected search to find materialized projection after raw events were corrupted")
	}
	rawMatches, err := repo.SearchTimeline(t.Context(), workspace, "malformed raw search payload", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rawMatches) != 0 {
		t.Fatalf("expected search to ignore corrupted raw event payloads, got %#v", rawMatches)
	}
	empty, err := repo.CreateSession(t.Context(), CreateSessionRequest{Workspace: workspace, Title: "empty search session"})
	if err != nil {
		t.Fatal(err)
	}
	emptyTimeline, err := repo.TranscriptEntries(t.Context(), empty.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyTimeline) != 0 {
		t.Fatalf("expected empty session to have no transcript entries, got %#v", emptyTimeline)
	}
	otherWorkspace := t.TempDir()
	otherMatches, err := repo.SearchTimeline(t.Context(), otherWorkspace, "projection-only searchable output", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherMatches) != 0 {
		t.Fatalf("expected workspace-scoped search not to leak restored content, got %#v", otherMatches)
	}
}

func TestRepositoryTranscriptArtifactReferenceAvoidsInliningLongOutput(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "artifact transcript",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactURI := "artifact://artifacts/sessions/" + created.SessionID + "/tasks/" + created.ID + "/tool-results/out.txt"
	longOutput := "preview line with " + artifactURI + "\n" + strings.Repeat("large artifact body ", 800) + "TAIL-SHOULD-NOT-BE-IN-TRANSCRIPT"
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{Tool: "shell", Input: "produce large artifact", Output: longOutput, Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventArtifactReference, EventPayload{Tool: "shell", Path: artifactURI, Message: "full output artifact"}); err != nil {
		t.Fatal(err)
	}

	entries, err := repo.TranscriptEntries(t.Context(), created.SessionID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var toolResult TimelineItem
	var artifactFound bool
	for _, entry := range entries {
		if entry.Kind == "tool_result" {
			toolResult = entry
		}
		if entry.Kind == "artifact" && entry.Target == artifactURI {
			artifactFound = true
		}
	}
	if toolResult.Kind == "" {
		t.Fatalf("expected materialized tool_result in %#v", entries)
	}
	if toolResult.Target != artifactURI {
		t.Fatalf("expected tool_result target artifact URI %q, got %#v", artifactURI, toolResult)
	}
	if strings.Contains(toolResult.Output, "TAIL-SHOULD-NOT-BE-IN-TRANSCRIPT") || len([]rune(toolResult.Output)) > maxInlineContextFieldRunes+64 {
		t.Fatalf("expected bounded transcript output, got %d runes: %q", len([]rune(toolResult.Output)), toolResult.Output)
	}
	if !artifactFound {
		t.Fatalf("expected artifact transcript entry with URI %q in %#v", artifactURI, entries)
	}
	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 20, TokenBudget: 4096})
	if err != nil {
		t.Fatal(err)
	}
	var refFound bool
	for _, ref := range envelope.ArtifactRefs {
		if ref.Path == artifactURI {
			refFound = true
		}
	}
	if !refFound || strings.Contains(timelineItemsText(envelope.Transcript), "TAIL-SHOULD-NOT-BE-IN-TRANSCRIPT") {
		t.Fatalf("expected context artifact ref without long tail, refs=%#v transcript=%#v", envelope.ArtifactRefs, envelope.Transcript)
	}
}

func transcriptEntryCount(t *testing.T, db *sql.DB, sessionID string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_entries WHERE session_id = ?`, sessionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestRepositoryContextEnvelopeBoundsTranscriptArtifactsAndCompactBoundary(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "context boundary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendMessage(t.Context(), created.SessionID, "assistant", "short summary for later resume", created.ID); err != nil {
		t.Fatal(err)
	}
	longOutput := strings.Repeat("large output ", 700) + "\nfull output: .liora/tool-results/task-output.txt"
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{Tool: "shell", Input: "generate", Output: longOutput, Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventArtifactReference, EventPayload{Tool: "shell", Path: ".liora/tool-results/task-output.txt", Message: "complete generated output"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventCompactBoundary, EventPayload{Message: "compacted before next turn", TokenEstimate: 4096}); err != nil {
		t.Fatal(err)
	}

	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 3, TokenBudget: 128})
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Session.ID != created.SessionID {
		t.Fatalf("unexpected context session %#v", envelope.Session)
	}
	if len(envelope.Transcript) > 3 || envelope.Budget.ItemLimit != 3 || !envelope.Budget.Truncated {
		t.Fatalf("expected bounded truncated transcript, got %#v", envelope.Budget)
	}
	if len(envelope.ArtifactRefs) == 0 {
		t.Fatalf("expected artifact refs in %#v", envelope)
	}
	var foundArtifact, foundBoundary bool
	for _, ref := range envelope.ArtifactRefs {
		if strings.Contains(ref.Path, ".liora/tool-results") {
			foundArtifact = true
		}
	}
	for _, boundary := range envelope.CompactBoundaries {
		if strings.Contains(boundary.Summary, "compacted") {
			foundBoundary = true
		}
	}
	if !foundArtifact || !foundBoundary {
		t.Fatalf("expected artifact and compact boundary, got %#v", envelope)
	}
	for _, item := range envelope.Transcript {
		if len([]rune(item.Output)) > maxInlineContextFieldRunes+64 {
			t.Fatalf("tool output was not compacted: %d runes", len([]rune(item.Output)))
		}
	}
}

func TestRepositoryContextEnvelopeReportsBudgetBuckets(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "bucketed user prompt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendMessage(t.Context(), created.SessionID, "assistant", "bucketed assistant summary", created.ID); err != nil {
		t.Fatal(err)
	}
	artifactURI := "artifact://budget/session/" + created.SessionID + "/tool-result.txt"
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{Tool: "shell", Input: "bucketed tool input", Output: "bucketed tool output " + artifactURI, Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventArtifactReference, EventPayload{Tool: "shell", Path: artifactURI, Message: "bucketed artifact preview"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventDiff, EventPayload{Diff: "+bucketed transcript diff"}); err != nil {
		t.Fatal(err)
	}

	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 20, TokenBudget: 4096})
	if err != nil {
		t.Fatal(err)
	}
	buckets := contextBudgetBucketsByName(t, envelope.Budget.Buckets)
	assertContextBudgetBucketNames(t, buckets)
	if bucketTokenSum(envelope.Budget.Buckets) != envelope.Budget.EstimatedTokens {
		t.Fatalf("expected bucket total to equal estimated tokens, budget=%#v", envelope.Budget)
	}
	for _, name := range []string{"user", "transcript", "tool_result", "artifact_preview"} {
		if buckets[name].EstimatedTokens <= 0 || buckets[name].Items <= 0 {
			t.Fatalf("expected non-zero %s bucket, got %#v in %#v", name, buckets[name], envelope.Budget)
		}
	}
	for _, name := range []string{"system", "memory"} {
		if buckets[name].EstimatedTokens != 0 || buckets[name].Items != 0 {
			t.Fatalf("expected absent %s bucket to be zero, got %#v", name, buckets[name])
		}
	}
}

func TestRepositoryContextEnvelopeBudgetBucketsRemainStableForEmptyAndTruncatedContext(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	empty, err := repo.CreateSession(t.Context(), CreateSessionRequest{Workspace: t.TempDir(), Title: "empty budget"})
	if err != nil {
		t.Fatal(err)
	}
	emptyEnvelope, err := repo.ContextEnvelope(t.Context(), empty.ID, ContextRequest{ItemLimit: 10, TokenBudget: 128})
	if err != nil {
		t.Fatal(err)
	}
	emptyBuckets := contextBudgetBucketsByName(t, emptyEnvelope.Budget.Buckets)
	assertContextBudgetBucketNames(t, emptyBuckets)
	if emptyEnvelope.Budget.EstimatedTokens != 0 || bucketTokenSum(emptyEnvelope.Budget.Buckets) != 0 {
		t.Fatalf("expected empty context to have zero token buckets, got %#v", emptyEnvelope.Budget)
	}

	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "truncated bucket prompt",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := repo.AppendMessage(t.Context(), created.SessionID, "assistant", fmt.Sprintf("long bucket transcript %d %s", i, strings.Repeat("payload ", 80)), created.ID); err != nil {
			t.Fatal(err)
		}
	}
	truncated, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 3, TokenBudget: 128})
	if err != nil {
		t.Fatal(err)
	}
	truncatedBuckets := contextBudgetBucketsByName(t, truncated.Budget.Buckets)
	assertContextBudgetBucketNames(t, truncatedBuckets)
	if bucketTokenSum(truncated.Budget.Buckets) != truncated.Budget.EstimatedTokens {
		t.Fatalf("expected truncated bucket total to equal estimated tokens, got %#v", truncated.Budget)
	}
	if !truncated.Budget.Truncated {
		t.Fatalf("expected tiny context budget to truncate, got %#v", truncated.Budget)
	}
	for _, bucket := range truncated.Budget.Buckets {
		if bucket.EstimatedTokens > truncated.Budget.EstimatedTokens {
			t.Fatalf("bucket %s exceeds total estimate: %#v", bucket.Name, truncated.Budget)
		}
	}
}

func TestRepositoryContextEnvelopePacksRelevantSources(t *testing.T) {
	root := t.TempDir()
	workspace := root + "/repo-a"
	storeRoot := store.New(root)
	for i := 0; i < 7; i++ {
		if _, err := storeRoot.CreateMemoryWithOptions(store.CreateMemoryRequest{
			Text:       fmt.Sprintf("workspace preference %d keep context focused", i),
			Kind:       "preference",
			Source:     "manual",
			Workspace:  workspace,
			Importance: 1 + i%5,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := storeRoot.CreateMemoryWithOptions(store.CreateMemoryRequest{
		Text:       "other workspace memory must not leak",
		Kind:       "rule",
		Workspace:  root + "/repo-b",
		Importance: 5,
	}); err != nil {
		t.Fatal(err)
	}
	db, err := storeRoot.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{Workspace: workspace, Prompt: "pack context"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if _, err := repo.AppendMessage(t.Context(), created.SessionID, "assistant", fmt.Sprintf("packed transcript %02d", i), created.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{Tool: "shell", Input: "generate", Output: "large output artifact://packed/result.txt", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventArtifactReference, EventPayload{Tool: "shell", Path: "artifact://packed/result.txt", Message: "packed artifact preview"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		status := TodoStatusPending
		if i == 6 {
			status = TodoStatusDone
		}
		priority := TodoPriorityNormal
		if i%2 == 0 {
			priority = TodoPriorityCritical
		}
		if _, err := repo.WriteTodos(t.Context(), TodoWriteRequest{
			SessionID:    created.SessionID,
			SourceTaskID: created.ID,
			Todos: []TodoWriteItem{{
				ID:       fmt.Sprintf("todo-pack-%d", i),
				Content:  fmt.Sprintf("packed todo %d", i),
				Status:   status,
				Priority: priority,
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 8, TokenBudget: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.Transcript) > 8 || len(envelope.Memories) != maxContextMemories || len(envelope.Todos) != maxContextTodos || len(envelope.ArtifactRefs) == 0 {
		t.Fatalf("expected packed transcript/memories/todos/artifacts, got transcript=%d memories=%d todos=%d refs=%d envelope=%#v", len(envelope.Transcript), len(envelope.Memories), len(envelope.Todos), len(envelope.ArtifactRefs), envelope)
	}
	if strings.Contains(contextMemoriesText(envelope.Memories), "other workspace") {
		t.Fatalf("cross-workspace memory leaked into context: %#v", envelope.Memories)
	}
	for _, todo := range envelope.Todos {
		if todo.Status == TodoStatusDone {
			t.Fatalf("completed todo should not be selected: %#v", envelope.Todos)
		}
	}
	pack := contextPackSourcesByName(t, envelope.Pack)
	if pack["memory"].Available != 7 || pack["memory"].Selected != maxContextMemories || !pack["memory"].Truncated {
		t.Fatalf("unexpected memory pack source: %#v", pack["memory"])
	}
	if pack["todo"].Available != 7 || pack["todo"].Selected != maxContextTodos || !pack["todo"].Truncated {
		t.Fatalf("unexpected todo pack source: %#v", pack["todo"])
	}
	if pack["transcript"].Available < pack["transcript"].Selected || pack["artifact_preview"].Selected == 0 {
		t.Fatalf("unexpected transcript/artifact pack sources: %#v", envelope.Pack)
	}
	buckets := contextBudgetBucketsByName(t, envelope.Budget.Buckets)
	if buckets["memory"].Items != len(envelope.Memories) || buckets["memory"].EstimatedTokens == 0 {
		t.Fatalf("expected memory budget bucket to reflect selected memories, buckets=%#v memories=%#v", buckets, envelope.Memories)
	}
}

func TestRepositoryContextEnvelopePackerExcludesIrrelevantAndTruncatesBudget(t *testing.T) {
	root := t.TempDir()
	workspace := root + "/repo-a"
	storeRoot := store.New(root)
	allowed, err := storeRoot.CreateMemoryWithOptions(store.CreateMemoryRequest{
		Text:       strings.Repeat("allowed workspace memory ", 80),
		Kind:       "rule",
		Workspace:  workspace,
		Importance: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := storeRoot.CreateMemoryWithOptions(store.CreateMemoryRequest{Text: "disabled memory must not leak", Kind: "preference", Workspace: workspace, Importance: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storeRoot.SetMemoryEnabled(disabled.ID, false); err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().Add(-time.Hour)
	if _, err := storeRoot.CreateMemoryWithOptions(store.CreateMemoryRequest{Text: "expired memory must not leak", Kind: "preference", Workspace: workspace, Importance: 5, ExpiresAt: &expiredAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := storeRoot.CreateMemoryWithOptions(store.CreateMemoryRequest{Text: "cross workspace memory must not leak", Kind: "preference", Workspace: root + "/repo-b", Importance: 5}); err != nil {
		t.Fatal(err)
	}
	db, err := storeRoot.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	empty, err := repo.CreateSession(t.Context(), CreateSessionRequest{Workspace: workspace, Title: "empty packer"})
	if err != nil {
		t.Fatal(err)
	}
	emptyEnvelope, err := repo.ContextEnvelope(t.Context(), empty.ID, ContextRequest{ItemLimit: 10, TokenBudget: 128})
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyEnvelope.Transcript) != 0 || len(emptyEnvelope.Todos) != 0 || len(emptyEnvelope.ArtifactRefs) != 0 {
		t.Fatalf("expected empty session to avoid transcript/todo/artifact context, got %#v", emptyEnvelope)
	}

	created, err := repo.Create(t.Context(), CreateRequest{Workspace: workspace, Prompt: "tight pack"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := repo.AppendMessage(t.Context(), created.SessionID, "assistant", strings.Repeat(fmt.Sprintf("tight transcript %d ", i), 80), created.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.WriteTodos(t.Context(), TodoWriteRequest{
		SessionID:    created.SessionID,
		SourceTaskID: created.ID,
		Todos: []TodoWriteItem{{
			ID:       "todo-done-low",
			Content:  "completed low priority should stay out",
			Status:   TodoStatusDone,
			Priority: TodoPriorityLow,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.WriteTodos(t.Context(), TodoWriteRequest{
		SessionID:    created.SessionID,
		SourceTaskID: created.ID,
		Todos: []TodoWriteItem{{
			ID:       "todo-open-critical",
			Content:  strings.Repeat("open critical todo ", 40),
			Status:   TodoStatusPending,
			Priority: TodoPriorityCritical,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 20, TokenBudget: 128})
	if err != nil {
		t.Fatal(err)
	}
	combined := timelineItemsText(envelope.Transcript) + contextMemoriesText(envelope.Memories)
	for _, forbidden := range []string{"disabled memory", "expired memory", "cross workspace memory", "completed low priority"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("irrelevant context %q leaked into %#v", forbidden, envelope)
		}
	}
	if !envelope.Budget.Truncated {
		t.Fatalf("expected tight budget to mark truncation, got %#v", envelope.Budget)
	}
	pack := contextPackSourcesByName(t, envelope.Pack)
	if pack["memory"].Available != 1 || pack["memory"].Selected > 1 {
		t.Fatalf("expected only enabled unexpired workspace memory to be available, allowed=%s pack=%#v memories=%#v", allowed.ID, pack["memory"], envelope.Memories)
	}
	if pack["todo"].Available != 2 || pack["todo"].Selected > 1 {
		t.Fatalf("expected completed todo to be excluded from selected context, pack=%#v todos=%#v", pack["todo"], envelope.Todos)
	}
	assertContextBudgetBucketNames(t, contextBudgetBucketsByName(t, envelope.Budget.Buckets))
}

func TestRepositoryContextEnvelopeDiagnosticsExplainSelectedPromptSources(t *testing.T) {
	root := t.TempDir()
	workspace := root + "/repo-a"
	storeRoot := store.New(root)
	memory, err := storeRoot.CreateMemoryWithOptions(store.CreateMemoryRequest{
		Text:       "prefer concise diagnostics",
		Kind:       "preference",
		Workspace:  workspace,
		Importance: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := storeRoot.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{Workspace: workspace, Prompt: "diagnose prompt context"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolCall, EventPayload{Tool: "shell", Input: "cat notes.md"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{Tool: "shell", Input: "cat notes.md", Output: "tool result preview artifact://diagnostics/result.txt", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventArtifactReference, EventPayload{Tool: "shell", Path: "artifact://diagnostics/result.txt", Message: "diagnostics artifact"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.WriteTodos(t.Context(), TodoWriteRequest{
		SessionID:    created.SessionID,
		SourceTaskID: created.ID,
		Todos: []TodoWriteItem{{
			ID:       "todo-diagnostics",
			Content:  "explain selected context",
			Status:   TodoStatusPending,
			Priority: TodoPriorityCritical,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 20, TokenBudget: 4096})
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := contextDiagnosticsBySource(envelope.Diagnostics)
	for _, source := range []string{"transcript", "tool_result", "todo", "memory", "artifact_preview"} {
		if len(diagnostics[source]) == 0 {
			t.Fatalf("expected diagnostic source %q in %#v", source, envelope.Diagnostics)
		}
	}
	if diagnostics["memory"][0].ItemID != memory.ID || !strings.Contains(diagnostics["memory"][0].Reason, "workspace") {
		t.Fatalf("expected memory diagnostic to explain workspace match, got %#v", diagnostics["memory"])
	}
	if diagnostics["todo"][0].ItemID != "todo-diagnostics" || !strings.Contains(diagnostics["todo"][0].Reason, "priority") {
		t.Fatalf("expected todo diagnostic to explain priority selection, got %#v", diagnostics["todo"])
	}
	for _, diagnostic := range envelope.Diagnostics {
		if diagnostic.Reason == "" || diagnostic.EstimatedTokens <= 0 {
			t.Fatalf("diagnostic missing reason or token estimate: %#v", diagnostic)
		}
	}
}

func TestRepositoryContextEnvelopeDiagnosticsExcludeOmittedContext(t *testing.T) {
	root := t.TempDir()
	workspace := root + "/repo-a"
	storeRoot := store.New(root)
	disabled, err := storeRoot.CreateMemoryWithOptions(store.CreateMemoryRequest{Text: "disabled diagnostic leak", Kind: "preference", Workspace: workspace, Importance: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storeRoot.SetMemoryEnabled(disabled.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := storeRoot.CreateMemoryWithOptions(store.CreateMemoryRequest{Text: "cross workspace diagnostic leak", Kind: "preference", Workspace: root + "/repo-b", Importance: 5}); err != nil {
		t.Fatal(err)
	}
	db, err := storeRoot.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	empty, err := repo.CreateSession(t.Context(), CreateSessionRequest{Workspace: workspace + "-empty", Title: "empty diagnostics"})
	if err != nil {
		t.Fatal(err)
	}
	emptyEnvelope, err := repo.ContextEnvelope(t.Context(), empty.ID, ContextRequest{ItemLimit: 10, TokenBudget: 128})
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyEnvelope.Diagnostics) != 0 {
		t.Fatalf("empty context should not invent diagnostics: %#v", emptyEnvelope.Diagnostics)
	}

	created, err := repo.Create(t.Context(), CreateRequest{Workspace: workspace, Prompt: "tight diagnostics"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := repo.AppendMessage(t.Context(), created.SessionID, "assistant", strings.Repeat(fmt.Sprintf("removed history %d ", i), 80), created.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.WriteTodos(t.Context(), TodoWriteRequest{
		SessionID:    created.SessionID,
		SourceTaskID: created.ID,
		Todos: []TodoWriteItem{{
			ID:       "todo-done-diagnostics",
			Content:  "done diagnostic leak",
			Status:   TodoStatusDone,
			Priority: TodoPriorityCritical,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 20, TokenBudget: 128})
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.Diagnostics) != len(envelope.Transcript)+len(envelope.Todos)+len(envelope.Memories)+len(envelope.ArtifactRefs) {
		t.Fatalf("diagnostics should describe only selected items, got diagnostics=%d transcript=%d todos=%d memories=%d refs=%d envelope=%#v", len(envelope.Diagnostics), len(envelope.Transcript), len(envelope.Todos), len(envelope.Memories), len(envelope.ArtifactRefs), envelope)
	}
	diagnosticText := contextDiagnosticsText(envelope.Diagnostics)
	for _, forbidden := range []string{"disabled diagnostic leak", "cross workspace diagnostic leak", "done diagnostic leak"} {
		if strings.Contains(diagnosticText, forbidden) {
			t.Fatalf("omitted context %q appeared in diagnostics: %#v", forbidden, envelope.Diagnostics)
		}
	}
	if !envelope.Budget.Truncated {
		t.Fatalf("expected tight budget diagnostics test to truncate, got %#v", envelope.Budget)
	}
}

func TestRepositoryCompactSessionWritesManualBoundaryAndContext(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "manual compact prompt",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventSummary, EventPayload{Message: "assistant response before compact"}); err != nil {
		t.Fatal(err)
	}
	result, err := repo.CompactSession(t.Context(), created.SessionID, CompactRequest{Mode: CompactModeManual, TokenBudget: 512, Reason: "user requested"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted || result.Boundary == nil {
		t.Fatalf("expected manual compact boundary, got %#v", result)
	}
	if result.Mode != CompactModeManual || !strings.Contains(result.Boundary.Summary, "Manual compact") || result.BeforeEstimatedTokens == 0 {
		t.Fatalf("unexpected manual compact result: %#v", result)
	}
	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 20, TokenBudget: 512})
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.CompactBoundaries) != 1 || !strings.Contains(envelope.CompactBoundaries[0].Summary, "Manual compact") {
		t.Fatalf("expected context envelope to include compact boundary, got %#v", envelope.CompactBoundaries)
	}
}

func TestRepositoryAutoCompactHonorsBudgetAndAvoidsDuplicateBoundary(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    strings.Repeat("large prompt ", 120),
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{
		Tool:   "shell",
		Output: strings.Repeat("tool output ", 180),
		Status: "ok",
	}); err != nil {
		t.Fatal(err)
	}
	auto, err := repo.CompactSession(t.Context(), created.SessionID, CompactRequest{Mode: CompactModeAuto, ItemLimit: 20, TokenBudget: 128, Reason: "threshold"})
	if err != nil {
		t.Fatal(err)
	}
	if !auto.Compacted || auto.Boundary == nil || !strings.Contains(auto.Boundary.Summary, "Auto compact") {
		t.Fatalf("expected auto compact to write boundary, got %#v", auto)
	}
	again, err := repo.CompactSession(t.Context(), created.SessionID, CompactRequest{Mode: CompactModeAuto, ItemLimit: 20, TokenBudget: 128})
	if err != nil {
		t.Fatal(err)
	}
	if again.Compacted || again.SkippedReason != compactSkippedAlreadyCompacted {
		t.Fatalf("expected duplicate auto compact to skip, got %#v", again)
	}
	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 20, TokenBudget: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.CompactBoundaries) != 1 {
		t.Fatalf("expected one compact boundary after duplicate auto attempt, got %#v", envelope.CompactBoundaries)
	}
	small, err := repo.Create(t.Context(), CreateRequest{Workspace: t.TempDir(), Prompt: "small", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	withinBudget, err := repo.CompactSession(t.Context(), small.SessionID, CompactRequest{Mode: CompactModeAuto, TokenBudget: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if withinBudget.Compacted || withinBudget.SkippedReason != compactSkippedWithinBudget {
		t.Fatalf("expected within-budget auto compact to skip, got %#v", withinBudget)
	}
	empty, err := repo.CreateSession(t.Context(), CreateSessionRequest{Workspace: t.TempDir(), Title: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	emptyResult, err := repo.CompactSession(t.Context(), empty.ID, CompactRequest{Mode: CompactModeManual})
	if err != nil {
		t.Fatal(err)
	}
	if emptyResult.Compacted || emptyResult.SkippedReason != compactSkippedEmptyContext {
		t.Fatalf("expected empty compact to skip, got %#v", emptyResult)
	}
}

func TestRepositoryCompactSessionPersistsBoundarySourceMapping(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "compact mapping prompt",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolCall, EventPayload{Tool: "shell", Input: "echo compact"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{Tool: "shell", Output: "compact result", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	before, err := repo.TranscriptEntries(t.Context(), created.SessionID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) < 3 {
		t.Fatalf("expected source transcript before compact, got %#v", before)
	}
	result, err := repo.CompactSession(t.Context(), created.SessionID, CompactRequest{Mode: CompactModeManual, TokenBudget: 512, Reason: "mapping"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted || result.Boundary == nil {
		t.Fatalf("expected compact boundary result, got %#v", result)
	}
	if result.Boundary.SourceStartID != before[0].ID || result.Boundary.SourceEndID != before[len(before)-1].ID || result.Boundary.SourceItemCount != len(before) {
		t.Fatalf("unexpected result boundary source mapping before=%#v boundary=%#v", before, result.Boundary)
	}
	boundaries, err := repo.CompactBoundaries(t.Context(), created.SessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(boundaries) != 1 {
		t.Fatalf("expected one persisted compact boundary, got %#v", boundaries)
	}
	persisted := boundaries[0]
	if persisted.SourceStartID != before[0].ID || persisted.SourceEndID != before[len(before)-1].ID || persisted.SourceItemCount != len(before) || persisted.TokenBudget != 512 {
		t.Fatalf("unexpected persisted compact boundary mapping: %#v", persisted)
	}
	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 20, TokenBudget: 512})
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.CompactBoundaries) != 1 || envelope.CompactBoundaries[0].SourceItemCount != len(before) {
		t.Fatalf("expected context envelope to expose persisted mapping, got %#v", envelope.CompactBoundaries)
	}
}

func TestRepositoryCompactBoundaryMappingHandlesMalformedEmptyAndLegacyFallback(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	empty, err := repo.CreateSession(t.Context(), CreateSessionRequest{Workspace: t.TempDir(), Title: "empty compact mapping"})
	if err != nil {
		t.Fatal(err)
	}
	emptyBoundaries, err := repo.CompactBoundaries(t.Context(), empty.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyBoundaries) != 0 {
		t.Fatalf("expected no compact boundaries for empty session, got %#v", emptyBoundaries)
	}
	created, err := repo.Create(t.Context(), CreateRequest{Workspace: t.TempDir(), Prompt: "legacy compact fallback", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventCompactBoundary, EventPayload{}); err == nil {
		t.Fatal("expected malformed compact boundary to fail")
	}
	boundaries, err := repo.CompactBoundaries(t.Context(), created.SessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(boundaries) != 0 {
		t.Fatalf("malformed compact boundary should not persist mapping rows, got %#v", boundaries)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventCompactBoundary, EventPayload{Message: "legacy compact event", TokenEstimate: 64}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `DELETE FROM compact_boundaries WHERE session_id = ?`, created.SessionID); err != nil {
		t.Fatal(err)
	}
	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 20, TokenBudget: 512})
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.CompactBoundaries) != 1 || envelope.CompactBoundaries[0].Summary != "legacy compact event" || envelope.CompactBoundaries[0].SourceItemCount != 0 {
		t.Fatalf("expected transcript-only compact boundary fallback, got %#v", envelope.CompactBoundaries)
	}
}

func TestRepositoryCompactSessionContinuesWithPostBoundaryToolPairs(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    strings.Repeat("long compact prompt ", 80),
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolCall, EventPayload{Tool: "shell", ToolCallID: "call-before-compact", Input: "cat before.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{Tool: "shell", ToolCallID: "call-before-compact", ToolResultID: "call-before-compact-result", Input: "cat before.txt", Output: "before compact output", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	compact, err := repo.CompactSession(t.Context(), created.SessionID, CompactRequest{Mode: CompactModeManual, ItemLimit: 40, TokenBudget: 4096, Reason: "continue test"})
	if err != nil {
		t.Fatal(err)
	}
	if !compact.Compacted || compact.Boundary == nil {
		t.Fatalf("expected compact boundary before continuation, got %#v", compact)
	}

	continued, err := repo.Create(t.Context(), CreateRequest{
		Workspace: created.Workspace,
		SessionID: created.SessionID,
		Prompt:    "continue after compact",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if continued.SessionID != created.SessionID {
		t.Fatalf("expected continuation to reuse session %q, got %#v", created.SessionID, continued)
	}
	if err := repo.AppendEvent(t.Context(), continued.ID, EventToolCall, EventPayload{Tool: "shell", ToolCallID: "call-after-compact", Input: "cat after.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), continued.ID, EventToolResult, EventPayload{Tool: "shell", ToolCallID: "call-after-compact", ToolResultID: "call-after-compact-result", Input: "cat after.txt", Output: "after compact output", Status: "ok"}); err != nil {
		t.Fatal(err)
	}

	entries, err := repo.TranscriptEntries(t.Context(), created.SessionID, 100)
	if err != nil {
		t.Fatal(err)
	}
	call, result, ok := findTimelineToolPair(entries, "call-after-compact")
	if !ok {
		t.Fatalf("expected post-compact transcript tool pair, entries=%#v", entries)
	}
	if call.TaskID != continued.ID || result.TaskID != continued.ID || result.ToolResultID != "call-after-compact-result" {
		t.Fatalf("unexpected post-compact transcript pair call=%#v result=%#v continued=%#v", call, result, continued)
	}
	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 100, TokenBudget: 4096})
	if err != nil {
		t.Fatal(err)
	}
	call, result, ok = findTimelineToolPair(envelope.Transcript, "call-after-compact")
	if !ok || result.ToolResultID != "call-after-compact-result" {
		t.Fatalf("expected post-compact context tool pair, call=%#v result=%#v envelope=%#v", call, result, envelope.Transcript)
	}
	if len(envelope.CompactBoundaries) != 1 || envelope.CompactBoundaries[0].SourceEndID == result.ID {
		t.Fatalf("expected original compact source mapping to remain bounded before post-compact pair, boundary=%#v result=%#v", envelope.CompactBoundaries, result)
	}
}

func TestRepositoryAutoCompactResumesAfterNewPostBoundaryToolPair(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    strings.Repeat("auto compact prompt ", 120),
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventToolResult, EventPayload{Tool: "shell", ToolCallID: "call-auto-before", ToolResultID: "call-auto-before-result", Input: "cat before.txt", Output: strings.Repeat("before auto output ", 120), Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	first, err := repo.CompactSession(t.Context(), created.SessionID, CompactRequest{Mode: CompactModeAuto, ItemLimit: 50, TokenBudget: 128, Reason: "threshold"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Compacted {
		t.Fatalf("expected initial auto compact, got %#v", first)
	}
	again, err := repo.CompactSession(t.Context(), created.SessionID, CompactRequest{Mode: CompactModeAuto, ItemLimit: 50, TokenBudget: 128, Reason: "threshold"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Compacted || again.SkippedReason != compactSkippedAlreadyCompacted {
		t.Fatalf("expected already compacted skip before new context, got %#v", again)
	}

	continued, err := repo.Create(t.Context(), CreateRequest{
		Workspace: created.Workspace,
		SessionID: created.SessionID,
		Prompt:    "continue with tool after auto compact",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), continued.ID, EventToolCall, EventPayload{Tool: "shell", ToolCallID: "call-auto-after", Input: "cat after.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), continued.ID, EventToolResult, EventPayload{Tool: "shell", ToolCallID: "call-auto-after", ToolResultID: "call-auto-after-result", Input: "cat after.txt", Output: strings.Repeat("after auto output ", 120), Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	afterNewContext, err := repo.CompactSession(t.Context(), created.SessionID, CompactRequest{Mode: CompactModeAuto, ItemLimit: 100, TokenBudget: 128, Reason: "threshold"})
	if err != nil {
		t.Fatal(err)
	}
	if !afterNewContext.Compacted || afterNewContext.Boundary == nil {
		t.Fatalf("expected auto compact to resume after post-boundary tool context, got %#v", afterNewContext)
	}

	envelope, err := repo.ContextEnvelope(t.Context(), created.SessionID, ContextRequest{ItemLimit: 100, TokenBudget: 4096})
	if err != nil {
		t.Fatal(err)
	}
	call, result, ok := findTimelineToolPair(envelope.Transcript, "call-auto-after")
	if !ok || result.ToolResultID != "call-auto-after-result" {
		t.Fatalf("expected post-boundary tool pair to remain visible after resumed auto compact, call=%#v result=%#v transcript=%#v", call, result, envelope.Transcript)
	}
	if len(envelope.CompactBoundaries) != 2 {
		t.Fatalf("expected two compact boundaries after resumed auto compact, got %#v", envelope.CompactBoundaries)
	}
}

func TestRepositoryTaskMetadata_persistsAutomationFields(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "nightly audit",
		Natural:   true,
		Origin:    OriginSchedule,
		Automation: AutomationMetadata{
			Kind:    AutomationKindSchedule,
			Risk:    AutomationRiskDangerous,
			Source:  "cron:nightly",
			Trigger: "0 2 * * *",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Origin != OriginSchedule || got.Automation.Kind != AutomationKindSchedule || got.Automation.Risk != AutomationRiskDangerous {
		t.Fatalf("unexpected task metadata %#v", got)
	}
	if got.Automation.Source != "cron:nightly" || got.Automation.Trigger != "0 2 * * *" {
		t.Fatalf("unexpected automation source/trigger %#v", got.Automation)
	}
	tasks, err := repo.List(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Automation.Risk != AutomationRiskDangerous {
		t.Fatalf("unexpected listed task metadata %#v", tasks)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"origin":"schedule"`, `"kind":"schedule"`, `"risk":"dangerous"`, `"source":"cron:nightly"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("expected task json to contain %s, got %s", want, string(data))
		}
	}
}

func TestRepositoryTaskMetadata_defaultsPrivilegedAutomationRiskToDangerous(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	cases := []struct {
		origin Origin
		kind   AutomationKind
	}{
		{OriginSchedule, AutomationKindSchedule},
		{OriginHook, AutomationKindHook},
		{OriginSubagent, AutomationKindSubagent},
	}
	for _, tc := range cases {
		t.Run(string(tc.origin), func(t *testing.T) {
			created, err := repo.Create(t.Context(), CreateRequest{
				Workspace:  t.TempDir(),
				Prompt:     "nightly audit",
				Natural:    true,
				Origin:     tc.origin,
				Automation: AutomationMetadata{Kind: tc.kind},
			})
			if err != nil {
				t.Fatal(err)
			}

			if created.Automation.Risk != AutomationRiskDangerous {
				t.Fatalf("expected missing %s risk to default dangerous, got %#v", tc.origin, created.Automation)
			}
			if !AutomationRequiresApproval(created.Origin, created.Automation) {
				t.Fatalf("expected %s task to require approval, got %#v", tc.origin, created)
			}
		})
	}
}

func TestRepositoryTaskMetadata_rejectsInvalidAutomationBoundary(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	cases := []CreateRequest{
		{
			Workspace: t.TempDir(),
			Prompt:    "bad risk",
			Origin:    OriginSchedule,
			Automation: AutomationMetadata{
				Kind: AutomationKindSchedule,
				Risk: AutomationRisk("maybe"),
			},
		},
		{
			Workspace: t.TempDir(),
			Prompt:    "kind mismatch",
			Origin:    OriginSchedule,
			Automation: AutomationMetadata{
				Kind: AutomationKindHook,
				Risk: AutomationRiskDangerous,
			},
		},
		{
			Workspace: t.TempDir(),
			Prompt:    "subagent kind mismatch",
			Origin:    OriginSubagent,
			Automation: AutomationMetadata{
				Kind: AutomationKindSchedule,
				Risk: AutomationRiskDangerous,
			},
		},
		{
			Workspace: t.TempDir(),
			Prompt:    "unknown origin",
			Origin:    Origin("timer"),
			Automation: AutomationMetadata{
				Kind: AutomationKindSchedule,
				Risk: AutomationRiskDangerous,
			},
		},
	}
	for _, request := range cases {
		if _, err := repo.Create(t.Context(), request); err == nil {
			t.Fatalf("expected invalid automation request to fail: %#v", request)
		}
	}
	tasks, err := repo.List(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("invalid automation metadata inserted tasks: %#v", tasks)
	}
}

func TestRepositoryCreatesAndReusesSessionTranscript(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	first, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "first thought",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "second thought",
		SessionID: first.SessionID,
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionID != first.SessionID {
		t.Fatalf("expected reused session %q, got %q", first.SessionID, second.SessionID)
	}

	session, err := repo.GetSession(t.Context(), first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.LastTaskID != second.ID || session.Workspace != workspace {
		t.Fatalf("unexpected session %#v", session)
	}
	messages, err := repo.Messages(t.Context(), first.SessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Content != "first thought" || messages[1].TaskID != second.ID {
		t.Fatalf("unexpected messages %#v", messages)
	}
	tasks, err := repo.ListBySession(t.Context(), first.SessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != second.ID || tasks[1].ID != first.ID {
		t.Fatalf("unexpected session tasks %#v", tasks)
	}
	if err := repo.AppendEvent(t.Context(), first.ID, EventToolResult, EventPayload{Tool: "read", Input: "notes.txt", Output: "hello", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), first.ID, EventSummary, EventPayload{Message: "read done"}); err != nil {
		t.Fatal(err)
	}
	timeline, err := repo.Timeline(t.Context(), first.SessionID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var combined strings.Builder
	for _, item := range timeline {
		kinds = append(kinds, item.Kind)
		combined.WriteString(item.Role)
		combined.WriteString(item.Content)
		combined.WriteString(item.Tool)
		combined.WriteString(item.Output)
		combined.WriteByte('\n')
	}
	for _, want := range []string{"message", "tool_result"} {
		if !containsString(kinds, want) {
			t.Fatalf("expected timeline kind %q in %#v", want, timeline)
		}
	}
	for _, want := range []string{"first thought", "second thought", "read done", "hello"} {
		if !strings.Contains(combined.String(), want) {
			t.Fatalf("expected timeline to contain %q, got %#v", want, timeline)
		}
	}
}

func TestRepositoryCancelsTask(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "long task",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Cancel(t.Context(), created.ID, "user requested"); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCancelled || got.CompletedAt == nil {
		t.Fatalf("unexpected cancelled task %#v", got)
	}
	events, err := repo.Events(t.Context(), created.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != EventCancelled || !strings.Contains(events[0].Payload, "user requested") {
		t.Fatalf("unexpected cancel events %#v", events)
	}
}

func TestRepositoryCancelResolvesPendingApprovalItem(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "run rm -rf build",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), created.ID, StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventPermissionRequest, EventPayload{
		Tool:       "run",
		ToolCallID: "cancelled-call",
		Input:      "rm -rf build",
		Risk:       "dangerous_shell",
		Status:     string(StatusWaitingUser),
	}); err != nil {
		t.Fatal(err)
	}

	if err := repo.Cancel(t.Context(), created.ID, "user requested"); err != nil {
		t.Fatal(err)
	}

	item, ok, err := repo.ApprovalItemForTask(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || item.Status != "resolved" || item.Decision != "cancelled" || item.ResolvedAt == nil {
		t.Fatalf("expected cancelled approval item, got %#v ok=%v", item, ok)
	}
}

func TestRepositoryQueuesAndReceivesUserInput(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	first, err := repo.Create(t.Context(), CreateRequest{Workspace: workspace, Prompt: "first", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Create(t.Context(), CreateRequest{Workspace: workspace, Prompt: "second", SessionID: first.SessionID, Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), first.ID, StatusRunning); err != nil {
		t.Fatal(err)
	}
	active, err := repo.HasActiveSessionTask(t.Context(), first.SessionID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("expected active task in session")
	}
	if err := repo.Queue(t.Context(), second.ID); err != nil {
		t.Fatal(err)
	}
	next, ok, err := repo.NextQueuedTask(t.Context(), first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || next.ID != second.ID {
		t.Fatalf("unexpected next queued task ok=%v task=%#v", ok, next)
	}
	if err := repo.UpdateStatus(t.Context(), first.ID, StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), first.ID, EventUserInputRequest, EventPayload{Message: "Which file?", Status: string(StatusWaitingUser)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReceiveUserInput(t.Context(), first.ID, "notes.txt"); err != nil {
		t.Fatal(err)
	}
	answer, ok, err := repo.LatestUserInput(t.Context(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || answer != "notes.txt" {
		t.Fatalf("unexpected latest input ok=%v answer=%q", ok, answer)
	}
}

func TestRepositoryBackgroundTaskCountsLostAndRecover(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	running, err := repo.Create(t.Context(), CreateRequest{
		Workspace:  workspace,
		Prompt:     "run background",
		Natural:    false,
		RunAsync:   true,
		Origin:     OriginBackground,
		Automation: AutomationMetadata{Kind: AutomationKindBackground, Risk: AutomationRiskSafe},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), running.ID, StatusRunning); err != nil {
		t.Fatal(err)
	}
	queued, err := repo.Create(t.Context(), CreateRequest{
		Workspace:  workspace,
		Prompt:     "run schedule",
		Natural:    false,
		RunAsync:   true,
		Origin:     OriginSchedule,
		Automation: AutomationMetadata{Kind: AutomationKindSchedule, Risk: AutomationRiskSafe},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Queue(t.Context(), queued.ID); err != nil {
		t.Fatal(err)
	}
	counts, err := repo.CountBackgroundTasks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if counts.Running != 1 || counts.Active != 2 {
		t.Fatalf("unexpected background counts %#v", counts)
	}
	next, ok, err := repo.NextQueuedBackgroundTask(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || next.ID != queued.ID {
		t.Fatalf("unexpected next background task ok=%v task=%#v", ok, next)
	}
	lostCount, err := repo.MarkLostBackgroundTasks(t.Context(), "restart")
	if err != nil {
		t.Fatal(err)
	}
	if lostCount != 1 {
		t.Fatalf("expected one lost task, got %d", lostCount)
	}
	lost, err := repo.Get(t.Context(), running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lost.Status != StatusLost {
		t.Fatalf("expected running background task lost, got %#v", lost)
	}
	if err := repo.RecoverLostTask(t.Context(), running.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	recovered, err := repo.Get(t.Context(), running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != StatusRecovered {
		t.Fatalf("expected recovered task status, got %#v", recovered)
	}
	events, err := repo.Events(t.Context(), running.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 || events[len(events)-2].Type != EventError || events[len(events)-1].Type != EventTaskQueued {
		t.Fatalf("expected lost and recovered events, got %#v", events)
	}
}

func TestRepositoryExpiryStaleMarksWaitScheduleAndHookTasks(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	now := time.Now().UTC()
	expired := formatTime(now.Add(-time.Minute))
	approval, err := repo.Create(t.Context(), CreateRequest{Workspace: t.TempDir(), Prompt: "approval", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	input, err := repo.Create(t.Context(), CreateRequest{Workspace: t.TempDir(), Prompt: "input", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := repo.Create(t.Context(), CreateRequest{
		Workspace:  t.TempDir(),
		Prompt:     "run schedule",
		Natural:    false,
		Origin:     OriginSchedule,
		Automation: AutomationMetadata{Kind: AutomationKindSchedule, Risk: AutomationRiskSafe},
		Schedule:   ScheduleMetadata{ID: "schedule-expired", MissedRuns: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := repo.Create(t.Context(), CreateRequest{Workspace: t.TempDir(), Prompt: "hook timeout", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		taskID    string
		status    Status
		eventType EventType
		payload   EventPayload
	}{
		{approval.ID, StatusWaitingUser, EventPermissionRequest, EventPayload{Message: "approval needed", Status: string(StatusWaitingUser), ExpiresAt: expired}},
		{input.ID, StatusWaitingUser, EventUserInputRequest, EventPayload{Message: "which file?", Status: string(StatusWaitingUser), ExpiresAt: expired}},
		{schedule.ID, StatusQueued, EventScheduleTriggered, EventPayload{ID: "schedule-expired", Message: "catch up", ExpiresAt: expired, MissedRuns: 4, CatchUpPolicy: string(ScheduleCatchUpPolicyRunOnce), CatchUpRuns: 1}},
		{hook.ID, StatusQueued, EventHookRun, EventPayload{Action: "PostToolUse", Message: "hook timed out", TimeoutSeconds: 30, ExpiresAt: expired}},
	} {
		if err := repo.UpdateStatus(t.Context(), item.taskID, item.status); err != nil {
			t.Fatal(err)
		}
		if err := repo.AppendEvent(t.Context(), item.taskID, item.eventType, item.payload); err != nil {
			t.Fatal(err)
		}
	}
	marked, err := repo.MarkExpiredTasksStale(t.Context(), now, "expiry sweep")
	if err != nil {
		t.Fatal(err)
	}
	if marked != 4 {
		t.Fatalf("expected 4 stale tasks, got %d", marked)
	}
	for _, id := range []string{approval.ID, input.ID, schedule.ID, hook.ID} {
		task, err := repo.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status != StatusStale || task.CompletedAt == nil {
			t.Fatalf("expected task %s stale with completed_at, got %#v", id, task)
		}
		events, err := repo.Events(t.Context(), id, 20)
		if err != nil {
			t.Fatal(err)
		}
		if !taskEventsHavePayloadStatus(t, events, StatusStale) {
			t.Fatalf("expected stale event payload for %s, got %#v", id, events)
		}
	}
	item, ok, err := repo.ApprovalItemForTask(t.Context(), approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || item.Status != "resolved" || item.Decision != "expired" || item.ResolvedAt == nil {
		t.Fatalf("expected expired approval item, got %#v ok=%v", item, ok)
	}
}

func TestRepositoryExpiryStaleSkipsNonExpiredTerminalAndRejectsMalformed(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	now := time.Now().UTC()
	future := formatTime(now.Add(time.Hour))
	past := formatTime(now.Add(-time.Hour))
	waiting, err := repo.Create(t.Context(), CreateRequest{Workspace: t.TempDir(), Prompt: "waiting", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), waiting.ID, StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), waiting.ID, EventUserInputRequest, EventPayload{Message: "not yet", ExpiresAt: future}); err != nil {
		t.Fatal(err)
	}
	terminal, err := repo.Create(t.Context(), CreateRequest{Workspace: t.TempDir(), Prompt: "terminal", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), terminal.ID, StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), terminal.ID, EventPermissionRequest, EventPayload{Message: "old approval", ExpiresAt: past}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), waiting.ID, EventHookRun, EventPayload{Message: "bad expiry", ExpiresAt: "not-a-time"}); err == nil {
		t.Fatal("expected malformed expires_at to fail closed")
	}
	if err := repo.AppendEvent(t.Context(), waiting.ID, EventHookRun, EventPayload{Message: "bad timeout", TimeoutSeconds: -1}); err == nil {
		t.Fatal("expected malformed timeout_seconds to fail closed")
	}
	marked, err := repo.MarkExpiredTasksStale(t.Context(), now, "expiry sweep")
	if err != nil {
		t.Fatal(err)
	}
	if marked != 0 {
		t.Fatalf("expected no stale tasks, got %d", marked)
	}
	waitingAfter, err := repo.Get(t.Context(), waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waitingAfter.Status != StatusWaitingUser {
		t.Fatalf("expected future expiry to remain waiting_user, got %#v", waitingAfter)
	}
	terminalAfter, err := repo.Get(t.Context(), terminal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminalAfter.Status != StatusCompleted {
		t.Fatalf("expected terminal task to remain completed, got %#v", terminalAfter)
	}
}

func TestRepositoryNotifiesSubscribersWhenEventIsAppended(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "stream events",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	notification, unsubscribe := repo.SubscribeEvents(ctx, created.ID)
	defer unsubscribe()

	if err := repo.AppendEvent(t.Context(), created.ID, EventSummary, EventPayload{Message: "ready"}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-notification:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("subscriber was not notified after appending an event")
	}
}

func TestRepositoryReadsEventsAfterSequence(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	created, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "stream incrementally",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventPlanning, EventPayload{Message: "one"}); err != nil {
		t.Fatal(err)
	}
	firstBatch, err := repo.EventsAfter(t.Context(), created.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstBatch) != 1 || firstBatch[0].Seq == 0 {
		t.Fatalf("unexpected first batch %#v", firstBatch)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, EventSummary, EventPayload{Message: "two"}); err != nil {
		t.Fatal(err)
	}
	secondBatch, err := repo.EventsAfter(t.Context(), created.ID, firstBatch[0].Seq, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondBatch) != 1 || secondBatch[0].Type != EventSummary || secondBatch[0].Seq <= firstBatch[0].Seq {
		t.Fatalf("unexpected second batch %#v after %#v", secondBatch, firstBatch)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func contextBudgetBucketsByName(t *testing.T, buckets []ContextBudgetBucket) map[string]ContextBudgetBucket {
	t.Helper()
	byName := make(map[string]ContextBudgetBucket, len(buckets))
	for _, bucket := range buckets {
		if bucket.Name == "" {
			t.Fatalf("budget bucket name is required in %#v", buckets)
		}
		if _, exists := byName[bucket.Name]; exists {
			t.Fatalf("duplicate budget bucket %q in %#v", bucket.Name, buckets)
		}
		byName[bucket.Name] = bucket
	}
	return byName
}

func assertContextBudgetBucketNames(t *testing.T, buckets map[string]ContextBudgetBucket) {
	t.Helper()
	for _, name := range []string{"system", "user", "transcript", "memory", "tool_result", "artifact_preview"} {
		if _, ok := buckets[name]; !ok {
			t.Fatalf("missing context budget bucket %q in %#v", name, buckets)
		}
	}
	if len(buckets) != 6 {
		t.Fatalf("expected exactly 6 context budget buckets, got %#v", buckets)
	}
}

func findTimelineToolPair(items []TimelineItem, toolCallID string) (TimelineItem, TimelineItem, bool) {
	var call, result TimelineItem
	for _, item := range items {
		if item.ToolCallID != toolCallID {
			continue
		}
		switch item.Kind {
		case "tool_call":
			call = item
		case "tool_result":
			result = item
		}
	}
	return call, result, call.ID != "" && result.ID != ""
}

func bucketTokenSum(buckets []ContextBudgetBucket) int {
	total := 0
	for _, bucket := range buckets {
		total += bucket.EstimatedTokens
	}
	return total
}

func contextPackSourcesByName(t *testing.T, pack ContextPack) map[string]ContextPackSource {
	t.Helper()
	byName := make(map[string]ContextPackSource, len(pack.Sources))
	for _, source := range pack.Sources {
		if source.Name == "" {
			t.Fatalf("context pack source missing name: %#v", pack)
		}
		if _, ok := byName[source.Name]; ok {
			t.Fatalf("duplicate context pack source %q in %#v", source.Name, pack)
		}
		byName[source.Name] = source
	}
	for _, name := range []string{"transcript", "todo", "memory", "artifact_preview"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing context pack source %q in %#v", name, pack)
		}
	}
	return byName
}

func contextMemoriesText(memories []ContextMemory) string {
	var builder strings.Builder
	for _, memory := range memories {
		builder.WriteString(memory.Text)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func contextDiagnosticsBySource(diagnostics []ContextDiagnostic) map[string][]ContextDiagnostic {
	bySource := make(map[string][]ContextDiagnostic)
	for _, diagnostic := range diagnostics {
		bySource[diagnostic.Source] = append(bySource[diagnostic.Source], diagnostic)
	}
	return bySource
}

func contextDiagnosticsText(diagnostics []ContextDiagnostic) string {
	var builder strings.Builder
	for _, diagnostic := range diagnostics {
		builder.WriteString(diagnostic.Source)
		builder.WriteByte('\n')
		builder.WriteString(diagnostic.ItemID)
		builder.WriteByte('\n')
		builder.WriteString(diagnostic.ItemKind)
		builder.WriteByte('\n')
		builder.WriteString(diagnostic.Reason)
		builder.WriteByte('\n')
		builder.WriteString(diagnostic.Summary)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func timelineItemsText(items []TimelineItem) string {
	var combined strings.Builder
	for _, item := range items {
		combined.WriteString(item.Kind)
		combined.WriteString(item.Role)
		combined.WriteString(item.Type)
		combined.WriteString(item.Content)
		combined.WriteString(item.Tool)
		combined.WriteString(item.Input)
		combined.WriteString(item.Output)
		combined.WriteString(item.Target)
		combined.WriteString(item.Status)
		combined.WriteString(item.Diff)
		combined.WriteString(item.Reason)
		combined.WriteByte('\n')
	}
	return combined.String()
}

func taskEventsHavePayloadStatus(t *testing.T, events []Event, status Status) bool {
	t.Helper()
	for _, event := range events {
		var payload EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			continue
		}
		if payload.Status == string(status) {
			return true
		}
	}
	return false
}
