package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

func TestServerThreadModelAttributionCoversNativeToolLoopAndPlannerFallback(t *testing.T) {
	t.Setenv("LIORA_AGENT_LOOP", "on")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("thread model attribution\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	nativeLLM := newNativeToolLoopLLMServer(t, nativeToolLoopLLMConfig{
		model:     "native-model",
		callID:    "native-read-1",
		tool:      "read",
		arguments: `{"path":"README.md"}`,
	})
	defer nativeLLM.Close()
	fallbackLLM := newResponsesPlanningLLMServer(t, responsesPlanningLLMConfig{
		model: "fallback-model",
		plan:  "read README.md",
	})
	defer fallbackLLM.Close()
	registry, err := llm.NewRegistry(llm.Config{
		Provider: llm.ProviderOpenAIChat,
		BaseURL:  nativeLLM.URL,
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

	nativeThread := createTestThread(t, server.URL, workspace, "Native")
	fallbackThread := createTestThread(t, server.URL, workspace, "Fallback")
	if _, err := persistentStore.UpdateThreadModelConfig(nativeThread.ID, store.UpdateThreadModelConfigRequest{Provider: llm.ProviderOpenAIChat, Model: "native-model", BaseURL: nativeLLM.URL, Profile: "native"}); err != nil {
		t.Fatal(err)
	}
	if _, err := persistentStore.UpdateThreadModelConfig(fallbackThread.ID, store.UpdateThreadModelConfigRequest{Provider: llm.ProviderOpenAIResponses, Model: "fallback-model", BaseURL: fallbackLLM.URL, Profile: "fallback"}); err != nil {
		t.Fatal(err)
	}

	native := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(nativeThread.ID)+`,"prompt":"native inspect","natural":true,"run_async":true}`)
	waitUntil(t, 3*time.Second, func() bool {
		nativeTask, nativeErr := repo.Get(t.Context(), native.Task.ID)
		return nativeErr == nil && nativeTask.Status == taskpkg.StatusCompleted
	})
	fallback := postTaskForStatus(t, server.URL, http.StatusAccepted, `{"workspace":`+quote(workspace)+`,"thread_id":`+quote(fallbackThread.ID)+`,"prompt":"fallback inspect","natural":true,"run_async":true}`)
	waitUntil(t, 3*time.Second, func() bool {
		fallbackTask, fallbackErr := repo.Get(t.Context(), fallback.Task.ID)
		return fallbackErr == nil && fallbackTask.Status == taskpkg.StatusCompleted
	})
	nativeModel := modelExpectation{provider: llm.ProviderOpenAIChat, model: "native-model", profile: "native"}
	fallbackModel := modelExpectation{provider: llm.ProviderOpenAIResponses, model: "fallback-model", profile: "fallback"}
	assertTaskModelAttribution(t, taskModelAttributionCheck{repo: repo, taskID: native.Task.ID, threadID: nativeThread.ID, model: nativeModel})
	assertTaskModelAttribution(t, taskModelAttributionCheck{repo: repo, taskID: fallback.Task.ID, threadID: fallbackThread.ID, model: fallbackModel})
	assertTaskTraceDoesNotUseModel(t, taskTraceModelExclusionCheck{repo: repo, taskID: native.Task.ID, model: fallbackModel})
	assertTaskTraceDoesNotUseModel(t, taskTraceModelExclusionCheck{repo: repo, taskID: fallback.Task.ID, model: nativeModel})
}

type nativeToolLoopLLMConfig struct {
	model     string
	callID    string
	tool      string
	arguments string
}

type responsesPlanningLLMConfig struct {
	model string
	plan  string
}

type modelExpectation struct {
	provider string
	model    string
	profile  string
}

type taskModelAttributionCheck struct {
	repo     *taskpkg.Repository
	taskID   string
	threadID string
	model    modelExpectation
}

type taskTraceModelExclusionCheck struct {
	repo   *taskpkg.Repository
	taskID string
	model  modelExpectation
}

func newNativeToolLoopLLMServer(t *testing.T, config nativeToolLoopLLMConfig) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
			Tools []any `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Model != config.model {
			http.Error(w, "unexpected model "+body.Model, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if hasToolMessage(body.Messages) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]any{"content": "native loop complete"},
				}},
			})
			return
		}
		if len(body.Tools) == 0 {
			http.Error(w, "expected native tool schemas", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"tool_calls": []map[string]any{{
						"id":   config.callID,
						"type": "function",
						"function": map[string]any{
							"name":      config.tool,
							"arguments": config.arguments,
						},
					}},
				},
			}},
		})
	}))
}

func newResponsesPlanningLLMServer(t *testing.T, config responsesPlanningLLMConfig) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Model != config.model {
			http.Error(w, "unexpected model "+body.Model, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"output_text": config.plan})
	}))
}

func hasToolMessage(messages []struct {
	Role string `json:"role"`
}) bool {
	for _, message := range messages {
		if message.Role == "tool" {
			return true
		}
	}
	return false
}

func assertTaskModelAttribution(t *testing.T, check taskModelAttributionCheck) {
	t.Helper()
	task, err := check.repo.Get(t.Context(), check.taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.SessionID != check.threadID {
		t.Fatalf("expected task %s bound to thread %s, got %#v", task.ID, check.threadID, task)
	}
	if task.ModelConfig == nil || task.ModelConfig.Provider != check.model.provider || task.ModelConfig.Model != check.model.model || task.ModelConfig.Profile != check.model.profile {
		t.Fatalf("unexpected task model config for %s: %#v", check.taskID, task.ModelConfig)
	}
	events, err := check.repo.Events(t.Context(), check.taskID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !eventPayloadHasModel(events, taskpkg.EventToolCall, check.model.provider, check.model.model, check.model.profile) {
		t.Fatalf("expected tool.call payload to include %#v, got %#v", check.model, events)
	}
	if !eventPayloadHasModel(events, taskpkg.EventToolResult, check.model.provider, check.model.model, check.model.profile) {
		t.Fatalf("expected tool.result payload to include %#v, got %#v", check.model, events)
	}
	timeline, err := check.repo.Timeline(t.Context(), check.threadID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !timelineHasKindModel(timeline, "tool_result", check.model) {
		t.Fatalf("expected timeline tool_result to include %#v, got %#v", check.model, timeline)
	}
	contextEnvelope, err := check.repo.ContextEnvelope(t.Context(), check.threadID, taskpkg.ContextRequest{ItemLimit: 50, TokenBudget: 2048})
	if err != nil {
		t.Fatal(err)
	}
	if !timelineHasKindModel(contextEnvelope.Transcript, "tool_result", check.model) {
		t.Fatalf("expected context transcript tool_result to include %#v, got %#v", check.model, contextEnvelope.Transcript)
	}
}

func assertTaskTraceDoesNotUseModel(t *testing.T, check taskTraceModelExclusionCheck) {
	t.Helper()
	events, err := check.repo.Events(t.Context(), check.taskID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if eventPayloadHasModel(events, taskpkg.EventToolCall, check.model.provider, check.model.model, check.model.profile) || eventPayloadHasModel(events, taskpkg.EventToolResult, check.model.provider, check.model.model, check.model.profile) {
		t.Fatalf("task %s trace leaked sibling model %#v into events %#v", check.taskID, check.model, events)
	}
}

func timelineHasKindModel(items []taskpkg.TimelineItem, kind string, model modelExpectation) bool {
	for _, item := range items {
		if item.Kind == kind && item.Provider == model.provider && item.Model == model.model && item.Profile == model.profile {
			return true
		}
	}
	return false
}
