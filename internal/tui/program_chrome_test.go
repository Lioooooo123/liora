package tui

import (
	"context"
	"strings"
	"testing"
)

func TestProgramChromeShowsWorkbenchMetadata_whenRendered(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{
		Workspace: "/tmp/project-with-a-longer-name",
		Model:     "deepseek-v4-pro",
		Core:      "embedded daemon",
		Safety:    "patch-first",
	}, fakeStreamingSubmitter{})
	model.resize(140, 24)

	// When
	rendered := model.headerView() + "\n" + model.statusLine()

	// Then
	for _, want := range []string{"Liora", "ready", "workspace", "project-with-a-longer-name", "model", "deepseek-v4-pro", "core", "embedded daemon", "safety", "patch-first", "/timeline", "/apply", "/cancel", "events", "type a request"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected chrome to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestProgramQueuesPrompt_whenTaskIsRunning(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{}, fakeStreamingSubmitter{})
	model.running = true

	// When
	cmd := model.submitLine("next request")

	// Then
	if cmd != nil {
		t.Fatal("expected queued prompt not to start immediately")
	}
	if len(model.pending) != 1 || model.pending[0] != "next request" {
		t.Fatalf("unexpected pending queue: %#v", model.pending)
	}
	if !strings.Contains(model.body.String(), "Queued for the next turn") {
		t.Fatalf("expected queue feedback, got:\n%s", model.body.String())
	}
}

func TestProgramAllowsControlCommand_whenTaskIsRunning(t *testing.T) {
	// Given
	seen := ""
	handler := CommandHandlerFunc(func(_ context.Context, line string) (string, bool, error) {
		seen = line
		return "cancelled", true, nil
	})
	model := newModel(context.Background(), Config{Commands: handler}, fakeStreamingSubmitter{})
	model.running = true

	// When
	cmd := model.submitLine("/cancel")
	if cmd == nil {
		t.Fatal("expected control command to run immediately")
	}
	msg := cmd()

	// Then
	result, ok := msg.(commandResultMsg)
	if !ok {
		t.Fatalf("expected commandResultMsg, got %T", msg)
	}
	if seen != "/cancel" {
		t.Fatalf("expected handler to see /cancel, got %q", seen)
	}
	if !result.handled || result.result != "cancelled" || result.err != nil {
		t.Fatalf("unexpected command result: %#v", result)
	}
	if len(model.pending) != 0 {
		t.Fatalf("control command should not be queued: %#v", model.pending)
	}
}
