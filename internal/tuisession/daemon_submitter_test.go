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

func TestDaemonSubmitterShowsDaemonCapabilitiesWithMCPTools(t *testing.T) {
	root := t.TempDir()
	storeRoot := t.TempDir()
	mcpScript := filepath.Join(storeRoot, "fake_mcp.py")
	if err := os.WriteFile(mcpScript, []byte(fakeMCPServerPython()), 0o700); err != nil {
		t.Fatal(err)
	}
	s := store.New(storeRoot)
	if err := s.SaveMCPConfig(store.MCPConfig{Servers: map[string]store.MCPServerConfig{
		"fake": {
			Command: "python3",
			Args:    []string{mcpScript},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	db, err := s.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Store: s}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)

	output, handled, err := submitter.HandleCommand(t.Context(), "/tools")
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected /tools to be handled by daemon submitter")
	}
	for _, want := range []string{"Built-in tools", "read <path>", "MCP tools", "mcp fake echo <json arguments>", "Echo text"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected /tools output to contain %q, got:\n%s", want, output)
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

func TestDaemonSubmitterSpawnsBackgroundTask(t *testing.T) {
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	executor := newBlockingShellExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "run sleep 100"}))
	runner.SetSandbox(executor)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, t.TempDir(), true)

	output, handled, err := submitter.HandleCommand(t.Context(), "/spawn slow task")
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected /spawn to be handled")
	}
	taskID := findOnlyTaskID(t, repo)
	for _, want := range []string{"Spawned task " + taskID, "Status:", "Session:", "/watch " + taskID, "/tail " + taskID} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected /spawn output to contain %q, got:\n%s", want, output)
		}
	}
	select {
	case <-executor.started:
	case <-time.After(3 * time.Second):
		t.Fatal("spawned task did not start in background")
	}
	taskRecord, err := repo.Get(t.Context(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if taskRecord.Status != taskpkg.StatusPlanning && taskRecord.Status != taskpkg.StatusRunning {
		t.Fatalf("expected spawned task to stay active, got %#v", taskRecord)
	}
	if _, _, err := submitter.HandleCommand(t.Context(), "/cancel "+taskID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not stop spawned task")
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
	taskID := findOnlyTaskID(t, repo)
	diffOutput, handled, err := submitter.HandleCommand(t.Context(), "/diff")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Diff " + taskID, "+++ b/notes.txt", "+hello", "Next:", "/apply"} {
		if !handled || !strings.Contains(diffOutput, want) {
			t.Fatalf("expected diff output to contain %q handled=%v output=%q", want, handled, diffOutput)
		}
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

func TestDaemonSubmitterDiffWithoutTask(t *testing.T) {
	submitter := &DaemonSubmitter{client: nil}
	output, handled, err := submitter.HandleCommand(t.Context(), "/diff")
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

func TestDaemonSubmitterReplayShowsFailureDiagnostics(t *testing.T) {
	root := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	taskRecord, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: root,
		Prompt:    "read missing.txt",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), taskRecord.ID, taskpkg.StatusFailed); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), taskRecord.ID, taskpkg.EventToolResult, taskpkg.EventPayload{
		Tool:   "read",
		Input:  "missing.txt",
		Output: "missing.txt: no such file or directory",
		Status: "error",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), taskRecord.ID, taskpkg.EventError, taskpkg.EventPayload{
		Message: "failed at step 1/1: read missing.txt",
		Status:  string(taskpkg.StatusFailed),
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, false)

	output, handled, err := submitter.HandleCommand(t.Context(), "/last")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Task " + taskRecord.ID, "[failed]", "tool.result: [error] read missing.txt", "missing.txt: no such file", "task.error: failed failed at step 1/1"} {
		if !handled || !strings.Contains(output, want) {
			t.Fatalf("expected /last output to contain %q handled=%v output=%q", want, handled, output)
		}
	}
}

func TestDaemonSubmitterTailsRecentTaskOutput(t *testing.T) {
	root := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	taskRecord, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: root,
		Prompt:    "run long output",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), taskRecord.ID, taskpkg.EventToolResult, taskpkg.EventPayload{
		Tool:   "run",
		Input:  "long output",
		Output: "line1\nline2\nline3\nline4\nline5",
		Status: "ok",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), taskRecord.ID, taskpkg.EventCompleted, taskpkg.EventPayload{Status: string(taskpkg.StatusCompleted)}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, false)

	output, handled, err := submitter.HandleCommand(t.Context(), "/tail 3")
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected /tail to be handled")
	}
	for _, want := range []string{"Tail " + taskRecord.ID + " last 3 lines", "line4", "line5", "task.completed"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected /tail output to contain %q, got:\n%s", want, output)
		}
	}
	if strings.Contains(output, "line1") {
		t.Fatalf("expected /tail to omit older lines, got:\n%s", output)
	}
}

