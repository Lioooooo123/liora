package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/trace"
)

type fakeTaskExecutor struct {
	started []TaskRequest
	outputs []TaskOutputRequest
	stopped []TaskStopRequest
}

func (f *fakeTaskExecutor) StartTask(_ context.Context, request TaskRequest) (TaskResult, error) {
	f.started = append(f.started, request)
	return TaskResult{TaskID: "task_child", Status: "running"}, nil
}

func (f *fakeTaskExecutor) ReadTaskOutput(_ context.Context, request TaskOutputRequest) (TaskOutputResult, error) {
	f.outputs = append(f.outputs, request)
	return TaskOutputResult{TaskID: request.TaskID, Status: "completed", Output: "child ok"}, nil
}

func (f *fakeTaskExecutor) StopTask(_ context.Context, request TaskStopRequest) (TaskStopResult, error) {
	f.stopped = append(f.stopped, request)
	return TaskStopResult{TaskID: request.TaskID, Status: "cancelled"}, nil
}

func TestToolLoopPassesTaskControlSchemasToModel(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)
	caller := &fakeToolCaller{completions: []llm.Completion{{Content: "nothing to do"}}}
	loop := NewToolLoop(a, caller, LoopOptions{})
	if _, err := loop.Run(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}

	var sawTask, sawTaskOutput, sawTaskStop bool
	for _, schema := range caller.lastTools {
		if schema.Name == "Task" && schema.Parameters["type"] == "object" {
			sawTask = true
		}
		if schema.Name == "TaskOutput" && schema.Parameters["type"] == "object" {
			sawTaskOutput = true
		}
		if schema.Name == "TaskStop" && schema.Parameters["type"] == "object" {
			sawTaskStop = true
		}
	}
	if !sawTask || !sawTaskOutput || !sawTaskStop {
		t.Fatalf("expected task-control schemas, saw Task=%t TaskOutput=%t TaskStop=%t tools=%#v", sawTask, sawTaskOutput, sawTaskStop, caller.lastTools)
	}
}

func TestToolLoopRoutesTaskControlToolsThroughExecutor(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)
	executor := &fakeTaskExecutor{}
	a.SetTaskExecutor(executor)
	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{
			{ID: "task_call", Name: "Task", Arguments: `{"prompt":"inspect package","subagent_name":"explorer","role":"search","scope":{"paths":["."],"network_hosts":["api.example.com"],"mcp_servers":["docs"],"mcp_tools":["docs.search"],"approval_actions":["read"]}}`},
		}},
		{ToolCalls: []llm.ToolCall{
			{ID: "output_call", Name: "TaskOutput", Arguments: `{"task_id":"task_child","wait_ms":100,"limit":1200}`},
		}},
		{ToolCalls: []llm.ToolCall{
			{ID: "stop_call", Name: "TaskStop", Arguments: `{"task_id":"task_child","reason":"done"}`},
		}},
		{Content: "Child task handled."},
	}}

	loop := NewToolLoop(a, caller, LoopOptions{})
	result, err := loop.Run(t.Context(), "delegate the inspection")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	if len(executor.started) != 1 || executor.started[0].Prompt != "inspect package" || executor.started[0].SubagentName != "explorer" || executor.started[0].Role != "search" {
		t.Fatalf("unexpected start requests %#v", executor.started)
	}
	if got := executor.started[0].Scope.Paths; len(got) != 1 || got[0] != "." {
		t.Fatalf("unexpected scope paths %#v", got)
	}
	if len(executor.outputs) != 1 || executor.outputs[0].TaskID != "task_child" || executor.outputs[0].WaitMilliseconds != 100 || executor.outputs[0].Limit != 1200 {
		t.Fatalf("unexpected output requests %#v", executor.outputs)
	}
	if len(executor.stopped) != 1 || executor.stopped[0].TaskID != "task_child" || executor.stopped[0].Reason != "done" {
		t.Fatalf("unexpected stop requests %#v", executor.stopped)
	}
	events := a.recorder.(*trace.MemoryRecorder).Events()
	if len(events) != 3 {
		t.Fatalf("expected task tool events, got %#v", events)
	}
	for _, want := range []string{"task_id: task_child", "status: completed", "status: cancelled"} {
		var found bool
		for _, event := range events {
			if strings.Contains(event.Output, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected event output to contain %q, got %#v", want, events)
		}
	}
}

func TestTaskOutputRejectsMalformedWaitAndLimit(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)
	executor := &fakeTaskExecutor{}
	a.SetTaskExecutor(executor)

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "negative wait",
			args: map[string]any{"task_id": "task_child", "wait_ms": -1},
			want: "wait_ms must be non-negative",
		},
		{
			name: "fractional wait",
			args: map[string]any{"task_id": "task_child", "wait_ms": 1.5},
			want: "wait_ms must be an integer",
		},
		{
			name: "non numeric wait",
			args: map[string]any{"task_id": "task_child", "wait_ms": "soon"},
			want: "wait_ms must be an integer",
		},
		{
			name: "negative limit",
			args: map[string]any{"task_id": "task_child", "limit": -10},
			want: "limit must be non-negative",
		},
		{
			name: "wrong limit type",
			args: map[string]any{"task_id": "task_child", "limit": true},
			want: "limit must be an integer",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.executeTaskOutput(t.Context(), tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
			if len(executor.outputs) != 0 {
				t.Fatalf("invalid TaskOutput args should not call executor, got %#v", executor.outputs)
			}
		})
	}
}

func TestToolLoopTaskToolsFailClosedWithoutExecutor(t *testing.T) {
	root := t.TempDir()
	a := newLoopAgent(t, root)
	caller := &fakeToolCaller{completions: []llm.Completion{
		{ToolCalls: []llm.ToolCall{{ID: "task_call", Name: "Task", Arguments: `{"prompt":"inspect"}`}}},
		{Content: "done"},
	}}

	loop := NewToolLoop(a, caller, LoopOptions{})
	result, err := loop.Run(t.Context(), "delegate")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed after model saw tool error, got %#v", result)
	}
	events := a.recorder.(*trace.MemoryRecorder).Events()
	if len(events) != 1 || events[0].Status != trace.StatusError || !strings.Contains(events[0].Output, "no task executor configured") {
		t.Fatalf("expected fail-closed task tool error, got %#v", events)
	}
}
