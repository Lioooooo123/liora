package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/trace"
)

type fakeSubmitter struct {
	inputs []string
}

func (f *fakeSubmitter) Submit(_ context.Context, input string) (TurnResult, error) {
	f.inputs = append(f.inputs, input)
	return TurnResult{
		PlannedSteps: "read app.txt\ndiff",
		AgentResult: agent.Result{
			Status:  agent.StatusCompleted,
			Summary: "completed 2 steps",
			Diff:    "--- a/app.txt\n+++ b/app.txt\n",
		},
		Events: []trace.Event{
			{Tool: "read", Input: "app.txt", Output: "hello", Status: trace.StatusOK},
			{Tool: "diff", Output: "--- a/app.txt\n+++ b/app.txt\n", Status: trace.StatusOK},
		},
	}, nil
}

func TestRenderWelcomeShowsWorkspaceAndModel(t *testing.T) {
	output := RenderWelcome(Config{
		Workspace: "/tmp/project",
		Model:     "deepseek-v4-pro",
	})

	for _, want := range []string{"Liora", "Workspace", "/tmp/project", "Model", "deepseek-v4-pro", "/help", "/tools", "/exit"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected welcome output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestInteractiveLoopSubmitsPromptAndExits(t *testing.T) {
	submitter := &fakeSubmitter{}
	var out strings.Builder
	app := New(Config{
		Workspace: "/tmp/project",
		Model:     "deepseek-v4-pro",
	}, submitter)

	err := app.Run(context.Background(), strings.NewReader("看一下 app.txt\n/exit\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(submitter.inputs) != 1 || submitter.inputs[0] != "看一下 app.txt" {
		t.Fatalf("unexpected submitted inputs %#v", submitter.inputs)
	}
	rendered := out.String()
	for _, want := range []string{"Working", "Plan", "Tools", "Summary", "completed 2 steps", "Bye"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered output to contain %q, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "You") {
		t.Fatalf("interactive output should not repeat user input in a You block, got:\n%s", rendered)
	}
}

func TestInteractiveLoopRendersAssistantAnswerWithoutTools(t *testing.T) {
	submitter := SubmitterFunc(func(_ context.Context, input string) (TurnResult, error) {
		return TurnResult{Answer: "你好，我是 Liora。"}, nil
	})
	var out strings.Builder
	app := New(Config{Workspace: "/tmp/project", Model: "deepseek-v4-pro"}, submitter)

	err := app.Run(context.Background(), strings.NewReader("你好\n/exit\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Assistant") || !strings.Contains(rendered, "你好，我是 Liora。") {
		t.Fatalf("expected assistant answer, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "Error: planner returned no steps") {
		t.Fatalf("unexpected planner error:\n%s", rendered)
	}
}

type fakeStreamingSubmitter struct{}

func (f fakeStreamingSubmitter) Submit(_ context.Context, input string) (TurnResult, error) {
	return TurnResult{}, nil
}

func (f fakeStreamingSubmitter) SubmitStream(_ context.Context, input string, onEvent func(StreamUpdate)) (TurnResult, error) {
	for _, update := range []StreamUpdate{
		streamUpdate("task.plan_ready", eventPayload{Steps: "list ."}),
		streamUpdate("tool.result", eventPayload{Tool: "list", Input: ".", Output: "README.md\n", Status: string(trace.StatusOK)}),
		streamUpdate("task.summary", eventPayload{Message: "completed 1 step"}),
		streamUpdate("task.completed", eventPayload{Status: "completed"}),
	} {
		onEvent(update)
	}
	return TurnResult{AgentResult: agent.Result{Status: agent.StatusCompleted, Summary: "completed 1 step"}}, nil
}

func TestInteractiveLoopStreamsTaskEvents(t *testing.T) {
	var out strings.Builder
	app := New(Config{Workspace: "/tmp/project", Model: "deepseek-v4-pro"}, fakeStreamingSubmitter{})

	err := app.Run(context.Background(), strings.NewReader("看看目录\n/exit\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{"Working", "Plan", "- list .", "Tools", "README.md", "Summary", "completed 1 step", "Status", "completed"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected streamed output to contain %q, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "You") {
		t.Fatalf("interactive stream output should not repeat user input, got:\n%s", rendered)
	}
}

func TestInteractiveLoopRendersMultilineToolOutput(t *testing.T) {
	submitter := SubmitterFunc(func(_ context.Context, input string) (TurnResult, error) {
		return TurnResult{
			PlannedSteps: "list .",
			Events: []trace.Event{
				{
					Tool:   "list",
					Input:  ".",
					Output: "README.md\ncmd/\ninternal/\n",
					Status: trace.StatusOK,
				},
			},
			AgentResult: agent.Result{Status: agent.StatusCompleted, Summary: "completed 1 step"},
		}, nil
	})
	var out strings.Builder
	app := New(Config{Workspace: "/tmp/project", Model: "deepseek-v4-pro"}, submitter)

	err := app.Run(context.Background(), strings.NewReader("看看目录\n/exit\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{"README.md", "cmd/", "internal/"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func streamUpdate(eventType string, payload eventPayload) StreamUpdate {
	data, _ := json.Marshal(payload)
	return StreamUpdate{
		Type:        eventType,
		PayloadJSON: string(data),
	}
}

func TestRenderTurnSeparatesSections(t *testing.T) {
	var out strings.Builder
	RenderTurn(&out, TurnView{
		Input:    "看看目录",
		ShowUser: true,
		TurnResult: TurnResult{
			Answer:       "",
			PlannedSteps: "list .",
			Events: []trace.Event{
				{Tool: "list", Input: ".", Output: "README.md\ncmd/\n", Status: trace.StatusOK},
			},
			AgentResult: agent.Result{Summary: "completed 1 step"},
		},
	})

	rendered := out.String()
	for _, want := range []string{"You", "Plan", "Tools", "Summary", "README.md", "cmd/", "│"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered turn to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestRenderTurnShowsNextActionsForDiff(t *testing.T) {
	var out strings.Builder
	RenderTurn(&out, TurnView{
		Input: "修改 app",
		TurnResult: TurnResult{
			AgentResult: agent.Result{
				Summary: "completed 1 step",
				Diff:    "--- a/app.txt\n+++ b/app.txt\n",
			},
		},
	})

	rendered := out.String()
	for _, want := range []string{"Diff", "Next", "apply", "cancel"} {
		if !strings.Contains(strings.ToLower(rendered), strings.ToLower(want)) {
			t.Fatalf("expected rendered output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestRenderTurnCanHideUserSection(t *testing.T) {
	var out strings.Builder
	RenderTurn(&out, TurnView{
		Input:      "你好",
		ShowUser:   false,
		TurnResult: TurnResult{Answer: "你好，我是 Liora。"},
	})

	rendered := out.String()
	if strings.Contains(rendered, "You") || strings.Contains(rendered, "你好\n") {
		t.Fatalf("expected hidden user section, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Assistant") {
		t.Fatalf("expected assistant section, got:\n%s", rendered)
	}
}
