package task

import (
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/store"
)

func TestRepositoryWriteAndReadTodosForSession(t *testing.T) {
	repo := newTodoTestRepository(t)
	created := createTodoTestTask(t, repo, t.TempDir())

	written, err := repo.WriteTodos(t.Context(), TodoWriteRequest{
		SessionID:    created.SessionID,
		SourceTaskID: created.ID,
		Todos: []TodoWriteItem{{
			ID:       "todo-plan",
			Content:  "Draft acceptance tests",
			Status:   TodoStatusPending,
			Priority: TodoPriorityHigh,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0].ID != "todo-plan" || written[0].SourceTaskID != created.ID {
		t.Fatalf("unexpected written todo %#v", written)
	}
	firstUpdatedAt := written[0].UpdatedAt
	if firstUpdatedAt.IsZero() {
		t.Fatalf("expected updated_at on created todo: %#v", written[0])
	}
	time.Sleep(time.Millisecond)

	written, err = repo.WriteTodos(t.Context(), TodoWriteRequest{
		SessionID:    created.SessionID,
		SourceTaskID: created.ID,
		Todos: []TodoWriteItem{{
			ID:       "todo-plan",
			Content:  "Draft acceptance tests",
			Status:   TodoStatusDone,
			Priority: TodoPriorityCritical,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if written[0].Status != TodoStatusDone || written[0].Priority != TodoPriorityCritical {
		t.Fatalf("expected updated todo, got %#v", written[0])
	}
	if !written[0].UpdatedAt.After(firstUpdatedAt) {
		t.Fatalf("expected updated_at to advance from %s to %s", firstUpdatedAt, written[0].UpdatedAt)
	}

	todos, err := repo.TodosBySession(t.Context(), created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(todos) != 1 || todos[0].ID != "todo-plan" || todos[0].Status != TodoStatusDone || todos[0].Priority != TodoPriorityCritical || todos[0].UpdatedAt.IsZero() {
		t.Fatalf("unexpected session todos %#v", todos)
	}
	events, err := repo.Events(t.Context(), created.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var todoEvents int
	for _, event := range events {
		if event.Type == EventTodoUpdated {
			todoEvents++
			if !strings.Contains(event.Payload, `"priority":"`) || !strings.Contains(event.Payload, `"source_task_id":"`) {
				t.Fatalf("todo event should include priority and source_task_id: %s", event.Payload)
			}
		}
	}
	if todoEvents != 2 {
		t.Fatalf("expected create and update todo events, got %d in %#v", todoEvents, events)
	}
}

func TestRepositoryWriteTodosRejectsMalformedInputsWithoutPartialRowsOrEvents(t *testing.T) {
	repo := newTodoTestRepository(t)
	workspace := t.TempDir()
	created := createTodoTestTask(t, repo, workspace)
	other := createTodoTestTask(t, repo, workspace)

	checks := []struct {
		name    string
		request TodoWriteRequest
		want    string
	}{
		{
			name:    "empty todos",
			request: TodoWriteRequest{SessionID: created.SessionID, SourceTaskID: created.ID},
			want:    "todos are required",
		},
		{
			name: "blank content",
			request: TodoWriteRequest{SessionID: created.SessionID, SourceTaskID: created.ID, Todos: []TodoWriteItem{{
				Content: "  ",
			}}},
			want: "content is required",
		},
		{
			name: "invalid status",
			request: TodoWriteRequest{SessionID: created.SessionID, SourceTaskID: created.ID, Todos: []TodoWriteItem{{
				Content: "Ship", Status: "stuck",
			}}},
			want: "invalid status",
		},
		{
			name: "invalid priority",
			request: TodoWriteRequest{SessionID: created.SessionID, SourceTaskID: created.ID, Todos: []TodoWriteItem{{
				Content: "Ship", Priority: "urgent",
			}}},
			want: "invalid priority",
		},
		{
			name: "item source mismatch",
			request: TodoWriteRequest{SessionID: created.SessionID, SourceTaskID: created.ID, Todos: []TodoWriteItem{{
				SourceTaskID: other.ID, Content: "Ship",
			}}},
			want: "does not match current task",
		},
		{
			name: "source task session mismatch",
			request: TodoWriteRequest{SessionID: created.SessionID, SourceTaskID: other.ID, Todos: []TodoWriteItem{{
				Content: "Ship",
			}}},
			want: "does not belong to session",
		},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			beforeTodos := todoRowCount(t, repo)
			beforeEvents := eventRowCount(t, repo, EventTodoUpdated)
			_, err := repo.WriteTodos(t.Context(), check.request)
			if err == nil || !strings.Contains(err.Error(), check.want) {
				t.Fatalf("expected %q error, got %v", check.want, err)
			}
			if got := todoRowCount(t, repo); got != beforeTodos {
				t.Fatalf("todo rows changed from %d to %d", beforeTodos, got)
			}
			if got := eventRowCount(t, repo, EventTodoUpdated); got != beforeEvents {
				t.Fatalf("todo events changed from %d to %d", beforeEvents, got)
			}
		})
	}
}

func newTodoTestRepository(t *testing.T) *Repository {
	t.Helper()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewRepository(db)
}

func createTodoTestTask(t *testing.T, repo *Repository, workspace string) Task {
	t.Helper()
	task, err := repo.Create(t.Context(), CreateRequest{Workspace: workspace, Prompt: "todo test", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func todoRowCount(t *testing.T, repo *Repository) int {
	t.Helper()
	var count int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM todos`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func eventRowCount(t *testing.T, repo *Repository, eventType EventType) int {
	t.Helper()
	var count int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM task_events WHERE type = ?`, string(eventType)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
