package tuisession

import (
	"context"
	"net/http"
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
	response  string
	responses []string
}

func (f *fakeGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	if len(f.responses) > 0 {
		response := f.responses[0]
		f.responses = f.responses[1:]
		return response, nil
	}
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
	submitter := NewDaemonSubmitter(client, root, true, "", false)
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

func TestDaemonSubmitterSendsNextTurnAsUserInputWhenTaskWaits(t *testing.T) {
	root := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{responses: []string{
		"ASK_USER: Which file should I edit?",
		"ANSWER: Resumed.",
	}}))
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner}))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	submitter := NewDaemonSubmitter(client, root, true, "", false)

	first, err := submitter.Submit(t.Context(), "edit it")
	if err != nil {
		t.Fatal(err)
	}
	if first.AgentResult.Status != agent.StatusWaitingUser || !strings.Contains(first.AgentResult.Summary, "Which file") {
		t.Fatalf("expected waiting user result, got %#v", first.AgentResult)
	}
	second, err := submitter.Submit(t.Context(), "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if second.AgentResult.Status != agent.StatusCompleted {
		t.Fatalf("expected completed after input, got %#v", second.AgentResult)
	}
	tasks, err := repo.List(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected input to resume same task, got %#v", tasks)
	}
	events, err := repo.Events(t.Context(), tasks[0].ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !containsEventType(eventTypes(events), taskpkg.EventUserInputReceived) {
		t.Fatalf("expected user input received event, got %#v", eventTypes(events))
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

func TestDaemonSubmitterHandlesMemoryThroughDaemon(t *testing.T) {
	root := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Store: persistentStore}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)

	out, handled, err := submitter.HandleCommand(t.Context(), "/memory add preference prefer compact memory panel")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "Memory saved") || !strings.Contains(out, "[preference source=manual enabled]") {
		t.Fatalf("unexpected add output handled=%v out=%q", handled, out)
	}
	memories, err := persistentStore.ListMemoriesWithOptions(store.MemoryListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 1 {
		t.Fatalf("unexpected stored memories %#v", memories)
	}
	memoryID := memories[0].ID
	out, handled, err = submitter.HandleCommand(t.Context(), "/memory search compact")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, memoryID) || !strings.Contains(out, "prefer compact memory panel") {
		t.Fatalf("unexpected search output handled=%v out=%q", handled, out)
	}
	out, handled, err = submitter.HandleCommand(t.Context(), "/memory edit "+memoryID+" prefer explicit memory panel")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "Memory updated") || !strings.Contains(out, "prefer explicit memory panel") {
		t.Fatalf("unexpected edit output handled=%v out=%q", handled, out)
	}
	out, handled, err = submitter.HandleCommand(t.Context(), "/memory disable "+memoryID)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "Memory disabled") || !strings.Contains(out, "[preference source=manual disabled]") {
		t.Fatalf("unexpected disable output handled=%v out=%q", handled, out)
	}
	out, handled, err = submitter.HandleCommand(t.Context(), "/memory")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || out != "No memories found." {
		t.Fatalf("unexpected list output handled=%v out=%q", handled, out)
	}
	out, handled, err = submitter.HandleCommand(t.Context(), "/memory list all")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, memoryID) || !strings.Contains(out, "prefer explicit memory panel") || !strings.Contains(out, "disabled") {
		t.Fatalf("unexpected list all output handled=%v out=%q", handled, out)
	}
}

