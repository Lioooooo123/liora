package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
)

func TestMain(m *testing.M) {
	if os.Getenv("LIORA_RUNTIME_FAKE_MCP_SERVER") == "1" {
		runRuntimeFakeMCPServer()
		return
	}
	os.Exit(m.Run())
}

type fakeGenerator struct {
	response    string
	responses   []string
	messages    []llm.Message
	allMessages [][]llm.Message
}

func (f *fakeGenerator) Generate(_ context.Context, messages []llm.Message) (string, error) {
	f.messages = append([]llm.Message(nil), messages...)
	f.allMessages = append(f.allMessages, append([]llm.Message(nil), messages...))
	if len(f.responses) > 0 {
		response := f.responses[0]
		f.responses = f.responses[1:]
		return response, nil
	}
	return f.response, nil
}

func TestTurnRuntimeReturnsDirectAnswerWithoutTools(t *testing.T) {
	root := t.TempDir()
	runtime, err := New(root, llm.NewPlanner(&fakeGenerator{response: "ANSWER: 你好，我是 Liora。"}))
	if err != nil {
		t.Fatal(err)
	}

	result, err := runtime.Submit(t.Context(), "你好")
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "你好，我是 Liora。" {
		t.Fatalf("unexpected answer %q", result.Answer)
	}
	if result.PlannedSteps != "" || len(result.Events) != 0 {
		t.Fatalf("expected no tool execution, got %#v", result)
	}
}

func TestTurnRuntimeExecutesPlannedTools(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := New(root, llm.NewPlanner(&fakeGenerator{response: "list ."}))
	if err != nil {
		t.Fatal(err)
	}

	result, err := runtime.Submit(t.Context(), "看看目录")
	if err != nil {
		t.Fatal(err)
	}
	if result.PlannedSteps != "list ." {
		t.Fatalf("unexpected planned steps %q", result.PlannedSteps)
	}
	if len(result.Events) != 1 || result.Events[0].Tool != "list" {
		t.Fatalf("unexpected events %#v", result.Events)
	}
	if !strings.Contains(result.Events[0].Output, "README.md") {
		t.Fatalf("expected list output, got %#v", result.Events[0])
	}
}

func TestTurnRuntimeReplansAfterToolFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	generator := &fakeGenerator{responses: []string{
		"read missing.txt",
		"list .\nread app.txt",
	}}
	runtime, err := New(root, llm.NewPlanner(generator))
	if err != nil {
		t.Fatal(err)
	}
	var plans []string
	var replans []string

	result, err := runtime.SubmitWithOptions(t.Context(), "看看 app 文件", SubmitOptions{
		OnPlan: func(steps string) {
			plans = append(plans, steps)
		},
		OnReplan: func(_ int, reason string) {
			replans = append(replans, reason)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentResult.Status != agent.StatusCompleted {
		t.Fatalf("expected completed, got %#v", result.AgentResult)
	}
	if len(plans) != 2 || plans[0] != "read missing.txt" || plans[1] != "list .\nread app.txt" {
		t.Fatalf("unexpected plan callbacks %#v", plans)
	}
	if len(replans) != 1 || !strings.Contains(replans[0], "missing.txt") {
		t.Fatalf("unexpected replan callbacks %#v", replans)
	}
	if !strings.Contains(result.PlannedSteps, "# Replan 1") || !strings.Contains(result.PlannedSteps, "read app.txt") {
		t.Fatalf("unexpected planned steps:\n%s", result.PlannedSteps)
	}
	if len(result.Events) != 3 {
		t.Fatalf("expected failed read plus two repaired events, got %#v", result.Events)
	}
	if result.Events[0].Status != "error" || result.Events[0].Tool != "read" {
		t.Fatalf("expected first event to be failed read, got %#v", result.Events[0])
	}
	if len(generator.allMessages) != 2 {
		t.Fatalf("expected initial plan and replan calls, got %d", len(generator.allMessages))
	}
	if !strings.Contains(generator.allMessages[1][1].Content, "read missing.txt") ||
		!strings.Contains(generator.allMessages[1][1].Content, "no such file") {
		t.Fatalf("expected replan prompt to include failure context:\n%s", generator.allMessages[1][1].Content)
	}
}

func TestTurnRuntimeReplanAnswerDoesNotReturnPreviousToolError(t *testing.T) {
	root := t.TempDir()
	generator := &fakeGenerator{responses: []string{
		"read missing.txt",
		"ANSWER: 我没找到这个文件。",
	}}
	runtime, err := New(root, llm.NewPlanner(generator))
	if err != nil {
		t.Fatal(err)
	}

	result, err := runtime.Submit(t.Context(), "看看缺失文件")
	if err != nil {
		t.Fatalf("replan direct answer should not return previous tool error: %v", err)
	}
	if result.Answer != "我没找到这个文件。" {
		t.Fatalf("unexpected answer %q", result.Answer)
	}
	if len(result.Events) != 1 || result.Events[0].Tool != "read" || result.Events[0].Status != "error" {
		t.Fatalf("expected failed tool event to remain in transcript, got %#v", result.Events)
	}
}

func TestRuntimeHandlesGoalAndMemoryCommands(t *testing.T) {
	root := t.TempDir()
	storeRoot := t.TempDir()
	runtime, err := New(root, llm.NewPlanner(&fakeGenerator{response: "ANSWER: unused"}), store.New(storeRoot))
	if err != nil {
		t.Fatal(err)
	}

	out, handled, err := runtime.HandleCommand(t.Context(), "/goal set ship the mvp")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "ship the mvp") {
		t.Fatalf("unexpected goal set output handled=%v out=%q", handled, out)
	}
	out, handled, err = runtime.HandleCommand(t.Context(), "/goal show")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "ship the mvp") {
		t.Fatalf("unexpected goal show output handled=%v out=%q", handled, out)
	}
	out, handled, err = runtime.HandleCommand(t.Context(), "/memory add prefer stable tui")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "saved") {
		t.Fatalf("unexpected memory add output handled=%v out=%q", handled, out)
	}
	out, handled, err = runtime.HandleCommand(t.Context(), "/memory search tui")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "prefer stable tui") {
		t.Fatalf("unexpected memory search output handled=%v out=%q", handled, out)
	}
}

