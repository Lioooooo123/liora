package task

import (
	"context"
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