func TestDaemonSubmitterHandlesPermissionRulesThroughDaemon(t *testing.T) {
	root := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Store: persistentStore}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)

	out, handled, err := submitter.HandleCommand(t.Context(), "/permission-rule add always_allow tool=run risk=network")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "Permission rule saved") || !strings.Contains(out, "always_allow") || !strings.Contains(out, "workspace="+root) {
		t.Fatalf("unexpected add output handled=%v out=%q", handled, out)
	}
	rules, err := persistentStore.ListPermissionRules(store.PermissionRuleListOptions{Workspace: root, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Tool != "run" || rules[0].Risk != "network" {
		t.Fatalf("unexpected stored rules %#v", rules)
	}
	ruleID := rules[0].ID
	out, handled, err = submitter.HandleCommand(t.Context(), "/permission-rules")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, ruleID) || !strings.Contains(out, "tool=run") {
		t.Fatalf("unexpected list output handled=%v out=%q", handled, out)
	}
	out, handled, err = submitter.HandleCommand(t.Context(), "/permission-rule delete "+ruleID)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "Permission rule deleted") {
		t.Fatalf("unexpected delete output handled=%v out=%q", handled, out)
	}
	rules, err = persistentStore.ListPermissionRules(store.PermissionRuleListOptions{Workspace: root, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected rule deleted, got %#v", rules)
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

type countingBlockingShellExecutor struct {
	started chan string
}

func newCountingBlockingShellExecutor() *countingBlockingShellExecutor {
	return &countingBlockingShellExecutor{started: make(chan string, 10)}
}

func (e *countingBlockingShellExecutor) Run(ctx context.Context, _ string, command string) (tools.ShellResult, error) {
	e.started <- command
	<-ctx.Done()
	return tools.ShellResult{ExitCode: -1}, ctx.Err()
}

func waitCountingStarted(t *testing.T, executor *countingBlockingShellExecutor) {
	t.Helper()
	select {
	case <-executor.started:
	case <-time.After(3 * time.Second):
		t.Fatal("task did not start")
	}
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
	for _, want := range []string{"Diff " + taskID, "已准备好变更", "notes.txt", "+1 -0", "变更预览:", "+ hello", "Next:", "/apply", "/diff"} {
		if !handled || !strings.Contains(diffOutput, want) {
			t.Fatalf("expected diff output to contain %q handled=%v output=%q", want, handled, diffOutput)
		}
	}
	for _, avoid := range []string{"+++ b/notes.txt", "--- a/notes.txt"} {
		if strings.Contains(diffOutput, avoid) {
			t.Fatalf("expected diff output to hide raw header %q, got:\n%s", avoid, diffOutput)
		}
	}
	output, handled, err := submitter.HandleCommand(t.Context(), "/apply")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"已应用变更", "真实工作区已更新", "notes.txt", "继续让我运行测试", "/timeline"} {
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

func TestDaemonSubmitterShowsAndWatchesChildTasksAndThreads(t *testing.T) {
	root := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Store: persistentStore}))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	parentThread, err := client.CreateConversationThread(t.Context(), store.CreateConversationThreadRequest{Workspace: root, Title: "Parent thread"})
	if err != nil {
		t.Fatal(err)
	}
	childThread, err := client.CreateConversationThread(t.Context(), store.CreateConversationThreadRequest{Workspace: root, Title: "Child thread"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: root,
		Prompt:    "parent orchestration",
		ThreadID:  &parentThread.ID,
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace:      root,
		Prompt:         "child investigation",
		ThreadID:       &childThread.ID,
		Natural:        false,
		Origin:         taskpkg.OriginSubagent,
		Automation:     taskpkg.AutomationMetadata{Kind: taskpkg.AutomationKindSubagent, Risk: taskpkg.AutomationRiskSafe},
		ParentTaskID:   parent.ID,
		ParentThreadID: parentThread.ID,
		ChildThreadID:  childThread.ID,
		SubagentName:   "explorer",
		Role:           "search",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), parent.ID, taskpkg.EventSubagentStarted, taskpkg.EventPayload{ParentTaskID: parent.ID, ID: child.ID, Message: "explorer started", Status: string(taskpkg.StatusRunning)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), parent.ID, taskpkg.EventCompleted, taskpkg.EventPayload{Status: string(taskpkg.StatusCompleted), Message: "parent done"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), child.ID, taskpkg.EventSummary, taskpkg.EventPayload{Message: "child found the answer"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), child.ID, taskpkg.EventCompleted, taskpkg.EventPayload{Status: string(taskpkg.StatusCompleted), Message: "child done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateCrossThreadMessage(t.Context(), childThread.ID, store.CreateCrossThreadMessageRequest{
		FromThreadID:    parentThread.ID,
		ToThreadID:      childThread.ID,
		TaskID:          child.ID,
		Summary:         "child handoff ready",
		ExplicitContent: "child thread output",
	}); err != nil {
		t.Fatal(err)
	}
	submitter := newTestSubmitter(t, server.URL, root, false)

	tasksOutput, handled, err := submitter.HandleCommand(t.Context(), "/tasks")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{parent.ID, child.ID, "parent=" + parent.ID, "parent_thread=" + parentThread.ID, "child_thread=" + childThread.ID, "subagent=explorer", "role=search"} {
		if !handled || !strings.Contains(tasksOutput, want) {
			t.Fatalf("expected /tasks output to contain %q handled=%v output=%q", want, handled, tasksOutput)
		}
	}

	threadsOutput, handled, err := submitter.HandleCommand(t.Context(), "/threads")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{childThread.ID, "child_of=via=" + child.ID, "parent=" + parent.ID, "subagent=explorer", "role=search"} {
		if !handled || !strings.Contains(threadsOutput, want) {
			t.Fatalf("expected /threads output to contain %q handled=%v output=%q", want, handled, threadsOutput)
		}
	}

	watchOutput, handled, err := submitter.HandleCommand(t.Context(), "/watch children "+parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Watch child tasks for " + parent.ID, "- " + parent.ID, "- " + child.ID, "subagent.started", "explorer started", "task.summary: child found the answer", "task.completed"} {
		if !handled || !strings.Contains(watchOutput, want) {
			t.Fatalf("expected /watch children output to contain %q handled=%v output=%q", want, handled, watchOutput)
		}
	}

	tailOutput, handled, err := submitter.HandleCommand(t.Context(), "/tail "+child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(tailOutput, "child found the answer") || strings.Contains(tailOutput, "parent done") {
		t.Fatalf("expected /tail child output to expand only child task handled=%v output=%q", handled, tailOutput)
	}

	inboxOutput, handled, err := submitter.HandleCommand(t.Context(), "/thread-inbox "+childThread.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Thread inbox " + childThread.ID, "from=" + parentThread.ID, "task=" + child.ID, "child handoff ready"} {
		if !handled || !strings.Contains(inboxOutput, want) {
			t.Fatalf("expected /thread-inbox child output to contain %q handled=%v output=%q", want, handled, inboxOutput)
		}
	}
}

func TestDaemonSubmitterWatchChildrenHandlesBoundaryInputs(t *testing.T) {
	root := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	parent, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: root,
		Prompt:    "parent with no children",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, false)

	for _, command := range []string{"/watch children", "/watch children " + parent.ID + " extra"} {
		output, handled, err := submitter.HandleCommand(t.Context(), command)
		if err == nil || !handled || output != "" || !strings.Contains(err.Error(), "usage: /watch children <parent_task_id>") {
			t.Fatalf("expected usage error for %q handled=%v output=%q err=%v", command, handled, output, err)
		}
	}
	output, handled, err := submitter.HandleCommand(t.Context(), "/watch children task_missing")
	if err == nil || !handled || output != "" || !strings.Contains(err.Error(), "parent task task_missing was not found") {
		t.Fatalf("expected unknown parent error handled=%v output=%q err=%v", handled, output, err)
	}
	output, handled, err = submitter.HandleCommand(t.Context(), "/watch children "+parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(output, "No child daemon tasks for parent "+parent.ID) {
		t.Fatalf("unexpected no-child output handled=%v output=%q", handled, output)
	}
	output, handled, err = submitter.HandleCommand(t.Context(), "/tail task_missing")
	if err != nil || !handled || !strings.Contains(output, "No event output for task task_missing.") {
		t.Fatalf("expected unknown child tail to return empty output handled=%v output=%q err=%v", handled, output, err)
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
	if err := repo.AppendEvent(t.Context(), sessions[0].LastTaskID, taskpkg.EventArtifactReference, taskpkg.EventPayload{Tool: "shell", Path: ".liora/tool-results/context.txt", Message: "context artifact"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), sessions[0].LastTaskID, taskpkg.EventCompactBoundary, taskpkg.EventPayload{Message: "context compact boundary"}); err != nil {
		t.Fatal(err)
	}
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
	contextOutput, handled, err := fresh.HandleCommand(t.Context(), "/context 8 512")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Context " + sessionID, "Budget:", "Transcript items:", "Artifacts: 1", "Compact boundaries: 1", ".liora/tool-results/context.txt"} {
		if !handled || !strings.Contains(contextOutput, want) {
			t.Fatalf("expected /context output to contain %q handled=%v output=%q", want, handled, contextOutput)
		}
	}
	historyOutput, handled, err := fresh.HandleCommand(t.Context(), "/history second")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"History \"second\"", sessionID, "user: second prompt"} {
		if !handled || !strings.Contains(historyOutput, want) {
			t.Fatalf("expected /history output to contain %q handled=%v output=%q", want, handled, historyOutput)
		}
	}
	resumeLatestOutput, handled, err := fresh.HandleCommand(t.Context(), "/resume-latest")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Resumed session " + sessionID, "Context: transcript_items=", "Transcript:", "User", "second prompt", "Tool result", "completed 1 step"} {
		if !handled || !strings.Contains(resumeLatestOutput, want) {
			t.Fatalf("expected /resume-latest output to contain %q handled=%v output=%q", want, handled, resumeLatestOutput)
		}
	}
	resumeOutput, handled, err := fresh.HandleCommand(t.Context(), "/resume-session "+sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Session " + sessionID, "Context: transcript_items=", "Transcript:", "User", "second prompt", "Tool result", "completed 1 step"} {
		if !handled || !strings.Contains(resumeOutput, want) {
			t.Fatalf("expected /resume-session output to contain %q handled=%v output=%q", want, handled, resumeOutput)
		}
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
	clearOutput, handled, err := fresh.HandleCommand(t.Context(), "/clear")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(clearOutput, "New session will be created") {
		t.Fatalf("unexpected /clear output handled=%v output=%q", handled, clearOutput)
	}
	if _, err := fresh.SubmitStream(t.Context(), "fourth prompt", nil); err != nil {
		t.Fatal(err)
	}
	sessions, err = repo.ListSessions(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 4 {
		t.Fatalf("expected /clear to create one more session, got %#v", sessions)
	}
}

func TestDaemonSubmitterWorkbenchShowsRestartedBackgroundOutputs(t *testing.T) {
	root := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	lost, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace:  root,
		Prompt:     "lost background",
		Natural:    false,
		Origin:     taskpkg.OriginBackground,
		Automation: taskpkg.AutomationMetadata{Kind: taskpkg.AutomationKindBackground, Risk: taskpkg.AutomationRiskSafe},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), lost.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), lost.ID, taskpkg.EventToolResult, taskpkg.EventPayload{Tool: "run", Output: "lost output before restart", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	completed, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace:  root,
		Prompt:     "completed background",
		Natural:    false,
		Origin:     taskpkg.OriginBackground,
		Automation: taskpkg.AutomationMetadata{Kind: taskpkg.AutomationKindBackground, Risk: taskpkg.AutomationRiskSafe},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), completed.ID, taskpkg.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	artifactURI := "artifact://artifacts/sessions/" + completed.SessionID + "/tasks/" + completed.ID + "/tool-results/out.txt"
	if err := repo.AppendEvent(t.Context(), completed.ID, taskpkg.EventToolResult, taskpkg.EventPayload{Tool: "run", Output: "completed output before restart", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), completed.ID, taskpkg.EventArtifactReference, taskpkg.EventPayload{Tool: "run", Path: artifactURI, Message: "completed artifact output"}); err != nil {
		t.Fatal(err)
	}
	empty, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace:  root,
		Prompt:     "empty background",
		Natural:    false,
		Origin:     taskpkg.OriginBackground,
		Automation: taskpkg.AutomationMetadata{Kind: taskpkg.AutomationKindBackground, Risk: taskpkg.AutomationRiskSafe},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)

	workbench, handled, err := submitter.HandleCommand(t.Context(), "/workbench")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Background lost:",
		lost.ID,
		"lost output before restart",
		"Background completed:",
		completed.ID,
		"completed output before restart",
		"/artifact " + artifactURI + " tail",
		empty.ID,
		"(no output yet)",
	} {
		if !handled || !strings.Contains(workbench, want) {
			t.Fatalf("expected workbench to contain %q handled=%v output=%q", want, handled, workbench)
		}
	}
}

