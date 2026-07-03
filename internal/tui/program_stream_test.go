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

func TestProgramFinalizesAssistantDeltaWithSummary_whenSummaryFormattingDiffers(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(100, 24)

	// When
	for _, update := range []StreamUpdate{
		streamUpdate("assistant.delta", eventPayload{Message: "Hi again! 😊"}),
		streamUpdate("assistant.delta", eventPayload{Message: "I'm Liora, your local-first coding agent."}),
		streamUpdate("task.summary", eventPayload{Message: "Hi again! 😊\n\nI'm Liora, your local-first coding agent."}),
	} {
		_, _ = model.Update(streamUpdateMsg{update: update})
	}
	transcript := model.body.String()

	// Then
	if got := strings.Count(transcript, "Assistant"); got != 1 {
		t.Fatalf("expected final summary to replace the delta block, got %d assistant blocks:\n%s", got, transcript)
	}
	if strings.Contains(transcript, "😊I'm") {
		t.Fatalf("expected final summary formatting to replace compact streamed text, got:\n%s", transcript)
	}
	for _, want := range []string{"Hi again!", "I'm Liora, your local-first coding agent."} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("expected transcript to contain %q, got:\n%s", want, transcript)
		}
	}
}
