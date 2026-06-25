package llm

import (
	"context"
	"strings"
	"testing"
)

type fakeGenerator struct {
	messages []Message
	response string
}

func (f *fakeGenerator) Generate(_ context.Context, messages []Message) (string, error) {
	f.messages = append([]Message(nil), messages...)
	return f.response, nil
}

func TestPlannerTurnsNaturalLanguageIntoToolSteps(t *testing.T) {
	generator := &fakeGenerator{response: "```text\nread app.txt\nreplace app.txt old new\ndiff\n```"}
	planner := NewPlanner(generator)

	steps, err := planner.Plan(t.Context(), PlanRequest{
		WorkspaceSummary: "files: app.txt",
		UserPrompt:       "把 old 改成 new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if steps != "read app.txt\nreplace app.txt old new\ndiff" {
		t.Fatalf("unexpected steps %q", steps)
	}
	if len(generator.messages) != 2 {
		t.Fatalf("expected system and user messages, got %#v", generator.messages)
	}
	if !strings.Contains(generator.messages[0].Content, "read <path>") {
		t.Fatalf("system prompt should describe allowed tools: %q", generator.messages[0].Content)
	}
	if !strings.Contains(generator.messages[0].Content, "list <path>") {
		t.Fatalf("system prompt should describe list tool: %q", generator.messages[0].Content)
	}
	if !strings.Contains(generator.messages[1].Content, "把 old 改成 new") {
		t.Fatalf("user prompt missing from planner request: %q", generator.messages[1].Content)
	}
}

func TestPlannerRejectsUnsupportedGeneratedTool(t *testing.T) {
	generator := &fakeGenerator{response: "teleport app.txt"}
	planner := NewPlanner(generator)

	_, err := planner.Plan(t.Context(), PlanRequest{UserPrompt: "delete file"})
	if err == nil {
		t.Fatal("expected unsupported tool error")
	}
	if !strings.Contains(err.Error(), "unsupported tool") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestPlannerSupportsDirectAnswer(t *testing.T) {
	generator := &fakeGenerator{response: "ANSWER: 你好，我是 Liora。"}
	planner := NewPlanner(generator)

	turn, err := planner.PlanTurn(t.Context(), PlanRequest{UserPrompt: "你好"})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Answer != "你好，我是 Liora。" {
		t.Fatalf("unexpected answer %q", turn.Answer)
	}
	if turn.Steps != "" {
		t.Fatalf("expected no tool steps, got %q", turn.Steps)
	}
}
