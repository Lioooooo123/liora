package task

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
	"github.com/Lioooooo123/liora/internal/tools"
)

type fakeGenerator struct {
	response string
}

type fakeSandboxExecutor struct {
	command string
}

type blockingSecondCommandExecutor struct {
	firstDone chan struct{}
	release   chan struct{}
}

func (f *fakeGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	return f.response, nil
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
