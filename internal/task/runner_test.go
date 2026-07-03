package task

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/store"
	"github.com/Lioooooo123/liora/internal/tools"
)

type fakeGenerator struct {
	response  string
	responses []string
}

type fakeStreamingToolGenerator struct {
	completion llm.Completion
	deltas     []string
}

type fakeSandboxExecutor struct {
	command string
}

type blockingSecondCommandExecutor struct {
	firstDone chan struct{}
	release   chan struct{}
}

func (f *fakeGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	if len(f.responses) > 0 {
		response := f.responses[0]
		f.responses = f.responses[1:]
		return response, nil
	}
	return f.response, nil
}

func (f *fakeStreamingToolGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	return f.completion.Content, nil
}

func (f *fakeStreamingToolGenerator) GenerateWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (llm.Completion, error) {
	return f.completion, nil
}

func (f *fakeStreamingToolGenerator) GenerateWithToolsStream(_ context.Context, _ []llm.Message, _ []llm.ToolSchema, onDelta llm.DeltaHandler) (llm.Completion, error) {
	for _, delta := range f.deltas {
		if err := onDelta(delta); err != nil {
			return llm.Completion{}, err
		}
	}
	return f.completion, nil
}

func (f *fakeStreamingToolGenerator) SupportsTools() bool {
	return true
}

func (f *fakeSandboxExecutor) Run(_ context.Context, _ string, command string) (tools.ShellResult, error) {
	f.command = command
	return tools.ShellResult{Stdout: "sandbox task ok\n", ExitCode: 0}, nil
}

func newBlockingSecondCommandExecutor() *blockingSecondCommandExecutor {
	return &blockingSecondCommandExecutor{
		firstDone: make(chan struct{}),
		release:   make(chan struct{}),
	}
}

func (e *blockingSecondCommandExecutor) Run(ctx context.Context, _ string, command string) (tools.ShellResult, error) {
	if command == "first" {
		close(e.firstDone)
		return tools.ShellResult{Stdout: "first ok\n", ExitCode: 0}, nil
	}
	select {
	case <-ctx.Done():
		return tools.ShellResult{ExitCode: -1}, ctx.Err()
	case <-e.release:
		return tools.ShellResult{Stdout: "second ok\n", ExitCode: 0}, nil
	}
}

func TestRunnerPersistsAssistantDeltaEventsFromStreamingToolLoop(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "say hi",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	generator := &fakeStreamingToolGenerator{
		completion: llm.Completion{Content: "hello"},
		deltas:     []string{"he", "llo"},
	}
	runner := NewRunner(repo, llm.NewPlanner(generator))
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var deltaText strings.Builder
	var sawDeltaBeforeSummary bool
	for _, event := range events {
		if event.Type == EventSummary && deltaText.Len() > 0 {
			sawDeltaBeforeSummary = true
		}
		if event.Type != EventAssistantDelta {
			continue
		}
		var payload EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatal(err)
		}
		deltaText.WriteString(payload.Message)
	}
	if deltaText.String() != "hello" || !sawDeltaBeforeSummary {
		t.Fatalf("expected assistant deltas before final summary, deltas=%q events=%#v", deltaText.String(), eventTypes(events))
	}
}

func TestRunnerExecutesTaskAndPersistsEvents(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "创建 notes",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "write notes.txt hello\nread notes.txt"}))
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("unexpected task status %#v", got)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var eventTypes []EventType
	var combinedPayload strings.Builder
	for _, event := range events {
		eventTypes = append(eventTypes, event.Type)
		combinedPayload.WriteString(event.Payload)
		combinedPayload.WriteByte('\n')
	}
	for _, want := range []EventType{EventPlanning, EventPlanReady, EventToolCall, EventToolResult, EventSummary, EventCompleted} {
		if !containsEventType(eventTypes, want) {
			t.Fatalf("expected event %s in %#v", want, eventTypes)
		}
	}
	if !strings.Contains(combinedPayload.String(), "notes.txt") || !strings.Contains(combinedPayload.String(), "completed 2 steps") {
		t.Fatalf("unexpected event payloads:\n%s", combinedPayload.String())
	}
}

