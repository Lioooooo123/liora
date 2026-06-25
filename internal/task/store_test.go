package task

import (
	"strings"
	"testing"

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
