package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/trace"
)

func TestToolSchedulerBatchesNonConflictingResourcesTogether(t *testing.T) {
	// Given
	calls := []llm.ToolCall{
		{ID: "read-app", Name: "read", Arguments: `{"path":"src/app.go"}`},
		{ID: "write-doc", Name: "write", Arguments: `{"path":"docs/notes.md","content":"notes"}`},
		{ID: "read-readme", Name: "read", Arguments: `{"path":"README.md"}`},
	}

	// When
	batches := scheduleToolBatches(calls)

	// Then
	assertScheduledBatches(t, batches, [][]string{{"read-app", "write-doc", "read-readme"}})
}

func TestToolSchedulerSerializesConflictingPathAccess(t *testing.T) {
	// Given
	calls := []llm.ToolCall{
		{ID: "read-note", Name: "read", Arguments: `{"path":"notes/today.md"}`},
		{ID: "write-note", Name: "write", Arguments: `{"path":"notes/today.md","content":"updated"}`},
		{ID: "read-note-again", Name: "read", Arguments: `{"path":"notes/today.md"}`},
	}

	// When
	batches := scheduleToolBatches(calls)

	// Then
	assertScheduledBatches(t, batches, [][]string{{"read-note"}, {"write-note"}, {"read-note-again"}})
}

func TestToolSchedulerSerializesMultipleWrites(t *testing.T) {
	// Given
	calls := []llm.ToolCall{
		{ID: "write-a", Name: "write", Arguments: `{"path":"a.txt","content":"a"}`},
		{ID: "write-b", Name: "write", Arguments: `{"path":"b.txt","content":"b"}`},
	}

	// When
	batches := scheduleToolBatches(calls)

	// Then
	assertScheduledBatches(t, batches, [][]string{{"write-a"}, {"write-b"}})
}

func TestToolSchedulerTreatsWorkspaceSearchAsConflictingWithWrites(t *testing.T) {
	// Given
	calls := []llm.ToolCall{
		{ID: "search-all", Name: "search", Arguments: `{"query":"TODO"}`},
		{ID: "write-note", Name: "write", Arguments: `{"path":"notes/today.md","content":"updated"}`},
		{ID: "read-doc", Name: "read", Arguments: `{"path":"docs/guide.md"}`},
	}

	// When
	batches := scheduleToolBatches(calls)

	// Then
	assertScheduledBatches(t, batches, [][]string{{"search-all"}, {"write-note", "read-doc"}})
}

func TestToolSchedulerKeepsShellAndExternalToolsExclusive(t *testing.T) {
	// Given
	calls := []llm.ToolCall{
		{ID: "read-before", Name: "read", Arguments: `{"path":"README.md"}`},
		{ID: "shell", Name: "run", Arguments: `{"command":"go test ./..."}`},
		{ID: "read-after", Name: "read", Arguments: `{"path":"docs/guide.md"}`},
		{ID: "task", Name: "Task", Arguments: `{"prompt":"inspect package"}`},
		{ID: "read-after-task", Name: "read", Arguments: `{"path":"docs/task.md"}`},
		{ID: "task-stop", Name: "TaskStop", Arguments: `{"task_id":"task_child","reason":"done"}`},
		{ID: "read-after-stop", Name: "read", Arguments: `{"path":"docs/stop.md"}`},
		{ID: "mcp", Name: "mcp", Arguments: `{"server":"github","tool":"list_issues"}`},
		{ID: "read-final", Name: "read", Arguments: `{"path":"docs/roadmap.md"}`},
	}

	// When
	batches := scheduleToolBatches(calls)

	// Then
	assertScheduledBatches(t, batches, [][]string{{"read-before"}, {"shell"}, {"read-after"}, {"task"}, {"read-after-task"}, {"task-stop"}, {"read-after-stop"}, {"mcp"}, {"read-final"}})
}

