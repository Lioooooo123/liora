package task

import (
	"context"
	"testing"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/store"
	"github.com/Lioooooo123/liora/internal/tools"
)

type countingShellExecutor struct {
	commands []string
}

func (e *countingShellExecutor) Run(_ context.Context, _ string, command string) (tools.ShellResult, error) {
	e.commands = append(e.commands, command)
	return tools.ShellResult{Stdout: command + " ok\n", ExitCode: 0}, nil
}

func TestRunnerRequiresSeparateApprovalForEachDangerousAction(t *testing.T) {
	// Given
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "run rm -rf build\nrun rm -rf dist",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := &countingShellExecutor{}
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetPermissionPolicy(permission.Policy{Mode: permission.ModePrompt})
	runner.SetSandbox(executor)

	// When
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	firstWait, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstWait.Status != StatusWaitingUser {
		t.Fatalf("expected first approval wait, got %#v", firstWait)
	}
	if err := repo.GrantApproval(t.Context(), task.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	secondWait, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if secondWait.Status != StatusWaitingUser {
		t.Fatalf("expected second approval wait instead of task-wide approval, got %#v", secondWait)
	}
	if got, want := executor.commands, []string{"rm -rf build"}; !equalStrings(got, want) {
		t.Fatalf("expected only first command to run once before second approval, got %#v", got)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := countEvents(events, EventPermissionRequest); got != 2 {
		t.Fatalf("expected two permission requests, got %d events=%#v", got, events)
	}
	if err := repo.GrantApproval(t.Context(), task.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	completed, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != StatusCompleted {
		t.Fatalf("expected completed after second approval, got %#v", completed)
	}
	if got, want := executor.commands, []string{"rm -rf build", "rm -rf dist"}; !equalStrings(got, want) {
		t.Fatalf("expected each dangerous command to run exactly once, got %#v", got)
	}
}

func TestRunnerRequiresSeparateApprovalForRepeatedDangerousAction(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "run rm -rf build\nrun rm -rf build",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := &countingShellExecutor{}
	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	runner.SetPermissionPolicy(permission.Policy{Mode: permission.ModePrompt})
	runner.SetSandbox(executor)

	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.GrantApproval(t.Context(), task.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	secondWait, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondWait.Status != StatusWaitingUser {
		t.Fatalf("expected repeated command to require a second approval, got %#v", secondWait)
	}
	if got, want := executor.commands, []string{"rm -rf build"}; !equalStrings(got, want) {
		t.Fatalf("expected repeated command to run only once before second approval, got %#v", got)
	}
	if err := repo.GrantApproval(t.Context(), task.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	if got, want := executor.commands, []string{"rm -rf build", "rm -rf build"}; !equalStrings(got, want) {
		t.Fatalf("expected repeated command to run once per approval, got %#v", got)
	}
}

func countEvents(events []Event, typ EventType) int {
	count := 0
	for _, event := range events {
		if event.Type == typ {
			count++
		}
	}
	return count
}
