package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/llm"
)

// fakeToolGenerator implements both llm.Generator and llm.ToolCaller so the
// runtime routes it to the native tool-use loop instead of the planner path.
type fakeToolGenerator struct {
	completions  []llm.Completion
	calls        int
	transcripts  [][]llm.Message
	plannerReply string
	plannerCalls int
}

func (f *fakeToolGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	f.plannerCalls++
	return f.plannerReply, nil
}

func (f *fakeToolGenerator) GenerateWithTools(_ context.Context, messages []llm.Message, _ []llm.ToolSchema) (llm.Completion, error) {
	snapshot := make([]llm.Message, len(messages))
	copy(snapshot, messages)
	f.transcripts = append(f.transcripts, snapshot)
	completion := f.completions[f.calls]
	f.calls++
	return completion, nil
}

func (f *fakeToolGenerator) SupportsTools() bool { return true }

func TestRuntimeRoutesToToolLoopWhenSupported(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	generator := &fakeToolGenerator{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", Arguments: `{"path":"README.md"}`}}},
		{Content: "The readme greets the world."},
	}}
	runtime, err := New(root, llm.NewPlanner(generator))
	if err != nil {
		t.Fatal(err)
	}

	var plans []string
	result, err := runtime.SubmitWithOptions(t.Context(), "summarize the readme", SubmitOptions{
		OnPlan: func(steps string) { plans = append(plans, steps) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentResult.Status != agent.StatusCompleted {
		t.Fatalf("expected completed, got %#v", result.AgentResult)
	}
	if result.AgentResult.Summary != "The readme greets the world." {
		t.Fatalf("unexpected summary %q", result.AgentResult.Summary)
	}
	if generator.calls != 2 {
		t.Fatalf("expected two model turns, got %d", generator.calls)
	}
	if len(plans) != 1 || !strings.Contains(plans[0], "read README.md") {
		t.Fatalf("unexpected plan callbacks %#v", plans)
	}
	if result.PlannedSteps != "read README.md" {
		t.Fatalf("unexpected planned steps %q", result.PlannedSteps)
	}
	if len(result.Events) != 1 || result.Events[0].Tool != "read" || result.Events[0].Status != "ok" {
		t.Fatalf("unexpected events %#v", result.Events)
	}
}

func TestRuntimeToolLoopFeedsErrorAndSignalsReplan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	generator := &fakeToolGenerator{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"missing.txt"}`}}},
		{ToolCalls: []llm.ToolCall{{ID: "c2", Name: "read", Arguments: `{"path":"README.md"}`}}},
		{Content: "Recovered and read the readme."},
	}}
	runtime, err := New(root, llm.NewPlanner(generator))
	if err != nil {
		t.Fatal(err)
	}

	var replans []string
	result, err := runtime.SubmitWithOptions(t.Context(), "read the readme", SubmitOptions{
		OnReplan: func(_ int, reason string) { replans = append(replans, reason) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentResult.Status != agent.StatusCompleted {
		t.Fatalf("expected completed, got %#v", result.AgentResult)
	}
	if len(replans) != 1 || !strings.Contains(replans[0], "read") {
		t.Fatalf("unexpected replan signals %#v", replans)
	}
	if len(result.Events) != 2 {
		t.Fatalf("expected failed read plus repaired read, got %#v", result.Events)
	}
	if result.Events[0].Status != "error" {
		t.Fatalf("expected first event to be an error, got %#v", result.Events[0])
	}
}

func TestRuntimeFallsBackToPlannerWhenLoopDisabled(t *testing.T) {
	t.Setenv("LIORA_AGENT_LOOP", "off")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	generator := &fakeToolGenerator{plannerReply: "ANSWER: listing skipped"}
	runtime, err := New(root, llm.NewPlanner(generator))
	if err != nil {
		t.Fatal(err)
	}

	_, err = runtime.Submit(t.Context(), "list .")
	if err != nil {
		t.Fatal(err)
	}
	if generator.calls != 0 {
		t.Fatalf("expected planner path (no tool-loop calls), got %d", generator.calls)
	}
	if generator.plannerCalls == 0 {
		t.Fatal("expected planner Generate to be used when loop disabled")
	}
}
