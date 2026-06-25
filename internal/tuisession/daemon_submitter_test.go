package tuisession

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/daemon"
	"github.com/Lioooooo123/liora/internal/daemonclient"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/tools"
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

func TestDaemonSubmitterCancelsCurrentTask(t *testing.T) {
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	executor := newBlockingShellExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "run sleep 100"}))
	runner.SetSandbox(executor)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, t.TempDir(), true)

	done := make(chan submitOutcome, 1)
	go func() {
		result, err := submitter.SubmitStream(t.Context(), "slow task", nil)
		done <- submitOutcome{result: result, err: err}
	}()
	select {
	case <-executor.started:
	case <-time.After(3 * time.Second):
		t.Fatal("task did not start")
	}
	output, handled, err := submitter.HandleCommand(t.Context(), "/cancel")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(output, "Cancelled task") {
		t.Fatalf("unexpected cancel output handled=%v output=%q", handled, output)
	}
	select {
	case <-executor.done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not stop shell")
	}
	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("cancelled submit should not return an error, got %v", outcome.err)
		}
		if outcome.result.AgentResult.Summary != "cancelled" {
			t.Fatalf("expected cancelled summary, got %#v", outcome.result.AgentResult)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("submit did not finish after cancel")
	}
}

type submitOutcome struct {
	result tui.TurnResult
	err    error
}

func TestDaemonSubmitterAppliesLastDiff(t *testing.T) {
	root := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "write notes.txt hello"}))
	runner.SetPatchMode(true)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)

	result, err := submitter.SubmitStream(t.Context(), "create notes", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.AgentResult.Diff) == "" {
		t.Fatalf("expected diff, got %#v", result.AgentResult)
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); err == nil {
		t.Fatal("patch mode should not mutate real workspace before apply")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	output, handled, err := submitter.HandleCommand(t.Context(), "/apply")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Applied task", "Files:", "notes.txt", "Next:", "/timeline"} {
		if !handled || !strings.Contains(output, want) {
			t.Fatalf("expected apply output to contain %q handled=%v output=%q", want, handled, output)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("unexpected applied file %q", string(data))
	}
}

func TestDaemonSubmitterApplyWithoutTask(t *testing.T) {
	submitter := &DaemonSubmitter{client: nil}
	output, handled, err := submitter.HandleCommand(t.Context(), "/apply")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(output, "No daemon task") {
		t.Fatalf("unexpected output handled=%v output=%q", handled, output)
	}
}

func TestDaemonSubmitterListsAndReplaysTasks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "list ."}))
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)

	result, err := submitter.SubmitStream(t.Context(), "list files", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.PlannedSteps != "list ." {
		t.Fatalf("unexpected plan %q", result.PlannedSteps)
	}
	tasksOutput, handled, err := submitter.HandleCommand(t.Context(), "/tasks")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(tasksOutput, "list files") || !strings.Contains(tasksOutput, "completed") {
		t.Fatalf("unexpected /tasks output handled=%v output=%q", handled, tasksOutput)
	}
	taskID := findOnlyTaskID(t, repo)
	lastOutput, handled, err := submitter.HandleCommand(t.Context(), "/last")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Task " + taskID, "Events:", "task.plan_ready", "tool.result", "task.completed"} {
		if !handled || !strings.Contains(lastOutput, want) {
			t.Fatalf("expected /last output to contain %q handled=%v output=%q", want, handled, lastOutput)
		}
	}
	resumeOutput, handled, err := submitter.HandleCommand(t.Context(), "/resume "+taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(resumeOutput, "Task "+taskID) {
		t.Fatalf("unexpected /resume output handled=%v output=%q", handled, resumeOutput)
	}
}

func TestDaemonSubmitterListsAndResumesSessions(t *testing.T) {
	root := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "list ."}))
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)

	if _, err := submitter.SubmitStream(t.Context(), "first prompt", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := submitter.SubmitStream(t.Context(), "second prompt", nil); err != nil {
		t.Fatal(err)
	}
	sessions, err := repo.ListSessions(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one reused session, got %#v", sessions)
	}
	sessionID := sessions[0].ID

	sessionsOutput, handled, err := submitter.HandleCommand(t.Context(), "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(sessionsOutput, sessionID) || !strings.Contains(sessionsOutput, "* "+sessionID) {
		t.Fatalf("unexpected /sessions output handled=%v output=%q", handled, sessionsOutput)
	}

	sessionOutput, handled, err := submitter.HandleCommand(t.Context(), "/session")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Session " + sessionID, "first prompt", "second prompt", "Tasks:"} {
		if !handled || !strings.Contains(sessionOutput, want) {
			t.Fatalf("expected /session output to contain %q handled=%v output=%q", want, handled, sessionOutput)
		}
	}

	fresh := newTestSubmitter(t, server.URL, root, true)
	resumeOutput, handled, err := fresh.HandleCommand(t.Context(), "/resume-session "+sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(resumeOutput, "Session "+sessionID) || !strings.Contains(resumeOutput, "second prompt") {
		t.Fatalf("unexpected /resume-session output handled=%v output=%q", handled, resumeOutput)
	}
	timelineOutput, handled, err := fresh.HandleCommand(t.Context(), "/timeline")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Timeline " + sessionID, "user: first prompt", "user: second prompt", "tool.result", "completed 1 step"} {
		if !handled || !strings.Contains(timelineOutput, want) {
			t.Fatalf("expected /timeline output to contain %q handled=%v output=%q", want, handled, timelineOutput)
		}
	}
}

func TestDaemonSubmitterApprovesAndDeniesWaitingTask(t *testing.T) {
	root := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetPermissionPolicy(permission.Policy{Mode: permission.ModePrompt})
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, false)
	var streamed []string

	result, err := submitter.SubmitStream(t.Context(), "run rm -rf build", func(update tui.StreamUpdate) {
		streamed = append(streamed, update.Type)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentResult.Status != agent.StatusWaitingUser || !containsStreamType(streamed, string(taskpkg.EventPermissionRequest)) {
		t.Fatalf("expected waiting approval result=%#v streamed=%#v", result.AgentResult, streamed)
	}
	output, handled, err := submitter.HandleCommand(t.Context(), "/approve")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Approved task", "Status:", "Next:", "/timeline"} {
		if !handled || !strings.Contains(output, want) {
			t.Fatalf("expected approve output to contain %q handled=%v output=%q", want, handled, output)
		}
	}
	waitUntil(t, 3*time.Second, func() bool {
		tasks, err := repo.List(t.Context(), 10)
		if err != nil {
			t.Fatal(err)
		}
		return len(tasks) == 1 && tasks[0].Status == taskpkg.StatusCompleted
	})

	second, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: root, Prompt: "run rm -rf other", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	submitter.rememberTask(second.ID)
	output, handled, err = submitter.HandleCommand(t.Context(), "/deny")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Denied task", "Status:", "Next:", "/timeline"} {
		if !handled || !strings.Contains(output, want) {
			t.Fatalf("expected deny output to contain %q handled=%v output=%q", want, handled, output)
		}
	}
	denied, err := repo.Get(t.Context(), second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if denied.Status != taskpkg.StatusCancelled {
		t.Fatalf("expected denied task to be cancelled, got %#v", denied)
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

func newTestRepository(t *testing.T) (*taskpkg.Repository, func()) {
	t.Helper()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	return taskpkg.NewRepository(db), func() { _ = db.Close() }
}

func newTestSubmitter(t *testing.T, serverURL string, root string, natural bool) *DaemonSubmitter {
	t.Helper()
	client, err := daemonclient.New(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	return NewDaemonSubmitter(client, root, natural)
}

func containsStreamType(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
