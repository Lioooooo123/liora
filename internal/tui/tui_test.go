package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
		Core:      "embedded daemon",
		Safety:    "patch-first",
	})

	for _, want := range []string{"Liora", "local agent workbench", "workspace", "/tmp/project", "model", "deepseek-v4-pro", "core", "embedded daemon", "safety", "patch-first", "/help", "/workbench", "/memory", "/exit"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected welcome output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestInteractiveLoopRendersGroupedHelp(t *testing.T) {
	var out strings.Builder
	app := New(Config{Workspace: "/tmp/project", Model: "deepseek-v4-pro"}, SubmitterFunc(func(_ context.Context, _ string) (TurnResult, error) {
		return TurnResult{}, nil
	}))

	if err := app.Run(context.Background(), strings.NewReader("/help\n/exit\n"), &out); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{"Help", "work", "/tools", "/workbench", "history", "/timeline", "/transcript", "changes", "/diff", "/apply", "approval", "/approvals", "context", "/memory", "system", "/doctor", "session", "/resume-latest"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected grouped help to contain %q, got:\n%s", want, rendered)
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
	for _, want := range []string{"Task - started", "Plan", "Tools", "Summary", "completed 2 steps", "Bye"} {
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
	for _, want := range []string{"Task - started", "Plan", "- list .", "Tools", "README.md", "Summary", "completed 1 step", "Status", "completed"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected streamed output to contain %q, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "You") {
		t.Fatalf("interactive stream output should not repeat user input, got:\n%s", rendered)
	}
}

func TestRenderStreamUpdateUsesCompactProgressLines(t *testing.T) {
	var out strings.Builder
	RenderStreamUpdate(&out, streamUpdate("task.planning", eventPayload{Message: "Planning task"}))
	RenderStreamUpdate(&out, streamUpdate("tool.call", eventPayload{Tool: "list", Input: "."}))

	rendered := out.String()
	for _, want := range []string{"Status - Planning task", "Tool - list ."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected compact progress output to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"│ Status", "│ Tool"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("expected progress output not to render boxed %q, got:\n%s", avoid, rendered)
		}
	}
}

type blockingStreamingSubmitter struct {
	cancelled <-chan struct{}
	started   chan struct{}
}

func (s *blockingStreamingSubmitter) Submit(_ context.Context, _ string) (TurnResult, error) {
	return TurnResult{}, nil
}

func (s *blockingStreamingSubmitter) SubmitStream(ctx context.Context, _ string, onEvent func(StreamUpdate)) (TurnResult, error) {
	close(s.started)
	onEvent(streamUpdate("task.plan_ready", eventPayload{Steps: "run long-task"}))
	select {
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	case <-s.cancelled:
		onEvent(streamUpdate("task.cancelled", eventPayload{Status: "cancelled", Message: "cancelled from test"}))
		return TurnResult{AgentResult: agent.Result{Status: agent.StatusFailed, Summary: "cancelled"}}, nil
	}
}

func TestStreamingLoopHandlesCommandWhileTaskRuns(t *testing.T) {
	cancelled := make(chan struct{})
	started := make(chan struct{})
	submitter := &blockingStreamingSubmitter{cancelled: cancelled, started: started}
	commandSeen := make(chan struct{})
	handler := CommandHandlerFunc(func(_ context.Context, line string) (string, bool, error) {
		if line != "/cancel" {
			return "", false, nil
		}
		close(commandSeen)
		close(cancelled)
		return "Cancelled task task_test.", true, nil
	})
	var out strings.Builder
	app := New(Config{Workspace: "/tmp/project", Model: "deepseek-v4-pro", Commands: handler}, submitter)

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(context.Background(), strings.NewReader("long task\n/cancel\n/exit\n"), &out)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("streaming task did not start")
	}
	select {
	case <-commandSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("running command was not handled")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("interactive loop did not exit")
	}
	rendered := out.String()
	for _, want := range []string{"Task - started", "Plan", "Cancelled task", "cancelled from test", "Bye"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected streaming command output to contain %q, got:\n%s", want, rendered)
		}
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
	for _, want := range []string{"Assistant", "已准备好变更", "Diff", "Next", "apply", "exit"} {
		if !strings.Contains(strings.ToLower(rendered), strings.ToLower(want)) {
			t.Fatalf("expected rendered output to contain %q, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "stop a running task") {
		t.Fatalf("completed diff guidance should not mention stopping a running task:\n%s", rendered)
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