func TestDaemonSubmitterWatchesActiveWorkspaceTasks(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	first, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: root,
		Prompt:    "first active",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: root,
		Prompt:    "second active",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: otherRoot,
		Prompt:    "other active",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, taskID := range []string{first.ID, second.ID, other.ID} {
		if err := repo.UpdateStatus(t.Context(), taskID, taskpkg.StatusRunning); err != nil {
			t.Fatal(err)
		}
	}
	for _, record := range []struct {
		id      string
		message string
	}{
		{id: first.ID, message: "first streamed"},
		{id: second.ID, message: "second streamed"},
		{id: other.ID, message: "other streamed"},
	} {
		if err := repo.AppendEvent(t.Context(), record.id, taskpkg.EventSummary, taskpkg.EventPayload{Message: record.message}); err != nil {
			t.Fatal(err)
		}
		if err := repo.AppendEvent(t.Context(), record.id, taskpkg.EventCompleted, taskpkg.EventPayload{Status: string(taskpkg.StatusCompleted)}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, false)

	output, handled, err := submitter.HandleCommand(t.Context(), "/watch")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Watch tasks", first.ID, second.ID, "task.summary: first streamed", "task.summary: second streamed", "task.completed"} {
		if !handled || !strings.Contains(output, want) {
			t.Fatalf("expected /watch output to contain %q handled=%v output=%q", want, handled, output)
		}
	}
	if strings.Contains(output, other.ID) || strings.Contains(output, "other streamed") {
		t.Fatalf("/watch should be scoped to current workspace active tasks, got %q", output)
	}
}