func TestRunnerPersistsPairedToolCallAndResultIDsForFailures(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "read missing file",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "read missing.txt"}))
	if err := runner.Run(t.Context(), task.ID); err == nil {
		t.Fatal("expected missing file task to fail")
	}

	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var call, result EventPayload
	for _, event := range events {
		switch event.Type {
		case EventToolCall:
			if err := json.Unmarshal([]byte(event.Payload), &call); err != nil {
				t.Fatal(err)
			}
		case EventToolResult:
			if err := json.Unmarshal([]byte(event.Payload), &result); err != nil {
				t.Fatal(err)
			}
		}
	}
	if call.ToolCallID == "" || result.ToolCallID == "" || call.ToolCallID != result.ToolCallID {
		t.Fatalf("expected paired tool call/result ids, call=%#v result=%#v", call, result)
	}
	if result.ToolResultID != call.ToolCallID+"-result" {
		t.Fatalf("expected deterministic tool result id, call=%#v result=%#v", call, result)
	}
	if result.Status != "error" {
		t.Fatalf("expected failed result status to be structured as error, got %#v", result)
	}
	if !strings.Contains(result.Output, "missing.txt") {
		t.Fatalf("expected failed result output to mention missing file, got %#v", result)
	}
}

func TestRunnerEmitsToolLifecycleEventsWithModelAttribution(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "write note",
		Natural:   true,
		ModelConfig: &ModelConfig{
			Provider: "openai-chat",
			Model:    "gpt-5",
			Profile:  "strong",
			Source:   "task_override",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "write notes.txt hello"}))
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !containsEventType(eventTypes(events), EventToolLifecycle) {
		t.Fatalf("expected tool lifecycle events, got %#v", eventTypes(events))
	}
	payloads := lifecyclePayloads(t, events)
	if got := lifecyclePayloadPhases(payloads); !reflect.DeepEqual(got, []string{"prepare", "authorize", "execute", "finalize"}) {
		t.Fatalf("unexpected lifecycle phases %#v payloads=%#v", got, payloads)
	}
	final := payloads[len(payloads)-1]
	if final.Provider != "openai-chat" || final.Model != "gpt-5" || final.Profile != "strong" {
		t.Fatalf("expected lifecycle model attribution, got %#v", final)
	}
	if final.Tool != "write" || final.ToolCallID == "" || final.ToolResultID == "" || final.Status != "ok" {
		t.Fatalf("unexpected finalize lifecycle payload %#v", final)
	}
	timeline, err := repo.Timeline(t.Context(), task.SessionID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !timelineHasLifecycle(timeline, "finalize") {
		t.Fatalf("expected timeline lifecycle item, got %#v", timeline)
	}
}

