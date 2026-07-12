package daemonclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/apply"
	"github.com/Lioooooo123/liora/internal/daemon"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/store"
	"github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/tools"
)

type fakeGenerator struct {
	response string
}

func (f *fakeGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	return f.response, nil
}

type blockingShellExecutor struct {
	started chan struct{}
	done    chan struct{}
}

func newBlockingShellExecutor() *blockingShellExecutor {
	return &blockingShellExecutor{
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (e *blockingShellExecutor) Run(ctx context.Context, _ string, _ string) (tools.ShellResult, error) {
	close(e.started)
	<-ctx.Done()
	close(e.done)
	return tools.ShellResult{ExitCode: -1}, ctx.Err()
}

func TestClientCapabilitiesAndTaskLifecycle(t *testing.T) {
	workspace := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	runner := task.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "write notes.txt hello\nread notes.txt"}))
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	if err := client.Health(t.Context()); err != nil {
		t.Fatal(err)
	}
	capabilities, err := client.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilities.Tools) == 0 {
		t.Fatal("expected capabilities")
	}
	if len(capabilities.MCPTools) != 0 || capabilities.MCPError != "" {
		t.Fatalf("expected no mcp tools by default, got %#v", capabilities)
	}

	created, err := client.CreateTask(t.Context(), task.CreateRequest{
		Workspace: workspace,
		Prompt:    "create notes",
		Natural:   true,
		RunAsync:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Task.ID == "" {
		t.Fatalf("unexpected task %#v", created.Task)
	}
	if created.Task.SessionID == "" {
		t.Fatalf("expected task session id %#v", created.Task)
	}
	stream, errs := client.StreamEvents(t.Context(), created.Task.ID)
	var eventTypes []task.EventType
	for event := range stream {
		eventTypes = append(eventTypes, event.Type)
		if event.Type == task.EventCompleted {
			break
		}
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	for _, want := range []task.EventType{task.EventPlanning, task.EventPlanReady, task.EventToolResult, task.EventCompleted} {
		if !containsEventType(eventTypes, want) {
			t.Fatalf("expected %s in streamed events %#v", want, eventTypes)
		}
	}

	got, err := client.GetTask(t.Context(), created.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("expected completed, got %#v", got)
	}
	tasks, err := client.ListTasks(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) == 0 || tasks[0].ID != created.Task.ID {
		t.Fatalf("unexpected task list %#v", tasks)
	}
	events, err := client.Events(t.Context(), created.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsEvent(events, task.EventCompleted) {
		t.Fatalf("expected completed event, got %#v", events)
	}
}

func TestClientCreateTaskHonorsChildScopeBoundary(t *testing.T) {
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	client := newTestClient(t, server.URL)
	workspace := t.TempDir()
	src := filepath.Join(workspace, "src")

	parent, err := client.CreateTask(t.Context(), task.CreateRequest{
		Workspace: workspace,
		Prompt:    "parent scope",
		Scope: task.TaskScope{
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
	child, err := client.CreateTask(t.Context(), task.CreateRequest{
		Workspace:    workspace,
		Prompt:       "child scope",
		ParentTaskID: parent.Task.ID,
		Scope: task.TaskScope{
			Paths:        []string{src},
			NetworkHosts: []string{"api.internal"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.Task.ParentTaskID != parent.Task.ID || !child.Task.InheritedScopeFromParent {
		t.Fatalf("unexpected child task %#v", child.Task)
	}
	if len(child.Task.ApprovalGrants) != 0 {
		t.Fatalf("child must not carry approval grants: %#v", child.Task.ApprovalGrants)
	}

	_, err = client.CreateTask(t.Context(), task.CreateRequest{
		Workspace:    workspace,
		Prompt:       "escalate",
		ParentTaskID: parent.Task.ID,
		Scope: task.TaskScope{
			MCPServers: []string{"dangerous-server"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "outside parent scope") {
		t.Fatalf("expected scope escalation error, got %v", err)
	}
}

func TestClientMemoryLifecycle(t *testing.T) {
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: task.NewRepository(db), Store: persistentStore}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	created, err := client.CreateMemory(t.Context(), store.CreateMemoryRequest{
		Text:       "remember workspace mood",
		Kind:       "preference",
		Source:     "client-test",
		Importance: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Text != "remember workspace mood" || created.Kind != "preference" || created.Source != "client-test" || created.Importance != 5 || !created.Enabled {
		t.Fatalf("unexpected created memory %#v", created)
	}
	updated, err := client.UpdateMemory(t.Context(), created.ID, store.UpdateMemoryRequest{Text: stringPtr("remember daemon workspace mood"), Importance: intPtr(4)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Text != "remember daemon workspace mood" || updated.Importance != 4 {
		t.Fatalf("unexpected updated memory %#v", updated)
	}
	got, err := client.GetMemory(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.Text != updated.Text {
		t.Fatalf("unexpected fetched memory %#v", got)
	}
	if err := client.DisableMemory(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	memories, err := client.ListMemories(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 0 {
		t.Fatalf("unexpected memories %#v", memories)
	}
	matches, err := client.SearchMemoriesWithOptions(t.Context(), "mood", 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].ID != created.ID || matches[0].Enabled {
		t.Fatalf("unexpected search matches %#v", matches)
	}
	if err := client.EnableMemory(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	memories, err = client.ListMemories(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 1 || memories[0].Text != updated.Text || !memories[0].Enabled {
		t.Fatalf("unexpected enabled memories %#v", memories)
	}
}

func TestClientPermissionRuleLifecycle(t *testing.T) {
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: task.NewRepository(db), Store: persistentStore}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	created, err := client.CreatePermissionRule(t.Context(), store.CreatePermissionRuleRequest{
		Action:    store.PermissionRuleAlwaysAsk,
		Workspace: "/repo",
		Tool:      "run",
		Risk:      "network",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Action != store.PermissionRuleAlwaysAsk || created.Tool != "run" || !created.Enabled {
		t.Fatalf("unexpected created rule %#v", created)
	}
	rules, err := client.ListPermissionRules(t.Context(), store.PermissionRuleListOptions{Workspace: "/repo", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].ID != created.ID {
		t.Fatalf("unexpected rules %#v", rules)
	}
	if err := client.DeletePermissionRule(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	rules, err = client.ListPermissionRules(t.Context(), store.PermissionRuleListOptions{Workspace: "/repo", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected deleted rule to disappear, got %#v", rules)
	}
}

func TestClientScheduleLifecycle(t *testing.T) {
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: task.NewRepository(db), Store: persistentStore}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	created, err := client.CreateSchedule(t.Context(), store.CreateScheduleRequest{
		ID:          "nightly",
		Workspace:   "/repo",
		TriggerKind: store.ScheduleTriggerCron,
		Trigger:     "0 2 * * *",
		Prompt:      "run nightly checks",
		Timezone:    "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "nightly" || created.TriggerKind != store.ScheduleTriggerCron || !created.Enabled {
		t.Fatalf("unexpected created schedule %#v", created)
	}
	schedules, err := client.ListSchedules(t.Context(), store.ScheduleListOptions{Workspace: "/repo", IncludeDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 1 || schedules[0].ID != created.ID {
		t.Fatalf("unexpected schedule list %#v", schedules)
	}
	got, err := client.GetSchedule(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.Prompt != created.Prompt {
		t.Fatalf("unexpected fetched schedule %#v", got)
	}
	updatedPrompt := "run nightly checks and summarize"
	updated, err := client.UpdateSchedule(t.Context(), created.ID, store.UpdateScheduleRequest{Prompt: &updatedPrompt})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Prompt != updatedPrompt {
		t.Fatalf("unexpected updated schedule %#v", updated)
	}
	paused, err := client.SetScheduleEnabled(t.Context(), created.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Enabled {
		t.Fatalf("expected paused schedule, got %#v", paused)
	}
	schedules, err = client.ListSchedules(t.Context(), store.ScheduleListOptions{Workspace: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 0 {
		t.Fatalf("expected paused schedule hidden by default, got %#v", schedules)
	}
	resumed, err := client.SetScheduleEnabled(t.Context(), created.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Enabled {
		t.Fatalf("expected resumed schedule, got %#v", resumed)
	}
	if err := client.DeleteSchedule(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	schedules, err = client.ListSchedules(t.Context(), store.ScheduleListOptions{Workspace: "/repo", IncludeDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 0 {
		t.Fatalf("expected deleted schedule to disappear, got %#v", schedules)
	}
}

func TestClientUsesCapabilityTokenForSensitiveAPIs(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := task.NewRepository(db)
	patchTask, err := repo.Create(t.Context(), task.CreateRequest{
		Workspace: workspace,
		Prompt:    "patch notes",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	approvalTask, err := repo.Create(t.Context(), task.CreateRequest{
		Workspace: workspace,
		Prompt:    "needs approval",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), approvalTask.ID, task.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), approvalTask.ID, task.EventPermissionRequest, task.EventPayload{Message: "approval needed"}); err != nil {
		t.Fatal(err)
	}
	patch, err := apply.CreatePatch(workspace, []apply.FileChange{{Path: "notes.txt", Before: "", After: "hello\n"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemon.NewServer(daemon.Config{
		Repository: repo,
		Store:      persistentStore,
		AuthToken:  "secret-token",
	}))
	defer server.Close()

	unauthorized := newTestClient(t, server.URL)
	if _, err := unauthorized.ListMemories(t.Context(), 0); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected unauthorized memory list, got %v", err)
	}
	if _, err := unauthorized.Apply(t.Context(), patchTask.ID, patch); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected unauthorized apply, got %v", err)
	}
	if _, err := unauthorized.Approve(t.Context(), approvalTask.ID); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected unauthorized approval, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("unauthorized apply changed workspace, stat err=%v", err)
	}

	client, err := New(server.URL, WithCapabilityToken("secret-token"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.CreateMemory(t.Context(), store.CreateMemoryRequest{Text: "token-protected memory", Kind: "note"})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := client.ListMemories(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.Text != "token-protected memory" || len(listed) != 1 || listed[0].Text != created.Text {
		t.Fatalf("unexpected authorized memory results created=%#v listed=%#v", created, listed)
	}
	applied, err := client.Apply(t.Context(), patchTask.ID, patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Files) != 1 || applied.Files[0] != "notes.txt" {
		t.Fatalf("unexpected apply result %#v", applied)
	}
	approved, err := client.Approve(t.Context(), approvalTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != task.StatusDraft || approved.ApprovalGranted {
		t.Fatalf("expected approved task, got %#v", approved)
	}
}

func TestClientTypedMethodsConstructCanonicalPaths(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.RequestURI() {
		case "GET /v1/capabilities":
			_, _ = w.Write([]byte(`{"tools":[{"name":"read","usage":"read <path>","kind":"builtin"}],"mcp_tools":[]}`))
		case "GET /v1/workbench?limit=5&workspace=%2Frepo":
			_, _ = w.Write([]byte(`{"workspace":"/repo","sessions":[],"active_tasks":[],"queued_tasks":[],"recent_tasks":[],"pending_approvals":[],"pending_user_inputs":[]}`))
		case "GET /v1/sessions/session-001/context?limit=5&token_budget=512":
			_, _ = w.Write([]byte(`{"session":{"id":"session-001","title":"Research","workspace":"/repo","created_at":"2026-07-01T00:00:00Z","updated_at":"2026-07-01T00:00:00Z"},"budget":{"max_tokens":512,"estimated_tokens":128,"item_limit":5,"truncated":false,"buckets":[{"name":"system","estimated_tokens":0,"items":0},{"name":"user","estimated_tokens":16,"items":1},{"name":"transcript","estimated_tokens":32,"items":1},{"name":"memory","estimated_tokens":0,"items":0},{"name":"tool_result","estimated_tokens":40,"items":1},{"name":"artifact_preview","estimated_tokens":40,"items":1}]},"transcript":[],"summaries":[],"artifact_refs":[],"compact_boundaries":[],"generated_at":"2026-07-01T00:00:00Z"}`))
		case "POST /v1/sessions/session-001/compact":
			_, _ = w.Write([]byte(`{"session":{"id":"session-001","title":"Research","workspace":"/repo","created_at":"2026-07-01T00:00:00Z","updated_at":"2026-07-01T00:00:00Z"},"mode":"auto","compacted":true,"reason":"threshold","token_budget":512,"before_estimated_tokens":2048,"after_estimated_tokens":128,"transcript_items":8,"boundary":{"task_id":"task-001","summary":"Auto compact: summarized 8 context items","token_estimate":2048,"created_at":"2026-07-01T00:00:00Z"},"generated_at":"2026-07-01T00:00:00Z"}`))
		case "GET /v1/memories?include_disabled=true&include_expired=true&limit=10&workspace=%2Frepo":
			_, _ = w.Write([]byte(`[]`))
		case "PATCH /v1/memories/mem-001":
			_, _ = w.Write([]byte(`{"id":"mem-001","text":"prefer explicit output","kind":"note","importance":3,"enabled":true,"created_at":"2026-07-01T00:00:00Z","updated_at":"2026-07-01T00:00:00Z"}`))
		case "DELETE /v1/memories/mem-001?workspace=%2Frepo":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.RequestURI(), http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	if _, err := client.Capabilities(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Workbench(t.Context(), "/repo", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SessionContext(t.Context(), "session-001", task.ContextRequest{ItemLimit: 5, TokenBudget: 512}); err != nil {
		t.Fatal(err)
	}
	compact, err := client.CompactSession(t.Context(), "session-001", task.CompactRequest{Mode: task.CompactModeAuto, ItemLimit: 5, TokenBudget: 512, Reason: "threshold"})
	if err != nil {
		t.Fatal(err)
	}
	if !compact.Compacted || compact.Boundary == nil || !strings.Contains(compact.Boundary.Summary, "Auto compact") {
		t.Fatalf("unexpected compact result %#v", compact)
	}
	if _, err := client.ListMemoriesWithOptions(t.Context(), store.MemoryListOptions{Workspace: "/repo", Limit: 10, IncludeDisabled: true, IncludeExpired: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateMemory(t.Context(), "mem-001", store.UpdateMemoryRequest{Text: stringPtr("prefer explicit output")}); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteMemoryForWorkspace(t.Context(), "mem-001", "/repo"); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"GET /v1/capabilities",
		"GET /v1/workbench?limit=5&workspace=%2Frepo",
		"GET /v1/sessions/session-001/context?limit=5&token_budget=512",
		"POST /v1/sessions/session-001/compact",
		"GET /v1/memories?include_disabled=true&include_expired=true&limit=10&workspace=%2Frepo",
		"PATCH /v1/memories/mem-001",
		"DELETE /v1/memories/mem-001?workspace=%2Frepo",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected typed client calls\nwant:\n%s\ngot:\n%s", strings.Join(want, "\n"), strings.Join(calls, "\n"))
	}
}

func TestClientRejectsMalformedTypedRequestParameters(t *testing.T) {
	client := newTestClient(t, "http://127.0.0.1:1")
	cases := []struct {
		name string
		run  func() error
		want string
	}{
		{
			name: "blank task id",
			run: func() error {
				_, err := client.GetTask(t.Context(), " ")
				return err
			},
			want: "task id is required",
		},
		{
			name: "blank session id",
			run: func() error {
				_, err := client.SessionMessages(t.Context(), "", 10)
				return err
			},
			want: "session id is required",
		},
		{
			name: "negative list limit",
			run: func() error {
				_, err := client.ListTasks(t.Context(), -1)
				return err
			},
			want: "limit cannot be negative",
		},
		{
			name: "negative context budget",
			run: func() error {
				_, err := client.SessionContext(t.Context(), "session-001", task.ContextRequest{TokenBudget: -1})
				return err
			},
			want: "token budget",
		},
		{
			name: "blank search query",
			run: func() error {
				_, err := client.SearchTimeline(t.Context(), "/repo", " ", 10)
				return err
			},
			want: "query is required",
		},
		{
			name: "blank memory text",
			run: func() error {
				_, err := client.CreateMemory(t.Context(), store.CreateMemoryRequest{Text: " "})
				return err
			},
			want: "memory text is required",
		},
		{
			name: "invalid memory kind",
			run: func() error {
				kind := "shadow"
				_, err := client.UpdateMemory(t.Context(), "mem-001", store.UpdateMemoryRequest{Kind: &kind})
				return err
			},
			want: "unknown memory kind",
		},
		{
			name: "invalid memory importance",
			run: func() error {
				_, err := client.UpdateMemory(t.Context(), "mem-001", store.UpdateMemoryRequest{Importance: intPtr(9)})
				return err
			},
			want: "memory importance",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}

	stream, errs := client.StreamEvents(t.Context(), " ")
	for range stream {
		t.Fatal("expected no events for blank task id")
	}
	if err := <-errs; err == nil || !strings.Contains(err.Error(), "task id is required") {
		t.Fatalf("unexpected stream error %v", err)
	}
}

func TestClientSessionLifecycle(t *testing.T) {
	workspace := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	sessionResponse, err := client.CreateSession(t.Context(), task.CreateSessionRequest{Workspace: workspace, Title: "study notes"})
	if err != nil {
		t.Fatal(err)
	}
	if sessionResponse.Session.ID == "" || sessionResponse.Session.Title != "study notes" {
		t.Fatalf("unexpected session %#v", sessionResponse.Session)
	}
	created, err := client.CreateTask(t.Context(), task.CreateRequest{
		Workspace: workspace,
		Prompt:    "read assignment",
		SessionID: sessionResponse.Session.ID,
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Task.SessionID != sessionResponse.Session.ID {
		t.Fatalf("expected task in session, got %#v", created.Task)
	}
	sessions, err := client.ListSessions(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].LastTaskID != created.Task.ID {
		t.Fatalf("unexpected sessions %#v", sessions)
	}
	otherWorkspace := t.TempDir()
	if _, err := client.CreateTask(t.Context(), task.CreateRequest{
		Workspace: otherWorkspace,
		Prompt:    "other workspace",
		Natural:   true,
	}); err != nil {
		t.Fatal(err)
	}
	workspaceSessions, err := client.ListSessionsForWorkspace(t.Context(), workspace, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaceSessions) != 1 || workspaceSessions[0].ID != sessionResponse.Session.ID {
		t.Fatalf("unexpected workspace sessions %#v", workspaceSessions)
	}
	workspaceTasks, err := client.ListTasksForWorkspace(t.Context(), workspace, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaceTasks) != 1 || workspaceTasks[0].ID != created.Task.ID {
		t.Fatalf("unexpected workspace tasks %#v", workspaceTasks)
	}
	messages, err := client.SessionMessages(t.Context(), sessionResponse.Session.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "read assignment" {
		t.Fatalf("unexpected messages %#v", messages)
	}
	tasks, err := client.SessionTasks(t.Context(), sessionResponse.Session.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != created.Task.ID {
		t.Fatalf("unexpected session tasks %#v", tasks)
	}
	got, err := client.GetSession(t.Context(), sessionResponse.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastTaskID != created.Task.ID {
		t.Fatalf("unexpected session %#v", got)
	}
	if err := repo.AppendEvent(t.Context(), created.Task.ID, task.EventSummary, task.EventPayload{Message: "assignment read"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.Task.ID, task.EventArtifactReference, task.EventPayload{Tool: "shell", Path: ".liora/tool-results/assignment.txt", Message: "assignment artifact"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.Task.ID, task.EventCompactBoundary, task.EventPayload{Message: "assignment compact boundary"}); err != nil {
		t.Fatal(err)
	}
	timeline, err := client.SessionTimeline(t.Context(), sessionResponse.Session.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var combined strings.Builder
	for _, item := range timeline {
		combined.WriteString(item.Role)
		combined.WriteString(item.Content)
		combined.WriteByte('\n')
	}
	if !strings.Contains(combined.String(), "read assignment") || !strings.Contains(combined.String(), "assignment read") {
		t.Fatalf("unexpected timeline %#v", timeline)
	}
	contextEnvelope, err := client.SessionContext(t.Context(), sessionResponse.Session.ID, task.ContextRequest{ItemLimit: 5, TokenBudget: 512})
	if err != nil {
		t.Fatal(err)
	}
	if contextEnvelope.Session.ID != sessionResponse.Session.ID || contextEnvelope.Budget.ItemLimit != 5 {
		t.Fatalf("unexpected session context %#v", contextEnvelope)
	}
	if len(contextEnvelope.Budget.Buckets) == 0 || contextBudgetBucketTokenSum(contextEnvelope.Budget.Buckets) != contextEnvelope.Budget.EstimatedTokens {
		t.Fatalf("expected budget buckets to total estimated tokens, got %#v", contextEnvelope.Budget)
	}
	if len(contextEnvelope.ArtifactRefs) == 0 || len(contextEnvelope.CompactBoundaries) == 0 {
		t.Fatalf("expected context refs and boundaries, got %#v", contextEnvelope)
	}
	matches, err := client.SearchTimeline(t.Context(), workspace, "assignment", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected timeline search matches")
	}
	var searchText strings.Builder
	for _, item := range matches {
		searchText.WriteString(item.Content)
		searchText.WriteString(item.Title)
		searchText.WriteString(item.Input)
	}
	if !strings.Contains(searchText.String(), "assignment") {
		t.Fatalf("unexpected timeline search matches %#v", matches)
	}
	if err := repo.UpdateStatus(t.Context(), created.Task.ID, task.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.Task.ID, task.EventPermissionRequest, task.EventPayload{
		Tool:   "run",
		Input:  "rm -rf build",
		Risk:   "dangerous_shell",
		Reason: "Command contains rm -rf.",
	}); err != nil {
		t.Fatal(err)
	}
	workbench, err := client.Workbench(t.Context(), workspace, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(workbench.Sessions) != 1 || workbench.Sessions[0].ID != sessionResponse.Session.ID {
		t.Fatalf("unexpected workbench sessions %#v", workbench.Sessions)
	}
	if !containsTask(workbench.ActiveTasks, created.Task.ID) || !containsTask(workbench.RecentTasks, created.Task.ID) {
		t.Fatalf("unexpected workbench tasks %#v", workbench)
	}
	if len(workbench.PendingApprovals) != 1 || workbench.PendingApprovals[0].Task.ID != created.Task.ID {
		t.Fatalf("unexpected workbench pending approvals %#v", workbench.PendingApprovals)
	}
	if item := workbench.PendingApprovals[0].Item; item.TaskID != created.Task.ID || item.ToolName != "run" || item.CommandPreview != "rm -rf build" || item.Risk != "dangerous_shell" {
		t.Fatalf("unexpected workbench approval item %#v", item)
	}
}

func TestClientWorkbenchDecodesBackgroundOutputs(t *testing.T) {
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	workspace := t.TempDir()
	background, err := repo.Create(t.Context(), task.CreateRequest{
		Workspace:  workspace,
		Prompt:     "completed background",
		Natural:    false,
		Origin:     task.OriginBackground,
		Automation: task.AutomationMetadata{Kind: task.AutomationKindBackground, Risk: task.AutomationRiskSafe},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), background.ID, task.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	artifactURI := "artifact://artifacts/sessions/" + background.SessionID + "/tasks/" + background.ID + "/tool-results/out.txt"
	if err := repo.AppendEvent(t.Context(), background.ID, task.EventToolResult, task.EventPayload{Tool: "run", Output: "client decoded background output", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), background.ID, task.EventArtifactReference, task.EventPayload{Tool: "run", Path: artifactURI, Message: "artifact output"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	workbench, err := client.Workbench(t.Context(), workspace, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !containsTask(workbench.BackgroundTasks, background.ID) || !containsTask(workbench.BackgroundCompletedTasks, background.ID) {
		t.Fatalf("expected background task buckets, got %#v", workbench)
	}
	if len(workbench.BackgroundOutputs) != 1 || workbench.BackgroundOutputs[0].TaskID != background.ID || !strings.Contains(workbench.BackgroundOutputs[0].Output, "client decoded background output") || workbench.BackgroundOutputs[0].ArtifactURI != artifactURI {
		t.Fatalf("unexpected background outputs %#v", workbench.BackgroundOutputs)
	}
}

func stringPtr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func TestClientCancelRunningTask(t *testing.T) {
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	executor := newBlockingShellExecutor()
	runner := task.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "run sleep 100"}))
	runner.SetSandbox(executor)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	created, err := client.CreateTask(t.Context(), task.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "slow",
		Natural:   true,
		RunAsync:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.started:
	case <-time.After(3 * time.Second):
		t.Fatal("task did not start shell")
	}
	cancelled, err := client.Cancel(t.Context(), created.Task.ID, "test requested")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != task.StatusCancelled {
		t.Fatalf("expected cancelled task, got %#v", cancelled)
	}
	select {
	case <-executor.done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not stop shell")
	}
}

func TestClientApprovesWaitingTask(t *testing.T) {
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	runner := task.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetPermissionPolicy(permission.Policy{Mode: permission.ModePrompt})
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	created, err := client.CreateTask(t.Context(), task.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "run rm -rf build",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Task.Status != task.StatusWaitingUser {
		t.Fatalf("expected waiting task, got %#v", created.Task)
	}
	approved, err := client.Approve(t.Context(), created.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != task.StatusDraft || approved.ApprovalGranted {
		t.Fatalf("expected approved task, got %#v", approved)
	}
}

func TestClientReturnsAPIError(t *testing.T) {
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	_, err := client.CreateTask(t.Context(), task.CreateRequest{Workspace: t.TempDir()})
	if err == nil {
		t.Fatal("expected API error")
	}
	if !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestClientStreamsStructuredTaskErrorEvent(t *testing.T) {
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	taskRecord, err := repo.Create(t.Context(), task.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "read missing.txt",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), taskRecord.ID, task.EventToolResult, task.EventPayload{
		Tool:   "read",
		Input:  "missing.txt",
		Output: "missing.txt: no such file or directory",
		Status: "error",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), taskRecord.ID, task.EventError, task.EventPayload{
		Message: "failed at step 1/1: read missing.txt",
		Status:  string(task.StatusFailed),
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	stream, errs := client.StreamEvents(t.Context(), taskRecord.ID)
	var types []task.EventType
	for event := range stream {
		types = append(types, event.Type)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if !containsEventType(types, task.EventToolResult) || !containsEventType(types, task.EventError) {
		t.Fatalf("expected tool result and task error, got %#v", types)
	}
}

func TestClientStreamsMultipleTasks(t *testing.T) {
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	first, err := repo.Create(t.Context(), task.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "first",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Create(t.Context(), task.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "second",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []struct {
		id      string
		message string
	}{
		{id: first.ID, message: "first done"},
		{id: second.ID, message: "second done"},
	} {
		if err := repo.AppendEvent(t.Context(), record.id, task.EventSummary, task.EventPayload{Message: record.message}); err != nil {
			t.Fatal(err)
		}
		if err := repo.AppendEvent(t.Context(), record.id, task.EventCompleted, task.EventPayload{Status: string(task.StatusCompleted)}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	stream, errs := client.StreamTaskEvents(t.Context(), []string{first.ID, second.ID, first.ID, " "})
	got := map[string][]task.EventType{}
	for event := range stream {
		got[event.TaskID] = append(got[event.TaskID], event.Type)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.ID, second.ID} {
		if !containsEventType(got[id], task.EventSummary) || !containsEventType(got[id], task.EventCompleted) {
			t.Fatalf("expected summary and completed for %s, got %#v", id, got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected duplicate task id to be streamed once, got %#v", got)
	}
}

func TestClientServesCrossThreadHandoffAPI(t *testing.T) {
	st := store.New(t.TempDir())
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Store: st}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	source, err := client.CreateConversationThread(t.Context(), store.CreateConversationThreadRequest{Workspace: "/repo-a", Title: "Source"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := client.CreateConversationThread(t.Context(), store.CreateConversationThreadRequest{Workspace: "/repo-a", Title: "Target"})
	if err != nil {
		t.Fatal(err)
	}
	threads, err := client.ListConversationThreads(t.Context(), "/repo-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 {
		t.Fatalf("expected two threads, got %#v", threads)
	}
	config, err := client.UpdateThreadModelConfig(t.Context(), source.ID, store.UpdateThreadModelConfigRequest{
		Provider: "openai-chat",
		Model:    "gpt-5",
		BaseURL:  "https://llm.example.test/v1",
		Profile:  "strong",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.ThreadID != source.ID || config.BaseURL == "" || config.Profile != "strong" {
		t.Fatalf("unexpected thread model config %#v", config)
	}
	fetchedConfig, err := client.GetThreadModelConfig(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fetchedConfig.Model != "gpt-5" {
		t.Fatalf("unexpected fetched thread model config %#v", fetchedConfig)
	}
	threads, err = client.ListConversationThreads(t.Context(), "/repo-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if threads[0].ModelConfig == nil && threads[1].ModelConfig == nil {
		t.Fatalf("expected listed threads to include model config, got %#v", threads)
	}
	if err := client.DeleteThreadModelConfig(t.Context(), source.ID); err != nil {
		t.Fatal(err)
	}
	message, err := client.CreateCrossThreadMessage(t.Context(), target.ID, store.CreateCrossThreadMessageRequest{
		FromThreadID:    source.ID,
		Summary:         "handoff summary",
		ExplicitContent: "explicit note",
		ArtifactRefs:    []store.CrossThreadArtifactRef{{Path: ".liora/artifacts/handoff.txt", Summary: "handoff artifact"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.Content != "explicit note" || message.Summary != "handoff summary" || len(message.ArtifactRefs) != 1 {
		t.Fatalf("unexpected handoff message %#v", message)
	}
	messages, err := client.ListCrossThreadMessages(t.Context(), target.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID != message.ID {
		t.Fatalf("unexpected handoff inbox %#v", messages)
	}
	if _, err := client.CreateCrossThreadMessage(t.Context(), " ", store.CreateCrossThreadMessageRequest{FromThreadID: source.ID, Summary: "bad"}); err == nil {
		t.Fatal("expected blank thread id to fail")
	}
}

func TestClientStreamTaskEventsRequiresIDs(t *testing.T) {
	client := newTestClient(t, "http://127.0.0.1:1")
	stream, errs := client.StreamTaskEvents(t.Context(), []string{" ", ""})
	for range stream {
		t.Fatal("expected no events")
	}
	err := <-errs
	if err == nil || !strings.Contains(err.Error(), "task ids are required") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestClientStreamTaskEventsStopsOnContextCancel(t *testing.T) {
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	taskRecord, err := repo.Create(t.Context(), task.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "wait",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	ctx, cancel := context.WithCancel(t.Context())
	stream, errs := client.StreamTaskEvents(ctx, []string{taskRecord.ID})
	cancel()
	for range stream {
	}
	if err := <-errs; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected stream error %v", err)
	}
}

func TestClientStreamStopsOnContextCancel(t *testing.T) {
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	taskRecord, err := repo.Create(t.Context(), task.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "wait",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	ctx, cancel := context.WithCancel(t.Context())
	events, errs := client.StreamEvents(ctx, taskRecord.ID)
	cancel()
	for range events {
	}
	if err := <-errs; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected stream error %v", err)
	}
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := New(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func newTestRepository(t *testing.T) (*task.Repository, func()) {
	t.Helper()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	return task.NewRepository(db), func() { _ = db.Close() }
}

func containsEvent(events []task.Event, want task.EventType) bool {
	for _, event := range events {
		if event.Type == want {
			return true
		}
	}
	return false
}

func containsEventType(events []task.EventType, want task.EventType) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func contextBudgetBucketTokenSum(buckets []task.ContextBudgetBucket) int {
	total := 0
	for _, bucket := range buckets {
		total += bucket.EstimatedTokens
	}
	return total
}

func containsTask(tasks []task.Task, want string) bool {
	for _, task := range tasks {
		if task.ID == want {
			return true
		}
	}
	return false
}
