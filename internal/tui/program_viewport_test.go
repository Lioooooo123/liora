package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestProgramKeepsViewportPosition_whenStreamingWhileUserScrolledHistory(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(72, 8)
	for i := 0; i < 24; i++ {
		model.appendSection("Assistant", "history line "+strings.Repeat("x", i%3+1))
	}
	model.vp.GotoTop()
	before := model.vp.YOffset()

	// When
	_, _ = model.Update(streamUpdateMsg{
		update: streamUpdate("task.summary", eventPayload{Message: "new streamed message while reading history"}),
	})

	// Then
	if model.vp.YOffset() != before {
		t.Fatalf("expected streaming to preserve scrollback offset %d, got %d", before, model.vp.YOffset())
	}
	if strings.Contains(model.vp.View(), "new streamed message") {
		t.Fatalf("expected viewport to stay on older history instead of jumping to latest output:\n%s", model.vp.View())
	}
	if !strings.Contains(model.body.String(), "new streamed message while reading history") {
		t.Fatalf("expected new stream output to be appended to transcript body, got:\n%s", model.body.String())
	}
}

func TestProgramEnablesMouseWheel_whenRenderingFullscreenView(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(72, 8)

	// When
	view := model.View()

	// Then
	if view.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("expected TUI view to enable wheel events with mouse mode %v, got %v", tea.MouseModeCellMotion, view.MouseMode)
	}
}

func TestProgramScrollsIntoHistory_whenPageUpOrWheelUp(t *testing.T) {
	// Given
	m := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	m.resize(72, 8)
	for i := 0; i < 24; i++ {
		m.appendSection("Assistant", "history line "+strings.Repeat("x", i%3+1))
	}
	m.vp.GotoBottom()
	bottom := m.vp.YOffset()
	if bottom == 0 {
		t.Fatal("expected transcript fixture to overflow viewport")
	}

	// When
	pageModel, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	m = pageModel.(*model)
	afterPageUp := m.vp.YOffset()
	wheelModel, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	m = wheelModel.(*model)

	// Then
	if afterPageUp >= bottom {
		t.Fatalf("expected page up to move into history from offset %d, got %d", bottom, afterPageUp)
	}
	if m.vp.YOffset() >= afterPageUp {
		t.Fatalf("expected wheel up to continue moving into history from offset %d, got %d", afterPageUp, m.vp.YOffset())
	}
}
