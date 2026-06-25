package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"coding-agent-mvp/internal/tools"
	"coding-agent-mvp/internal/trace"
)

type fakeMCPExecutor struct {
	server string
	tool   string
	args   map[string]any
}

func (f *fakeMCPExecutor) Call(_ context.Context, server string, tool string, args map[string]any) (string, error) {
	f.server = server
	f.tool = tool
	f.args = args
	return "mcp output", nil
}

func TestAgentExecutesScriptedCodingTaskAndRecordsTrace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("hello old agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	workspace, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	runner := New(workspace, recorder)

	result, err := runner.Run(t.Context(), `read app.txt
replace app.txt old new
run grep -q "hello new agent" app.txt
diff`)
	if err != nil {
		t.Fatal(err)
	}

	if result.Status != StatusCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if !strings.Contains(result.Summary, "4 steps") {
		t.Fatalf("expected summary to mention step count, got %q", result.Summary)
	}

	updated, err := os.ReadFile(filepath.Join(root, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "hello new agent\n" {
		t.Fatalf("unexpected updated file %q", string(updated))
	}

	events := recorder.Events()
	if len(events) != 4 {
		t.Fatalf("expected 4 trace events, got %d: %#v", len(events), events)
	}
	if events[1].Tool != "replace" || events[1].Status != trace.StatusOK {
		t.Fatalf("unexpected replace event: %#v", events[1])
	}
	if !strings.Contains(result.Diff, "hello new agent") {
		t.Fatalf("expected diff to include replacement, got %q", result.Diff)
	}
}

func TestAgentStopsOnFailedStep(t *testing.T) {
	root := t.TempDir()
	workspace, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	runner := New(workspace, recorder)

	result, err := runner.Run(t.Context(), `run sh -c "exit 7"
write should-not-exist.txt skipped`)
	if err == nil {
		t.Fatal("expected failed shell step to return error")
	}
	if result.Status != StatusFailed {
		t.Fatalf("expected failed status, got %s", result.Status)
	}
	if _, statErr := os.Stat(filepath.Join(root, "should-not-exist.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("agent continued after failure, stat err: %v", statErr)
	}
	if len(recorder.Events()) != 1 {
		t.Fatalf("expected only failed first step to be recorded, got %#v", recorder.Events())
	}
}

func TestAgentExecutesListTool(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	runner := New(workspace, recorder)

	result, err := runner.Run(t.Context(), "list .")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	if result.Summary != "completed 1 step" {
		t.Fatalf("unexpected summary %q", result.Summary)
	}
	events := recorder.Events()
	if len(events) != 1 || events[0].Tool != "list" || !strings.Contains(events[0].Output, "README.md") {
		t.Fatalf("unexpected events %#v", events)
	}
}

func TestAgentExecutesMCPTool(t *testing.T) {
	root := t.TempDir()
	workspace, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	executor := &fakeMCPExecutor{}
	runner := New(workspace, recorder)
	runner.SetMCP(executor)

	result, err := runner.Run(t.Context(), `mcp fake echo {"text":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	if executor.server != "fake" || executor.tool != "echo" || executor.args["text"] != "hello" {
		t.Fatalf("unexpected MCP call server=%q tool=%q args=%#v", executor.server, executor.tool, executor.args)
	}
	events := recorder.Events()
	if len(events) != 1 || events[0].Tool != "mcp" || !strings.Contains(events[0].Output, "mcp output") {
		t.Fatalf("unexpected MCP trace events %#v", events)
	}
}
