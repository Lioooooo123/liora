package daemon

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

type panicOnPromptGenerator struct {
	panicPrompt string
	response    string
}

func newPanicOnPromptGenerator(panicPrompt string, response string) *panicOnPromptGenerator {
	return &panicOnPromptGenerator{panicPrompt: panicPrompt, response: response}
}

func (g *panicOnPromptGenerator) Generate(_ context.Context, messages []llm.Message) (string, error) {
	for _, message := range messages {
		if strings.Contains(message.Content, g.panicPrompt) {
			panic("planner panic for isolation test")
		}
	}
	return g.response, nil
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

func TestServerChildTaskScopeRequiresParentSubset(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()
	workspace := t.TempDir()
	src := filepath.Join(workspace, "src")

	parent := postTaskForTest(t, server.URL, fmt.Sprintf(`{
			"workspace":%s,
			"prompt":"parent scope",
			"scope":{
				"paths":[%s],
			"network_hosts":["api.internal"],
			"mcp_servers":["filesystem"],
			"mcp_tools":["filesystem.read"],
			"approval_actions":["apply_patch"]
		}
		}`, quote(workspace), quote(workspace)))
	child := postTaskForTest(t, server.URL, fmt.Sprintf(`{
			"workspace":%s,
		"prompt":"child scope",
		"parent_task_id":%s,
		"scope":{
				"paths":[%s],
			"network_hosts":["api.internal"],
			"mcp_servers":["filesystem"],
			"mcp_tools":["filesystem.read"],
			"approval_actions":["apply_patch"]
		}
		}`, quote(workspace), quote(parent.Task.ID), quote(src)))
	if child.Task.ParentTaskID != parent.Task.ID || !child.Task.InheritedScopeFromParent {
		t.Fatalf("expected child to inherit bounded parent scope, got %#v", child.Task)
	}
	if len(child.Task.ApprovalGrants) != 0 {
		t.Fatalf("child must not carry approval grants: %#v", child.Task.ApprovalGrants)
	}
	if got := child.Task.Scope.Paths; len(got) != 1 || got[0] != src {
		t.Fatalf("unexpected child scope %#v", child.Task.Scope)
	}

	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(fmt.Sprintf(`{
			"workspace":%s,
		"prompt":"escalate",
		"parent_task_id":%s,
		"scope":{"network_hosts":["public.example.com"]}
		}`, quote(workspace), quote(parent.Task.ID))))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected escalation to fail with 400, got %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body["error"], "outside parent scope") {
		t.Fatalf("unexpected error body %#v", body)
	}
}

func TestServerTaskRelationMetadataRoundTrips(t *testing.T) {
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{Repository: repo, Store: persistentStore}))
	defer server.Close()
	workspace := t.TempDir()
	parentThread := createTestThread(t, server.URL, workspace, "Parent thread")
	childThread := createTestThread(t, server.URL, workspace, "Child thread")

	parent := postTaskForTest(t, server.URL, `{
		"workspace":`+quote(workspace)+`,
		"thread_id":`+quote(parentThread.ID)+`,
		"prompt":"parent relation",
		"scope":{"paths":[`+quote(workspace)+`]}
	}`)
	child := postTaskForTest(t, server.URL, fmt.Sprintf(`{
		"workspace":%s,
		"thread_id":%s,
		"prompt":"child relation",
		"parent_task_id":%s,
		"parent_thread_id":%s,
		"child_thread_id":%s,
		"subagent_name":"review-worker",
		"role":"reviewer",
		"scope":{"paths":[%s]}
	}`, quote(workspace), quote(childThread.ID), quote(parent.Task.ID), quote(parentThread.ID), quote(childThread.ID), quote(workspace+"/src")))
	if child.Task.ParentThreadID != parentThread.ID || child.Task.ChildThreadID != childThread.ID {
		t.Fatalf("expected child relation metadata in create response, got %#v", child.Task)
	}
	if child.Task.SubagentName != "review-worker" || child.Task.Role != "reviewer" {
		t.Fatalf("expected child subagent metadata in create response, got %#v", child.Task)
	}

	resp, err := http.Get(server.URL + "/v1/tasks/" + child.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected get status %d", resp.StatusCode)
	}
	var got taskpkg.Task
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ParentThreadID != parentThread.ID || got.ChildThreadID != childThread.ID || got.SubagentName != "review-worker" || got.Role != "reviewer" {
		t.Fatalf("expected get to round-trip relation metadata, got %#v", got)
	}

	resp, err = http.Get(server.URL + "/v1/tasks?workspace=" + url.QueryEscape(workspace) + "&limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected list status %d", resp.StatusCode)
	}
	var listed []taskpkg.Task
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if !taskListHasRelationMetadata(listed, child.Task.ID, parentThread.ID, childThread.ID) {
		t.Fatalf("expected list to include child relation metadata, got %#v", listed)
	}

	resp, err = http.Get(server.URL + "/v1/workbench?workspace=" + url.QueryEscape(workspace) + "&limit=10")
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
	if !workbenchHasRelationMetadata(workbench, child.Task.ID, parentThread.ID, childThread.ID) {
		t.Fatalf("expected workbench to include child relation metadata, got %#v", workbench)
	}
}

