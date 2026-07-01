package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

type fakeCompletionProvider struct {
	items []Completion
}

func (f fakeCompletionProvider) Completions(_ context.Context, _ string) ([]Completion, error) {
	return f.items, nil
}

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

func TestProgramTranscriptWrapsAssistantText_whenViewportNarrows(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(72, 18)
	longReply := "你好！我是 Liora，一个本地优先的编程助手，在单个工作区中帮助你读取代码、修改文件、运行验证并保持上下文。"

	// When
	_, _ = model.Update(streamUpdateMsg{
		update: streamUpdate("task.summary", eventPayload{Message: longReply}),
	})
	model.resize(42, 18)
	view := model.View()

	// Then
	for _, want := range []string{"Assistant", "本地优先", "保持上下文"} {
		if !strings.Contains(view.Content, want) {
			t.Fatalf("expected wrapped transcript to contain %q, got:\n%s", want, view.Content)
		}
	}
	assertVisibleLinesWithinWidth(t, view.Content, 42)
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

func TestProgramInputPanelKeepsRunningNextActionInsideWidth(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(42, 16)
	model.running = true
	model.lastStatus = "running tool"
	model.eventCount = 11
	model.nextAction = "write"

	// When
	panel := model.inputPanelView()

	// Then
	if !strings.Contains(panel, "write") {
		t.Fatalf("expected running footer to keep next action visible, got:\n%s", panel)
	}
	assertVisibleLinesWithinWidth(t, panel, 42)
}

func TestProgramCommandResultUsesAssistantSection_whenApplyCompletes(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(80, 20)

	// When
	model.renderCommandResult(commandResultMsg{line: "/apply", handled: true, result: "完成。\n文件:\n- notes.txt"})

	// Then
	view := model.View()
	for _, want := range []string{"Assistant", "完成", "notes.txt"} {
		if !strings.Contains(view.Content, want) {
			t.Fatalf("expected apply command result to look assistant-facing with %q, got:\n%s", want, view.Content)
		}
	}
	if strings.Contains(view.Content, "System") {
		t.Fatalf("apply command result should not be rendered as a generic system message:\n%s", view.Content)
	}
}

func TestProgramShowsSkillCompletions_whenTypingSkillSlashCommand(t *testing.T) {
	// Given
	m := newModel(context.Background(), Config{
		Workspace: "/tmp/project",
		Completions: fakeCompletionProvider{items: []Completion{
			{Value: "/skill review", Label: "/skill review", Description: "Review code changes"},
			{Value: "/skill tests", Label: "/skill tests", Description: "Generate tests"},
		}},
	}, fakeStreamingSubmitter{})
	m.resize(96, 20)
	m.input.SetValue("/skill re")

	// When
	m.refreshCompletions()
	panel := m.inputPanelView()

	// Then
	for _, want := range []string{"/skill review", "Review code changes"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("expected skill completion panel to contain %q, got:\n%s", want, panel)
		}
	}
	if strings.Contains(panel, "/skill tests") {
		t.Fatalf("completion panel should filter by typed skill prefix, got:\n%s", panel)
	}
}

func TestProgramSlashPaletteShowsSkillCompletions_whenTypingSlash(t *testing.T) {
	// Given
	m := newModel(context.Background(), Config{
		Workspace: "/tmp/project",
		Completions: fakeCompletionProvider{items: []Completion{
			{Value: "/skill review", Label: "review", Description: "Review code changes"},
			{Value: "/skill tests", Label: "tests", Description: "Generate tests"},
		}},
	}, fakeStreamingSubmitter{})
	m.resize(96, 20)
	m.input.SetValue("/")

	// When
	m.refreshCompletions()
	panel := m.inputPanelView()

	// Then
	for _, want := range []string{"Commands", "Skills", "/help", "review", "Review code changes"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("expected slash palette to contain %q, got:\n%s", want, panel)
		}
	}
}

func TestProgramTabAppliesSkillCompletion_whenSlashPrefixMatchesSkillName(t *testing.T) {
	// Given
	m := newModel(context.Background(), Config{
		Workspace: "/tmp/project",
		Completions: fakeCompletionProvider{items: []Completion{
			{Value: "/skill review", Label: "review", Description: "Review code changes"},
			{Value: "/skill tests", Label: "tests", Description: "Generate tests"},
		}},
	}, fakeStreamingSubmitter{})
	m.resize(96, 20)
	m.input.SetValue("/re")
	m.refreshCompletions()

	// When
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := updated.(*model).input.Value()

	// Then
	if got != "/skill review" {
		t.Fatalf("expected /re tab to complete to /skill review, got %q", got)
	}
}

func TestProgramShowsAllSkillCompletions_whenSkillCommandHasTrailingSpace(t *testing.T) {
	// Given
	m := newModel(context.Background(), Config{
		Workspace: "/tmp/project",
		Completions: fakeCompletionProvider{items: []Completion{
			{Value: "/skill review", Label: "/skill review", Description: "Review code changes"},
			{Value: "/skill tests", Label: "/skill tests", Description: "Generate tests"},
		}},
	}, fakeStreamingSubmitter{})
	m.resize(96, 20)
	m.input.SetValue("/skill ")

	// When
	m.refreshCompletions()
	panel := m.inputPanelView()

	// Then
	for _, want := range []string{"/skill review", "/skill tests"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("expected trailing-space skill completion panel to contain %q, got:\n%s", want, panel)
		}
	}
}

func TestProgramTabAppliesSkillCompletion_whenSingleSkillMatches(t *testing.T) {
	// Given
	m := newModel(context.Background(), Config{
		Workspace: "/tmp/project",
		Completions: fakeCompletionProvider{items: []Completion{
			{Value: "/skill review", Label: "/skill review", Description: "Review code changes"},
		}},
	}, fakeStreamingSubmitter{})
	m.resize(96, 20)
	m.input.SetValue("/skill re")
	m.refreshCompletions()

	// When
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := updated.(*model).input.Value()

	// Then
	if got != "/skill review" {
		t.Fatalf("expected tab to complete skill command, got %q", got)
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

func assertVisibleLinesWithinWidth(t *testing.T, content string, width int) {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("expected line width <= %d, got %d for %q\n%s", width, got, line, content)
		}
	}
}
