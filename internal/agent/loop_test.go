package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/tools"
	"github.com/Lioooooo123/liora/internal/trace"
)

// fakeToolCaller drives the loop with a scripted sequence of completions. Each
// call to GenerateWithTools returns the next completion and records the messages
// it received so tests can assert the transcript fed back to the model.
type fakeToolCaller struct {
	completions []llm.Completion
	calls       int
	lastTools   []llm.ToolSchema
	transcripts [][]llm.Message
}

func (f *fakeToolCaller) GenerateWithTools(_ context.Context, messages []llm.Message, schemas []llm.ToolSchema) (llm.Completion, error) {
	snapshot := make([]llm.Message, len(messages))
	copy(snapshot, messages)
	f.transcripts = append(f.transcripts, snapshot)
	f.lastTools = schemas
	completion := f.completions[f.calls]
	f.calls++
	return completion, nil
}

func newLoopAgent(t *testing.T, root string) *Agent {
	t.Helper()
	workspace, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	return New(workspace, trace.NewMemoryRecorder())
}

func TestToolLoopRunsObserveActUntilNoToolCalls(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newLoopAgent(t, root)

	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", Arguments: `{"path":"README.md"}`}}},
		{Content: "Read the readme; it greets the world."},
	}}

	var planned string
	loop := NewToolLoop(a, caller, LoopOptions{OnPlan: func(steps string) { planned = steps }})
	result, err := loop.Run(t.Context(), "summarize the readme")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	if result.Summary != "Read the readme; it greets the world." {
		t.Fatalf("unexpected summary %q", result.Summary)
	}
	if caller.calls != 2 {
		t.Fatalf("expected 2 model calls, got %d", caller.calls)
	}
	if !strings.Contains(planned, "read README.md") {
		t.Fatalf("expected plan to render first tool call, got %q", planned)
	}

	// Second model call should see assistant tool_calls and the tool result.
	second := caller.transcripts[1]
	var sawAssistant, sawToolResult bool
	for _, message := range second {
		if message.Role == "assistant" && len(message.ToolCalls) == 1 && message.ToolCalls[0].ID == "call_1" {
			sawAssistant = true
		}
		if message.Role == "tool" && message.ToolCallID == "call_1" && strings.Contains(message.Content, "hello") {
			sawToolResult = true
		}
	}
	if !sawAssistant || !sawToolResult {
		t.Fatalf("expected transcript to carry assistant tool_calls and tool result, got %#v", second)
	}

	events := a.recorder.(*trace.MemoryRecorder).Events()
	if len(events) != 1 || events[0].Tool != "read" || events[0].Status != trace.StatusOK {
		t.Fatalf("unexpected events %#v", events)
	}
}

func TestToolLoopFeedsToolErrorBackAndSelfRepairs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("real content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newLoopAgent(t, root)

	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"missing.txt"}`}}},
		{ToolCalls: []llm.ToolCall{{ID: "c2", Name: "read", Arguments: `{"path":"README.md"}`}}},
		{Content: "Recovered and read README.md."},
	}}

	var replanCalls []string
	loop := NewToolLoop(a, caller, LoopOptions{
		OnReplan: func(attempt int, reason string) { replanCalls = append(replanCalls, reason) },
	})
	result, err := loop.Run(t.Context(), "read the file")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	if len(replanCalls) != 1 {
		t.Fatalf("expected one replan signal, got %#v", replanCalls)
	}
	if !strings.Contains(replanCalls[0], "read") {
		t.Fatalf("expected replan reason to mention failing tool, got %q", replanCalls[0])
	}

	events := a.recorder.(*trace.MemoryRecorder).Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 tool events, got %#v", events)
	}
	if events[0].Status != trace.StatusError {
		t.Fatalf("expected first read to be recorded as error, got %#v", events[0])
	}
	if events[1].Status != trace.StatusOK {
		t.Fatalf("expected second read to succeed, got %#v", events[1])
	}

	// The error completion's tool message should carry the failure and is_error.
	second := caller.transcripts[1]
	var sawError bool
	for _, message := range second {
		if message.Role == "tool" && message.ToolCallID == "c1" && message.ToolError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatalf("expected tool error message fed back, got %#v", second)
	}
}

func TestToolLoopWritesFileAndReportsDiff(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)

	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "w1", Name: "write", Arguments: `{"path":"note.txt","content":"hi there\n"}`}}},
		{Content: "Created note.txt."},
	}}

	loop := NewToolLoop(a, caller, LoopOptions{})
	result, err := loop.Run(t.Context(), "create a note")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	data, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hi there\n" {
		t.Fatalf("unexpected file content %q", string(data))
	}
}

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

func TestToolLoopStopsForApproval(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)
	a.SetPermissionChecker(permission.Policy{Mode: permission.ModePrompt})

	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "d1", Name: "run", Arguments: `{"command":"rm -rf build"}`}}},
	}}

	loop := NewToolLoop(a, caller, LoopOptions{})
	result, err := loop.Run(t.Context(), "delete build")
	if err == nil {
		t.Fatal("expected approval error")
	}
	var required *permission.RequiredError
	if !errors.As(err, &required) {
		t.Fatalf("expected RequiredError, got %v", err)
	}
	if result.Status != StatusWaitingUser {
		t.Fatalf("expected waiting status, got %#v", result)
	}
	if len(a.recorder.(*trace.MemoryRecorder).Events()) != 0 {
		t.Fatalf("expected no tools to run before approval, got %#v", a.recorder.(*trace.MemoryRecorder).Events())
	}
}

func TestToolLoopStopsAtMaxTurns(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)

	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "l1", Name: "list", Arguments: `{"path":"."}`}}},
		{ToolCalls: []llm.ToolCall{{ID: "l2", Name: "list", Arguments: `{"path":"."}`}}},
		{ToolCalls: []llm.ToolCall{{ID: "l3", Name: "list", Arguments: `{"path":"."}`}}},
	}}

	loop := NewToolLoop(a, caller, LoopOptions{MaxTurns: 2})
	result, err := loop.Run(t.Context(), "keep listing")
	if err == nil {
		t.Fatal("expected max turns error")
	}
	if result.Status != StatusFailed {
		t.Fatalf("expected failed status, got %#v", result)
	}
	if !strings.Contains(result.Summary, "2 turns") {
		t.Fatalf("expected summary to mention turn cap, got %q", result.Summary)
	}
}

func TestToolLoopPassesSchemasToModel(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)
	caller := &fakeToolCaller{completions: []llm.Completion{{Content: "nothing to do"}}}
	loop := NewToolLoop(a, caller, LoopOptions{})
	if _, err := loop.Run(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	if len(caller.lastTools) == 0 {
		t.Fatal("expected schemas to be passed to the model")
	}
	var sawRead bool
	for _, schema := range caller.lastTools {
		if schema.Name == "read" && schema.Parameters["type"] == "object" {
			sawRead = true
		}
	}
	if !sawRead {
		t.Fatalf("expected read schema with object type, got %#v", caller.lastTools)
	}
}
