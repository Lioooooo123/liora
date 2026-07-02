package tui

import (
	"context"
	"strings"
	"testing"
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