func TestDaemonSubmitterShowsPromptContextSourceSummary(t *testing.T) {
	root := t.TempDir()
	persistentStore := store.New(t.TempDir())
	memory, err := persistentStore.CreateMemoryWithOptions(store.CreateMemoryRequest{
		Text:       "prefer source summaries without expanding content",
		Kind:       "preference",
		Workspace:  root,
		Importance: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Store: persistentStore}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)

	created, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: root,
		Prompt:    "diagnose prompt context",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, taskpkg.EventToolCall, taskpkg.EventPayload{Tool: "shell", Input: "cat notes.md"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, taskpkg.EventToolResult, taskpkg.EventPayload{Tool: "shell", Input: "cat notes.md", Output: "prompt context tool result artifact://prompt-context/result.txt", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), created.ID, taskpkg.EventArtifactReference, taskpkg.EventPayload{Tool: "shell", Path: "artifact://prompt-context/result.txt", Message: "prompt context artifact"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.WriteTodos(t.Context(), taskpkg.TodoWriteRequest{
		SessionID:    created.SessionID,
		SourceTaskID: created.ID,
		Todos: []taskpkg.TodoWriteItem{{
			ID:       "todo-prompt-context",
			Content:  "show prompt context source summary",
			Status:   taskpkg.TodoStatusPending,
			Priority: taskpkg.TodoPriorityCritical,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	submitter.rememberSession(created.SessionID)

	output, handled, err := submitter.HandleCommand(t.Context(), "/prompt-context 20 4096")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Prompt context " + created.SessionID,
		"Budget:",
		"Sources:",
		"transcript: selected=",
		"todo: selected=",
		"memory: selected=",
		"artifact_preview: selected=",
		"Diagnostics:",
		"transcript id=",
		"tool_result id=",
		"todo id=todo-prompt-context kind=pending tokens=",
		"memory id=" + memory.ID + " kind=preference tokens=",
		"artifact_preview id=artifact://prompt-context/result.txt kind=artifact_preview tokens=",
		"reason=enabled unexpired memory matched the current workspace",
	} {
		if !handled || !strings.Contains(output, want) {
			t.Fatalf("expected /prompt-context output to contain %q handled=%v output=%q", want, handled, output)
		}
	}
	if strings.Contains(output, "prefer source summaries without expanding content") || strings.Contains(output, "show prompt context source summary") {
		t.Fatalf("/prompt-context should summarize sources without expanding selected content, got %q", output)
	}

	aliasOutput, handled, err := submitter.HandleCommand(t.Context(), "/context sources 20 4096")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(aliasOutput, "Prompt context "+created.SessionID) || !strings.Contains(aliasOutput, "Diagnostics:") {
		t.Fatalf("expected /context sources alias to show prompt context summary handled=%v output=%q", handled, aliasOutput)
	}
}

func TestDaemonSubmitterPromptContextHandlesEmptyAndOmittedSources(t *testing.T) {
	root := t.TempDir()
	persistentStore := store.New(t.TempDir())
	disabled, err := persistentStore.CreateMemoryWithOptions(store.CreateMemoryRequest{
		Text:       "disabled prompt context leak",
		Kind:       "preference",
		Workspace:  root,
		Importance: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistentStore.SetMemoryEnabled(disabled.ID, false); err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().Add(-time.Hour)
	if _, err := persistentStore.CreateMemoryWithOptions(store.CreateMemoryRequest{
		Text:       "expired prompt context leak",
		Kind:       "preference",
		Workspace:  root,
		Importance: 5,
		ExpiresAt:  &expiredAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := persistentStore.CreateMemoryWithOptions(store.CreateMemoryRequest{
		Text:       "cross workspace prompt context leak",
		Kind:       "preference",
		Workspace:  root + "-other",
		Importance: 5,
	}); err != nil {
		t.Fatal(err)
	}
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Store: persistentStore}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)

	output, handled, err := submitter.HandleCommand(t.Context(), "/prompt-context")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(output, "No current daemon session.") {
		t.Fatalf("unexpected empty /prompt-context output handled=%v output=%q", handled, output)
	}

	emptySession, err := repo.CreateSession(t.Context(), taskpkg.CreateSessionRequest{Workspace: root, Title: "empty prompt context"})
	if err != nil {
		t.Fatal(err)
	}
	emptyOutput, handled, err := submitter.HandleCommand(t.Context(), "/prompt-context 10 256")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Prompt context " + emptySession.ID, "Sources:", "transcript: selected=0/0", "Diagnostics: none"} {
		if !handled || !strings.Contains(emptyOutput, want) {
			t.Fatalf("expected empty /prompt-context output to contain %q handled=%v output=%q", want, handled, emptyOutput)
		}
	}
	if _, err := persistentStore.CreateMemoryWithOptions(store.CreateMemoryRequest{
		Text:       strings.Repeat("allowed tight prompt context memory ", 20),
		Kind:       "rule",
		Workspace:  root,
		Importance: 5,
	}); err != nil {
		t.Fatal(err)
	}

	created, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: root,
		Prompt:    strings.Repeat("tight prompt context ", 80),
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := repo.AppendMessage(t.Context(), created.SessionID, "assistant", strings.Repeat("tight transcript ", 90), created.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.WriteTodos(t.Context(), taskpkg.TodoWriteRequest{
		SessionID:    created.SessionID,
		SourceTaskID: created.ID,
		Todos: []taskpkg.TodoWriteItem{{
			ID:       "todo-done-prompt-context",
			Content:  "done prompt context leak",
			Status:   taskpkg.TodoStatusDone,
			Priority: taskpkg.TodoPriorityCritical,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	submitter.rememberSession(created.SessionID)

	tightOutput, handled, err := submitter.HandleCommand(t.Context(), "/prompt-context 20 128")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Prompt context " + created.SessionID, "truncated=true", "Sources:", "Diagnostics:"} {
		if !handled || !strings.Contains(tightOutput, want) {
			t.Fatalf("expected tight /prompt-context output to contain %q handled=%v output=%q", want, handled, tightOutput)
		}
	}
	for _, forbidden := range []string{"disabled prompt context leak", "expired prompt context leak", "cross workspace prompt context leak", "done prompt context leak"} {
		if strings.Contains(tightOutput, forbidden) {
			t.Fatalf("omitted source content %q leaked into /prompt-context output %q", forbidden, tightOutput)
		}
	}
}

func TestDaemonSubmitterCompactCommandWritesManualAndAutoBoundaries(t *testing.T) {
	root := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)

	manualTask, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: root,
		Prompt:    "manual compact prompt",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), manualTask.ID, taskpkg.EventSummary, taskpkg.EventPayload{Message: "manual compact assistant response"}); err != nil {
		t.Fatal(err)
	}
	submitter.rememberSession(manualTask.SessionID)
	manualOutput, handled, err := submitter.HandleCommand(t.Context(), "/compact 20 512")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Compact " + manualTask.SessionID, "Mode: manual", "Compacted: true", "Boundary: Manual compact"} {
		if !handled || !strings.Contains(manualOutput, want) {
			t.Fatalf("expected /compact output to contain %q handled=%v output=%q", want, handled, manualOutput)
		}
	}

	autoTask, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: root,
		Prompt:    strings.Repeat("auto compact prompt ", 120),
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), autoTask.ID, taskpkg.EventToolResult, taskpkg.EventPayload{
		Tool:   "shell",
		Output: strings.Repeat("auto compact output ", 180),
		Status: "ok",
	}); err != nil {
		t.Fatal(err)
	}
	submitter.rememberSession(autoTask.SessionID)
	autoOutput, handled, err := submitter.HandleCommand(t.Context(), "/compact auto 20 128")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Mode: auto", "Compacted: true", "Boundary: Auto compact"} {
		if !handled || !strings.Contains(autoOutput, want) {
			t.Fatalf("expected /compact auto output to contain %q handled=%v output=%q", want, handled, autoOutput)
		}
	}
	duplicateOutput, handled, err := submitter.HandleCommand(t.Context(), "/compact auto 20 128")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Mode: auto", "Compacted: false", "Skipped: already_compacted"} {
		if !handled || !strings.Contains(duplicateOutput, want) {
			t.Fatalf("expected duplicate /compact auto output to contain %q handled=%v output=%q", want, handled, duplicateOutput)
		}
	}
}

func TestDaemonSubmitterResumeCommandsHandleEmptyAndUsage(t *testing.T) {
	root := t.TempDir()
	repo, closeDB := newTestRepository(t)
	defer closeDB()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "unused"}))
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)

	output, handled, err := submitter.HandleCommand(t.Context(), "/resume-latest")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(output, "No previous daemon session for this workspace.") {
		t.Fatalf("unexpected empty /resume-latest output handled=%v output=%q", handled, output)
	}
	output, handled, err = submitter.HandleCommand(t.Context(), "/resume-session")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(output, "Usage: /resume-session <session_id>") {
		t.Fatalf("unexpected /resume-session usage output handled=%v output=%q", handled, output)
	}
}

