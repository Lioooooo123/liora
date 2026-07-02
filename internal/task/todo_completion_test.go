package task

import (
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
)

func TestRunnerCompletesWhenOpenTodoIsExplained(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	source, err := repo.Create(t.Context(), CreateRequest{Workspace: workspace, Prompt: "seed todo", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.WriteTodos(t.Context(), TodoWriteRequest{
		SessionID:    source.SessionID,
		SourceTaskID: source.ID,
		Todos: []TodoWriteItem{{
			ID:       "todo-plan",
			Content:  "Finish benchmark evidence",
			Status:   TodoStatusInProgress,
			Priority: TodoPriorityCritical,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		SessionID: source.SessionID,
		Prompt:    "finish",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "ANSWER: todo-plan remains in_progress because benchmark evidence is waiting on external input."}))
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("expected explained open todo task to complete, got %#v", got)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("explained open todo status=%s events=%v", got.Status, eventTypes(events))
	if !containsEventType(eventTypes(events), EventCompleted) {
		t.Fatalf("expected task.completed event, got %#v", eventTypes(events))
	}
}

func TestRunnerFailsCompletionWhenOpenTodoIsUnexplained(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	source, err := repo.Create(t.Context(), CreateRequest{Workspace: workspace, Prompt: "seed todo", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.WriteTodos(t.Context(), TodoWriteRequest{
		SessionID:    source.SessionID,
		SourceTaskID: source.ID,
		Todos: []TodoWriteItem{{
			ID:       "critical-plan",
			Content:  "Ship release notes",
			Status:   TodoStatusPending,
			Priority: TodoPriorityCritical,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		SessionID: source.SessionID,
		Prompt:    "finish",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "ANSWER: all done"}))
	gateErr := runner.Run(t.Context(), task.ID)
	if gateErr == nil || !strings.Contains(gateErr.Error(), "todo completion gate blocked") {
		t.Fatalf("expected todo completion gate error, got %v", gateErr)
	}

	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("expected unexplained open todo task to fail, got %#v", got)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	types := eventTypes(events)
	t.Logf("unexplained open todo status=%s error=%v events=%v", got.Status, gateErr, types)
	if containsEventType(types, EventCompleted) || !containsEventType(types, EventError) {
		t.Fatalf("expected task.error without task.completed, got %#v", types)
	}
	var payloads strings.Builder
	for _, event := range events {
		payloads.WriteString(event.Payload)
		payloads.WriteByte('\n')
	}
	if !strings.Contains(payloads.String(), "critical-plan") || !strings.Contains(payloads.String(), "Ship release notes") {
		t.Fatalf("expected blocker details in events, got:\n%s", payloads.String())
	}
}

func TestRunnerAllowsNonCriticalPendingTodoBeforeCompletion(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	source, err := repo.Create(t.Context(), CreateRequest{Workspace: workspace, Prompt: "seed todo", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.WriteTodos(t.Context(), TodoWriteRequest{
		SessionID:    source.SessionID,
		SourceTaskID: source.ID,
		Todos: []TodoWriteItem{{
			ID:       "normal-backlog",
			Content:  "Tidy optional docs",
			Status:   TodoStatusPending,
			Priority: TodoPriorityNormal,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		SessionID: source.SessionID,
		Prompt:    "finish",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "ANSWER: all done"}))
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("expected non-critical pending todo to allow completion, got %#v", got)
	}
	t.Logf("non-critical pending todo status=%s", got.Status)
}
