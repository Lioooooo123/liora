package tui

import (
	"context"
	"testing"
)

func TestProgramAllowsApprovalsCommandDuringRunningTurn(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.running = true

	// When
	canRun := isControlCommandDuringRun("/approvals")

	// Then
	if !canRun {
		t.Fatal("expected /approvals to run immediately while a task is waiting for approval")
	}
}

func TestProgramAllowsReviewPanelsDuringRunningTurn(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.running = true

	for _, command := range []string{"/diff", "/todo", "/tail", "/timeline", "/workbench", "/watch"} {
		if !isControlCommandDuringRun(command) {
			t.Fatalf("expected %s to open immediately while a task is running", command)
		}
	}
}