func TestRuntimeListsBuiltinToolsCommand(t *testing.T) {
	root := t.TempDir()
	runtime, err := New(root, llm.NewPlanner(&fakeGenerator{response: "ANSWER: unused"}))
	if err != nil {
		t.Fatal(err)
	}

	out, handled, err := runtime.HandleCommand(t.Context(), "/tools")
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected /tools to be handled")
	}
	for _, want := range []string{"read <path>", "document <path>", "run <shell command>", "mcp <server>", "read_only", "shell"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected /tools output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRuntimeListsSkillsCommand(t *testing.T) {
	root := t.TempDir()
	storeRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".liora", "skills", "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".liora", "skills", "tests", "SKILL.md"), []byte("# Test Skill\nGenerate tests"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := New(root, llm.NewPlanner(&fakeGenerator{response: "ANSWER: unused"}), store.New(storeRoot))
	if err != nil {
		t.Fatal(err)
	}

	out, handled, err := runtime.HandleCommand(t.Context(), "/skills")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "tests") || !strings.Contains(out, "Test Skill") {
		t.Fatalf("unexpected skills output handled=%v out=%q", handled, out)
	}

	out, handled, err = runtime.HandleCommand(t.Context(), "/skill tests")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "Test Skill") || !strings.Contains(out, "Generate tests") {
		t.Fatalf("unexpected skill output handled=%v out=%q", handled, out)
	}
}

func TestRuntimeInjectsPersistentContextIntoPlanner(t *testing.T) {
	root := t.TempDir()
	storeRoot := t.TempDir()
	s := store.New(storeRoot)
	if err := s.SetGoal("support MCP"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMemory("remember concise output"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".liora", "skills", "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".liora", "skills", "tests", "SKILL.md"), []byte("# Test Skill\nGenerate tests"), 0o600); err != nil {
		t.Fatal(err)
	}
	generator := &fakeGenerator{response: "ANSWER: ok"}
	runtime, err := New(root, llm.NewPlanner(generator), s)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.Submit(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	if len(generator.messages) != 2 {
		t.Fatalf("unexpected messages %#v", generator.messages)
	}
	userPrompt := generator.messages[1].Content
	for _, want := range []string{"Current goal: support MCP", "remember concise output", "tests: Test Skill", "Generate tests"} {
		if !strings.Contains(userPrompt, want) {
			t.Fatalf("expected planner context to contain %q, got:\n%s", want, userPrompt)
		}
	}
}

func TestRuntimeMCPCommandListsTools(t *testing.T) {
	root := t.TempDir()
	storeRoot := t.TempDir()
	s := store.New(storeRoot)
	if err := s.SaveMCPConfig(store.MCPConfig{Servers: map[string]store.MCPServerConfig{
		"fake": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestRuntimeMCPCommandListsTools"},
			Env:     map[string]string{"LIORA_RUNTIME_FAKE_MCP_SERVER": "1"},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	runtime, err := New(root, llm.NewPlanner(&fakeGenerator{response: "ANSWER: unused"}), s)
	if err != nil {
		t.Fatal(err)
	}

	out, handled, err := runtime.HandleCommand(t.Context(), "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "fake/echo") {
		t.Fatalf("unexpected mcp output handled=%v out=%q", handled, out)
	}
}

func runRuntimeFakeMCPServer() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		method, _ := req["method"].(string)
		id := req["id"]
		if method == "notifications/initialized" {
			continue
		}
		switch method {
		case "initialize":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": "2025-06-18",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "fake", "version": "0.0.1"},
				},
			})
		case "tools/list":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "echo",
						"description": "Echo text",
						"inputSchema": map[string]any{"type": "object"},
					}},
				},
			})
		case "tools/call":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "ok"}},
				},
			})
		default:
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"error":   map[string]any{"code": -32601, "message": fmt.Sprintf("unknown %s", method)},
			})
		}
	}
	os.Exit(0)
}
