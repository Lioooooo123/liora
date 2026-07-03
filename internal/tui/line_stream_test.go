package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/agent"
)

type markdownStreamingSubmitter struct{}

func (s markdownStreamingSubmitter) Submit(_ context.Context, _ string) (TurnResult, error) {
	return TurnResult{}, nil
}

func (s markdownStreamingSubmitter) SubmitStream(_ context.Context, _ string, onEvent func(StreamUpdate)) (TurnResult, error) {
	for _, part := range []string{
		"## Result\n\n",
		"1. **Rendered** item\n",
		"2. `inline code`\n\n",
		"```sh\n",
		"go test ./internal/tui\n",
		"```",
	} {
		onEvent(streamUpdate("assistant.delta", eventPayload{Message: part}))
	}
	onEvent(streamUpdate("task.summary", eventPayload{Message: "## Result\n\n1. **Rendered** item\n2. `inline code`\n\n```sh\ngo test ./internal/tui\n```"}))
	return TurnResult{AgentResult: agent.Result{Status: agent.StatusCompleted, Summary: "done"}}, nil
}

func TestStreamingLoopRendersAssistantMarkdown_whenMarkdownArrivesAsDeltas(t *testing.T) {
	// Given
	var out strings.Builder
	app := New(Config{Workspace: "/tmp/project", Model: "test-model"}, markdownStreamingSubmitter{})

	// When
	if err := app.Run(context.Background(), strings.NewReader("show markdown\n/exit\n"), &out); err != nil {
		t.Fatal(err)
	}
	rendered := terminalPlainText(out.String())

	// Then
	if got := strings.Count(rendered, "╭─ Assistant"); got != 1 {
		t.Fatalf("expected one rendered assistant panel for markdown stream, got %d:\n%s", got, rendered)
	}
	for _, want := range []string{"Result", "Rendered item", "inline code", "go test ./internal/tui"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected markdown stream output to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"## Result", "**Rendered**", "`inline code`", "```sh", "```"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("markdown stream output should not expose raw marker %q, got:\n%s", avoid, rendered)
		}
	}
}

func TestLineStreamRendererRendersAssistantMarkdownDelta_withoutWaitingForSummary(t *testing.T) {
	// Given
	var out strings.Builder
	renderer := newLineStreamRenderer(&out)

	// When
	renderer.Render(streamUpdate("assistant.delta", eventPayload{Message: "## Result\n\n- **Live** item\n"}))
	rendered := terminalPlainText(out.String())

	// Then
	for _, want := range []string{"Assistant", "Result", "Live item"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected live markdown delta to contain %q before summary, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"## Result", "**Live**"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("live markdown delta should not expose raw marker %q, got:\n%s", avoid, rendered)
		}
	}
}

func TestLineStreamRendererCompletesAssistantDelta_whenSummaryExtendsPrefix(t *testing.T) {
	// Given
	var out strings.Builder
	renderer := newLineStreamRenderer(&out)

	// When
	renderer.Render(streamUpdate("assistant.delta", eventPayload{Message: "completed "}))
	renderer.Render(streamUpdate("task.summary", eventPayload{Message: "completed 1 step"}))
	rendered := terminalPlainText(out.String())

	// Then
	if !strings.Contains(rendered, "completed 1 step") {
		t.Fatalf("expected summary suffix to complete streamed assistant text, got:\n%s", rendered)
	}
	if got := strings.Count(rendered, "Assistant"); got != 1 {
		t.Fatalf("expected summary completion to stay in one assistant panel, got %d:\n%s", got, rendered)
	}
}
