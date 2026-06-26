package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/apply"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/tools"
)

type fakeGenerator struct {
	response string
}

func (f *fakeGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	return f.response, nil
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

func TestServerCreatesTaskAndServesEvents(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Runner:     taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "write notes.txt hello\nread notes.txt"})),
	}))
	defer server.Close()

	body := strings.NewReader(`{"workspace":` + quote(workspace) + `,"prompt":"创建 notes","natural":true}`)
	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected create status %d", resp.StatusCode)
	}
	var created taskpkg.CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Task.ID == "" || created.Task.Status != taskpkg.StatusCompleted {
		t.Fatalf("unexpected created task %#v", created.Task)
	}

	resp, err = http.Get(server.URL + "/v1/tasks/" + created.Task.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var events []taskpkg.Event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if !hasEvent(events, taskpkg.EventCompleted) {
		t.Fatalf("expected completed event, got %#v", events)
	}

	resp, err = http.Get(server.URL + "/v1/tasks/" + created.Task.ID + "/events/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sse strings.Builder
	if _, err := io.Copy(&sse, resp.Body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sse.String(), "event: task.completed") {
		t.Fatalf("expected completed SSE event, got:\n%s", sse.String())
	}
}

func TestServerServesCapabilities(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := httptest.NewServer(NewServer(Config{Repository: taskpkg.NewRepository(db)}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}
	var body struct {
		Tools []struct {
			Name  string `json:"name"`
			Usage string `json:"usage"`
			Kind  string `json:"kind"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var foundRead bool
	var foundRunShell bool
	for _, tool := range body.Tools {
		if tool.Name == "read" {
			foundRead = true
		}
		if tool.Usage == "run <shell command>" && tool.Kind == "shell" {
			foundRunShell = true
		}
	}
	if !foundRead || !foundRunShell {
		t.Fatalf("unexpected capabilities response %#v", body)
	}
}

func TestServerServesMCPToolsInCapabilities(t *testing.T) {
	if os.Getenv("LIORA_DAEMON_FAKE_MCP_SERVER") == "1" {
		runDaemonFakeMCPServer()
		return
	}
	storeRoot := t.TempDir()
	s := store.New(storeRoot)
	if err := s.SaveMCPConfig(store.MCPConfig{Servers: map[string]store.MCPServerConfig{
		"fake": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestServerServesMCPToolsInCapabilities"},
			Env:     map[string]string{"LIORA_DAEMON_FAKE_MCP_SERVER": "1"},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	db, err := s.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := httptest.NewServer(NewServer(Config{Repository: taskpkg.NewRepository(db), Store: s}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}
	var body struct {
		MCPTools []struct {
			Server string `json:"server"`
			Name   string `json:"name"`
			Usage  string `json:"usage"`
			Kind   string `json:"kind"`
		} `json:"mcp_tools"`
		MCPError string `json:"mcp_error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.MCPError != "" {
		t.Fatalf("unexpected mcp error %q", body.MCPError)
	}
	if len(body.MCPTools) != 1 || body.MCPTools[0].Server != "fake" || body.MCPTools[0].Name != "echo" || body.MCPTools[0].Kind != "external" {
		t.Fatalf("unexpected mcp capabilities %#v", body.MCPTools)
	}
	if body.MCPTools[0].Usage != "mcp fake echo <json arguments>" {
		t.Fatalf("unexpected mcp usage %q", body.MCPTools[0].Usage)
	}
}

func TestServerServesSessionTranscript(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(`{"workspace":`+quote(workspace)+`,"prompt":"first","natural":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected create status %d", resp.StatusCode)
	}
	var first taskpkg.CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	if first.Task.SessionID == "" {
		t.Fatalf("expected session id in task %#v", first.Task)
	}

	resp, err = http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(`{"workspace":`+quote(workspace)+`,"prompt":"second","session_id":`+quote(first.Task.SessionID)+`,"natural":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected second create status %d", resp.StatusCode)
	}

	resp, err = http.Get(server.URL + "/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sessions []taskpkg.Session
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != first.Task.SessionID {
		t.Fatalf("unexpected sessions %#v", sessions)
	}

	resp, err = http.Get(server.URL + "/v1/sessions/" + first.Task.SessionID + "/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var messages []taskpkg.Message
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Content != "first" || messages[1].Content != "second" {
		t.Fatalf("unexpected messages %#v", messages)
	}

	resp, err = http.Get(server.URL + "/v1/sessions/" + first.Task.SessionID + "/tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tasks []taskpkg.Task
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].SessionID != first.Task.SessionID {
		t.Fatalf("unexpected session tasks %#v", tasks)
	}
	if err := repo.AppendEvent(t.Context(), first.Task.ID, taskpkg.EventSummary, taskpkg.EventPayload{Message: "first done"}); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(server.URL + "/v1/sessions/" + first.Task.SessionID + "/timeline")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var timeline []taskpkg.TimelineItem
	if err := json.NewDecoder(resp.Body).Decode(&timeline); err != nil {
		t.Fatal(err)
	}
	var timelineText strings.Builder
	for _, item := range timeline {
		timelineText.WriteString(item.Role)
		timelineText.WriteString(item.Content)
		timelineText.WriteByte('\n')
	}
	if !strings.Contains(timelineText.String(), "first") || !strings.Contains(timelineText.String(), "second") || !strings.Contains(timelineText.String(), "first done") {
		t.Fatalf("unexpected timeline %#v", timeline)
	}

	resp, err = http.Get(server.URL + "/v1/timeline/search?workspace=" + url.QueryEscape(workspace) + "&q=first")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var matches []taskpkg.TimelineItem
	if err := json.NewDecoder(resp.Body).Decode(&matches); err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected timeline search matches")
	}
	var searchText strings.Builder
	for _, item := range matches {
		searchText.WriteString(item.Content)
		searchText.WriteString(item.Title)
	}
	if !strings.Contains(searchText.String(), "first") {
		t.Fatalf("unexpected timeline search matches %#v", matches)
	}
}

func TestServerServesWorkspaceWorkbench(t *testing.T) {
	workspace := t.TempDir()
	otherWorkspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	session, err := repo.CreateSession(t.Context(), taskpkg.CreateSessionRequest{Workspace: workspace, Title: "main session"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "active task",
		SessionID: session.ID,
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), active.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}
	pending, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "run rm -rf build",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), pending.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), pending.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{
		Tool:   "run",
		Input:  "rm -rf build",
		Risk:   "dangerous_shell",
		Reason: "Command contains rm -rf.",
	}); err != nil {
		t.Fatal(err)
	}
	other, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: otherWorkspace,
		Prompt:    "other task",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), other.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()
	resp, err := http.Get(server.URL + "/v1/workbench?workspace=" + url.QueryEscape(workspace))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected workbench status %d", resp.StatusCode)
	}
	var workbench taskpkg.Workbench
	if err := json.NewDecoder(resp.Body).Decode(&workbench); err != nil {
		t.Fatal(err)
	}
	if workbench.Workspace != workspace {
		t.Fatalf("unexpected workspace %q", workbench.Workspace)
	}
	if !hasSession(workbench.Sessions, session.ID) {
		t.Fatalf("unexpected sessions %#v", workbench.Sessions)
	}
	if !hasTask(workbench.ActiveTasks, active.ID) || !hasTask(workbench.ActiveTasks, pending.ID) {
		t.Fatalf("expected active and pending tasks in active list, got %#v", workbench.ActiveTasks)
	}
	if !hasTask(workbench.RecentTasks, active.ID) || !hasTask(workbench.RecentTasks, pending.ID) || hasTask(workbench.RecentTasks, other.ID) {
		t.Fatalf("unexpected recent tasks %#v", workbench.RecentTasks)
	}
	if len(workbench.PendingApprovals) != 1 || workbench.PendingApprovals[0].Task.ID != pending.ID {
		t.Fatalf("unexpected pending approvals %#v", workbench.PendingApprovals)
	}
	if workbench.PendingApprovals[0].Request.Risk != "dangerous_shell" {
		t.Fatalf("unexpected approval request %#v", workbench.PendingApprovals[0].Request)
	}
}