func TestServerPermissionRulesPersistAndAffectTasks(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "run curl https://example.com/data.json"}))
	server := httptest.NewServer(NewServer(Config{Repository: repo, Runner: runner, Store: persistentStore}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/permission-rules", "application/json", strings.NewReader(`{
		"action":"always_ask",
		"workspace":`+quote(workspace)+`,
		"tool":"run",
		"risk":"network",
		"reason":"saved ask rule"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected create rule status %d: %s", resp.StatusCode, string(body))
	}
	var rule store.PermissionRule
	if err := json.NewDecoder(resp.Body).Decode(&rule); err != nil {
		t.Fatal(err)
	}
	if rule.ID == "" || rule.Action != store.PermissionRuleAlwaysAsk {
		t.Fatalf("unexpected rule %#v", rule)
	}
	reloaded, err := persistentStore.ListPermissionRules(store.PermissionRuleListOptions{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != 1 || reloaded[0].ID != rule.ID {
		t.Fatalf("expected persisted rule, got %#v", reloaded)
	}

	body := strings.NewReader(`{"workspace":` + quote(workspace) + `,"prompt":"fetch data","natural":true}`)
	resp, err = http.Post(server.URL+"/v1/tasks", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		responseBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected create task status %d: %s", resp.StatusCode, string(responseBody))
	}
	var created taskpkg.CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Task.Status != taskpkg.StatusWaitingUser {
		t.Fatalf("expected task to pause for saved permission rule, got %#v", created.Task)
	}
	events, err := repo.Events(t.Context(), created.Task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEvent(events, taskpkg.EventPermissionRequest) {
		t.Fatalf("expected permission request event, got %#v", events)
	}
}

func TestServerPermissionRulesRejectInvalidRequests(t *testing.T) {
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := httptest.NewServer(NewServer(Config{Repository: taskpkg.NewRepository(db), Store: persistentStore}))
	defer server.Close()

	for _, body := range []string{
		`{"action":"sometimes","workspace":"/repo"}`,
		`{"action":"always_allow"}`,
		`{"action":"always_allow","workspace":"/repo","tool":"unknown"}`,
		`{"action":"always_allow","workspace":"/repo","risk":"unknown"}`,
		`{"action":"always_allow","scope":"workspace"}`,
	} {
		resp, err := http.Post(server.URL+"/v1/permission-rules", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			responseBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("expected 400 for %s, got %d: %s", body, resp.StatusCode, string(responseBody))
		}
		resp.Body.Close()
	}
	rules, err := persistentStore.ListPermissionRules(store.PermissionRuleListOptions{IncludeDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("invalid requests should not create rules, got %#v", rules)
	}
}

func postTaskForTest(t *testing.T, baseURL string, body string) taskpkg.CreateResponse {
	t.Helper()
	resp, err := http.Post(baseURL+"/v1/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		responseBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected create status %d: %s", resp.StatusCode, string(responseBody))
	}
	var created taskpkg.CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return created
}

func taskListHasRelationMetadata(tasks []taskpkg.Task, taskID string, parentThreadID string, childThreadID string) bool {
	for _, task := range tasks {
		if task.ID == taskID && task.ParentThreadID == parentThreadID && task.ChildThreadID == childThreadID {
			return true
		}
	}
	return false
}

func workbenchHasRelationMetadata(workbench taskpkg.Workbench, taskID string, parentThreadID string, childThreadID string) bool {
	if taskListHasRelationMetadata(workbench.ActiveTasks, taskID, parentThreadID, childThreadID) ||
		taskListHasRelationMetadata(workbench.QueuedTasks, taskID, parentThreadID, childThreadID) ||
		taskListHasRelationMetadata(workbench.RecentTasks, taskID, parentThreadID, childThreadID) {
		return true
	}
	for _, thread := range workbench.Threads {
		if taskListHasRelationMetadata(thread.ActiveTasks, taskID, parentThreadID, childThreadID) ||
			taskListHasRelationMetadata(thread.QueuedTasks, taskID, parentThreadID, childThreadID) ||
			taskListHasRelationMetadata(thread.RecentTasks, taskID, parentThreadID, childThreadID) {
			return true
		}
	}
	return false
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

func TestServerStartsWithOldDatabaseFixtureAndKeepsMigrationIdempotent(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "liora.db")
	loadDaemonSQLFixture(t, dbPath, "old-liora-v0.1.sql")
	persistentStore := store.New(root)

	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{Repository: repo, Store: persistentStore}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/memories?include_disabled=true")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected memories status %d", resp.StatusCode)
	}
	var memories []store.Memory
	if err := json.NewDecoder(resp.Body).Decode(&memories); err != nil {
		t.Fatal(err)
	}
	if len(memories) != 1 || memories[0].Text != "legacy sqlite memory" {
		t.Fatalf("expected legacy sqlite memory through daemon API, got %#v", memories)
	}
	if _, err := db.Exec(`INSERT INTO todos (id, title, schema_version) VALUES ('todo-daemon-migration', 'new table writable', ?)`, store.CurrentSchemaVersion); err != nil {
		t.Fatalf("expected new schema-versioned table to be writable: %v", err)
	}
	assertDaemonSchemaVersion(t, db, store.CurrentSchemaVersion)
	server.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server = httptest.NewServer(NewServer(Config{Repository: taskpkg.NewRepository(db), Store: persistentStore}))
	defer server.Close()
	assertDaemonSchemaVersion(t, db, store.CurrentSchemaVersion)
	assertDaemonRowCount(t, db, `SELECT COUNT(*) FROM memories WHERE text = 'legacy sqlite memory'`, 1)
	assertDaemonRowCount(t, db, `SELECT COUNT(*) FROM memory_types WHERE kind IN ('note', 'preference', 'rule', 'automation')`, 4)
	assertDaemonRowCount(t, db, `SELECT COUNT(*) FROM todos WHERE id = 'todo-daemon-migration'`, 1)
}

func TestServerCapabilityGateProtectsSensitiveAPIs(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	persistentStore := store.New(t.TempDir())
	patchTask, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "patch notes",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	approvalTask, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "needs approval",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), approvalTask.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), approvalTask.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{Message: "approval needed"}); err != nil {
		t.Fatal(err)
	}
	patch, err := apply.CreatePatch(workspace, []apply.FileChange{{Path: "notes.txt", Before: "", After: "hello\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), patchTask.ID, taskpkg.EventDiff, taskpkg.EventPayload{Diff: patch}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), patchTask.ID, taskpkg.EventArtifactReference, taskpkg.EventPayload{Path: ".liora/artifacts/notes.txt", Message: "artifact ref"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Store:      persistentStore,
		AuthToken:  "secret-token",
	}))
	defer server.Close()

	for _, path := range []string{"/healthz", "/v1/capabilities"} {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected public %s to return 200, got %d", path, resp.StatusCode)
		}
	}
	unauthorizedCases := []struct {
		name   string
		method string
		path   string
		body   string
		header string
	}{
		{name: "missing memory credential", method: http.MethodPost, path: "/v1/memories", body: `{"text":"stolen memory","kind":"note"}`},
		{name: "malformed bearer", method: http.MethodGet, path: "/v1/memories", header: "Bearer"},
		{name: "wrong bearer", method: http.MethodPost, path: "/v1/tasks/" + approvalTask.ID + "/approval", body: `{"decision":"approve"}`, header: "Bearer wrong-token"},
		{name: "query token apply", method: http.MethodPost, path: "/v1/tasks/" + patchTask.ID + "/apply?token=secret-token", body: `{"patch":` + quote(patch) + `}`},
		{name: "body token schedule task", method: http.MethodPost, path: "/v1/tasks", body: `{"token":"secret-token","workspace":` + quote(workspace) + `,"prompt":"nightly","natural":true,"run_async":true,"origin":"schedule","automation":{"kind":"schedule","risk":"safe"}}`},
		{name: "wrong token hook task", method: http.MethodPost, path: "/v1/tasks", body: `{"workspace":` + quote(workspace) + `,"prompt":"hook","natural":true,"run_async":true,"origin":"hook","automation":{"kind":"hook","risk":"safe"}}`, header: "Bearer wrong-token"},
		{name: "query token artifact event read", method: http.MethodGet, path: "/v1/tasks/" + patchTask.ID + "/events?token=secret-token"},
	}
	for _, tc := range unauthorizedCases {
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			request, err := http.NewRequest(tc.method, server.URL+tc.path, body)
			if err != nil {
				t.Fatal(err)
			}
			if tc.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			if tc.header != "" {
				request.Header.Set("Authorization", tc.header)
			}
			resp, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("expected unauthorized %s request, got %d", tc.name, resp.StatusCode)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(workspace, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("unauthorized apply changed workspace, stat err=%v", err)
	}
	approvalAfterReject, err := repo.Get(t.Context(), approvalTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approvalAfterReject.Status != taskpkg.StatusWaitingUser || approvalAfterReject.ApprovalGranted {
		t.Fatalf("unauthorized approval changed task: %#v", approvalAfterReject)
	}
	tasksAfterReject, err := repo.List(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasksAfterReject) != 2 {
		t.Fatalf("unauthorized schedule/hook task creation changed task list: %#v", tasksAfterReject)
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/v1/memories", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Liora-Capability", "secret-token")
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected authorized memories request, got %d", resp.StatusCode)
	}
	authMemory, err := http.NewRequest(http.MethodPost, server.URL+"/v1/memories", strings.NewReader(`{"text":"token-protected memory","kind":"note"}`))
	if err != nil {
		t.Fatal(err)
	}
	authMemory.Header.Set("Authorization", "Bearer secret-token")
	authMemory.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(authMemory)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected authorized memory create, got %d", resp.StatusCode)
	}
	authEvents, err := http.NewRequest(http.MethodGet, server.URL+"/v1/tasks/"+patchTask.ID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	authEvents.Header.Set("Authorization", "Bearer secret-token")
	resp, err = http.DefaultClient.Do(authEvents)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected authorized artifact event read, got %d", resp.StatusCode)
	}
	authApprove, err := http.NewRequest(http.MethodPost, server.URL+"/v1/tasks/"+approvalTask.ID+"/approval", strings.NewReader(`{"decision":"approve"}`))
	if err != nil {
		t.Fatal(err)
	}
	authApprove.Header.Set("Authorization", "Bearer secret-token")
	authApprove.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(authApprove)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected authorized approval, got %d", resp.StatusCode)
	}
	authApply, err := http.NewRequest(http.MethodPost, server.URL+"/v1/tasks/"+patchTask.ID+"/apply", strings.NewReader(`{"patch":`+quote(patch)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	authApply.Header.Set("Authorization", "Bearer secret-token")
	authApply.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(authApply)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected authorized apply, got %d", resp.StatusCode)
	}
	for _, body := range []string{
		`{"workspace":` + quote(workspace) + `,"prompt":"nightly","natural":true,"origin":"schedule","automation":{"kind":"schedule","risk":"safe"}}`,
		`{"workspace":` + quote(workspace) + `,"prompt":"hook","natural":true,"origin":"hook","automation":{"kind":"hook","risk":"safe"}}`,
	} {
		authCreate, err := http.NewRequest(http.MethodPost, server.URL+"/v1/tasks", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		authCreate.Header.Set("Authorization", "Bearer secret-token")
		authCreate.Header.Set("Content-Type", "application/json")
		resp, err = http.DefaultClient.Do(authCreate)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected authorized schedule/hook task create, got %d", resp.StatusCode)
		}
	}
}

func loadDaemonSQLFixture(t *testing.T, dbPath string, name string) {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("../store/testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(payload)); err != nil {
		t.Fatal(err)
	}
}

func assertDaemonSchemaVersion(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected schema version %d, got %d", want, got)
	}
}

func assertDaemonRowCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected row count %d for %q, got %d", want, query, got)
	}
}

