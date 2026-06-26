package agent

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/tools"
	"github.com/Lioooooo123/liora/internal/trace"
)

type fakeMCPExecutor struct {
	server string
	tool   string
	args   map[string]any
}

type fakeShellExecutor struct {
	workspace string
	command   string
}

func (f *fakeMCPExecutor) Call(_ context.Context, server string, tool string, args map[string]any) (string, error) {
	f.server = server
	f.tool = tool
	f.args = args
	return "mcp output", nil
}

func (f *fakeShellExecutor) Run(_ context.Context, workspace string, command string) (tools.ShellResult, error) {
	f.workspace = workspace
	f.command = command
	return tools.ShellResult{Stdout: "sandbox ok\n", ExitCode: 0}, nil
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

func TestAgentRunToolUsesInjectedShellExecutor(t *testing.T) {
	root := t.TempDir()
	workspace, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	executor := &fakeShellExecutor{}
	runner := New(workspace, recorder)
	runner.SetShellExecutor(executor)

	result, err := runner.Run(t.Context(), `run echo hello`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	if executor.workspace != root || executor.command != "echo hello" {
		t.Fatalf("unexpected shell call workspace=%q command=%q", executor.workspace, executor.command)
	}
	events := recorder.Events()
	if len(events) != 1 || !strings.Contains(events[0].Output, "sandbox ok") {
		t.Fatalf("unexpected trace events %#v", events)
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

func TestAgentAcceptsMarkdownPlanAndEscapedSpacePath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Assignment Question.pdf"), []byte("%PDF test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	runner := New(workspace, recorder)

	result, err := runner.Run(t.Context(), `1. list .
- stat Assignment\ Question.pdf`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	events := recorder.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %#v", events)
	}
	if events[1].Tool != "stat" || !strings.Contains(events[1].Output, "Assignment Question.pdf") {
		t.Fatalf("unexpected stat event %#v", events[1])
	}
}

func TestAgentExecutesComplexWorkspaceTools(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	runner := New(workspace, recorder)

	result, err := runner.Run(t.Context(), `glob *.go src
read src/main.go 1 1
stat src/main.go
mkdir docs
append docs/log.txt hello
edit docs/log.txt hello hi
tree . 2
delete docs/log.txt`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	events := recorder.Events()
	if len(events) != 8 {
		t.Fatalf("expected 8 events, got %#v", events)
	}
	if !strings.Contains(events[0].Output, "src/main.go") {
		t.Fatalf("unexpected glob output %#v", events[0])
	}
	if !strings.Contains(events[1].Output, "1\tpackage main") {
		t.Fatalf("unexpected read output %#v", events[1])
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "log.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected file deleted, stat err %v", err)
	}
}

func TestAgentExecutesDocumentTool(t *testing.T) {
	root := t.TempDir()
	writeAgentTestDOCX(t, filepath.Join(root, "assignment.docx"), []string{"Assignment brief", "Explain the architecture"})
	workspace, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	runner := New(workspace, recorder)

	result, err := runner.Run(t.Context(), "document assignment.docx 1 2")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	events := recorder.Events()
	if len(events) != 1 || events[0].Tool != "document" || !strings.Contains(events[0].Output, "Assignment brief") {
		t.Fatalf("unexpected document event %#v", events)
	}
}

func writeAgentTestDOCX(t *testing.T, path string, paragraphs []string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zipWriter := zip.NewWriter(file)
	defer zipWriter.Close()
	writer, err := zipWriter.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, paragraph := range paragraphs {
		builder.WriteString("<w:p><w:r><w:t>")
		builder.WriteString(paragraph)
		builder.WriteString("</w:t></w:r></w:p>")
	}
	builder.WriteString("</w:body></w:document>")
	if _, err := writer.Write([]byte(builder.String())); err != nil {
		t.Fatal(err)
	}
}

func TestAgentParsesEscapedAndQuotedPathArguments(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "assignment question.pdf"), []byte("pdf placeholder\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "course notes.txt"), []byte("notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	runner := New(workspace, recorder)

	result, err := runner.Run(t.Context(), `stat assignment\ question.pdf
stat "course notes.txt"`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	events := recorder.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %#v", events)
	}
	for _, event := range events {
		if event.Status != trace.StatusOK || !strings.Contains(event.Output, "size=") {
			t.Fatalf("unexpected stat event %#v", event)
		}
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

func TestAgentStopsBeforeStepThatRequiresApproval(t *testing.T) {
	root := t.TempDir()
	workspace, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	recorder := trace.NewMemoryRecorder()
	runner := New(workspace, recorder)
	runner.SetPermissionChecker(permission.Policy{Mode: permission.ModePrompt})

	result, err := runner.Run(t.Context(), `run rm -rf build
write should-not-exist.txt skipped`)
	if err == nil {
		t.Fatal("expected approval error")
	}
	if result.Status != StatusWaitingUser {
		t.Fatalf("expected waiting status, got %#v", result)
	}
	if len(recorder.Events()) != 0 {
		t.Fatalf("expected no tools to run before approval, got %#v", recorder.Events())
	}
	if _, statErr := os.Stat(filepath.Join(root, "should-not-exist.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("agent continued after approval request, stat err: %v", statErr)
	}
}
