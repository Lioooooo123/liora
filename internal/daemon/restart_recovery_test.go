package daemon

import (
	"testing"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

func TestServerRestartRecovery_failsUnattachedForegroundAndStartsQueuedTurn(t *testing.T) {
	// Given
	workspace := t.TempDir()
	st := store.New(t.TempDir())
	db, err := st.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	active, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "running before restart", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), active.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), active.ID, taskpkg.EventPlanning, taskpkg.EventPayload{Message: "Planning task", Status: string(taskpkg.StatusPlanning)}); err != nil {
		t.Fatal(err)
	}
	queued, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, SessionID: active.SessionID, Prompt: "run queued after restart", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Queue(t.Context(), queued.ID); err != nil {
		t.Fatal(err)
	}
	executor := newBackgroundBlockingExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(executor)

	// When
	handler := newServer(Config{Repository: repo, Runner: runner, Store: st, Foreground: ForegroundLimits{MaxConcurrent: 1, MaxActive: 4}})
	defer handler.cancelRunning(queued.ID)

	// Then
	recoveredActive, err := repo.Get(t.Context(), active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredActive.Status != taskpkg.StatusFailed {
		t.Fatalf("expected unattached foreground task to become failed on restart, got %#v", recoveredActive)
	}
	events, err := repo.Events(t.Context(), active.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPayloadStatus(events, taskpkg.StatusFailed) {
		t.Fatalf("expected failed restart event for unattached foreground task, got %#v", events)
	}
	waitBackgroundStarted(t, executor)
	startedQueued, err := repo.Get(t.Context(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !isForegroundActive(startedQueued.Status) {
		t.Fatalf("expected queued foreground task to start after restart recovery, got %#v", startedQueued)
	}
}
