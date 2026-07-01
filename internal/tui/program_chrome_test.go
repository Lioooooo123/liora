package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

func TestProgramChromeUsesCompactHeader_whenTranscriptExists(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{
		Workspace: "/tmp/project-with-a-longer-name",
		Model:     "deepseek-v4-pro",
		Core:      "embedded daemon",
		Safety:    "patch-first",
	}, fakeStreamingSubmitter{})
	model.body.WriteString("Assistant\nhello\n")
	model.resize(140, 24)

	// When
	rendered := model.headerView() + "\n" + model.statusLine()

	// Then
	for _, want := range []string{"✦", "LIORA", "ready", "─", "events", "type a request"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected chrome to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"workspace", "project-with-a-longer-name", "model", "deepseek-v4-pro", "core", "embedded daemon", "safety", "patch-first", "/timeline", "/workbench", "/cancel"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("compact transcript header should not contain old chrome %q, got:\n%s", avoid, rendered)
		}
	}
}

func TestProgramWelcomeCardShown_whenTranscriptIsEmpty(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{
		Workspace: "/tmp/project",
		Model:     "deepseek-v4-pro",
		Core:      "embedded daemon",
		Safety:    "patch-first",
	}, fakeStreamingSubmitter{})
	model.resize(100, 24)

	// When
	view := model.View()

	// Then
	for _, want := range []string{"Welcome to Liora", "Directory:", "/diff", "/apply", "╭", "╰"} {
		if !strings.Contains(view.Content, want) {
			t.Fatalf("expected welcome card to contain %q, got:\n%s", want, view.Content)
		}
	}
	if strings.Contains(view.Content, "workspace /tmp/project") {
		t.Fatalf("empty state should not render the transcript chrome header, got:\n%s", view.Content)
	}
}

func TestProgramInputPanelLeavesBottomBreathingRoom_whenRendered(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(100, 24)

	// When
	view := model.View()

	// Then
	if !strings.HasSuffix(view.Content, "\n \n ") {
		t.Fatalf("expected input panel to leave bottom breathing room, got:\n%q", view.Content)
	}
	for _, want := range []string{"╭", "╰", "liora", "request, or /help for commands"} {
		if !strings.Contains(view.Content, want) {
			t.Fatalf("expected input panel to contain %q, got:\n%s", want, view.Content)
		}
	}
	if view.Cursor == nil {
		t.Fatal("expected real input cursor")
	}
	if view.Cursor.Shape != tea.CursorBar {
		t.Fatalf("expected bar cursor for CJK input, got %v", view.Cursor.Shape)
	}
	if view.Cursor.Position.X < 2 {
		t.Fatalf("expected cursor inside bordered input box, got x=%d", view.Cursor.Position.X)
	}
	if view.Cursor.Position.Y >= lipgloss.Height(view.Content)-1 {
		t.Fatalf("expected cursor above bottom breathing room, cursor=%d height=%d", view.Cursor.Position.Y, lipgloss.Height(view.Content))
	}
}

func TestProgramInputCursorUsesCJKDisplayWidth_whenChineseTextEntered(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(100, 24)
	model.input.SetValue("你给我写一个代码")

	// When
	view := model.View()

	// Then
	if view.Cursor == nil {
		t.Fatal("expected real input cursor")
	}
	wantMinX := 2 + lipgloss.Width(model.input.Prompt+"你给我写一个代码")
	if view.Cursor.Position.X < wantMinX {
		t.Fatalf("expected cursor after CJK text at x >= %d, got %d\n%s", wantMinX, view.Cursor.Position.X, view.Content)
	}
	if view.Cursor.Shape != tea.CursorBar {
		t.Fatalf("expected bar cursor for Chinese input, got %v", view.Cursor.Shape)
	}
}

func TestProgramSubmitLineShowsUserMessage_whenNaturalPromptStarts(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(100, 24)

	// When
	cmd := model.submitLine("帮我看一下项目结构")

	// Then
	if cmd == nil {
		t.Fatal("expected natural prompt to start a turn")
	}
	view := model.View()
	for _, want := range []string{"You", "帮我看一下项目结构"} {
		if !strings.Contains(view.Content, want) {
			t.Fatalf("expected submitted prompt to be visible as %q, got:\n%s", want, view.Content)
		}
	}
	if strings.Contains(view.Content, "Welcome to Liora") {
		t.Fatalf("started transcript should leave welcome card, got:\n%s", view.Content)
	}
}

func TestProgramStatusLineUsesShortRunningAction_whenTaskStarts(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(48, 16)

	// When
	cmd := model.submitLine("hi")

	// Then
	if cmd == nil {
		t.Fatal("expected natural prompt to start a turn")
	}
	status := model.statusLine()
	if !strings.Contains(status, "/cancel") {
		t.Fatalf("expected running status to show cancel action, got:\n%s", status)
	}
	if strings.Contains(status, "stops the r") || strings.Contains(status, "stops the run") {
		t.Fatalf("status should not render truncated long action, got:\n%s", status)
	}
}

func TestProgramFooterShowsProgress_whenInternalEventsAreHidden(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(80, 20)
	model.submitLine("hi")

	// When
	model.noteStreamUpdate(streamUpdate("task.planning", eventPayload{Message: "Planning task"}))
	model.noteStreamUpdate(streamUpdate("sandbox.workspace", eventPayload{Message: "workspace mode: copy"}))

	// Then
	status := model.statusLine()
	for _, want := range []string{"preparing", "waiting for model", "events 2"} {
		if !strings.Contains(status, want) {
			t.Fatalf("expected status to contain %q, got:\n%s", want, status)
		}
	}
}

func TestProgramAddsFallback_whenTurnCompletesWithoutVisibleOutput(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(80, 20)
	model.submitLine("hi")

	// When
	_, _ = model.Update(turnDoneMsg{})

	// Then
	view := model.View()
	for _, want := range []string{"You", "hi", "Assistant", "Completed."} {
		if !strings.Contains(view.Content, want) {
			t.Fatalf("expected completed fallback to contain %q, got:\n%s", want, view.Content)
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
