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
