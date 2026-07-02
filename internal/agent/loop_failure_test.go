package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/llm"
)

func TestToolLoopStopsOnRepeatedIdenticalFailingToolCall(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)

	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"missing.txt"}`}}},
		{ToolCalls: []llm.ToolCall{{ID: "c2", Name: "read", Arguments: `{"path":"missing.txt"}`}}},
		{Content: "This should not be reached."},
	}}

	loop := NewToolLoop(a, caller, LoopOptions{})
	result, err := loop.Run(t.Context(), "read the missing file")
	if err == nil {
		t.Fatalf("expected repeated failing tool call error, got result=%#v", result)
	}
	if result.Status != StatusFailed {
		t.Fatalf("expected failed status, got %#v", result)
	}
	if !strings.Contains(result.Summary, "repeated failing tool call") {
		t.Fatalf("expected summary to name repeated failing tool call, got %q", result.Summary)
	}
	if !strings.Contains(err.Error(), "read") || !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("expected error to mention repeated read missing.txt call, got %v", err)
	}
	if caller.calls != 2 {
		t.Fatalf("expected loop to stop after repeated failure before third completion, got %d model calls", caller.calls)
	}
}

func TestToolLoopAllowsRepeatedShellCommandWhenFailureOutputChanges(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)
	command := `n=$(cat count.txt 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" > count.txt; echo "failure-$n"; exit 1`
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}

	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run", Arguments: string(args)}}},
		{ToolCalls: []llm.ToolCall{{ID: "c2", Name: "run", Arguments: string(args)}}},
		{Content: "Saw two different failures and stopped."},
	}}

	loop := NewToolLoop(a, caller, LoopOptions{})
	result, err := loop.Run(t.Context(), "run the flaky command twice")
	if err != nil {
		t.Fatalf("expected changed failure output to allow model repair, got %v", err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed status, got %#v", result)
	}
	if caller.calls != 3 {
		t.Fatalf("expected third model completion after two different failures, got %d calls", caller.calls)
	}
}

func TestToolLoopStopsOnRepeatedLargeFailingToolOutput(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)
	command := `awk 'BEGIN { for (i = 0; i < 60000; i++) printf "x"; exit 1 }'`
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}

	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run", Arguments: string(args)}}},
		{ToolCalls: []llm.ToolCall{{ID: "c2", Name: "run", Arguments: string(args)}}},
		{Content: "This should not be reached."},
	}}

	loop := NewToolLoop(a, caller, LoopOptions{})
	result, err := loop.Run(t.Context(), "repeat a large failing command")
	if err == nil {
		t.Fatalf("expected repeated large failing tool call error, got result=%#v", result)
	}
	if result.Status != StatusFailed {
		t.Fatalf("expected failed status, got %#v", result)
	}
	if caller.calls != 2 {
		t.Fatalf("expected loop to stop after second large failure, got %d model calls", caller.calls)
	}

	firstMessage := modelToolMessage(caller.transcripts[1], "c1")
	if !strings.Contains(firstMessage, "output_path: .liora/tool-results/") {
		t.Fatalf("expected first large failure to be persisted, got %q", firstMessage)
	}
}