func TestServerServesMemoryAPI(t *testing.T) {
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := httptest.NewServer(NewServer(Config{Repository: taskpkg.NewRepository(db), Store: persistentStore}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/memories", "application/json", strings.NewReader(`{"text":"prefer tiny local ui token=secret","kind":"preference","source":"test","workspace":"/repo-a","importance":5}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected create status %d", resp.StatusCode)
	}
	var created store.Memory
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || strings.Contains(created.Text, "secret") || created.Kind != "preference" || created.Source != "test" || created.Workspace != "/repo-a" || created.Importance != 5 || !created.Enabled {
		t.Fatalf("unexpected created memory %#v", created)
	}

	patchBody := strings.NewReader(`{"text":"prefer tiny local daemon ui","importance":4}`)
	request, err := http.NewRequest(http.MethodPatch, server.URL+"/v1/memories/"+url.PathEscape(created.ID), patchBody)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected patch status %d", resp.StatusCode)
	}
	var updated store.Memory
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Text != "prefer tiny local daemon ui" || updated.Importance != 4 {
		t.Fatalf("unexpected updated memory %#v", updated)
	}

	resp, err = http.Post(server.URL+"/v1/memories/"+url.PathEscape(created.ID)+"/disable", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected disable status %d", resp.StatusCode)
	}

	resp, err = http.Get(server.URL + "/v1/memories?q=tiny")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected search status %d", resp.StatusCode)
	}
	var memories []store.Memory
	if err := json.NewDecoder(resp.Body).Decode(&memories); err != nil {
		t.Fatal(err)
	}
	if len(memories) != 0 {
		t.Fatalf("expected disabled memory hidden by default, got %#v", memories)
	}
	resp, err = http.Get(server.URL + "/v1/memories?q=tiny&include_disabled=true")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected include disabled status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&memories); err != nil {
		t.Fatal(err)
	}
	if len(memories) != 1 || memories[0].Text != "prefer tiny local daemon ui" || memories[0].Enabled {
		t.Fatalf("unexpected memories %#v", memories)
	}
	resp, err = http.Get(server.URL + "/v1/memories?q=tiny&workspace=%2Frepo-b&include_disabled=true")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected wrong workspace status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&memories); err != nil {
		t.Fatal(err)
	}
	if len(memories) != 0 {
		t.Fatalf("expected workspace isolation, got %#v", memories)
	}

	resp, err = http.Post(server.URL+"/v1/memories/"+url.PathEscape(created.ID)+"/enable", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected enable status %d", resp.StatusCode)
	}

	request, err = http.NewRequest(http.MethodDelete, server.URL+"/v1/memories/"+url.PathEscape(created.ID)+"?workspace=%2Frepo-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected wrong workspace delete 404, got %d", resp.StatusCode)
	}
	request, err = http.NewRequest(http.MethodDelete, server.URL+"/v1/memories/"+url.PathEscape(created.ID)+"?workspace=%2Frepo-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected delete 204, got %d", resp.StatusCode)
	}

	resp, err = http.Post(server.URL+"/v1/memories", "application/json", strings.NewReader(`{"text":"bad","kind":"unknown","importance":3}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected unknown kind 400, got %d", resp.StatusCode)
	}
	resp, err = http.Post(server.URL+"/v1/memories", "application/json", strings.NewReader(`{"text":"bad","kind":"note","importance":9}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid importance 400, got %d", resp.StatusCode)
	}
	resp, err = http.Post(server.URL+"/v1/memories", "application/json", strings.NewReader(`{"text":"   ","kind":"note"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected blank text 400, got %d", resp.StatusCode)
	}
	request, err = http.NewRequest(http.MethodPatch, server.URL+"/v1/memories/missing", strings.NewReader(`{"text":"nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected missing id 404, got %d", resp.StatusCode)
	}
}

func TestServerCrossThreadHandoffSharesOnlyMinimalFields(t *testing.T) {
	workspace := t.TempDir()
	st := store.New(t.TempDir())
	db, err := st.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "full prompt api_key=sk-live-1234567890abcdef should stay local",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateMemoryWithOptions(store.CreateMemoryRequest{Workspace: workspace, Text: "remember raw token=secret-value"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo, Store: st}))
	defer server.Close()

	source := createTestThread(t, server.URL, workspace, "Source")
	target := createTestThread(t, server.URL, workspace, "Target")
	body := strings.NewReader(`{
		"from_thread_id":` + quote(source.ID) + `,
		"task_id":` + quote(task.ID) + `,
		"summary":"Audit finished; secret material omitted",
		"explicit_content":"Please inspect the referenced audit output.",
		"artifact_refs":[{"path":".liora/artifacts/audit.txt","summary":"audit output"}],
		"prompt":"full prompt api_key=sk-live-1234567890abcdef should stay local",
		"secret":"sk-live-1234567890abcdef",
		"memory":"remember raw token=secret-value",
		"approval_rule":"approval: always allow"
	}`)
	resp, err := http.Post(server.URL+"/v1/threads/"+target.ID+"/messages", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected handoff status %d", resp.StatusCode)
	}
	var message store.CrossThreadMessage
	if err := json.NewDecoder(resp.Body).Decode(&message); err != nil {
		t.Fatal(err)
	}
	assertNoCrossThreadLeak(t, message)
	if message.Content != message.ExplicitContent || message.Summary == "" || len(message.ArtifactRefs) != 1 {
		t.Fatalf("unexpected minimal handoff %#v", message)
	}
	if message.TaskID != task.ID {
		t.Fatalf("expected explicit task reference %s, got %#v", task.ID, message)
	}

	resp, err = http.Get(server.URL + "/v1/threads/" + target.ID + "/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var messages []store.CrossThreadMessage
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID != message.ID {
		t.Fatalf("unexpected thread inbox %#v", messages)
	}
	assertNoCrossThreadLeak(t, messages[0])

	sourceTimeline, err := repo.Timeline(t.Context(), source.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	targetTimeline, err := repo.Timeline(t.Context(), target.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !timelineHasRoleContent(sourceTimeline, "thread_message.sent", message.ID) || !timelineHasRoleContent(sourceTimeline, "thread_link.created", target.ID) {
		t.Fatalf("expected source transcript to include sent/link events, got %#v", sourceTimeline)
	}
	if !timelineHasRoleContent(sourceTimeline, "thread_message.sent", "task_id="+task.ID) {
		t.Fatalf("expected source transcript to include explicit task reference, got %#v", sourceTimeline)
	}
	if !timelineHasRoleContent(targetTimeline, "thread_message.received", message.ID) || !timelineHasRoleContent(targetTimeline, "thread_link.created", source.ID) {
		t.Fatalf("expected target transcript to include received/link events, got %#v", targetTimeline)
	}
	if !timelineHasRoleContent(targetTimeline, "thread_message.received", "task_id="+task.ID) {
		t.Fatalf("expected target transcript to include explicit task reference, got %#v", targetTimeline)
	}
	assertNoCrossThreadLeak(t, sourceTimeline)
	assertNoCrossThreadLeak(t, targetTimeline)
}

func TestServerCrossThreadHandoffTranscriptSurvivesDaemonRestart(t *testing.T) {
	workspace := t.TempDir()
	st := store.New(t.TempDir())
	db, err := st.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "handoff task", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo, Store: st}))
	source := createTestThread(t, server.URL, workspace, "Source")
	target := createTestThread(t, server.URL, workspace, "Target")
	resp, err := http.Post(server.URL+"/v1/threads/"+target.ID+"/messages", "application/json", strings.NewReader(`{
		"from_thread_id":`+quote(source.ID)+`,
		"task_id":`+quote(task.ID)+`,
		"summary":"Restart-safe handoff summary",
		"explicit_content":"Continue from the attached restart artifact.",
		"artifact_refs":[{"path":".liora/artifacts/restart.txt","summary":"restart artifact"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected handoff status %d", resp.StatusCode)
	}
	var message store.CrossThreadMessage
	if err := json.NewDecoder(resp.Body).Decode(&message); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	server.Close()

	restartedRepo := taskpkg.NewRepository(db)
	restarted := httptest.NewServer(NewServer(Config{Repository: restartedRepo, Store: st}))
	defer restarted.Close()
	resp, err = http.Get(restarted.URL + "/v1/threads/" + target.ID + "/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var inbox []store.CrossThreadMessage
	if err := json.NewDecoder(resp.Body).Decode(&inbox); err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 1 || inbox[0].ID != message.ID || len(inbox[0].ArtifactRefs) != 1 {
		t.Fatalf("expected restarted daemon to recover inbox message, got %#v", inbox)
	}
	sourceTimeline := getSessionTimeline(t, restarted.URL, source.ID)
	targetTimeline := getSessionTimeline(t, restarted.URL, target.ID)
	if !timelineHasRoleContent(sourceTimeline, "thread_message.sent", message.ID) || !timelineHasRoleContent(sourceTimeline, "thread_link.created", target.ID) {
		t.Fatalf("expected restarted source transcript to recover sent/link entries, got %#v", sourceTimeline)
	}
	if !timelineHasRoleContent(targetTimeline, "thread_message.received", message.ID) || !timelineHasRoleContent(targetTimeline, "thread_link.created", source.ID) {
		t.Fatalf("expected restarted target transcript to recover received/link entries, got %#v", targetTimeline)
	}
	if !timelineHasRoleContent(targetTimeline, "thread_message.received", "restart artifact") {
		t.Fatalf("expected restarted target transcript to recover artifact summary, got %#v", targetTimeline)
	}
}

func TestServerCrossThreadHandoffRequiresCrossWorkspaceAuthorization(t *testing.T) {
	st := store.New(t.TempDir())
	server := httptest.NewServer(NewServer(Config{Store: st}))
	defer server.Close()
	source := createTestThread(t, server.URL, "/repo-a", "Source")
	target := createTestThread(t, server.URL, "/repo-b", "Target")

	body := strings.NewReader(`{"from_thread_id":` + quote(source.ID) + `,"summary":"handoff summary"}`)
	resp, err := http.Post(server.URL+"/v1/threads/"+target.ID+"/messages", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected forbidden cross-workspace handoff, got %d", resp.StatusCode)
	}

	body = strings.NewReader(`{"from_thread_id":` + quote(source.ID) + `}`)
	resp, err = http.Post(server.URL+"/v1/threads/"+target.ID+"/messages", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected bad request for empty handoff, got %d", resp.StatusCode)
	}

	body = strings.NewReader(`{"from_thread_id":` + quote(source.ID) + `,"summary":"handoff summary","cross_workspace_authorized":true,"cross_workspace_auth_reason":"user forwarded explicitly"}`)
	resp, err = http.Post(server.URL+"/v1/threads/"+target.ID+"/messages", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected authorized cross-workspace handoff, got %d", resp.StatusCode)
	}
	var message store.CrossThreadMessage
	if err := json.NewDecoder(resp.Body).Decode(&message); err != nil {
		t.Fatal(err)
	}
	if !message.CrossWorkspaceAuthorized || message.CrossWorkspaceAuthReason == "" {
		t.Fatalf("expected authorization metadata, got %#v", message)
	}
}

func TestServerServesMCPToolsInCapabilities(t *testing.T) {
	if os.Getenv("LIORA_DAEMON_FAKE_MCP_SERVER") == "1" {
		runDaemonFakeMCPServer()
		return
	}
	storeRoot := t.TempDir()
	s := store.New(storeRoot)
	disabled := false
	if err := s.SaveMCPConfig(store.MCPConfig{Servers: map[string]store.MCPServerConfig{
		"disabled": {
			Command:     os.Args[0],
			Args:        []string{"-test.run=TestServerServesMCPToolsInCapabilities"},
			Env:         map[string]string{"LIORA_DAEMON_FAKE_MCP_SERVER": "fail"},
			Enabled:     &disabled,
			Source:      "workspace",
			Version:     "0.9.0",
			Permissions: []string{"network:blocked.example.test"},
		},
		"fake": {
			Command:     os.Args[0],
			Args:        []string{"-test.run=TestServerServesMCPToolsInCapabilities"},
			Env:         map[string]string{"LIORA_DAEMON_FAKE_MCP_SERVER": "1"},
			Source:      "global",
			Version:     "1.2.3",
			Permissions: []string{"filesystem:read"},
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
			Server      string   `json:"server"`
			Name        string   `json:"name"`
			Usage       string   `json:"usage"`
			Kind        string   `json:"kind"`
			Permissions []string `json:"permissions"`
		} `json:"mcp_tools"`
		MCPServers []struct {
			Name        string   `json:"name"`
			Enabled     bool     `json:"enabled"`
			Source      string   `json:"source"`
			Version     string   `json:"version"`
			Permissions []string `json:"permissions"`
			ToolCount   int      `json:"tool_count"`
			Auth        string   `json:"auth"`
			LastError   string   `json:"last_error"`
		} `json:"mcp_servers"`
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
	if len(body.MCPTools[0].Permissions) != 1 || body.MCPTools[0].Permissions[0] != "filesystem:read" {
		t.Fatalf("unexpected mcp tool permissions %#v", body.MCPTools[0].Permissions)
	}
	if len(body.MCPServers) != 2 {
		t.Fatalf("expected two mcp server statuses, got %#v", body.MCPServers)
	}
	if body.MCPServers[0].Name != "disabled" || body.MCPServers[0].Enabled || body.MCPServers[0].ToolCount != 0 || body.MCPServers[0].Auth != "not_probed" {
		t.Fatalf("unexpected disabled mcp status %#v", body.MCPServers[0])
	}
	if body.MCPServers[1].Name != "fake" || !body.MCPServers[1].Enabled || body.MCPServers[1].Source != "global" || body.MCPServers[1].Version != "1.2.3" || body.MCPServers[1].ToolCount != 1 {
		t.Fatalf("unexpected enabled mcp status %#v", body.MCPServers[1])
	}
}

func TestServerServesMCPToolsAndErrorWhenOneServerFails(t *testing.T) {
	storeRoot := t.TempDir()
	s := store.New(storeRoot)
	if err := s.SaveMCPConfig(store.MCPConfig{Servers: map[string]store.MCPServerConfig{
		"bad": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestServerServesMCPToolsAndErrorWhenOneServerFails"},
			Env:     map[string]string{"LIORA_DAEMON_FAKE_MCP_SERVER": "fail"},
		},
		"fake": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestServerServesMCPToolsAndErrorWhenOneServerFails"},
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
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		MCPTools []struct {
			Server string `json:"server"`
			Name   string `json:"name"`
		} `json:"mcp_tools"`
		MCPError string `json:"mcp_error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tools) == 0 {
		t.Fatalf("expected builtin tools to remain available")
	}
	if len(body.MCPTools) != 1 || body.MCPTools[0].Server != "fake" || body.MCPTools[0].Name != "echo" {
		t.Fatalf("unexpected mcp capabilities %#v", body.MCPTools)
	}
	if !strings.Contains(body.MCPError, "bad") {
		t.Fatalf("expected partial mcp error for bad server, got %q", body.MCPError)
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
	if err := repo.AppendEvent(t.Context(), first.Task.ID, taskpkg.EventArtifactReference, taskpkg.EventPayload{Tool: "shell", Path: ".liora/tool-results/first.txt", Message: "full result"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), first.Task.ID, taskpkg.EventCompactBoundary, taskpkg.EventPayload{Message: "boundary after first task"}); err != nil {
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

	resp, err = http.Get(server.URL + "/v1/sessions/" + first.Task.SessionID + "/context?limit=4&token_budget=256")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected context status %d", resp.StatusCode)
	}
	var contextEnvelope taskpkg.ContextEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&contextEnvelope); err != nil {
		t.Fatal(err)
	}
	if contextEnvelope.Session.ID != first.Task.SessionID || contextEnvelope.Budget.ItemLimit != 4 || len(contextEnvelope.Transcript) > 4 {
		t.Fatalf("unexpected context envelope %#v", contextEnvelope)
	}
	if len(contextEnvelope.Budget.Buckets) == 0 || contextBudgetBucketTokenTotal(contextEnvelope.Budget.Buckets) != contextEnvelope.Budget.EstimatedTokens {
		t.Fatalf("expected context budget buckets to total estimated tokens, got %#v", contextEnvelope.Budget)
	}
	if len(contextEnvelope.ArtifactRefs) == 0 || len(contextEnvelope.CompactBoundaries) == 0 {
		t.Fatalf("expected context artifact refs and compact boundaries, got %#v", contextEnvelope)
	}

	resp, err = http.Get(server.URL + "/v1/sessions/" + first.Task.SessionID + "/context?token_budget=bad")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected bad context budget to return 400, got %d", resp.StatusCode)
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
	item := workbench.PendingApprovals[0].Item
	if item.TaskID != pending.ID || item.ToolName != "run" || item.ArgsPreview != "rm -rf build" || item.CommandPreview != "rm -rf build" || item.Risk != "dangerous_shell" || item.Status != "pending" {
		t.Fatalf("unexpected approval item %#v", item)
	}
}

func TestServerWorkbenchProjectsConversationThreadState(t *testing.T) {
	workspace := t.TempDir()
	otherWorkspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	activeThread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	waitingThread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "Waiting"})
	if err != nil {
		t.Fatal(err)
	}
	otherThread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: otherWorkspace, Title: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo, Store: persistentStore}))
	defer server.Close()

	active := postTaskForStatus(t, server.URL, http.StatusCreated, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(activeThread.ID)+`,"prompt":"active","natural":false}`)
	if err := repo.UpdateStatus(t.Context(), active.Task.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}
	waiting := postTaskForStatus(t, server.URL, http.StatusCreated, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(waitingThread.ID)+`,"prompt":"waiting","natural":false}`)
	if err := repo.UpdateStatus(t.Context(), waiting.Task.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), waiting.Task.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{Message: "approval needed", Status: string(taskpkg.StatusWaitingUser)}); err != nil {
		t.Fatal(err)
	}
	_ = postTaskForStatus(t, server.URL, http.StatusCreated, `{"workspace":`+quote(otherWorkspace)+`,"thread_id":`+quote(otherThread.ID)+`,"prompt":"other","natural":false}`)

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
	activeSummary, ok := findThreadWorkbench(workbench.Threads, activeThread.ID)
	if !ok {
		t.Fatalf("missing active thread summary %#v", workbench.Threads)
	}
	if activeSummary.TranscriptSessionID != activeThread.ID || activeSummary.ContextSessionID != activeThread.ID {
		t.Fatalf("thread transcript/context should alias thread session, got %#v", activeSummary)
	}
	if activeSummary.Lifecycle != "active" || !hasTask(activeSummary.ActiveTasks, active.Task.ID) || activeSummary.LastTaskID != active.Task.ID {
		t.Fatalf("unexpected active thread summary %#v", activeSummary)
	}
	waitingSummary, ok := findThreadWorkbench(workbench.Threads, waitingThread.ID)
	if !ok {
		t.Fatalf("missing waiting thread summary %#v", workbench.Threads)
	}
	if waitingSummary.Lifecycle != string(taskpkg.StatusWaitingUser) || len(waitingSummary.PendingApprovals) != 1 || waitingSummary.PendingApprovals[0].Task.ID != waiting.Task.ID {
		t.Fatalf("unexpected waiting thread summary %#v", waitingSummary)
	}
	if _, ok := findThreadWorkbench(workbench.Threads, otherThread.ID); ok {
		t.Fatalf("workbench leaked other workspace thread %#v", workbench.Threads)
	}
}

