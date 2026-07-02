package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/daemonclient"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/tuisession"
)

func TestServerToolFailuresRemainObservableAcrossTailTimelineAndTrace(t *testing.T) {
	t.Setenv("LIORA_AGENT_LOOP", "on")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("recovered content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered := runToolFailureScenario(t, workspace, []nativeToolLoopTurn{
		{callID: "missing-one", tool: "read", arguments: `{"path":"missing-one.txt"}`},
		{callID: "missing-two", tool: "read", arguments: `{"path":"missing-two.txt"}`},
		{callID: "read-ok", tool: "read", arguments: `{"path":"README.md"}`},
		{content: "Recovered after two file misses."},
	})
	taskRecord := recovered.task
	if taskRecord.Status != taskpkg.StatusCompleted {
		t.Fatalf("expected recovered task to complete, got %#v", taskRecord)
	}
	assertFailureObservability(t, recovered.repo, taskRecord.SessionID, taskRecord.ID, "missing-one.txt", "error")
	assertFailureObservability(t, recovered.repo, taskRecord.SessionID, taskRecord.ID, "missing-two.txt", "error")
	assertFailureObservability(t, recovered.repo, taskRecord.SessionID, taskRecord.ID, "recovered content", "ok")

	repeated := runToolFailureScenario(t, workspace, []nativeToolLoopTurn{
		{callID: "repeat-one", tool: "read", arguments: `{"path":"still-missing.txt"}`},
		{callID: "repeat-two", tool: "read", arguments: `{"path":"still-missing.txt"}`},
	})
	failedTask := repeated.task
	if failedTask.Status != taskpkg.StatusFailed {
		t.Fatalf("expected repeated failure task to fail early, got %#v", failedTask)
	}
	assertFailureObservability(t, repeated.repo, failedTask.SessionID, failedTask.ID, "still-missing.txt", "error")
	assertFailureObservability(t, repeated.repo, failedTask.SessionID, failedTask.ID, "repeated failing tool call", string(taskpkg.StatusFailed))
	output := tailTaskOutput(t, repeated.serverURL, workspace, failedTask.SessionID, failedTask.ID)
	for _, want := range []string{"still-missing.txt", "no such file", "repeated failing tool call", "task.error"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected /tail output to contain %q, got:\n%s", want, output)
		}
	}
}

type toolFailureScenario struct {
	repo      *taskpkg.Repository
	serverURL string
	task      taskpkg.Task
}

type nativeToolLoopTurn struct {
	callID    string
	tool      string
	arguments string
	content   string
}

func runToolFailureScenario(t *testing.T, workspace string, turns []nativeToolLoopTurn) toolFailureScenario {
	t.Helper()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := taskpkg.NewRepository(db)
	llmServer := newScriptedNativeToolLoopServer(t, "failure-observability-model", turns)
	t.Cleanup(llmServer.Close)
	registry, err := llm.NewRegistry(llm.Config{
		Provider: llm.ProviderOpenAIChat,
		BaseURL:  llmServer.URL,
		APIKey:   "test-key",
		Model:    "failure-observability-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "ANSWER: fallback should not be used"}))
	runner.SetLLMRegistry(registry)
	handler := newServer(Config{Repository: repo, Runner: runner, Store: persistentStore})
	server := httptest.NewServer(handler.routes())
	t.Cleanup(server.Close)

	thread := createTestThread(t, server.URL, workspace, "Failure Observability")
	created := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(thread.ID)+`,"prompt":"exercise failure observability","natural":true,"run_async":true}`)
	waitUntil(t, 3*time.Second, func() bool {
		taskRecord, taskErr := repo.Get(t.Context(), created.Task.ID)
		return taskErr == nil && (taskRecord.Status == taskpkg.StatusCompleted || taskRecord.Status == taskpkg.StatusFailed)
	})
	taskRecord, err := repo.Get(t.Context(), created.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return toolFailureScenario{repo: repo, serverURL: server.URL, task: taskRecord}
}

func newScriptedNativeToolLoopServer(t *testing.T, model string, turns []nativeToolLoopTurn) *httptest.Server {
	t.Helper()
	next := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		var body struct {
			Model string `json:"model"`
			Tools []any  `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Model != model {
			http.Error(w, "unexpected model "+body.Model, http.StatusBadRequest)
			return
		}
		if len(body.Tools) == 0 {
			http.Error(w, "expected native tool schemas", http.StatusBadRequest)
			return
		}
		if next >= len(turns) {
			http.Error(w, "script exhausted", http.StatusInternalServerError)
			return
		}
		turn := turns[next]
		next++
		w.Header().Set("Content-Type", "application/json")
		if turn.content != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]any{"content": turn.content},
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"tool_calls": []map[string]any{{
						"id":   turn.callID,
						"type": "function",
						"function": map[string]any{
							"name":      turn.tool,
							"arguments": turn.arguments,
						},
					}},
				},
			}},
		})
	}))
}

func assertFailureObservability(t *testing.T, repo *taskpkg.Repository, sessionID string, taskID string, needle string, status string) {
	t.Helper()
	events, err := repo.Events(t.Context(), taskID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !eventsContainPayload(events, needle, status) {
		t.Fatalf("expected task trace events to contain %q status %q, got %#v", needle, status, events)
	}
	timeline, err := repo.Timeline(t.Context(), sessionID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !timelineContains(timeline, needle, status) {
		t.Fatalf("expected timeline to contain %q status %q, got %#v", needle, status, timeline)
	}
}

func eventsContainPayload(events []taskpkg.Event, needle string, status string) bool {
	for _, event := range events {
		if !strings.Contains(event.Payload, needle) {
			continue
		}
		if status == "" || strings.Contains(event.Payload, `"status":"`+status+`"`) {
			return true
		}
	}
	return false
}

func timelineContains(items []taskpkg.TimelineItem, needle string, status string) bool {
	for _, item := range items {
		text := strings.Join([]string{item.Content, item.Input, item.Output, item.Reason}, "\n")
		if strings.Contains(text, needle) && (status == "" || item.Status == status) {
			return true
		}
	}
	return false
}

func tailTaskOutput(t *testing.T, serverURL string, workspace string, sessionID string, taskID string) string {
	t.Helper()
	client, err := daemonclient.New(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	submitter := tuisession.NewDaemonSubmitter(client, workspace, true, sessionID, false)
	output, handled, err := submitter.HandleCommand(t.Context(), "/tail "+taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected /tail to be handled")
	}
	return output
}
