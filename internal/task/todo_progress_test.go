package task

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
)

func TestRunnerStreamsTodoProgressBeforeTaskCompletes(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt: `todo_write {"todos":[{"id":"progress-plan","content":"Execute visible plan","status":"pending","priority":"high"}]}
todo_write {"todos":[{"id":"progress-plan","content":"Execute visible plan","status":"in_progress","priority":"high"}]}
run first
run block
todo_write {"todos":[{"id":"progress-plan","content":"Execute visible plan","status":"done","priority":"high"}]}`,
		Natural: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := newBlockingSecondCommandExecutor()
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(executor)

	done := make(chan error, 1)
	go func() {
		done <- runner.Run(t.Context(), task.ID)
	}()

	select {
	case <-executor.firstDone:
	case err := <-done:
		events, eventErr := repo.Events(t.Context(), task.ID, 100)
		if eventErr != nil {
			t.Fatalf("task ended before in-flight checkpoint with err=%v and events failed: %v", err, eventErr)
		}
		t.Fatalf("task ended before in-flight checkpoint with err=%v events=%v statuses=%v", err, eventTypes(events), todoStatusesFromEvents(t, events))
	case <-time.After(3 * time.Second):
		events, eventErr := repo.Events(t.Context(), task.ID, 100)
		if eventErr != nil {
			t.Fatalf("task did not reach the in-flight checkpoint and events failed: %v", eventErr)
		}
		t.Fatalf("task did not reach the in-flight checkpoint; events=%v statuses=%v", eventTypes(events), todoStatusesFromEvents(t, events))
	}

	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	inFlightStatuses := todoStatusesFromEvents(t, events)
	t.Logf("in-flight todo statuses=%v task events=%v", inFlightStatuses, eventTypes(events))
	if !equalStrings(inFlightStatuses, []string{TodoStatusPending, TodoStatusInProgress}) {
		t.Fatalf("expected visible pending -> in_progress before completion, got %#v", inFlightStatuses)
	}
	if containsEventType(eventTypes(events), EventCompleted) {
		t.Fatalf("task completed before blocked command was released: %#v", eventTypes(events))
	}

	close(executor.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	events, err = repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	finalStatuses := todoStatusesFromEvents(t, events)
	t.Logf("final todo statuses=%v task events=%v", finalStatuses, eventTypes(events))
	if !equalStrings(finalStatuses, []string{TodoStatusPending, TodoStatusInProgress, TodoStatusDone}) {
		t.Fatalf("expected pending -> in_progress -> done, got %#v", finalStatuses)
	}
	if !containsEventType(eventTypes(events), EventCompleted) {
		t.Fatalf("expected task.completed after final todo update, got %#v", eventTypes(events))
	}
}

func todoStatusesFromEvents(t *testing.T, events []Event) []string {
	t.Helper()
	var statuses []string
	for _, event := range events {
		if event.Type != EventTodoUpdated {
			continue
		}
		var payload EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode todo payload: %v", err)
		}
		statuses = append(statuses, payload.Status)
	}
	return statuses
}

func equalStrings(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