func TestServerConversationThreadRenameArchiveAndTaskGuard(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{Repository: repo, Store: persistentStore}))
	defer server.Close()

	thread := createTestThread(t, server.URL, workspace, "Draft")
	renamed := patchThread(t, server.URL, thread.ID, `{"title":"Renamed","archived":true}`)
	if renamed.Title != "Renamed" || renamed.ArchivedAt == nil {
		t.Fatalf("expected renamed archived thread, got %#v", renamed)
	}
	resp, err := http.Get(server.URL + "/v1/threads?workspace=" + url.QueryEscape(workspace))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var activeThreads []store.ConversationThread
	if err := json.NewDecoder(resp.Body).Decode(&activeThreads); err != nil {
		t.Fatal(err)
	}
	if len(activeThreads) != 0 {
		t.Fatalf("archived thread should be hidden by default, got %#v", activeThreads)
	}
	resp, err = http.Get(server.URL + "/v1/threads?workspace=" + url.QueryEscape(workspace) + "&include_archived=true")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var allThreads []store.ConversationThread
	if err := json.NewDecoder(resp.Body).Decode(&allThreads); err != nil {
		t.Fatal(err)
	}
	if len(allThreads) != 1 || allThreads[0].ID != thread.ID || allThreads[0].ArchivedAt == nil {
		t.Fatalf("expected include_archived to return archived thread, got %#v", allThreads)
	}
	resp, err = http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(`{"workspace":`+quote(workspace)+`,"thread_id":`+quote(thread.ID)+`,"prompt":"blocked","natural":false}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected archived thread task create to fail with 400, got %d", resp.StatusCode)
	}
	unarchived := patchThread(t, server.URL, thread.ID, `{"archived":false}`)
	if unarchived.ArchivedAt != nil {
		t.Fatalf("expected unarchived thread, got %#v", unarchived)
	}
	created := postTaskForStatus(t, server.URL, http.StatusCreated, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(thread.ID)+`,"prompt":"allowed","natural":false}`)
	if created.Task.SessionID != thread.ID {
		t.Fatalf("expected unarchived thread task to bind session, got %#v", created.Task)
	}
}

func TestServerConversationThreadModelConfigAPI(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	server := httptest.NewServer(NewServer(Config{Store: persistentStore}))
	defer server.Close()

	architect := createTestThread(t, server.URL, workspace, "Architect")
	batch := createTestThread(t, server.URL, workspace, "Batch")
	config := patchThreadModel(t, server.URL, architect.ID, `{
		"provider":"openai-chat",
		"model":"gpt-5",
		"base_url":"https://llm.example.test/v1",
		"profile":"strong"
	}`)
	if config.ThreadID != architect.ID || config.Provider != "openai-chat" || config.Model != "gpt-5" || config.BaseURL == "" || config.Profile != "strong" {
		t.Fatalf("unexpected thread model config %#v", config)
	}

	resp, err := http.Get(server.URL + "/v1/threads/" + architect.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected get thread status %d", resp.StatusCode)
	}
	var fetched store.ConversationThread
	if err := json.NewDecoder(resp.Body).Decode(&fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.ModelConfig == nil || fetched.ModelConfig.Model != "gpt-5" || fetched.ModelConfig.BaseURL == "" {
		t.Fatalf("expected fetched thread to include model config, got %#v", fetched)
	}

	inherited := patchThreadModel(t, server.URL, batch.ID, `{"inherited_from_thread_id":`+quote(architect.ID)+`}`)
	if inherited.InheritedFromThreadID != architect.ID {
		t.Fatalf("unexpected inherited model config %#v", inherited)
	}

	resp, err = http.Get(server.URL + "/v1/threads/" + architect.ID + "/model")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected get thread model status %d", resp.StatusCode)
	}
	var got store.ThreadModelConfig
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ThreadID != architect.ID || got.Profile != "strong" {
		t.Fatalf("unexpected fetched model config %#v", got)
	}

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/v1/threads/"+architect.ID+"/model", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected delete thread model status %d", resp.StatusCode)
	}
	resp, err = http.Get(server.URL + "/v1/threads/" + architect.ID + "/model")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected deleted model config to return 404, got %d", resp.StatusCode)
	}
}

func TestServerConversationThreadModelConfigRejectsMalformedBindings(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	server := httptest.NewServer(NewServer(Config{Store: persistentStore}))
	defer server.Close()

	local := createTestThread(t, server.URL, workspace, "Local")
	foreign := createTestThread(t, server.URL, t.TempDir(), "Foreign")

	resp := patchThreadModelForStatus(t, server.URL, local.ID, `{"provider":"openai-chat"}`, http.StatusBadRequest)
	resp.Body.Close()
	resp = patchThreadModelForStatus(t, server.URL, local.ID, `{"inherited_from_thread_id":`+quote(foreign.ID)+`}`, http.StatusBadRequest)
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body["error"], "belongs to workspace") {
		t.Fatalf("unexpected cross-workspace model error %#v", body)
	}
}

func TestServerServesDiffAfterOneThousandEvents(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "long streamed patch task",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a long streamed answer: the diff lands after >1000 events.
	for i := 0; i < 1100; i++ {
		if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventAssistantDelta, taskpkg.EventPayload{Message: "delta"}); err != nil {
			t.Fatal(err)
		}
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
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diff buried after 1000 events returned %d, want 200", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "+++ b/notes.txt") {
		t.Fatalf("unexpected diff response %s", string(data))
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

func TestMultiTaskEventStreamServesTaskEnvelopes(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	first, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: t.TempDir(), Prompt: "first", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: t.TempDir(), Prompt: "second", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []struct {
		id      string
		message string
	}{
		{id: first.ID, message: "first done"},
		{id: second.ID, message: "second done"},
	} {
		if err := repo.AppendEvent(t.Context(), record.id, taskpkg.EventSummary, taskpkg.EventPayload{Message: record.message}); err != nil {
			t.Fatal(err)
		}
		if err := repo.AppendEvent(t.Context(), record.id, taskpkg.EventCompleted, taskpkg.EventPayload{Status: string(taskpkg.StatusCompleted)}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/tasks/events/stream?task_id=" + first.ID + "&task_id=" + second.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{`"task_id":"` + first.ID + `"`, `"task_id":"` + second.ID + `"`, "first done", "second done", "event: task.completed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected multi-task stream to contain %q, got:\n%s", want, body)
		}
	}
}

func TestMultiTaskEventStreamContinuesAfterPermissionRequest(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: t.TempDir(), Prompt: "needs approval", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{Message: "approval needed"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	done := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		resp, err := http.Get(server.URL + "/v1/tasks/events/stream?task_id=" + task.ID)
		if err != nil {
			errs <- err
			return
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			errs <- err
			return
		}
		done <- string(data)
	}()
	select {
	case body := <-done:
		t.Fatalf("multi-task stream ended at permission request:\n%s", body)
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventPermissionApproved, taskpkg.EventPayload{Message: "approved"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventCompleted, taskpkg.EventPayload{Status: string(taskpkg.StatusCompleted)}); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-done:
		for _, want := range []string{"event: permission.requested", "event: permission.approved", "event: task.completed"} {
			if !strings.Contains(body, want) {
				t.Fatalf("expected stream to contain %q, got:\n%s", want, body)
			}
		}
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("multi-task stream did not complete after approval and completion")
	}
}

func TestServerWorkspaceThreadEventStream_deliversSiblingEventsWhilePeerWaits(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	firstThread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "first"})
	if err != nil {
		t.Fatal(err)
	}
	secondThread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "second"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo, Store: persistentStore}))
	defer server.Close()

	first := postTaskForStatus(t, server.URL, http.StatusCreated, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(firstThread.ID)+`,"prompt":"needs input","natural":false}`)
	second := postTaskForStatus(t, server.URL, http.StatusCreated, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(secondThread.ID)+`,"prompt":"sibling","natural":false}`)
	if first.Task.SessionID != firstThread.ID || second.Task.SessionID != secondThread.ID {
		t.Fatalf("expected thread-bound sessions, got first=%q second=%q", first.Task.SessionID, second.Task.SessionID)
	}
	if err := repo.AppendEvent(t.Context(), first.Task.ID, taskpkg.EventUserInputRequest, taskpkg.EventPayload{Message: "which file?", Status: string(taskpkg.StatusWaitingUser)}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/tasks/events/stream?task_id="+url.QueryEscape(first.Task.ID)+"&task_id="+url.QueryEscape(second.Task.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected stream status %d", resp.StatusCode)
	}

	var body strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	siblingAppended := false
	for scanner.Scan() {
		line := scanner.Text()
		body.WriteString(line)
		body.WriteByte('\n')
		if strings.Contains(line, "event: user_input.requested") && !siblingAppended {
			siblingAppended = true
			if err := repo.AppendEvent(t.Context(), second.Task.ID, taskpkg.EventSummary, taskpkg.EventPayload{Message: "sibling still streams"}); err != nil {
				t.Fatal(err)
			}
			if err := repo.AppendEvent(t.Context(), second.Task.ID, taskpkg.EventCompleted, taskpkg.EventPayload{Status: string(taskpkg.StatusCompleted)}); err != nil {
				t.Fatal(err)
			}
		}
		if strings.Contains(line, "sibling still streams") {
			cancel()
			break
		}
	}
	if !siblingAppended {
		t.Fatalf("stream did not expose waiting peer event:\n%s", body.String())
	}
	if !strings.Contains(body.String(), "sibling still streams") {
		t.Fatalf("sibling event was blocked by waiting peer:\n%s", body.String())
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("unexpected stream scanner error: %v", err)
	}
}

func TestServerWorkspaceThreadEventStream_fairlyInterleavesLargeBacklog(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	firstThread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "first"})
	if err != nil {
		t.Fatal(err)
	}
	secondThread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "second"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo, Store: persistentStore}))
	defer server.Close()

	first := postTaskForStatus(t, server.URL, http.StatusCreated, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(firstThread.ID)+`,"prompt":"large backlog","natural":false}`)
	second := postTaskForStatus(t, server.URL, http.StatusCreated, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(secondThread.ID)+`,"prompt":"sibling","natural":false}`)
	largeOutput := strings.Repeat("x", 4096)
	for i := 0; i < taskEventStreamBatchLimit+8; i++ {
		if err := repo.AppendEvent(t.Context(), first.Task.ID, taskpkg.EventSummary, taskpkg.EventPayload{Message: fmt.Sprintf("first-backlog-%02d %s", i, largeOutput)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.AppendEvent(t.Context(), first.Task.ID, taskpkg.EventCompleted, taskpkg.EventPayload{Status: string(taskpkg.StatusCompleted)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), second.Task.ID, taskpkg.EventSummary, taskpkg.EventPayload{Message: "second-ready"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), second.Task.ID, taskpkg.EventCompleted, taskpkg.EventPayload{Status: string(taskpkg.StatusCompleted)}); err != nil {
		t.Fatal(err)
	}

	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(server.URL + "/v1/tasks/events/stream?task_id=" + url.QueryEscape(first.Task.ID) + "&task_id=" + url.QueryEscape(second.Task.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	secondIndex := strings.Index(body, "second-ready")
	nextBacklogIndex := strings.Index(body, fmt.Sprintf("first-backlog-%02d", taskEventStreamBatchLimit))
	if secondIndex == -1 || nextBacklogIndex == -1 {
		t.Fatalf("missing expected stream events, second=%d nextBacklog=%d:\n%s", secondIndex, nextBacklogIndex, body)
	}
	if secondIndex > nextBacklogIndex {
		t.Fatalf("sibling event was starved behind one thread backlog: second=%d firstNextBatch=%d", secondIndex, nextBacklogIndex)
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

func TestServerRecoversRunnerPanicWithoutCancellingSiblingThread(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(newPanicOnPromptGenerator("panic task", "ANSWER: sibling ok")))
	handler := newServer(Config{Repository: repo, Runner: runner, Store: persistentStore})
	server := httptest.NewServer(handler.routes())
	defer server.Close()

	panicThread := createTestThread(t, server.URL, workspace, "panic")
	siblingThread := createTestThread(t, server.URL, workspace, "sibling")
	panicTask := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(panicThread.ID)+`,"prompt":"panic task","natural":true,"run_async":true}`)
	siblingTask := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(siblingThread.ID)+`,"prompt":"sibling task","natural":true,"run_async":true}`)

	waitUntil(t, 3*time.Second, func() bool {
		task, err := repo.Get(t.Context(), panicTask.Task.ID)
		return err == nil && task.Status == taskpkg.StatusFailed
	})
	waitUntil(t, 3*time.Second, func() bool {
		task, err := repo.Get(t.Context(), siblingTask.Task.ID)
		return err == nil && task.Status == taskpkg.StatusCompleted
	})
	waitUntil(t, 3*time.Second, func() bool {
		return !handler.isRunning(panicTask.Task.ID) && !handler.isRunning(siblingTask.Task.ID)
	})
	events, err := repo.Events(t.Context(), panicTask.Task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEvent(events, taskpkg.EventError) {
		t.Fatalf("expected recovered panic to append task.error, got %#v", events)
	}
	sibling, err := repo.Get(t.Context(), siblingTask.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sibling.SessionID != siblingThread.ID {
		t.Fatalf("sibling task should stay bound to sibling thread, got %#v", sibling)
	}
}

func TestServerParallelThreadsRouteStrongAndCheapModelsWithMetadata(t *testing.T) {
	t.Setenv("LIORA_AGENT_LOOP", "off")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("parallel model metadata\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	barrier := newLLMRequestBarrier(2)
	strongLLM := newPlanningLLMServer(t, "strong-model", barrier)
	defer strongLLM.Close()
	cheapLLM := newPlanningLLMServer(t, "cheap-model", barrier)
	defer cheapLLM.Close()
	registry, err := llm.NewRegistry(llm.Config{
		Provider: llm.ProviderOpenAIChat,
		BaseURL:  strongLLM.URL,
		APIKey:   "test-key",
		Model:    "default-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "ANSWER: fallback should not be used"}))
	runner.SetLLMRegistry(registry)
	handler := newServer(Config{Repository: repo, Runner: runner, Store: persistentStore, Foreground: ForegroundLimits{MaxConcurrent: 2, MaxActive: 4}})
	server := httptest.NewServer(handler.routes())
	defer server.Close()

	strongThread := createTestThread(t, server.URL, workspace, "Strong")
	cheapThread := createTestThread(t, server.URL, workspace, "Cheap")
	if _, err := persistentStore.UpdateThreadModelConfig(strongThread.ID, store.UpdateThreadModelConfigRequest{Provider: "openai-chat", Model: "strong-model", BaseURL: strongLLM.URL, Profile: "strong"}); err != nil {
		t.Fatal(err)
	}
	if _, err := persistentStore.UpdateThreadModelConfig(cheapThread.ID, store.UpdateThreadModelConfigRequest{Provider: "openai-chat", Model: "cheap-model", BaseURL: cheapLLM.URL, Profile: "cheap"}); err != nil {
		t.Fatal(err)
	}
	strong := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(strongThread.ID)+`,"prompt":"inspect strong","natural":true,"run_async":true}`)
	cheap := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(cheapThread.ID)+`,"prompt":"inspect cheap","natural":true,"run_async":true}`)

	waitUntil(t, 3*time.Second, func() bool {
		strongTask, strongErr := repo.Get(t.Context(), strong.Task.ID)
		cheapTask, cheapErr := repo.Get(t.Context(), cheap.Task.ID)
		return strongErr == nil && cheapErr == nil && strongTask.Status == taskpkg.StatusCompleted && cheapTask.Status == taskpkg.StatusCompleted
	})
	assertTaskModelAndTrace(t, repo, strong.Task.ID, strongThread.ID, "openai-chat", "strong-model", "strong")
	assertTaskModelAndTrace(t, repo, cheap.Task.ID, cheapThread.ID, "openai-chat", "cheap-model", "cheap")
}

func TestServerProvider429OnlyFailsMatchingThreadModel(t *testing.T) {
	t.Setenv("LIORA_AGENT_LOOP", "off")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("healthy provider still works\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	limitedAttempts := 0
	var limitedMu sync.Mutex
	limitedLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		limitedMu.Lock()
		limitedAttempts++
		limitedMu.Unlock()
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer limitedLLM.Close()
	healthyLLM := newPlanningLLMServer(t, "healthy-model", newLLMRequestBarrier(1))
	defer healthyLLM.Close()
	registry, err := llm.NewRegistry(llm.Config{
		Provider: llm.ProviderOpenAIChat,
		BaseURL:  limitedLLM.URL,
		APIKey:   "test-key",
		Model:    "default-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "ANSWER: fallback should not be used"}))
	runner.SetLLMRegistry(registry)
	handler := newServer(Config{Repository: repo, Runner: runner, Store: persistentStore, Foreground: ForegroundLimits{MaxConcurrent: 2, MaxActive: 4}})
	server := httptest.NewServer(handler.routes())
	defer server.Close()

	limitedThread := createTestThread(t, server.URL, workspace, "Limited")
	healthyThread := createTestThread(t, server.URL, workspace, "Healthy")
	if _, err := persistentStore.UpdateThreadModelConfig(limitedThread.ID, store.UpdateThreadModelConfigRequest{Provider: "openai-chat", Model: "limited-model", BaseURL: limitedLLM.URL, Profile: "limited"}); err != nil {
		t.Fatal(err)
	}
	if _, err := persistentStore.UpdateThreadModelConfig(healthyThread.ID, store.UpdateThreadModelConfigRequest{Provider: "openai-chat", Model: "healthy-model", BaseURL: healthyLLM.URL, Profile: "healthy"}); err != nil {
		t.Fatal(err)
	}
	limited := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(limitedThread.ID)+`,"prompt":"limited provider","natural":true,"run_async":true}`)
	healthy := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(healthyThread.ID)+`,"prompt":"healthy provider","natural":true,"run_async":true}`)

	waitUntil(t, 3*time.Second, func() bool {
		limitedTask, limitedErr := repo.Get(t.Context(), limited.Task.ID)
		healthyTask, healthyErr := repo.Get(t.Context(), healthy.Task.ID)
		return limitedErr == nil && healthyErr == nil && limitedTask.Status == taskpkg.StatusFailed && healthyTask.Status == taskpkg.StatusCompleted
	})
	limitedMu.Lock()
	attempts := limitedAttempts
	limitedMu.Unlock()
	if attempts < 3 {
		t.Fatalf("expected limited provider retries, got %d attempts", attempts)
	}
	limitedTask, err := repo.Get(t.Context(), limited.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if limitedTask.ModelConfig == nil || limitedTask.ModelConfig.Model != "limited-model" || limitedTask.ModelConfig.Profile != "limited" {
		t.Fatalf("limited task lost its model metadata %#v", limitedTask.ModelConfig)
	}
	assertTaskModelAndTrace(t, repo, healthy.Task.ID, healthyThread.ID, "openai-chat", "healthy-model", "healthy")
}

func TestServerRejectsAsyncTaskWithoutRunner(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(`{"workspace":`+quote(t.TempDir())+`,"prompt":"run echo hi","natural":false,"run_async":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d", resp.StatusCode)
	}
	tasks, err := repo.List(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("async task without runner should not be created, got %#v", tasks)
	}
}

