package daemonclient

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if !approved.ApprovalGranted {
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