func TestDaemonToolOutputSinkPersistsSessionArtifactAndEvent(t *testing.T) {
	workspace := t.TempDir()
	storeRoot := t.TempDir()
	db, err := store.New(storeRoot).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "large output",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := daemonToolOutputSink{
		root:      storeRoot,
		repo:      repo,
		taskID:    task.ID,
		sessionID: task.SessionID,
	}

	outputPath, err := sink.PersistToolOutput(t.Context(), llm.ToolCall{ID: "call/1", Name: "mcp"}, "large output body")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(outputPath, "artifact://artifacts/sessions/"+task.SessionID+"/tasks/"+task.ID+"/tool-results/") {
		t.Fatalf("expected session artifact URI, got %q", outputPath)
	}
	if _, err := os.Stat(filepath.Join(storeRoot, strings.TrimPrefix(outputPath, "artifact://"))); err != nil {
		t.Fatalf("expected artifact file under store root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".liora", "tool-results")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("daemon artifact sink should not create workspace tool-results, err=%v", err)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var payload EventPayload
	for _, event := range events {
		if event.Type == EventArtifactReference {
			if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
				t.Fatal(err)
			}
		}
	}
	if payload.Path != outputPath || payload.Tool != "mcp" || payload.ToolCallID != "call/1" {
		t.Fatalf("expected artifact reference event to point at sink output, got %#v", payload)
	}
}

func TestRunnerRoutesNaturalTaskThroughThreadModelRegistry(t *testing.T) {
	workspace := t.TempDir()
	llmRequests := make(chan string, 1)
	threadLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected LLM path %s", r.URL.Path)
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		llmRequests <- body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ANSWER: routed through thread model"}}]}`))
	}))
	defer threadLLM.Close()

	persistentStore := store.New(t.TempDir())
	thread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "model thread"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistentStore.UpdateThreadModelConfig(thread.ID, store.UpdateThreadModelConfigRequest{
		Provider: llm.ProviderOpenAIChat,
		Model:    "thread-model",
		BaseURL:  threadLLM.URL,
		Profile:  "strong",
	}); err != nil {
		t.Fatal(err)
	}
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	if _, err := repo.EnsureSession(t.Context(), thread.ID, thread.Title, workspace); err != nil {
		t.Fatal(err)
	}
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		SessionID: thread.ID,
		Prompt:    "which model handles this?",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := llm.NewRegistry(llm.Config{
		Provider: llm.ProviderOpenAIChat,
		BaseURL:  "http://default.invalid",
		APIKey:   "test-key",
		Model:    "default-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "ANSWER: default planner should not be used"}))
	runner.SetStore(persistentStore)
	runner.SetLLMRegistry(registry)
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-llmRequests:
		if got != "thread-model" {
			t.Fatalf("expected thread model request, got %q", got)
		}
	default:
		t.Fatal("thread LLM endpoint was not called")
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var payloads strings.Builder
	for _, event := range events {
		payloads.WriteString(event.Payload)
		payloads.WriteByte('\n')
	}
	if !strings.Contains(payloads.String(), `"provider":"openai-chat"`) || !strings.Contains(payloads.String(), `"model":"thread-model"`) || !strings.Contains(payloads.String(), `"profile":"strong"`) {
		t.Fatalf("expected event metadata to include resolved thread model, got:\n%s", payloads.String())
	}
	if !strings.Contains(payloads.String(), "routed through thread model") {
		t.Fatalf("expected thread LLM response in task events, got:\n%s", payloads.String())
	}
}

func TestRunnerResolvesModelBindingHierarchyIntoTaskMetadata(t *testing.T) {
	workspace := t.TempDir()
	otherWorkspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	if _, err := persistentStore.UpdateWorkspaceModelConfig(workspace, store.UpdateWorkspaceModelConfigRequest{
		Provider: llm.ProviderOpenAIChat,
		Model:    "workspace-model",
		Profile:  "workspace-profile",
	}); err != nil {
		t.Fatal(err)
	}
	thread, err := persistentStore.CreateConversationThread(store.CreateConversationThreadRequest{Workspace: workspace, Title: "thread"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistentStore.UpdateThreadModelConfig(thread.ID, store.UpdateThreadModelConfigRequest{
		Provider: llm.ProviderOpenAIChat,
		Model:    "thread-model",
		Profile:  "thread-profile",
	}); err != nil {
		t.Fatal(err)
	}
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	if _, err := repo.EnsureSession(t.Context(), thread.ID, thread.Title, workspace); err != nil {
		t.Fatal(err)
	}
	registry, err := llm.NewRegistry(llm.Config{
		Provider: llm.ProviderOpenAIChat,
		APIKey:   "key",
		Model:    "global-model",
		Profile:  "global-profile",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetStore(persistentStore)
	runner.SetLLMRegistry(registry)

	cases := []struct {
		name       string
		request    CreateRequest
		wantModel  string
		wantSource string
	}{
		{
			name:       "global",
			request:    CreateRequest{Workspace: otherWorkspace, Prompt: "write global.txt ok", Natural: false},
			wantModel:  "global-model",
			wantSource: "global_default",
		},
		{
			name:       "workspace",
			request:    CreateRequest{Workspace: workspace, Prompt: "write workspace.txt ok", Natural: false},
			wantModel:  "workspace-model",
			wantSource: "workspace_default",
		},
		{
			name:       "thread",
			request:    CreateRequest{Workspace: workspace, SessionID: thread.ID, Prompt: "write thread.txt ok", Natural: false},
			wantModel:  "thread-model",
			wantSource: "thread_override",
		},
		{
			name: "task",
			request: CreateRequest{
				Workspace: workspace,
				SessionID: thread.ID,
				Prompt:    "write task.txt ok",
				Natural:   false,
				ModelConfig: &ModelConfig{
					Provider: llm.ProviderOpenAIChat,
					Model:    "task-model",
					Profile:  "task-profile",
				},
			},
			wantModel:  "task-model",
			wantSource: "task_override",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task, err := repo.Create(t.Context(), tc.request)
			if err != nil {
				t.Fatal(err)
			}
			if err := runner.Run(t.Context(), task.ID); err != nil {
				t.Fatal(err)
			}
			got, err := repo.Get(t.Context(), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.ModelConfig == nil || got.ModelConfig.Model != tc.wantModel || got.ModelConfig.Source != tc.wantSource {
				t.Fatalf("unexpected resolved task model %#v", got.ModelConfig)
			}
			events, err := repo.Events(t.Context(), task.ID, 20)
			if err != nil {
				t.Fatal(err)
			}
			payloads := strings.Join(eventPayloads(events), "\n")
			if !strings.Contains(payloads, `"model":"`+tc.wantModel+`"`) {
				t.Fatalf("expected event payloads to include resolved model %q, got:\n%s", tc.wantModel, payloads)
			}
		})
	}
}

func TestRunnerExecutesScriptTaskWithoutPlanner(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "write smoke.txt ok\nread smoke.txt",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("unexpected task status %#v", got)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !containsEventType(eventTypes(events), EventToolResult) {
		t.Fatalf("expected tool result event, got %#v", events)
	}
}

func TestRunnerUsesSandboxExecutorForScriptTask(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "run echo hello",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	sandbox := &fakeSandboxExecutor{}
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetSandbox(sandbox)
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	if sandbox.command != "echo hello" {
		t.Fatalf("unexpected sandbox command %q", sandbox.command)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var payloads strings.Builder
	for _, event := range events {
		payloads.WriteString(event.Payload)
	}
	if !strings.Contains(payloads.String(), "sandbox task ok") {
		t.Fatalf("expected sandbox output in events, got %s", payloads.String())
	}
	if !containsEventType(eventTypes(events), EventSandboxRun) {
		t.Fatalf("expected sandbox run event, got %#v", eventTypes(events))
	}
}

func TestRunnerPersistsScriptToolEventsWhileTaskIsRunning(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "run first\nrun block",
		Natural:   false,
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
	case <-time.After(3 * time.Second):
		t.Fatal("first command did not run")
	}
	waitUntil(t, 3*time.Second, func() bool {
		events, err := repo.Events(t.Context(), task.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Type == EventToolResult && strings.Contains(event.Payload, "first ok") {
				return true
			}
		}
		return false
	})

	close(executor.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunnerPersistsNaturalToolEventsWhileTaskIsRunning(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "do it",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := newBlockingSecondCommandExecutor()
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "run first\nrun block"}))
	runner.SetSandbox(executor)
	done := make(chan error, 1)
	go func() {
		done <- runner.Run(t.Context(), task.ID)
	}()

	select {
	case <-executor.firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("first command did not run")
	}
	waitUntil(t, 3*time.Second, func() bool {
		events, err := repo.Events(t.Context(), task.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Type == EventToolResult && strings.Contains(event.Payload, "first ok") {
				return true
			}
		}
		return false
	})

	close(executor.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunnerPersistsNaturalPlanBeforeTaskCompletes(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "plan it",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := newBlockingSecondCommandExecutor()
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "run first\nrun block"}))
	runner.SetSandbox(executor)
	done := make(chan error, 1)
	go func() {
		done <- runner.Run(t.Context(), task.ID)
	}()

	select {
	case <-executor.firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("first command did not run")
	}
	waitUntil(t, 3*time.Second, func() bool {
		events, err := repo.Events(t.Context(), task.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Type == EventPlanReady && strings.Contains(event.Payload, "run first") {
				return true
			}
		}
		return false
	})

	close(executor.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunnerReplansNaturalTaskAfterToolFailure(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "app.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "read the app file",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{responses: []string{
		"read missing.txt",
		"list .\nread app.txt",
	}}))
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("unexpected task status %#v", got)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	types := eventTypes(events)
	if !containsEventType(types, EventReplanning) || countEventType(types, EventPlanReady) != 2 {
		t.Fatalf("expected replan and two plan events, got %#v", types)
	}
	var payloads strings.Builder
	for _, event := range events {
		payloads.WriteString(event.Payload)
		payloads.WriteByte('\n')
	}
	if !strings.Contains(payloads.String(), "missing.txt") || !strings.Contains(payloads.String(), "app.txt") || !strings.Contains(payloads.String(), "completed 2 steps") || !strings.Contains(payloads.String(), `"replan_reason"`) {
		t.Fatalf("unexpected event payloads:\n%s", payloads.String())
	}
	timeline, err := repo.Timeline(t.Context(), task.SessionID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var sawReplan bool
	for _, item := range timeline {
		if item.Type == string(EventReplanning) && item.Kind == "status" {
			sawReplan = true
		}
	}
	if !sawReplan {
		t.Fatalf("expected timeline to expose replan status, got %#v", timeline)
	}
}

func TestRunnerPatchModeProducesDiffWithoutMutatingWorkspace(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "write notes.txt hello",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetPatchMode(true)
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("patch mode should not mutate real workspace, stat err: %v", err)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var payloads strings.Builder
	for _, event := range events {
		payloads.WriteString(event.Payload)
	}
	if !strings.Contains(payloads.String(), "+++ b/notes.txt") || !strings.Contains(payloads.String(), "+hello") {
		t.Fatalf("expected diff event for notes.txt, got %s", payloads.String())
	}
	if !containsEventType(eventTypes(events), EventSandboxWorkspace) {
		t.Fatalf("expected sandbox workspace event, got %#v", eventTypes(events))
	}
}

func TestRunnerWaitsForPermissionBeforeDangerousShell(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "run rm -rf build",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetPermissionPolicy(permission.Policy{Mode: permission.ModePrompt})
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusWaitingUser {
		t.Fatalf("expected waiting user status, got %#v", got)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var payloads strings.Builder
	for _, event := range events {
		payloads.WriteString(event.Payload)
	}
	if !containsEventType(eventTypes(events), EventPermissionRequest) || !strings.Contains(payloads.String(), "dangerous_shell") {
		t.Fatalf("expected permission request event, got %#v payloads=%s", eventTypes(events), payloads.String())
	}
}

func TestRunnerWaitsForPermissionBeforeNetworkShell(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "run curl https://example.com/data.json",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetPermissionPolicy(permission.Policy{Mode: permission.ModePrompt})
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusWaitingUser {
		t.Fatalf("expected waiting user status, got %#v", got)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var payloads strings.Builder
	for _, event := range events {
		payloads.WriteString(event.Payload)
	}
	if !containsEventType(eventTypes(events), EventPermissionRequest) || !strings.Contains(payloads.String(), `"risk":"network"`) {
		t.Fatalf("expected network permission request event, got %#v payloads=%s", eventTypes(events), payloads.String())
	}
}

func TestRunnerChildTaskDoesNotInheritParentPermissionShortcuts(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	parent, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "parent",
		Scope: TaskScope{
			Paths:        []string{workspace},
			NetworkHosts: []string{"api.internal"},
			MCPServers:   []string{"docs"},
			MCPTools:     []string{"docs.search"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistentStore.CreatePermissionRule(store.CreatePermissionRuleRequest{
		Action:    store.PermissionRuleAlwaysAllow,
		Workspace: workspace,
		SessionID: parent.SessionID,
		Tool:      "run",
		Risk:      "dangerous_shell",
		Reason:    "parent shortcut",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := persistentStore.CreatePermissionRule(store.CreatePermissionRuleRequest{
		Action:    store.PermissionRuleAlwaysAllow,
		Workspace: workspace,
		SessionID: parent.SessionID,
		Tool:      "mcp",
		Risk:      "external",
		Reason:    "parent external shortcut",
	}); err != nil {
		t.Fatal(err)
	}
	child, err := repo.Create(t.Context(), CreateRequest{
		Workspace:    workspace,
		Prompt:       "child",
		ParentTaskID: parent.ID,
		Origin:       OriginSubagent,
	})
	if err != nil {
		t.Fatal(err)
	}
	childWithNetwork, err := repo.Create(t.Context(), CreateRequest{
		Workspace:    workspace,
		Prompt:       "network child",
		ParentTaskID: parent.ID,
		Origin:       OriginSubagent,
		Scope:        TaskScope{NetworkHosts: []string{"api.internal"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetStore(persistentStore)
	runner.SetPermissionPolicy(permission.Policy{
		Mode:               permission.ModePrompt,
		NetworkDefaultDeny: true,
		NetworkAllowlist:   []string{"api.internal"},
	})

	parentChecker := runner.permissionChecker(parent)
	if err := parentChecker.Check(t.Context(), permission.Request{Tool: "run", Input: "rm -rf build"}); err != nil {
		t.Fatalf("expected parent always-allow rule to apply, got %v", err)
	}
	childChecker := runner.permissionChecker(child)
	assertPermissionRequired(t, childChecker.Check(t.Context(), permission.Request{Tool: "run", Input: "rm -rf build"}), "dangerous_shell")
	assertPermissionRequired(t, childChecker.Check(t.Context(), permission.Request{Tool: "run", Input: "curl https://api.internal/data.json"}), "network")
	assertPermissionRequired(t, childChecker.Check(t.Context(), permission.Request{Tool: "mcp", Input: "docs search {}"}), "external")

	networkChecker := runner.permissionChecker(childWithNetwork)
	if err := networkChecker.Check(t.Context(), permission.Request{Tool: "run", Input: "curl https://api.internal/data.json"}); err != nil {
		t.Fatalf("expected explicit child network scope to allow matching host, got %v", err)
	}
}

func TestRunnerDefaultDeniesAutomationNetworkShell(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "run curl https://evil.example.net/data.json",
		Natural:   false,
		Origin:    OriginBackground,
		Automation: AutomationMetadata{
			Kind: AutomationKindBackground,
			Risk: AutomationRiskSafe,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetPermissionPolicy(permission.Policy{
		Mode:               permission.ModeAuto,
		NetworkDefaultDeny: true,
		NetworkAllowlist:   []string{"example.com"},
	})
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusWaitingUser {
		t.Fatalf("expected automation network command to wait for approval, got %#v", got)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !containsEventType(eventTypes(events), EventPermissionRequest) {
		t.Fatalf("expected permission request event, got %#v", eventTypes(events))
	}
}

func TestRunnerContinuesAfterApproval(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "run rm -rf build",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetPermissionPolicy(permission.Policy{Mode: permission.ModePrompt})
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	waiting, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != StatusWaitingUser {
		t.Fatalf("expected waiting before approval, got %#v", waiting)
	}
	if err := repo.GrantApproval(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("expected completed after approval, got %#v", got)
	}
}

func TestRunnerWaitsForUserInputAndContinuesWithAnswer(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "update the selected file",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	generator := &fakeGenerator{responses: []string{
		"ASK_USER: Which file should I edit?",
		"ANSWER: I will edit notes.txt.",
	}}
	runner := NewRunner(repo, llm.NewPlanner(generator))

	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	waiting, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != StatusWaitingUser {
		t.Fatalf("expected waiting for user input, got %#v", waiting)
	}
	if err := repo.ReceiveUserInput(t.Context(), task.ID, "notes.txt"); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("expected completed after user input, got %#v", got)
	}
	events, err := repo.Events(t.Context(), task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	types := eventTypes(events)
	if !containsEventType(types, EventUserInputRequest) || !containsEventType(types, EventUserInputReceived) {
		t.Fatalf("expected user input events, got %#v", types)
	}
}

func containsEventType(events []EventType, want EventType) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func eventTypes(events []Event) []EventType {
	types := make([]EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func eventPayloads(events []Event) []string {
	payloads := make([]string, 0, len(events))
	for _, event := range events {
		payloads = append(payloads, event.Payload)
	}
	return payloads
}

func lifecyclePayloads(t *testing.T, events []Event) []EventPayload {
	t.Helper()
	var payloads []EventPayload
	for _, event := range events {
		if event.Type != EventToolLifecycle {
			continue
		}
		var payload EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode lifecycle payload: %v", err)
		}
		payloads = append(payloads, payload)
	}
	return payloads
}

func lifecyclePayloadPhases(payloads []EventPayload) []string {
	phases := make([]string, 0, len(payloads))
	for _, payload := range payloads {
		phases = append(phases, payload.Phase)
	}
	return phases
}

func timelineHasLifecycle(items []TimelineItem, phase string) bool {
	for _, item := range items {
		if item.Kind == "tool_lifecycle" && item.Status == phase {
			return true
		}
	}
	return false
}

func countEventType(events []EventType, want EventType) int {
	count := 0
	for _, event := range events {
		if event == want {
			count++
		}
	}
	return count
}

func assertPermissionRequired(t *testing.T, err error, wantRisk string) {
	t.Helper()
	var required *permission.RequiredError
	if !errors.As(err, &required) {
		t.Fatalf("expected permission required for risk %q, got %v", wantRisk, err)
	}
	if required.Request.Risk != wantRisk {
		t.Fatalf("expected permission risk %q, got %#v", wantRisk, required.Request)
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