func TestServerQueuesSessionTaskAndStartsItAfterActiveTaskFinishes(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	active, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "first", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), active.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "ANSWER: queued done"}))
	handler := newServer(Config{Repository: repo, Runner: runner})
	server := httptest.NewServer(handler.routes())
	defer server.Close()

	body := `{"workspace":` + quote(workspace) + `,"session_id":` + quote(active.SessionID) + `,"prompt":"second","natural":true,"run_async":true,"queue":true}`
	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected queued create status %d", resp.StatusCode)
	}
	var created taskpkg.CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected queued task, got %#v", created.Task)
	}
	if err := repo.UpdateStatus(t.Context(), active.ID, taskpkg.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	handler.startNextQueuedAfter(active.ID)
	waitUntil(t, 3*time.Second, func() bool {
		task, err := repo.Get(t.Context(), created.Task.ID)
		if err != nil || task.Status != taskpkg.StatusCompleted {
			return false
		}
		events, err := repo.Events(t.Context(), created.Task.ID, 0)
		if err != nil {
			return false
		}
		return hasEvent(events, taskpkg.EventCompleted)
	})
}

func TestServerForegroundTurnDefaultsToSessionQueueBehindActiveTask(t *testing.T) {
	for _, blockerStatus := range []taskpkg.Status{taskpkg.StatusRunning, taskpkg.StatusWaitingUser} {
		t.Run(string(blockerStatus), func(t *testing.T) {
			workspace := t.TempDir()
			db, err := store.New(t.TempDir()).OpenDB()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			repo := taskpkg.NewRepository(db)
			blocker, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "first", Natural: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.UpdateStatus(t.Context(), blocker.ID, blockerStatus); err != nil {
				t.Fatal(err)
			}
			runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "ANSWER: queued foreground done"}))
			handler := newServer(Config{Repository: repo, Runner: runner})
			server := httptest.NewServer(handler.routes())
			defer server.Close()

			body := `{"workspace":` + quote(workspace) + `,"session_id":` + quote(blocker.SessionID) + `,"prompt":"second foreground","natural":true}`
			resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				t.Fatalf("expected queued foreground status 202, got %d", resp.StatusCode)
			}
			var created taskpkg.CreateResponse
			if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
			if created.Task.Status != taskpkg.StatusQueued {
				t.Fatalf("expected foreground turn to queue behind %s, got %#v", blockerStatus, created.Task)
			}
			events, err := repo.Events(t.Context(), created.Task.ID, 20)
			if err != nil {
				t.Fatal(err)
			}
			if !hasEvent(events, taskpkg.EventTaskQueued) {
				t.Fatalf("expected task.queued event, got %#v", events)
			}
			handler.startNextQueuedAfter(blocker.ID)
			stillQueued, err := repo.Get(t.Context(), created.Task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stillQueued.Status != taskpkg.StatusQueued {
				t.Fatalf("non-terminal blocker must not start queued foreground, got %#v", stillQueued)
			}
			if err := repo.UpdateStatus(t.Context(), blocker.ID, taskpkg.StatusCompleted); err != nil {
				t.Fatal(err)
			}
			handler.startNextQueuedAfter(blocker.ID)
			waitUntil(t, 3*time.Second, func() bool {
				task, err := repo.Get(t.Context(), created.Task.ID)
				return err == nil && task.Status == taskpkg.StatusCompleted
			})
		})
	}
}

