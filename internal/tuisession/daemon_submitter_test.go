package tuisession

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/daemon"
	"github.com/Lioooooo123/liora/internal/daemonclient"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/tui"
)

type fakeGenerator struct {
	response string
}

func (f *fakeGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	return f.response, nil
}

func TestDaemonSubmitterStreamsFromDaemon(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "list ."}))
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner}))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	submitter := NewDaemonSubmitter(client, root, true)
	var streamed []string

	result, err := submitter.SubmitStream(t.Context(), "看看目录", func(update tui.StreamUpdate) {
		streamed = append(streamed, update.Type)
	})
	if err != nil {
		events, eventsErr := repo.Events(t.Context(), findOnlyTaskID(t, repo), 100)
		t.Fatalf("submit failed: %v result=%#v streamed=%#v events=%#v eventsErr=%v", err, result, streamed, events, eventsErr)
	}
	if result.PlannedSteps != "list ." {
		t.Fatalf("unexpected planned steps %q", result.PlannedSteps)
	}
	if len(result.Events) != 1 || result.Events[0].Tool != "list" || !strings.Contains(result.Events[0].Output, "README.md") {
		t.Fatalf("unexpected result events %#v", result.Events)
	}
	if result.AgentResult.Status != agent.StatusCompleted {
		t.Fatalf("unexpected result status %#v", result.AgentResult)
	}
	for _, want := range []string{string(taskpkg.EventPlanReady), string(taskpkg.EventToolResult), string(taskpkg.EventCompleted)} {
		if !containsStreamType(streamed, want) {
			t.Fatalf("expected streamed event %s in %#v", want, streamed)
		}
	}
}

func findOnlyTaskID(t *testing.T, repo *taskpkg.Repository) string {
	t.Helper()
	tasks, err := repo.List(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one task, got %#v", tasks)
	}
	return tasks[0].ID
}

func containsStreamType(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}