func TestServerServesDiffAndAppliesPatch(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "patch notes",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	patch, err := apply.CreatePatch(workspace, []apply.FileChange{{Path: "notes.txt", Before: "", After: "hello\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventDiff, taskpkg.EventPayload{Diff: patch}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()
	resp, err := http.Get(server.URL + "/v1/tasks/" + task.ID + "/diff")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "+++ b/notes.txt") {
		t.Fatalf("unexpected diff response %s", string(data))
	}

	resp, err = http.Post(server.URL+"/v1/tasks/"+task.ID+"/apply", "application/json", strings.NewReader(`{"patch":`+quote(patch)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected apply status %d", resp.StatusCode)
	}
	applied, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(applied), "notes.txt") {
		t.Fatalf("unexpected apply response %s", string(applied))
	}
}

func TestEventStreamWaitsForNewEventsUntilTaskCompletes(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "slow task",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	done := make(chan string, 1)
	go func() {
		resp, err := http.Get(server.URL + "/v1/tasks/" + task.ID + "/events/stream")
		if err != nil {
			done <- err.Error()
			return
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			done <- err.Error()
			return
		}
		done <- string(data)
	}()

	time.Sleep(100 * time.Millisecond)
	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventSummary, taskpkg.EventPayload{Message: "later"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), task.ID, taskpkg.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventCompleted, taskpkg.EventPayload{Status: string(taskpkg.StatusCompleted)}); err != nil {
		t.Fatal(err)
	}

	select {
	case body := <-done:
		if !strings.Contains(body, "event: task.summary") || !strings.Contains(body, "later") || !strings.Contains(body, "event: task.completed") {
			t.Fatalf("stream did not include later events:\n%s", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish after task completion")
	}
}

func TestServerCancelsTask(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "long task",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/tasks/"+task.ID+"/cancel", "application/json", strings.NewReader(`{"reason":"user clicked stop"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected cancel status %d", resp.StatusCode)
	}
	var cancelled taskpkg.Task
	if err := json.NewDecoder(resp.Body).Decode(&cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != taskpkg.StatusCancelled {
		t.Fatalf("unexpected cancelled task %#v", cancelled)
	}
	stream, err := http.Get(server.URL + "/v1/tasks/" + task.ID + "/events/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	data, err := io.ReadAll(stream.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "event: task.cancelled") || !strings.Contains(string(data), "user clicked stop") {
		t.Fatalf("unexpected cancel stream:\n%s", string(data))
	}
}

func TestServerCancelStopsRunningAsyncTask(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	executor := newBlockingShellExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(executor)
	handler := newServer(Config{Repository: repo, Runner: runner})
	server := httptest.NewServer(handler.routes())
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(`{"workspace":`+quote(t.TempDir())+`,"prompt":"run long-task","natural":false,"run_async":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected async create status %d", resp.StatusCode)
	}
	var created taskpkg.CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	select {
	case <-executor.started:
	case <-time.After(3 * time.Second):
		t.Fatal("async runner did not start")
	}

	cancelResp, err := http.Post(server.URL+"/v1/tasks/"+created.Task.ID+"/cancel", "application/json", strings.NewReader(`{"reason":"stop now"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer cancelResp.Body.Close()
	if cancelResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected cancel status %d", cancelResp.StatusCode)
	}

	select {
	case <-executor.done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not stop running async task")
	}
	waitUntil(t, 3*time.Second, func() bool {
		return !handler.isRunning(created.Task.ID)
	})

	got, err := repo.Get(t.Context(), created.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != taskpkg.StatusCancelled {
		t.Fatalf("cancelled async task should stay cancelled, got %#v", got)
	}
}

func TestServerApprovesWaitingPermissionTask(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetPermissionPolicy(permission.Policy{Mode: permission.ModePrompt})
	server := httptest.NewServer(NewServer(Config{Repository: repo, Runner: runner}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(`{"workspace":`+quote(t.TempDir())+`,"prompt":"run rm -rf build","natural":false}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected create status %d", resp.StatusCode)
	}
	var created taskpkg.CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Task.Status != taskpkg.StatusWaitingUser {
		t.Fatalf("expected waiting task, got %#v", created.Task)
	}

	stream, err := http.Get(server.URL + "/v1/tasks/" + created.Task.ID + "/events/stream")
	if err != nil {
		t.Fatal(err)
	}
	streamData, err := io.ReadAll(stream.Body)
	_ = stream.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(streamData), "event: permission.requested") {
		t.Fatalf("expected permission stream event, got:\n%s", string(streamData))
	}

	approve, err := http.Post(server.URL+"/v1/tasks/"+created.Task.ID+"/approval", "application/json", strings.NewReader(`{"decision":"approve"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer approve.Body.Close()
	if approve.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected approve status %d", approve.StatusCode)
	}
	waitUntil(t, 3*time.Second, func() bool {
		task, err := repo.Get(t.Context(), created.Task.ID)
		if err != nil {
			t.Fatal(err)
		}
		return task.Status == taskpkg.StatusCompleted
	})
}

func TestServerDeniesWaitingPermissionTask(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "run rm -rf build",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), task.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/tasks/"+task.ID+"/approval", "application/json", strings.NewReader(`{"decision":"deny","reason":"too risky"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected deny status %d", resp.StatusCode)
	}
	cancelled, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != taskpkg.StatusCancelled {
		t.Fatalf("expected cancelled task, got %#v", cancelled)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEvent(events, taskpkg.EventPermissionDenied) {
		t.Fatalf("expected permission denied event, got %#v", events)
	}
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

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func hasEvent(events []taskpkg.Event, eventType taskpkg.EventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func hasTask(tasks []taskpkg.Task, taskID string) bool {
	for _, task := range tasks {
		if task.ID == taskID {
			return true
		}
	}
	return false
}

func hasSession(sessions []taskpkg.Session, sessionID string) bool {
	for _, session := range sessions {
		if session.ID == sessionID {
			return true
		}
	}
	return false
}

func runDaemonFakeMCPServer() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		method, _ := req["method"].(string)
		id := req["id"]
		if method == "notifications/initialized" {
			continue
		}
		switch method {
		case "initialize":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": "2025-06-18",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "fake", "version": "0.0.1"},
				},
			})
		case "tools/list":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "echo",
						"description": "Echo text",
						"inputSchema": map[string]any{"type": "object"},
					}},
				},
			})
		case "tools/call":
			params, _ := req["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": fmt.Sprint(args["text"])}},
				},
			})
		default:
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}
	os.Exit(0)
}

func TestMain(m *testing.M) {
	if os.Getenv("LIORA_DAEMON_FAKE_MCP_SERVER") == "1" {
		runDaemonFakeMCPServer()
		return
	}
	os.Exit(m.Run())
}