func TestToolLoopDispatchesNonConflictingWriteAndReadInOneBatch(t *testing.T) {
	// Given
	root := t.TempDir()
	a := newLoopAgent(t, root)
	todoStarted := make(chan struct{}, 1)
	a.SetSkillReader(blockingSkillReader{todoStarted: todoStarted})
	a.SetTodoExecutor(signalingTodoExecutor{started: todoStarted})
	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{
			{ID: "skill-first", Name: "skill", Arguments: `{"name":"slow"}`},
			{ID: "todo-second", Name: "todo_write", Arguments: `{"todos":[{"content":"parallel todo"}]}`},
		}},
		{Content: "done"},
	}}

	// When
	loop := NewToolLoop(a, caller, LoopOptions{})
	result, err := loop.Run(t.Context(), "read skill and update todos")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %#v", result)
	}
	events := a.recorder.(*trace.MemoryRecorder).Events()
	if len(events) != 2 {
		t.Fatalf("expected two events, got %#v", events)
	}
	if events[0].ToolCallID != "skill-first" || events[1].ToolCallID != "todo-second" {
		t.Fatalf("expected provider-order events, got %#v", events)
	}
	secondTurn := caller.transcripts[1]
	if !toolMessageContains(secondTurn, "skill-first", "slow skill ready") || !toolMessageContains(secondTurn, "todo-second", "parallel todo") {
		t.Fatalf("expected provider-order tool messages, got %#v", secondTurn)
	}
}

func TestToolLoopApprovalStopsMixedBatchBeforeAnyDispatch(t *testing.T) {
	// Given
	root := t.TempDir()
	a := newLoopAgent(t, root)
	skills := &recordingSkillReader{}
	a.SetSkillReader(skills)
	a.SetPermissionChecker(permission.Policy{Mode: permission.ModePrompt})
	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{
			{ID: "skill-first", Name: "skill", Arguments: `{"name":"safe"}`},
			{ID: "write-second", Name: "write", Arguments: `{"path":"notes/today.md","content":"blocked"}`},
		}},
	}}

	// When
	loop := NewToolLoop(a, caller, LoopOptions{})
	result, err := loop.Run(t.Context(), "read skill and write note")

	// Then
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
	if skills.called {
		t.Fatal("expected no tools to run before mixed batch approval")
	}
	if events := a.recorder.(*trace.MemoryRecorder).Events(); len(events) != 0 {
		t.Fatalf("expected no trace events before approval, got %#v", events)
	}
}

func assertScheduledBatches(t *testing.T, batches []toolBatch, want [][]string) {
	t.Helper()
	if len(batches) != len(want) {
		t.Fatalf("expected %d batches, got %d: %#v", len(want), len(batches), batches)
	}
	for i, batch := range batches {
		if len(batch) != len(want[i]) {
			t.Fatalf("expected batch %d to have %d calls, got %d: %#v", i, len(want[i]), len(batch), batch)
		}
		for j, scheduled := range batch {
			if scheduled.call.ID != want[i][j] {
				t.Fatalf("expected batch %d call %d to be %q, got %q", i, j, want[i][j], scheduled.call.ID)
			}
		}
	}
}

type recordingSkillReader struct {
	called bool
}

func (r *recordingSkillReader) ReadSkill(_ string, name string, _ int, _ int) (string, error) {
	r.called = true
	return name + " skill ready", nil
}

type blockingSkillReader struct {
	todoStarted <-chan struct{}
}

func (r blockingSkillReader) ReadSkill(_ string, name string, _ int, _ int) (string, error) {
	if name != "slow" {
		return name + " skill ready", nil
	}
	select {
	case <-r.todoStarted:
		return "slow skill ready", nil
	case <-time.After(2 * time.Second):
		return "slow skill timed out waiting for todo_write", nil
	}
}

type signalingTodoExecutor struct {
	started chan<- struct{}
}

func (e signalingTodoExecutor) WriteTodos(_ context.Context, todos []Todo) ([]Todo, error) {
	select {
	case e.started <- struct{}{}:
	default:
	}
	return todos, nil
}

func (e signalingTodoExecutor) ReadTodos(_ context.Context) ([]Todo, error) {
	return nil, nil
}

func toolMessageContains(messages []llm.Message, toolCallID string, text string) bool {
	for _, message := range messages {
		if message.Role == "tool" && message.ToolCallID == toolCallID && strings.Contains(message.Content, text) {
			return true
		}
	}
	return false
}
