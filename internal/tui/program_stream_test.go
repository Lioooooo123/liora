package tui

import (
	"context"
	"strings"
	"testing"
)

func TestProgramAggregatesAssistantDeltaStream_whenRenderingFullscreenTurn(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(72, 18)

	// When
	for _, delta := range []string{"你好", "！", "😊", "有什么", "我可以"} {
		_, _ = model.Update(streamUpdateMsg{
			update: streamUpdate("assistant.delta", eventPayload{Message: delta}),
		})
	}
	transcript := model.body.String()

	// Then
	if got := strings.Count(transcript, "Assistant"); got != 1 {
		t.Fatalf("expected one assistant block for one delta stream, got %d:\n%s", got, transcript)
	}
	for _, want := range []string{"你好", "！", "😊", "有什么", "我可以"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("expected aggregated assistant stream to contain %q, got:\n%s", want, transcript)
		}
	}
}

func TestProgramSkipsDuplicateSummary_whenAssistantDeltaAlreadyRendered(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(72, 18)

	// When
	for _, update := range []StreamUpdate{
		streamUpdate("assistant.delta", eventPayload{Message: "你好"}),
		streamUpdate("assistant.delta", eventPayload{Message: "！"}),
		streamUpdate("task.summary", eventPayload{Message: "你好！"}),
	} {
		_, _ = model.Update(streamUpdateMsg{update: update})
	}
	transcript := model.body.String()

	// Then
	if got := strings.Count(transcript, "Assistant"); got != 1 {
		t.Fatalf("expected duplicate final summary to reuse the delta block, got %d assistant blocks:\n%s", got, transcript)
	}
	if strings.Count(transcript, "你好") != 1 {
		t.Fatalf("expected assistant text to appear once, got:\n%s", transcript)
	}
}
