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

func TestPlannerSupportsUserQuestion(t *testing.T) {
	generator := &fakeGenerator{response: "ASK_USER: Which file should I edit?"}
	planner := NewPlanner(generator)

	turn, err := planner.PlanTurn(t.Context(), PlanRequest{UserPrompt: "change it"})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Question != "Which file should I edit?" {
		t.Fatalf("unexpected question %q", turn.Question)
	}
	if turn.Steps != "" || turn.Answer != "" {
		t.Fatalf("expected only question, got %#v", turn)
	}
}

func TestPlannerPromptDocumentsUserQuestionOutput(t *testing.T) {
	prompt := plannerSystemPrompt()
	for _, want := range []string{"ASK_USER:", "one precise question", "cannot proceed safely"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected planner prompt to contain %q:\n%s", want, prompt)
		}
	}
}

func TestPlannerExtractsStepsFromMarkdownOutput(t *testing.T) {
	generator := &fakeGenerator{response: `可以，我会先看文件：

1. list .
2. stat "Assignment Question.pdf"
3. document "Assignment Question.pdf"

然后总结。`}
	planner := NewPlanner(generator)

	steps, err := planner.Plan(t.Context(), PlanRequest{UserPrompt: "帮我看看 Assignment Question.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	want := "list .\nstat \"Assignment Question.pdf\"\ndocument \"Assignment Question.pdf\""
	if steps != want {
		t.Fatalf("unexpected steps %q", steps)
	}
}

func TestPlannerExtractsStepsFromFencedList(t *testing.T) {
	generator := &fakeGenerator{response: "```plan\n- read README.md\n- run go test ./...\n```"}
	planner := NewPlanner(generator)

	steps, err := planner.Plan(t.Context(), PlanRequest{UserPrompt: "跑测试"})
	if err != nil {
		t.Fatal(err)
	}
	if steps != "read README.md\nrun go test ./..." {
		t.Fatalf("unexpected steps %q", steps)
	}
}

func TestPlannerReplansWithFailureContext(t *testing.T) {
	generator := &fakeGenerator{response: "list .\nread app.txt"}
	planner := NewPlanner(generator)

	turn, err := planner.ReplanTurn(t.Context(), ReplanRequest{
		WorkspaceSummary: "files: README.md app.txt",
		UserPrompt:       "看看 app 文件",
		PreviousSteps:    "read missing.txt",
		Failure:          "open missing.txt: no such file or directory",
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Steps != "list .\nread app.txt" {
		t.Fatalf("unexpected replan steps %q", turn.Steps)
	}
	if len(generator.messages) != 2 {
		t.Fatalf("expected system and user messages, got %#v", generator.messages)
	}
	if !strings.Contains(generator.messages[0].Content, "repair planner") || !strings.Contains(generator.messages[0].Content, "read <path>") {
		t.Fatalf("unexpected replan system prompt:\n%s", generator.messages[0].Content)
	}
	userPrompt := generator.messages[1].Content
	for _, want := range []string{"看看 app 文件", "read missing.txt", "open missing.txt"} {
		if !strings.Contains(userPrompt, want) {
			t.Fatalf("expected replan prompt to contain %q, got:\n%s", want, userPrompt)
		}
	}
}
