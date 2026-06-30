package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/trace"
)

type fakeLoopMCP struct {
	output string
}

func (f fakeLoopMCP) Call(_ context.Context, _ string, _ string, _ map[string]any) (string, error) {
	return f.output, nil
}

func TestToolLoopPersistsLargeToolOutputForModel(t *testing.T) {
	root := t.TempDir()
	largeOutput := strings.Repeat("x", 60_000) + "tail survives"
	a := newLoopAgent(t, root)
	a.SetMCP(fakeLoopMCP{output: largeOutput})

	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "call_big", Name: "mcp", Arguments: `{"server":"fake","tool":"large","arguments":{}}`}}},
		{Content: "Handled large output."},
	}}

	loop := NewToolLoop(a, caller, LoopOptions{})
	result, err := loop.Run(t.Context(), "call a noisy tool")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}

	toolMessage := modelToolMessage(caller.transcripts[1], "call_big")
	if !strings.Contains(toolMessage, "output_path: .liora/tool-results/") {
		t.Fatalf("expected output_path in model-facing tool result, got %q", toolMessage)
	}
	if strings.Contains(toolMessage, "tail survives") {
		t.Fatalf("expected only preview in model-facing result, got tail in %q", toolMessage)
	}
	outputPath := outputPathFromMessage(t, toolMessage)
	data, err := os.ReadFile(filepath.Join(root, outputPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != largeOutput {
		t.Fatalf("persisted output mismatch")
	}

	events := a.recorder.(*trace.MemoryRecorder).Events()
	if len(events) != 1 || !strings.Contains(events[0].Output, "output_path: "+outputPath) {
		t.Fatalf("expected event output to point at persisted output, got %#v", events)
	}
}

func modelToolMessage(messages []llm.Message, toolCallID string) string {
	for _, message := range messages {
		if message.Role == "tool" && message.ToolCallID == toolCallID {
			return message.Content
		}
	}
	return ""
}

func outputPathFromMessage(t *testing.T, message string) string {
	t.Helper()
	for _, line := range strings.Split(message, "\n") {
		if path, ok := strings.CutPrefix(line, "output_path: "); ok {
			return path
		}
	}
	t.Fatalf("missing output_path in %q", message)
	return ""
}