func TestDaemonSubmitterWatchesExplicitTasks(t *testing.T) {
	root := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	taskRecord, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: root,
		Prompt:    "explicit",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), taskRecord.ID, taskpkg.EventToolResult, taskpkg.EventPayload{
		Tool:   "run",
		Input:  "echo hi",
		Output: "hi",
		Status: "ok",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), taskRecord.ID, taskpkg.EventCompleted, taskpkg.EventPayload{Status: string(taskpkg.StatusCompleted)}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, t.TempDir(), false)

	output, handled, err := submitter.HandleCommand(t.Context(), "/watch "+taskRecord.ID+" "+taskRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Watch tasks", taskRecord.ID, "tool.result[ok]: run echo hi hi", "task.completed"} {
		if !handled || !strings.Contains(output, want) {
			t.Fatalf("expected explicit /watch output to contain %q handled=%v output=%q", want, handled, output)
		}
	}
	if strings.Count(output, "- "+taskRecord.ID) != 1 {
		t.Fatalf("expected explicit /watch to deduplicate task ids, got %q", output)
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
	otherWorkspace := t.TempDir()
	otherTask, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: otherWorkspace,
		Prompt:    "other workspace prompt",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	sessionsOutput, handled, err := submitter.HandleCommand(t.Context(), "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(sessionsOutput, sessionID) || !strings.Contains(sessionsOutput, "* "+sessionID) {
		t.Fatalf("unexpected /sessions output handled=%v output=%q", handled, sessionsOutput)
	}
	if strings.Contains(sessionsOutput, otherTask.SessionID) {
		t.Fatalf("/sessions should be scoped to current workspace, got %q", sessionsOutput)
	}
	workbenchOutput, handled, err := submitter.HandleCommand(t.Context(), "/workbench")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Workbench " + root, "Sessions:", sessionID, "Active tasks:", "Recent tasks:"} {
		if !handled || !strings.Contains(workbenchOutput, want) {
			t.Fatalf("expected /workbench output to contain %q handled=%v output=%q", want, handled, workbenchOutput)
		}
	}
	if strings.Contains(workbenchOutput, otherTask.ID) || strings.Contains(workbenchOutput, otherTask.SessionID) {
		t.Fatalf("/workbench should be scoped to current workspace, got %q", workbenchOutput)
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
	timelineOutput, handled, err := fresh.HandleCommand(t.Context(), "/timeline")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Timeline " + sessionID, "user: first prompt", "user: second prompt", "tool.result", "completed 1 step"} {
		if !handled || !strings.Contains(timelineOutput, want) {
			t.Fatalf("expected auto-resumed /timeline output to contain %q handled=%v output=%q", want, handled, timelineOutput)
		}
	}
	transcriptOutput, handled, err := fresh.HandleCommand(t.Context(), "/transcript 20")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Transcript " + sessionID + " last 20 items", "User", "first prompt", "Tool result", "completed 1 step"} {
		if !handled || !strings.Contains(transcriptOutput, want) {
			t.Fatalf("expected /transcript output to contain %q handled=%v output=%q", want, handled, transcriptOutput)
		}
	}
	resumeLatestOutput, handled, err := fresh.HandleCommand(t.Context(), "/resume-latest")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(resumeLatestOutput, "Resumed session "+sessionID) {
		t.Fatalf("unexpected /resume-latest output handled=%v output=%q", handled, resumeLatestOutput)
	}
	resumeOutput, handled, err := fresh.HandleCommand(t.Context(), "/resume-session "+sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(resumeOutput, "Session "+sessionID) || !strings.Contains(resumeOutput, "second prompt") {
		t.Fatalf("unexpected /resume-session output handled=%v output=%q", handled, resumeOutput)
	}
	newOutput, handled, err := fresh.HandleCommand(t.Context(), "/new-session")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(newOutput, "New session will be created") {
		t.Fatalf("unexpected /new-session output handled=%v output=%q", handled, newOutput)
	}
	if _, err := fresh.SubmitStream(t.Context(), "third prompt", nil); err != nil {
		t.Fatal(err)
	}
	sessions, err = repo.ListSessions(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected /new-session to create one more current-workspace session, got %#v", sessions)
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
	taskID := findOnlyTaskID(t, repo)
	otherWorkspaceTask, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: t.TempDir(), Prompt: "run rm -rf elsewhere", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), otherWorkspaceTask.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), otherWorkspaceTask.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{
		Tool:   "run",
		Input:  "rm -rf elsewhere",
		Risk:   "dangerous_shell",
		Reason: "other workspace",
	}); err != nil {
		t.Fatal(err)
	}
	pendingOutput, handled, err := submitter.HandleCommand(t.Context(), "/approvals")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Pending approvals", taskID, "request: run rm -rf build", "risk: dangerous_shell", "reason: Command contains rm -rf.", "/approve " + taskID, "/deny " + taskID} {
		if !handled || !strings.Contains(pendingOutput, want) {
			t.Fatalf("expected approvals output to contain %q handled=%v output=%q", want, handled, pendingOutput)
		}
	}
	if strings.Contains(pendingOutput, otherWorkspaceTask.ID) || strings.Contains(pendingOutput, "elsewhere") {
		t.Fatalf("/approvals should be scoped to current workspace, got %q", pendingOutput)
	}
	output, handled, err := submitter.HandleCommand(t.Context(), "/approve "+taskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Approved task", "Status:", "Next:", "/timeline"} {
		if !handled || !strings.Contains(output, want) {
			t.Fatalf("expected approve output to contain %q handled=%v output=%q", want, handled, output)
		}
	}
	waitUntil(t, 3*time.Second, func() bool {
		taskRecord, err := repo.Get(t.Context(), taskID)
		if err != nil {
			t.Fatal(err)
		}
		return taskRecord.Status == taskpkg.StatusCompleted
	})

	second, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: root, Prompt: "run rm -rf other", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	submitter.rememberTask(second.ID)
	output, handled, err = submitter.HandleCommand(t.Context(), "/deny "+second.ID)
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

func fakeMCPServerPython() string {
	return `import json
import sys

for line in sys.stdin:
    try:
        req = json.loads(line)
    except Exception:
        continue
    method = req.get("method")
    req_id = req.get("id")
    if method == "notifications/initialized":
        continue
    if method == "initialize":
        result = {
            "protocolVersion": "2025-06-18",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "fake", "version": "0.0.1"},
        }
        print(json.dumps({"jsonrpc": "2.0", "id": req_id, "result": result}), flush=True)
    elif method == "tools/list":
        result = {
            "tools": [{
                "name": "echo",
                "description": "Echo text",
                "inputSchema": {"type": "object"},
            }]
        }
        print(json.dumps({"jsonrpc": "2.0", "id": req_id, "result": result}), flush=True)
    else:
        error = {"code": -32601, "message": "method not found"}
        print(json.dumps({"jsonrpc": "2.0", "id": req_id, "error": error}), flush=True)
`
}