func TestServerForegroundTurnQueuePreservesFIFOAndSkipsIndependentSessions(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	blocker, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: workspace, Prompt: "first", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), blocker.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "ANSWER: done"}))
	server := httptest.NewServer(NewServer(Config{Repository: repo, Runner: runner}))
	defer server.Close()

	firstQueued := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"session_id":`+quote(blocker.SessionID)+`,"prompt":"second","natural":true}`)
	secondQueued := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"session_id":`+quote(blocker.SessionID)+`,"prompt":"third","natural":true}`)
	if firstQueued.Task.Status != taskpkg.StatusQueued || secondQueued.Task.Status != taskpkg.StatusQueued {
		t.Fatalf("expected both same-session foreground turns queued, got %#v %#v", firstQueued.Task, secondQueued.Task)
	}
	next, ok, err := repo.NextQueuedTask(t.Context(), blocker.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || next.ID != firstQueued.Task.ID {
		t.Fatalf("expected oldest queued foreground first, ok=%v next=%#v first=%#v", ok, next, firstQueued.Task)
	}
	independent := postTaskForStatus(t, server.URL, http.StatusCreated, `{"workspace":`+quote(workspace)+`,"prompt":"independent","natural":true}`)
	if independent.Task.Status == taskpkg.StatusQueued || independent.Task.SessionID == blocker.SessionID {
		t.Fatalf("missing session_id should create independent non-queued turn, got %#v", independent.Task)
	}
	background := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"session_id":`+quote(blocker.SessionID)+`,"prompt":"background","natural":true,"run_async":true,"origin":"background","automation":{"kind":"background","risk":"safe"}}`)
	if background.Task.Status == taskpkg.StatusQueued {
		t.Fatalf("default foreground queue must not force safe background task into queue: %#v", background.Task)
	}
}

func postTaskForStatus(t *testing.T, baseURL string, wantStatus int, body string) taskpkg.CreateResponse {
	t.Helper()
	resp, err := http.Post(baseURL+"/v1/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		responseBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected create status %d, want %d: %s", resp.StatusCode, wantStatus, string(responseBody))
	}
	var created taskpkg.CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return created
}

func TestServerAcceptsUserInputAndResumesWaitingTask(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{responses: []string{
		"ASK_USER: Which file should I edit?",
		"ANSWER: Resumed with user input.",
	}}))
	server := httptest.NewServer(NewServer(Config{Repository: repo, Runner: runner}))
	defer server.Close()

	createBody := `{"workspace":` + quote(workspace) + `,"prompt":"edit the file","natural":true,"run_async":true}`
	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected create status %d", resp.StatusCode)
	}
	var created taskpkg.CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	var workbench taskpkg.Workbench
	waitUntil(t, 3*time.Second, func() bool {
		task, err := repo.Get(t.Context(), created.Task.ID)
		if err != nil || task.Status != taskpkg.StatusWaitingUser {
			return false
		}
		workbenchResp, err := http.Get(server.URL + "/v1/workbench?workspace=" + url.QueryEscape(workspace))
		if err != nil {
			return false
		}
		defer workbenchResp.Body.Close()
		if err := json.NewDecoder(workbenchResp.Body).Decode(&workbench); err != nil {
			return false
		}
		return len(workbench.PendingUserInputs) == 1 && len(workbench.PendingApprovals) == 0
	})
	if len(workbench.PendingUserInputs) != 1 || len(workbench.PendingApprovals) != 0 {
		t.Fatalf("expected pending user input only, got %#v", workbench)
	}

	inputResp, err := http.Post(server.URL+"/v1/tasks/"+created.Task.ID+"/input", "application/json", strings.NewReader(`{"message":"notes.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer inputResp.Body.Close()
	if inputResp.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected input status %d", inputResp.StatusCode)
	}
	waitUntil(t, 3*time.Second, func() bool {
		task, err := repo.Get(t.Context(), created.Task.ID)
		if err != nil || task.Status != taskpkg.StatusCompleted {
			return false
		}
		events, err := repo.Events(t.Context(), created.Task.ID, 0)
		return err == nil && hasEvent(events, taskpkg.EventCompleted)
	})
	events, err := repo.Events(t.Context(), created.Task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	types := make([]taskpkg.EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	for _, want := range []taskpkg.EventType{taskpkg.EventUserInputRequest, taskpkg.EventUserInputReceived, taskpkg.EventCompleted} {
		if !containsEventType(types, want) {
			t.Fatalf("expected %s in %#v", want, types)
		}
	}
}

func TestServerRejectsWaitingUserWrongRouteAndMalformedWaitType(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	approvalTask, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: t.TempDir(), Prompt: "needs approval", Natural: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), approvalTask.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), approvalTask.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{Message: "approval needed"}); err != nil {
		t.Fatal(err)
	}
	wrongInput, err := http.Post(server.URL+"/v1/tasks/"+approvalTask.ID+"/input", "application/json", strings.NewReader(`{"message":"not approval"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer wrongInput.Body.Close()
	if wrongInput.StatusCode != http.StatusConflict {
		t.Fatalf("expected input on approval wait to conflict, got %d", wrongInput.StatusCode)
	}
	blankInput, err := http.Post(server.URL+"/v1/tasks/"+approvalTask.ID+"/input", "application/json", strings.NewReader(`{"message":"   "}`))
	if err != nil {
		t.Fatal(err)
	}
	defer blankInput.Body.Close()
	if blankInput.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected blank input rejection, got %d", blankInput.StatusCode)
	}
	approvalAfterWrongRoute, err := repo.Get(t.Context(), approvalTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approvalAfterWrongRoute.ApprovalGranted || approvalAfterWrongRoute.Status != taskpkg.StatusWaitingUser {
		t.Fatalf("wrong input route mutated approval wait: %#v", approvalAfterWrongRoute)
	}

	inputTask, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: t.TempDir(), Prompt: "needs input", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), inputTask.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), inputTask.ID, taskpkg.EventUserInputRequest, taskpkg.EventPayload{Message: "Which file?"}); err != nil {
		t.Fatal(err)
	}
	wrongApproval, err := http.Post(server.URL+"/v1/tasks/"+inputTask.ID+"/approval", "application/json", strings.NewReader(`{"decision":"approve"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer wrongApproval.Body.Close()
	if wrongApproval.StatusCode != http.StatusConflict {
		t.Fatalf("expected approval on user-input wait to conflict, got %d", wrongApproval.StatusCode)
	}
	inputAfterWrongRoute, err := repo.Get(t.Context(), inputTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inputAfterWrongRoute.ApprovalGranted || inputAfterWrongRoute.Status != taskpkg.StatusWaitingUser {
		t.Fatalf("wrong approval route mutated input wait: %#v", inputAfterWrongRoute)
	}

	malformedTask, err := repo.Create(t.Context(), taskpkg.CreateRequest{Workspace: t.TempDir(), Prompt: "malformed wait", Natural: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), malformedTask.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO task_events (id, task_id, type, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, "event_malformed_wait", malformedTask.ID, string(taskpkg.EventUserInputRequest), "{", time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	malformedInput, err := http.Post(server.URL+"/v1/tasks/"+malformedTask.ID+"/input", "application/json", strings.NewReader(`{"message":"answer"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer malformedInput.Body.Close()
	if malformedInput.StatusCode != http.StatusConflict {
		t.Fatalf("expected malformed input wait to fail closed, got %d", malformedInput.StatusCode)
	}
	malformedApproval, err := http.Post(server.URL+"/v1/tasks/"+malformedTask.ID+"/approval", "application/json", strings.NewReader(`{"decision":"approve"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer malformedApproval.Body.Close()
	if malformedApproval.StatusCode != http.StatusConflict {
		t.Fatalf("expected malformed approval wait to fail closed, got %d", malformedApproval.StatusCode)
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

func TestServerRejectsDuplicateApprovalWhileTaskRunning(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "run long-task",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), task.ID, taskpkg.StatusWaitingUser); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{Message: "approval needed"}); err != nil {
		t.Fatal(err)
	}
	executor := newBlockingShellExecutor()
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(executor)
	server := httptest.NewServer(NewServer(Config{Repository: repo, Runner: runner}))
	defer server.Close()

	approve, err := http.Post(server.URL+"/v1/tasks/"+task.ID+"/approval", "application/json", strings.NewReader(`{"decision":"approve"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer approve.Body.Close()
	if approve.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected first approve status %d", approve.StatusCode)
	}
	select {
	case <-executor.started:
	case <-time.After(3 * time.Second):
		t.Fatal("approved task did not start")
	}
	duplicate, err := http.Post(server.URL+"/v1/tasks/"+task.ID+"/approval", "application/json", strings.NewReader(`{"decision":"approve"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer duplicate.Body.Close()
	if duplicate.StatusCode != http.StatusConflict {
		t.Fatalf("expected duplicate approval conflict, got %d", duplicate.StatusCode)
	}
	cancelResp, err := http.Post(server.URL+"/v1/tasks/"+task.ID+"/cancel", "application/json", strings.NewReader(`{"reason":"test cleanup"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer cancelResp.Body.Close()
	select {
	case <-executor.done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not stop approved task")
	}
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
	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{Message: "approval needed"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/tasks/"+task.ID+"/approval", "application/json", strings.NewReader(`{"decision":"deny","reason":"too risky","decided_by":"tester"}`))
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
	item, ok, err := repo.ApprovalItemForTask(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || item.Status != "resolved" || item.Decision != "denied" || item.DecidedBy != "tester" || item.ResolvedAt == nil {
		t.Fatalf("unexpected denied approval item %#v ok=%v", item, ok)
	}
}

func TestServerApprovalMissingTaskReturnsClearError(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/tasks/not-a-real-task/approval", "application/json", strings.NewReader(`{"decision":"approve"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected missing approval task to return 404, got %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "task not found" {
		t.Fatalf("unexpected missing approval task error %#v", body)
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

func containsEventType(events []taskpkg.EventType, want taskpkg.EventType) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func createTestThread(t *testing.T, baseURL string, workspace string, title string) store.ConversationThread {
	t.Helper()
	body := strings.NewReader(`{"workspace":` + quote(workspace) + `,"title":` + quote(title) + `}`)
	resp, err := http.Post(baseURL+"/v1/threads", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected create thread status %d", resp.StatusCode)
	}
	var thread store.ConversationThread
	if err := json.NewDecoder(resp.Body).Decode(&thread); err != nil {
		t.Fatal(err)
	}
	if thread.ID == "" || thread.Workspace != workspace {
		t.Fatalf("unexpected thread %#v", thread)
	}
	return thread
}

type llmRequestBarrier struct {
	mu    sync.Mutex
	want  int
	count int
	ready chan struct{}
}

func newLLMRequestBarrier(want int) *llmRequestBarrier {
	return &llmRequestBarrier{want: want, ready: make(chan struct{})}
}

func (b *llmRequestBarrier) arrive() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count++
	if b.count == b.want {
		close(b.ready)
	}
}

func newPlanningLLMServer(t *testing.T, expectedModel string, barrier *llmRequestBarrier) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Model != expectedModel {
			http.Error(w, "unexpected model "+body.Model, http.StatusBadRequest)
			return
		}
		barrier.arrive()
		select {
		case <-barrier.ready:
		case <-time.After(3 * time.Second):
			http.Error(w, "parallel model request barrier timed out", http.StatusGatewayTimeout)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"read README.md"}}]}`))
	}))
}

func assertTaskModelAndTrace(t *testing.T, repo *taskpkg.Repository, taskID string, threadID string, provider string, model string, profile string) {
	t.Helper()
	task, err := repo.Get(t.Context(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.SessionID != threadID {
		t.Fatalf("expected task %s bound to thread %s, got %#v", task.ID, threadID, task)
	}
	if task.ModelConfig == nil || task.ModelConfig.Provider != provider || task.ModelConfig.Model != model || task.ModelConfig.Profile != profile {
		t.Fatalf("unexpected task model config for %s: %#v", taskID, task.ModelConfig)
	}
	events, err := repo.Events(t.Context(), taskID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !eventPayloadHasModel(events, taskpkg.EventToolResult, provider, model, profile) {
		t.Fatalf("expected tool.result payload to include %s/%s/%s, got %#v", provider, model, profile, events)
	}
	timeline, err := repo.Timeline(t.Context(), threadID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !timelineHasModel(timeline, provider, model, profile) {
		t.Fatalf("expected timeline to include %s/%s/%s, got %#v", provider, model, profile, timeline)
	}
}

func eventPayloadHasModel(events []taskpkg.Event, eventType taskpkg.EventType, provider string, model string, profile string) bool {
	for _, event := range events {
		if event.Type != eventType {
			continue
		}
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			continue
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

func getSessionTimeline(t *testing.T, baseURL string, sessionID string) []taskpkg.TimelineItem {
	t.Helper()
	resp, err := http.Get(baseURL + "/v1/sessions/" + sessionID + "/timeline?limit=50")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected timeline status %d", resp.StatusCode)
	}
	var timeline []taskpkg.TimelineItem
	if err := json.NewDecoder(resp.Body).Decode(&timeline); err != nil {
		t.Fatal(err)
	}
	return timeline
}

func patchThread(t *testing.T, baseURL string, threadID string, body string) store.ConversationThread {
	t.Helper()
	request, err := http.NewRequest(http.MethodPatch, baseURL+"/v1/threads/"+threadID, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected patch thread status %d: %s", resp.StatusCode, string(responseBody))
	}
	var thread store.ConversationThread
	if err := json.NewDecoder(resp.Body).Decode(&thread); err != nil {
		t.Fatal(err)
	}
	return thread
}

func patchThreadModel(t *testing.T, baseURL string, threadID string, body string) store.ThreadModelConfig {
	t.Helper()
	resp := patchThreadModelForStatus(t, baseURL, threadID, body, http.StatusOK)
	defer resp.Body.Close()
	var config store.ThreadModelConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		t.Fatal(err)
	}
	return config
}

func patchThreadModelForStatus(t *testing.T, baseURL string, threadID string, body string, status int) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPatch, baseURL+"/v1/threads/"+threadID+"/model", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != status {
		responseBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("unexpected patch thread model status %d: %s", resp.StatusCode, string(responseBody))
	}
	return resp
}

func assertNoCrossThreadLeak(t *testing.T, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)
	for _, forbidden := range []string{"sk-live-1234567890abcdef", "secret-value", "remember raw token", "approval: always allow", "full prompt api_key"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("cross-thread response leaked %q in %s", forbidden, rendered)
		}
	}
}

func timelineHasRoleContent(items []taskpkg.TimelineItem, role string, content string) bool {
	for _, item := range items {
		if item.Kind == "message" && item.Role == role && strings.Contains(item.Content, content) {
			return true
		}
	}
	return false
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

func findThreadWorkbench(threads []taskpkg.ThreadWorkbench, threadID string) (taskpkg.ThreadWorkbench, bool) {
	for _, thread := range threads {
		if thread.ID == threadID {
			return thread, true
		}
	}
	return taskpkg.ThreadWorkbench{}, false
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

func contextBudgetBucketTokenTotal(buckets []taskpkg.ContextBudgetBucket) int {
	total := 0
	for _, bucket := range buckets {
		total += bucket.EstimatedTokens
	}
	return total
}

func TestMain(m *testing.M) {
	if os.Getenv("LIORA_DAEMON_FAKE_MCP_SERVER") == "fail" {
		os.Exit(7)
	}
	if os.Getenv("LIORA_DAEMON_FAKE_MCP_SERVER") == "1" {
		runDaemonFakeMCPServer()
		return
	}
	os.Exit(m.Run())
}
