package tui

import (
	"context"
	"encoding/json"
	"os"
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

	for _, want := range []string{"›", "liora", "local agent workbench", "workspace", "/tmp/project", "model", "deepseek-v4-pro", "core", "embedded daemon", "safety", "patch-first", "/help", "/workbench", "/memory", "/schedule", "/exit"} {
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
	for _, want := range []string{"Help", "work", "/tools", "/workbench", "history", "/timeline", "/transcript", "changes", "/diff", "/apply", "approval", "/approvals", "context", "/memory", "/schedule", "system", "/doctor", "session", "/resume-latest"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected grouped help to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestInteractiveLoopRendersApplyCompletionAsAssistant_whenCommandHandled(t *testing.T) {
	handler := CommandHandlerFunc(func(_ context.Context, line string) (string, bool, error) {
		if line == "/apply" {
			return "完成。\n文件:\n- notes.txt", true, nil
		}
		return "", false, nil
	})
	var out strings.Builder
	app := New(Config{Workspace: "/tmp/project", Commands: handler}, &fakeSubmitter{})

	if err := app.Run(context.Background(), strings.NewReader("/apply\n/exit\n"), &out); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{"Assistant", "完成", "notes.txt"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected apply completion to render as assistant-facing %q, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "System") {
		t.Fatalf("apply completion should not render as generic system output:\n%s", rendered)
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
	for _, want := range []string{"You", "看一下 app.txt", "Assistant", "completed 2 steps", "Bye"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered output to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"Plan", "Tools", "Task - started", "hello"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("interactive output should hide internal %q, got:\n%s", avoid, rendered)
		}
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
		streamUpdate("task.created", eventPayload{Message: input}),
		streamUpdate("task.plan_ready", eventPayload{Steps: "list ."}),
		streamUpdate("prompt_context.snapshot", eventPayload{
			Message:       "Prompt context snapshot",
			Output:        "Prompt context session-001\nHash: sha256:abc123",
			Target:        "sha256:abc123",
			TokenEstimate: 12,
			TokenBudget:   4096,
		}),
		streamUpdate("tool.result", eventPayload{Tool: "list", Input: ".", Output: "README.md\n", Status: string(trace.StatusOK)}),
		streamUpdate("assistant.delta", eventPayload{Message: "completed "}),
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
	for _, want := range []string{"You", "看看目录", "Assistant", "completed 1 step"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected streamed output to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"Task - started", "Plan", "- list .", "Tools", "README.md", "Status", "Event", "task.created", "tool.result", "prompt_context.snapshot", "Prompt context snapshot"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("stream output should hide internal %q, got:\n%s", avoid, rendered)
		}
	}
}

func TestRenderStreamUpdateShowsAssistantDelta(t *testing.T) {
	var out strings.Builder
	RenderStreamUpdate(&out, streamUpdate("assistant.delta", eventPayload{Message: "正在处理"}))

	rendered := out.String()
	if !strings.Contains(rendered, "Assistant") || !strings.Contains(rendered, "正在处理") {
		t.Fatalf("expected assistant delta to render as assistant text, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "Event") || strings.Contains(rendered, "assistant.delta") {
		t.Fatalf("assistant delta should not expose event identity, got:\n%s", rendered)
	}
}

func TestRenderStreamUpdateHidesInternalProgress(t *testing.T) {
	var out strings.Builder
	RenderStreamUpdate(&out, streamUpdate("task.created", eventPayload{Message: "hi"}))
	RenderStreamUpdate(&out, streamUpdate("task.planning", eventPayload{Message: "Planning task"}))
	RenderStreamUpdate(&out, streamUpdate("tool.call", eventPayload{Tool: "list", Input: "."}))
	RenderStreamUpdate(&out, streamUpdate("prompt_context.snapshot", eventPayload{Message: "Prompt context snapshot"}))

	rendered := out.String()
	for _, avoid := range []string{"task.created", "Event", "Status - Planning task", "Tool - list .", "│ Status", "│ Tool", "prompt_context.snapshot", "Prompt context snapshot"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("expected progress output to hide %q, got:\n%s", avoid, rendered)
		}
	}
}

func TestRenderStreamUpdateHidesPromptContextSnapshotEvenWhenMalformed(t *testing.T) {
	var out strings.Builder
	RenderStreamUpdate(&out, StreamUpdate{Type: "prompt_context.snapshot", PayloadJSON: "{"})

	if rendered := out.String(); rendered != "" {
		t.Fatalf("prompt context snapshot should stay hidden in chat, got:\n%s", rendered)
	}
}

func TestRenderStreamUpdateShowsUserInputRequestAsAssistantQuestion(t *testing.T) {
	var out strings.Builder
	RenderStreamUpdate(&out, streamUpdate("user_input.requested", eventPayload{Message: "你想继续哪一项？"}))

	rendered := out.String()
	if !strings.Contains(rendered, "Assistant") || !strings.Contains(rendered, "你想继续哪一项？") {
		t.Fatalf("expected user input request to render as assistant question, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "Event") || strings.Contains(rendered, "user_input.requested") {
		t.Fatalf("user input request should not expose event identity, got:\n%s", rendered)
	}
}

func TestDaemonEventFormattersShareSemantics(t *testing.T) {
	update := streamUpdate("todo.updated", eventPayload{ID: "todo-1", Action: "complete", Message: "write tests", Status: "done", Priority: "high", SourceTaskID: "task-1"})

	section := FormatDaemonEventUpdate(update)
	replay := FormatDaemonEventReplay(update.Type, update.PayloadJSON)
	tail := strings.Join(FormatDaemonEventTail(update.Type, update.PayloadJSON), "\n")
	watch := FormatDaemonEventWatch("task-001", update.Type, update.PayloadJSON)

	for name, got := range map[string]string{
		"bubble": section.Body,
		"replay": replay,
		"tail":   tail,
		"watch":  watch,
	} {
		if !strings.Contains(got, "write tests") || !strings.Contains(got, "priority=high") || !strings.Contains(got, "source_task_id=task-1") {
			t.Fatalf("%s formatter did not preserve shared event semantics: %q", name, got)
		}
	}
	if !section.Visible || section.Title != "Todo" {
		t.Fatalf("unexpected bubble section %#v", section)
	}
}

func TestDaemonEventFormattersShowToolLifecycleOutsideChat(t *testing.T) {
	payload := `{"tool":"read","phase":"execute","tool_call_id":"read_1","input":"README.md","status":"running","access_mode":"read","access_resource":"path","access_argument":"README.md","batch_id":"batch-1","batch_size":2}`
	update := StreamUpdate{Type: "tool.lifecycle", PayloadJSON: payload}

	section := FormatDaemonEventUpdate(update)
	if section.Visible {
		t.Fatalf("tool lifecycle should stay hidden from chat transcript, got %#v", section)
	}

	replay := FormatDaemonEventReplay(update.Type, update.PayloadJSON)
	tail := strings.Join(FormatDaemonEventTail(update.Type, update.PayloadJSON), "\n")
	watch := FormatDaemonEventWatch("task-001", update.Type, update.PayloadJSON)
	for name, got := range map[string]string{
		"replay": replay,
		"tail":   tail,
		"watch":  watch,
	} {
		for _, want := range []string{"tool.lifecycle[execute]", "read README.md", "access=read:path(README.md)", "batch=batch-1/2"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s formatter lost lifecycle detail %q: %q", name, want, got)
			}
		}
	}
}

func TestDaemonEventFormattersHandleMalformedAndUnknownEvents(t *testing.T) {
	malformed := StreamUpdate{Type: "custom.event", PayloadJSON: "{"}

	section := FormatDaemonEventUpdate(malformed)
	if !section.Visible || !strings.Contains(section.Body, "custom.event") || !strings.Contains(section.Body, "malformed payload") {
		t.Fatalf("unexpected malformed section %#v", section)
	}
	for name, got := range map[string]string{
		"replay": FormatDaemonEventReplay(malformed.Type, malformed.PayloadJSON),
		"tail":   strings.Join(FormatDaemonEventTail(malformed.Type, malformed.PayloadJSON), "\n"),
		"watch":  FormatDaemonEventWatch("task-001", malformed.Type, malformed.PayloadJSON),
	} {
		if !strings.Contains(got, "custom.event") || !strings.Contains(got, "malformed payload") {
			t.Fatalf("%s formatter did not preserve malformed event identity: %q", name, got)
		}
	}

	unknown := streamUpdate("custom.event", eventPayload{Message: "hello from future"})
	if got := FormatDaemonEventUpdate(unknown); !got.Visible || !strings.Contains(got.Body, "custom.event: hello from future") {
		t.Fatalf("unexpected unknown event section %#v", got)
	}
}

func TestDaemonEventFormattersCoverCatalogFixture(t *testing.T) {
	fixture := readDaemonEventCatalogFixture(t)
	expected := make([]string, 0, len(fixture.Events))
	covered := make([]string, 0, len(fixture.Events))

	for _, frame := range fixture.Events {
		payload, err := json.Marshal(frame.Data)
		if err != nil {
			t.Fatalf("marshal catalog payload for %s: %v", frame.Event, err)
		}
		replay := FormatDaemonEventReplay(frame.Event, string(payload))
		tail := strings.Join(FormatDaemonEventTail(frame.Event, string(payload)), "\n")
		watch := FormatDaemonEventWatch("task-catalog", frame.Event, string(payload))
		if strings.Contains(replay, "malformed payload") || strings.Contains(tail, "malformed payload") || strings.Contains(watch, "malformed payload") {
			t.Fatalf("catalog event %s formatted as malformed: replay=%q tail=%q watch=%q", frame.Event, replay, tail, watch)
		}
		if !strings.Contains(replay, frame.Event) || !strings.Contains(tail, frame.Event) || !strings.Contains(watch, frame.Event) {
			t.Fatalf("catalog event %s lost identity: replay=%q tail=%q watch=%q", frame.Event, replay, tail, watch)
		}
		expected = append(expected, frame.Event)
		covered = append(covered, frame.Event)
	}

	if missing := missingDaemonEventCoverage(expected, covered); len(missing) != 0 {
		t.Fatalf("missing daemon event formatter coverage: %#v", missing)
	}
}

func TestDaemonEventFormatterCoverageDetectsMissingCatalogEvent(t *testing.T) {
	fixture := readDaemonEventCatalogFixture(t)
	if len(fixture.Events) == 0 {
		t.Fatal("expected catalog events")
	}
	expected := make([]string, 0, len(fixture.Events))
	for _, frame := range fixture.Events {
		expected = append(expected, frame.Event)
	}

	missing := missingDaemonEventCoverage(expected, expected[1:])

	if len(missing) != 1 || missing[0] != expected[0] {
		t.Fatalf("expected missing first catalog event, got %#v", missing)
	}
}

type daemonEventCatalogFixture struct {
	Events []struct {
		Event string       `json:"event"`
		Data  eventPayload `json:"data"`
	} `json:"events"`
}

func readDaemonEventCatalogFixture(t *testing.T) daemonEventCatalogFixture {
	t.Helper()

	payload, err := os.ReadFile("../protocol/testdata/daemon-event-catalog.json")
	if err != nil {
		t.Fatalf("read daemon event catalog fixture: %v", err)
	}
	var fixture daemonEventCatalogFixture
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatalf("decode daemon event catalog fixture: %v", err)
	}
	return fixture
}

func missingDaemonEventCoverage(expected []string, covered []string) []string {
	coveredSet := make(map[string]bool, len(covered))
	for _, event := range covered {
		coveredSet[event] = true
	}
	var missing []string
	for _, event := range expected {
		if !coveredSet[event] {
			missing = append(missing, event)
		}
	}
	return missing
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
	for _, want := range []string{"Cancelled task", "cancelled from test", "Bye"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected streaming command output to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"Task - started", "Plan"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("streaming command output should hide internal %q, got:\n%s", avoid, rendered)
		}
	}
}

func TestInteractiveLoopHidesMultilineToolOutput(t *testing.T) {
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
	for _, want := range []string{"You", "看看目录", "Assistant", "completed 1 step"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered output to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"README.md", "cmd/", "internal/", "Tools", "Plan"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("expected rendered output to hide %q, got:\n%s", avoid, rendered)
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
	for _, want := range []string{"You", "Assistant", "completed 1 step", "│"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered turn to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"Plan", "Tools", "README.md", "cmd/"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("expected rendered turn to hide internal %q, got:\n%s", avoid, rendered)
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
	for _, want := range []string{"Assistant", "已准备好变更", "apply"} {
		if !strings.Contains(strings.ToLower(rendered), strings.ToLower(want)) {
			t.Fatalf("expected rendered output to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"Diff", "+++ a/app.txt"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("completed diff guidance should stay compact and hide %q:\n%s", avoid, rendered)
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
