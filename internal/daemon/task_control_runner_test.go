package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/tools"
)

func TestTaskControlChildRunsThroughRunnerPatchModeArtifactsAndEvents(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "parent.txt"), []byte("parent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	generator := &scriptedToolGenerator{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{
			{ID: "child_run_big", Name: "run", Arguments: `{"command":"generate large output"}`},
			{ID: "child_write", Name: "write", Arguments: `{"path":"child.txt","content":"child wrote through patch workspace\n"}`},
			{ID: "child_diff", Name: "diff", Arguments: `{}`},
		}},
		{Content: "child runner complete"},
	}}
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(generator))
	runner.SetStore(persistentStore)
	runner.SetPatchMode(true)
	runner.SetSandbox(largeOutputShellExecutor{output: strings.Repeat("child-output\n", 6000)})
	s := newServer(Config{Repository: repo, Store: persistentStore, Runner: runner})

	parent, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "parent",
		Natural:   true,
		Origin:    taskpkg.OriginForeground,
		Scope:     taskpkg.TaskScope{Paths: []string{workspace}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), parent.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}

	child, err := s.CreateChildTask(t.Context(), parent, taskpkg.ChildTaskRequest{
		Prompt:       "child edits through runner",
		SubagentName: "writer",
		Role:         "implementation",
		Scope:        taskpkg.TaskScope{Paths: []string{workspace}},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		got, err := repo.Get(t.Context(), child.ID)
		if err != nil {
			return false
		}
		return got.Status == taskpkg.StatusCompleted
	})

	if _, err := os.Stat(filepath.Join(workspace, "child.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("patch-mode child should not mutate source workspace before apply, err=%v", err)
	}
	events, err := repo.Events(t.Context(), child.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []taskpkg.EventType{taskpkg.EventSandboxWorkspace, taskpkg.EventToolCall, taskpkg.EventToolResult, taskpkg.EventArtifactReference, taskpkg.EventDiff, taskpkg.EventCompleted} {
		if !hasEvent(events, want) {
			t.Fatalf("expected child event %s in %#v", want, events)
		}
	}
	if !eventPayloadContains(events, taskpkg.EventSandboxWorkspace, "workspace mode: copy") {
		t.Fatalf("expected child runner to use patch copy workspace, got %#v", events)
	}
	artifactURI := artifactURIFromEvents(t, events)
	if !strings.HasPrefix(artifactURI, "artifact://artifacts/sessions/"+child.SessionID+"/tasks/"+child.ID+"/tool-results/") {
		t.Fatalf("expected child artifact URI to be session/task scoped, got %q", artifactURI)
	}
	if _, err := os.Stat(filepath.Join(persistentStore.Root(), filepath.FromSlash(strings.TrimPrefix(artifactURI, "artifact://")))); err != nil {
		t.Fatalf("expected child artifact file under store root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".liora", "tool-results")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child artifact sink should not write workspace tool-results, err=%v", err)
	}
	_, output, err := s.ReadChildTaskOutput(t.Context(), parent, taskpkg.ChildTaskOutputRequest{TaskID: child.ID, Limit: 120000})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "artifact://artifacts/") || !strings.Contains(output, "child.txt") {
		t.Fatalf("expected TaskOutput to expose artifact and diff hints, got %q", output)
	}
}

func TestTaskControlChildPermissionWaitStaysOnChildTask(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	generator := &scriptedToolGenerator{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "dangerous_child_run", Name: "run", Arguments: `{"command":"rm -rf build"}`}}},
	}}
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(generator))
	runner.SetStore(persistentStore)
	runner.SetPermissionPolicy(permission.Policy{Mode: permission.ModePrompt})
	s := newServer(Config{Repository: repo, Store: persistentStore, Runner: runner})

	parent, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "parent",
		Natural:   true,
		Origin:    taskpkg.OriginForeground,
		Scope:     taskpkg.TaskScope{Paths: []string{workspace}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), parent.ID, taskpkg.StatusRunning); err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateChildTask(t.Context(), parent, taskpkg.ChildTaskRequest{
		Prompt: "child dangerous action",
		Scope:  taskpkg.TaskScope{Paths: []string{workspace}},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		got, err := repo.Get(t.Context(), child.ID)
		if err != nil {
			return false
		}
		return got.Status == taskpkg.StatusWaitingUser
	})

	parentAfter, err := repo.Get(t.Context(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parentAfter.Status != taskpkg.StatusRunning {
		t.Fatalf("child permission wait must not mutate parent status, got %#v", parentAfter)
	}
	childEvents, err := repo.Events(t.Context(), child.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEvent(childEvents, taskpkg.EventPermissionRequest) {
		t.Fatalf("expected child permission request event, got %#v", childEvents)
	}
	if hasEvent(childEvents, taskpkg.EventToolCall) {
		t.Fatalf("dangerous child tool should not run before approval, got %#v", childEvents)
	}
	item, ok, err := repo.ApprovalItemForTask(t.Context(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || item.TaskID != child.ID || item.ToolCallID != "dangerous_child_run" || item.ToolName != "run" || item.Risk != "dangerous_shell" {
		t.Fatalf("expected approval item scoped to child tool call, got %#v ok=%t", item, ok)
	}
}

type scriptedToolGenerator struct {
	completions []llm.Completion
	calls       int
}

func (g *scriptedToolGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	return "ANSWER: scripted child generator is native-tool only", nil
}

func (g *scriptedToolGenerator) GenerateWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (llm.Completion, error) {
	if g.calls >= len(g.completions) {
		return llm.Completion{Content: "scripted child complete"}, nil
	}
	completion := g.completions[g.calls]
	g.calls++
	return completion, nil
}

func (g *scriptedToolGenerator) SupportsTools() bool { return true }

type largeOutputShellExecutor struct {
	output string
}

func (e largeOutputShellExecutor) Run(_ context.Context, _ string, _ string) (tools.ShellResult, error) {
	return tools.ShellResult{Stdout: e.output, ExitCode: 0}, nil
}

func eventPayloadContains(events []taskpkg.Event, eventType taskpkg.EventType, text string) bool {
	for _, event := range events {
		if event.Type != eventType {
			continue
		}
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err == nil && strings.Contains(payload.Message, text) {
			return true
		}
	}
	return false
}

func artifactURIFromEvents(t *testing.T, events []taskpkg.Event) string {
	t.Helper()
	for _, event := range events {
		if event.Type != taskpkg.EventArtifactReference {
			continue
		}
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Path
	}
	t.Fatal("missing artifact reference event")
	return ""
}
