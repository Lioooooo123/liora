package agent

import (
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/trace"
)

func TestToolLoopExecutesNativeSkillTool(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)
	reader := &fakeSkillReader{}
	a.SetSkillReader(reader)

	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "s1", Name: "skill", Arguments: `{"name":"review","start_line":2,"line_count":2}`}}},
		{Content: "Loaded review skill."},
	}}

	var planned string
	loop := NewToolLoop(a, caller, LoopOptions{OnPlan: func(steps string) { planned = steps }})
	result, err := loop.Run(t.Context(), "load the review skill")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	if reader.workspaceRoot != root || reader.name != "review" || reader.startLine != 2 || reader.lineCount != 2 {
		t.Fatalf("unexpected skill call root=%q name=%q start=%d count=%d", reader.workspaceRoot, reader.name, reader.startLine, reader.lineCount)
	}
	if !strings.Contains(planned, "skill review") {
		t.Fatalf("expected rendered plan to include skill name, got %q", planned)
	}
	events := a.recorder.(*trace.MemoryRecorder).Events()
	if len(events) != 1 || events[0].Tool != "skill" || !strings.Contains(events[0].Output, "table-driven tests") {
		t.Fatalf("unexpected skill events %#v", events)
	}
}

func TestToolLoopSkillToolFailsWithoutReader(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)

	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "s1", Name: "skill", Arguments: `{"name":"review"}`}}},
	}}
	loop := NewToolLoop(a, caller, LoopOptions{MaxTurns: 1})

	result, err := loop.Run(t.Context(), "load the review skill")
	if err == nil || !strings.Contains(err.Error(), "tool loop exceeded 1 turns") {
		t.Fatalf("expected one-turn loop limit after tool error, got result=%#v err=%v", result, err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("expected failed status, got %#v", result)
	}
	events := a.recorder.(*trace.MemoryRecorder).Events()
	if len(events) != 1 || events[0].Tool != "skill" || events[0].Status != trace.StatusError {
		t.Fatalf("unexpected skill error events %#v", events)
	}
	if !strings.Contains(events[0].Output, "no skill reader configured") {
		t.Fatalf("expected missing reader output, got %#v", events[0])
	}
}