func TestDaemonSubmitterThreadCommands(t *testing.T) {
	root := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Store: persistentStore}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.CreateConversationThread(t.Context(), store.CreateConversationThreadRequest{Workspace: root, Title: "First"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.CreateConversationThread(t.Context(), store.CreateConversationThreadRequest{Workspace: root, Title: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	third, err := client.CreateConversationThread(t.Context(), store.CreateConversationThreadRequest{Workspace: root, Title: "Third"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateThreadModelConfig(t.Context(), first.ID, store.UpdateThreadModelConfigRequest{Provider: "openai-chat", Model: "gpt-5", Profile: "strong"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateTask(t.Context(), taskpkg.CreateRequest{Workspace: root, ThreadID: &first.ID, Prompt: "active", Natural: false}); err != nil {
		t.Fatal(err)
	}
	threadsOutput, handled, err := submitter.HandleCommand(t.Context(), "/threads")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Threads:", first.ID, second.ID, third.ID, "model=openai-chat/gpt-5", "profile=strong"} {
		if !handled || !strings.Contains(threadsOutput, want) {
			t.Fatalf("expected /threads output to contain %q handled=%v output=%q", want, handled, threadsOutput)
		}
	}
	switchOutput, handled, err := submitter.HandleCommand(t.Context(), "/thread "+second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(switchOutput, "Switched thread "+second.ID) {
		t.Fatalf("unexpected /thread output handled=%v output=%q", handled, switchOutput)
	}
	sendUsage, handled, err := submitter.HandleCommand(t.Context(), "/thread-send")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(sendUsage, "Usage: /thread-send <thread_id> <message>") {
		t.Fatalf("unexpected /thread-send usage handled=%v output=%q", handled, sendUsage)
	}
	sendOutput, handled, err := submitter.HandleCommand(t.Context(), "/thread-send "+third.ID+" Please review the failing smoke output")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(sendOutput, "Sent thread message") || !strings.Contains(sendOutput, "To: "+third.ID) {
		t.Fatalf("unexpected /thread-send output handled=%v output=%q", handled, sendOutput)
	}
	inboxOutput, handled, err := submitter.HandleCommand(t.Context(), "/thread-inbox "+third.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Thread inbox " + third.ID, "from=" + second.ID, "Please review the failing smoke output"} {
		if !handled || !strings.Contains(inboxOutput, want) {
			t.Fatalf("expected /thread-inbox output to contain %q handled=%v output=%q", want, handled, inboxOutput)
		}
	}
	messages, err := client.ListCrossThreadMessages(t.Context(), third.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].FromThreadID != second.ID || messages[0].ToThreadID != third.ID || messages[0].ExplicitContent != "Please review the failing smoke output" {
		t.Fatalf("unexpected persisted thread messages %#v", messages)
	}
	targetTranscript, err := client.SessionMessages(t.Context(), third.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSessionRoleContent(targetTranscript, "thread_message.received", messages[0].ID) {
		t.Fatalf("expected target thread transcript to include received handoff, got %#v", targetTranscript)
	}
	workbenchOutput, handled, err := submitter.HandleCommand(t.Context(), "/workbench")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Threads:", "* " + second.ID, first.ID, "last=", "model=openai-chat/gpt-5"} {
		if !handled || !strings.Contains(workbenchOutput, want) {
			t.Fatalf("expected /workbench thread output to contain %q handled=%v output=%q", want, handled, workbenchOutput)
		}
	}
	renameOutput, handled, err := submitter.HandleCommand(t.Context(), "/thread-rename "+second.ID+" Renamed thread")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(renameOutput, "Renamed thread "+second.ID+": Renamed thread") {
		t.Fatalf("unexpected /thread-rename output handled=%v output=%q", handled, renameOutput)
	}
	archiveOutput, handled, err := submitter.HandleCommand(t.Context(), "/thread-archive "+second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(archiveOutput, "Archived thread "+second.ID) {
		t.Fatalf("unexpected /thread-archive output handled=%v output=%q", handled, archiveOutput)
	}
	threadsOutput, handled, err = submitter.HandleCommand(t.Context(), "/threads")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || strings.Contains(threadsOutput, second.ID) {
		t.Fatalf("/threads should hide archived current-workspace thread by default, got %q", threadsOutput)
	}
	unarchiveOutput, handled, err := submitter.HandleCommand(t.Context(), "/thread-unarchive "+second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(unarchiveOutput, "Unarchived thread "+second.ID) {
		t.Fatalf("unexpected /thread-unarchive output handled=%v output=%q", handled, unarchiveOutput)
	}
}

func TestDaemonSubmitterModelCommandQueriesAndSwitchesThreadModel(t *testing.T) {
	root := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Store: persistentStore}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	noThread, handled, err := submitter.HandleCommand(t.Context(), "/model")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(noThread, "No current thread") {
		t.Fatalf("unexpected /model without thread handled=%v output=%q", handled, noThread)
	}
	thread, err := client.CreateConversationThread(t.Context(), store.CreateConversationThreadRequest{Workspace: root, Title: "Model Switch"})
	if err != nil {
		t.Fatal(err)
	}
	if _, handled, err := submitter.HandleCommand(t.Context(), "/thread "+thread.ID); err != nil || !handled {
		t.Fatalf("failed to switch thread handled=%v err=%v", handled, err)
	}
	defaultModel, handled, err := submitter.HandleCommand(t.Context(), "/model")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Thread model " + thread.ID, "Model: default", "/model set <provider> <model> [profile]"} {
		if !handled || !strings.Contains(defaultModel, want) {
			t.Fatalf("expected default /model output to contain %q handled=%v output=%q", want, handled, defaultModel)
		}
	}
	profileOnlyMissingConfig, handled, err := submitter.HandleCommand(t.Context(), "/model set strong")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(profileOnlyMissingConfig, "No thread model is configured") {
		t.Fatalf("unexpected profile-only missing-config output handled=%v output=%q", handled, profileOnlyMissingConfig)
	}
	updated, handled, err := submitter.HandleCommand(t.Context(), "/model set openai-chat gpt-5 strong")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Updated thread model " + thread.ID, "Provider: openai-chat", "Model: gpt-5", "Profile: strong"} {
		if !handled || !strings.Contains(updated, want) {
			t.Fatalf("expected set output to contain %q handled=%v output=%q", want, handled, updated)
		}
	}
	shown, handled, err := submitter.HandleCommand(t.Context(), "/model")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(shown, "Provider: openai-chat") || !strings.Contains(shown, "Profile: strong") {
		t.Fatalf("unexpected shown model output handled=%v output=%q", handled, shown)
	}
	switchedProfile, handled, err := submitter.HandleCommand(t.Context(), "/model set cheap")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(switchedProfile, "Profile: cheap") {
		t.Fatalf("unexpected profile switch output handled=%v output=%q", handled, switchedProfile)
	}
	config, err := client.GetThreadModelConfig(t.Context(), thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if config.Provider != "openai-chat" || config.Model != "gpt-5" || config.Profile != "cheap" {
		t.Fatalf("unexpected persisted model config %#v", config)
	}
	malformed, handled, err := submitter.HandleCommand(t.Context(), "/model set too many fields here")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(malformed, "Usage: /model") {
		t.Fatalf("unexpected malformed output handled=%v output=%q", handled, malformed)
	}
}

func TestDaemonSubmitterCreatesThreeThreadsAndSpawnsParallelRuns(t *testing.T) {
	root := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	executor := newCountingBlockingShellExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "run sleep 100"}))
	runner.SetSandbox(executor)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{
		Repository: repo,
		Runner:     runner,
		Store:      persistentStore,
		Foreground: daemon.ForegroundLimits{MaxConcurrent: 3, MaxActive: 6},
	}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	usage, handled, err := submitter.HandleCommand(t.Context(), "/thread-new")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(usage, "Usage: /thread-new <title>") {
		t.Fatalf("unexpected /thread-new usage handled=%v output=%q", handled, usage)
	}
	titles := []string{"Alpha", "Beta", "Gamma"}
	for _, title := range titles {
		output, handled, err := submitter.HandleCommand(t.Context(), "/thread-new "+title)
		if err != nil {
			t.Fatal(err)
		}
		if !handled || !strings.Contains(output, "Created thread") || !strings.Contains(output, "Title: "+title) {
			t.Fatalf("unexpected /thread-new output handled=%v output=%q", handled, output)
		}
	}
	threads, err := client.ListConversationThreadsWithOptions(t.Context(), store.ConversationThreadListOptions{Workspace: root, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	threadIDs := map[string]string{}
	for _, thread := range threads {
		threadIDs[thread.Title] = thread.ID
	}
	for _, title := range titles {
		if threadIDs[title] == "" {
			t.Fatalf("missing created thread %q in %#v", title, threads)
		}
	}

	taskIDs := map[string]string{}
	for _, title := range titles {
		threadID := threadIDs[title]
		if _, handled, err := submitter.HandleCommand(t.Context(), "/thread "+threadID); err != nil || !handled {
			t.Fatalf("failed to switch thread %s handled=%v err=%v", threadID, handled, err)
		}
		output, handled, err := submitter.HandleCommand(t.Context(), "/spawn parallel "+title)
		if err != nil {
			t.Fatal(err)
		}
		if !handled || !strings.Contains(output, "Spawned task") {
			t.Fatalf("unexpected /spawn output handled=%v output=%q", handled, output)
		}
		waitCountingStarted(t, executor)
		thread, err := client.GetConversationThread(t.Context(), threadID)
		if err != nil {
			t.Fatal(err)
		}
		if thread.LastTaskID == "" {
			t.Fatalf("expected thread %s to record last task after /spawn", threadID)
		}
		taskIDs[threadID] = thread.LastTaskID
	}
	if len(taskIDs) != 3 {
		t.Fatalf("expected three distinct running thread tasks, got %#v", taskIDs)
	}
	for threadID, taskID := range taskIDs {
		taskRecord, err := repo.Get(t.Context(), taskID)
		if err != nil {
			t.Fatal(err)
		}
		if taskRecord.SessionID != threadID {
			t.Fatalf("expected task %s to stay bound to thread session %s, got %#v", taskID, threadID, taskRecord)
		}
		if taskRecord.Status != taskpkg.StatusPlanning && taskRecord.Status != taskpkg.StatusRunning {
			t.Fatalf("expected task %s to be active, got %#v", taskID, taskRecord)
		}
	}
	betaID := threadIDs["Beta"]
	if _, handled, err := submitter.HandleCommand(t.Context(), "/thread "+betaID); err != nil || !handled {
		t.Fatalf("failed to switch to beta handled=%v err=%v", handled, err)
	}
	workbench, handled, err := submitter.HandleCommand(t.Context(), "/workbench")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Threads:", "* " + betaID, threadIDs["Alpha"], threadIDs["Gamma"], "Active tasks:"} {
		if !handled || !strings.Contains(workbench, want) {
			t.Fatalf("expected /workbench output to contain %q handled=%v output=%q", want, handled, workbench)
		}
	}
	for _, taskID := range taskIDs {
		if _, _, err := submitter.HandleCommand(t.Context(), "/cancel "+taskID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDaemonSubmitterThreadModelMetadataVisibleInTranscript(t *testing.T) {
	t.Setenv("LIORA_AGENT_LOOP", "off")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("model metadata\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"read README.md"}}]}`))
	}))
	defer llmServer.Close()
	registry, err := llm.NewRegistry(llm.Config{
		Provider: llm.ProviderOpenAIChat,
		BaseURL:  llmServer.URL,
		APIKey:   "test-key",
		Model:    "default-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "read README.md"}))
	runner.SetLLMRegistry(registry)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner, Store: persistentStore}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, root, true)
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := client.CreateConversationThread(t.Context(), store.CreateConversationThreadRequest{Workspace: root, Title: "Model bound"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateThreadModelConfig(t.Context(), thread.ID, store.UpdateThreadModelConfigRequest{Provider: "openai-chat", Model: "gpt-5", Profile: "strong"}); err != nil {
		t.Fatal(err)
	}
	if _, handled, err := submitter.HandleCommand(t.Context(), "/thread "+thread.ID); err != nil || !handled {
		t.Fatalf("failed to switch model thread handled=%v err=%v", handled, err)
	}

	result, err := submitter.SubmitStream(t.Context(), "inspect model metadata", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentResult.Status != agent.StatusCompleted {
		t.Fatalf("expected completed result, got %#v", result.AgentResult)
	}
	updatedThread, err := client.GetConversationThread(t.Context(), thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	taskID := updatedThread.LastTaskID
	if taskID == "" {
		t.Fatal("expected thread last task to be recorded")
	}
	events, err := repo.Events(t.Context(), taskID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !eventPayloadHasModel(t, events, taskpkg.EventToolResult, "openai-chat", "gpt-5", "strong") {
		t.Fatalf("expected tool trace event to include resolved model metadata, got %#v", events)
	}
	timeline, err := client.SessionTimeline(t.Context(), thread.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !timelineHasModel(timeline, "openai-chat", "gpt-5", "strong") {
		t.Fatalf("expected timeline to include model metadata, got %#v", timeline)
	}
	transcript, handled, err := submitter.HandleCommand(t.Context(), "/transcript")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"model=openai-chat/gpt-5", "profile=strong"} {
		if !handled || !strings.Contains(transcript, want) {
			t.Fatalf("expected transcript to contain %q handled=%v output=%q", want, handled, transcript)
		}
	}
	workbench, handled, err := submitter.HandleCommand(t.Context(), "/workbench")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{thread.ID, "model=openai-chat/gpt-5", "profile=strong"} {
		if !handled || !strings.Contains(workbench, want) {
			t.Fatalf("expected workbench to contain %q handled=%v output=%q", want, handled, workbench)
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
	for _, want := range []string{"Pending approvals", taskID, "tool_call_id:", "request: run rm -rf build", "risk: dangerous_shell", "command: rm -rf build", "reason: Command contains rm -rf.", "/approve " + taskID, "/deny " + taskID} {
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
	if err := repo.UpdateStatus(t.Context(), second.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), second.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{
		Tool:   "run",
		Input:  "rm -rf other",
		Risk:   "dangerous_shell",
		Reason: "deny branch",
	}); err != nil {
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
	return NewDaemonSubmitter(client, root, natural, "", false)
}

func containsStreamType(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func containsEventType(events []taskpkg.EventType, want taskpkg.EventType) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func eventTypes(events []taskpkg.Event) []taskpkg.EventType {
	types := make([]taskpkg.EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func containsSessionRoleContent(messages []taskpkg.Message, role string, content string) bool {
	for _, message := range messages {
		if message.Role == role && strings.Contains(message.Content, content) {
			return true
		}
	}
	return false
}

func eventPayloadHasModel(t *testing.T, events []taskpkg.Event, eventType taskpkg.EventType, provider string, model string, profile string) bool {
	t.Helper()
	for _, event := range events {
		if event.Type != eventType {
			continue
		}
		payload, err := eventPayload(event)
		if err != nil {
			t.Fatal(err)
		}
		if payload.Provider == provider && payload.Model == model && payload.Profile == profile {
			return true
		}
	}
	return false
}

func timelineHasModel(items []taskpkg.TimelineItem, provider string, model string, profile string) bool {
	for _, item := range items {
		if item.Provider == provider && item.Model == model && item.Profile == profile {
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
