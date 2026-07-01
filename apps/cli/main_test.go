package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeToolStep is one structured tool call the stateful fake LLM should request,
// in order, before it finishes the tool-use loop with a plain-text summary.
type fakeToolStep struct {
	name string
	args string
}

// toolLoopHandler returns an OpenAI Chat Completions handler that mimics a model
// driving the native tool-use loop. It counts how many tool results the request
// already carries (one per completed step) and returns the next tool call, or the
// final natural-language summary once every step has run. This mirrors how Claude
// Code / Kimi Code style loops terminate on "no more tool_calls" rather than a
// provider stop_reason.
func toolLoopHandler(final string, steps ...fakeToolStep) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		completed := bytes.Count(body, []byte(`"role":"tool"`))
		w.Header().Set("Content-Type", "application/json")
		if completed >= len(steps) {
			payload, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"role": "assistant", "content": final}},
				},
			})
			_, _ = w.Write(payload)
			return
		}
		step := steps[completed]
		payload, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]any{
						{
							"id":   "call_" + strconv.Itoa(completed+1),
							"type": "function",
							"function": map[string]any{
								"name":      step.name,
								"arguments": step.args,
							},
						},
					},
				}},
			},
		})
		_, _ = w.Write(payload)
	}
}

func TestCLIDoctorReportsAnthropicConfigWithoutSecret(t *testing.T) {
	// Given
	cmd := exec.Command("go", "run", ".", "-doctor")
	cmd.Env = cleanLLMEnv(t,
		"LIORA_LLM_PROVIDER=anthropic",
		"LIORA_LLM_API_KEY=test-secret",
		"LIORA_LLM_MODEL=claude-test",
	)

	// When
	output, err := cmd.CombinedOutput()

	// Then
	if err != nil {
		t.Fatalf("doctor command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{
		"provider: anthropic",
		"display: Anthropic",
		"model: claude-test",
		"base_url: https://api.anthropic.com/v1",
		"api_key: configured",
		"tools: supported",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected doctor output to contain %q, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "test-secret") {
		t.Fatalf("doctor output leaked API key:\n%s", rendered)
	}
}

func TestCLIDoctorReportsGeminiMissingKeyWithoutFailing(t *testing.T) {
	// Given
	cmd := exec.Command("go", "run", ".", "-doctor")
	cmd.Env = cleanLLMEnv(t,
		"LIORA_LLM_PROVIDER=gemini",
		"LIORA_LLM_MODEL=gemini-test",
	)

	// When
	output, err := cmd.CombinedOutput()

	// Then
	if err != nil {
		t.Fatalf("doctor command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{
		"provider: gemini",
		"display: Gemini",
		"model: gemini-test",
		"base_url: https://generativelanguage.googleapis.com",
		"api_key: missing",
		"tools: unsupported",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected doctor output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestCLIDoctorIgnoresLegacyBaseURL_whenNamespacedProviderIsSet(t *testing.T) {
	// Given
	cmd := exec.Command("go", "run", ".", "-doctor")
	cmd.Env = cleanLLMEnv(t,
		"LIORA_LLM_PROVIDER=anthropic",
		"LIORA_LLM_API_KEY=test-secret",
		"LIORA_LLM_MODEL=claude-test",
		"OPENAI_BASE_URL=https://api.deepseek.com",
	)

	// When
	output, err := cmd.CombinedOutput()

	// Then
	if err != nil {
		t.Fatalf("doctor command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	if !strings.Contains(rendered, "base_url: https://api.anthropic.com/v1") {
		t.Fatalf("expected namespaced provider to use its default base URL, got:\n%s", rendered)
	}
}

func TestCLIInteractiveDoctorCommandReportsConfigWithoutSecret(t *testing.T) {
	// Given
	workspace := t.TempDir()
	cmd := exec.Command("go", "run", ".", "-interactive", "-workspace", workspace)
	cmd.Env = cleanLLMEnv(t,
		"LIORA_LLM_PROVIDER=anthropic",
		"LIORA_LLM_API_KEY=test-secret",
		"LIORA_LLM_MODEL=claude-test",
	)
	cmd.Stdin = strings.NewReader("/doctor\n/exit\n")

	// When
	output, err := cmd.CombinedOutput()

	// Then
	if err != nil {
		t.Fatalf("interactive doctor command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{
		"Liora doctor",
		"workspace: " + workspace,
		"core: embedded daemon",
		"safety: patch-first",
		"provider: anthropic",
		"display: Anthropic",
		"model: claude-test",
		"api_key: configured",
		"tools: supported",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected interactive doctor output to contain %q, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "test-secret") {
		t.Fatalf("interactive doctor output leaked API key:\n%s", rendered)
	}
}

func cleanLLMEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	blocked := map[string]bool{
		"LIORA_LLM_PROVIDER": true,
		"LIORA_LLM_BASE_URL": true,
		"LIORA_LLM_API_KEY":  true,
		"LIORA_LLM_MODEL":    true,
		"OPENAI_PROVIDER":    true,
		"OPENAI_BASE_URL":    true,
		"OPENAI_API_KEY":     true,
		"OPENAI_MODEL":       true,
	}
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if ok && blocked[name] {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "HOME="+t.TempDir())
	return append(env, extra...)
}

func TestCLINaturalModeUsesLLMPlan(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "app.txt"), []byte("hello old agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [
				{"message": {"role": "assistant", "content": "read app.txt\nreplace app.txt old new\nrun grep -q \"hello new agent\" app.txt\ndiff"}}
			]
		}`))
	}))
	defer server.Close()

	tracePath := filepath.Join(workspace, "trace.jsonl")
	cmd := exec.Command(
		"go",
		"run",
		".",
		"-workspace", workspace,
		"-natural",
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
		"-trace-out", tracePath,
		"-prompt", "把 old 改成 new",
	)
	cmd.Env = append(os.Environ(), "OPENAI_API_KEY=test-key")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	if !strings.Contains(string(output), "planned steps:") {
		t.Fatalf("expected planned steps in output, got %s", string(output))
	}
	updated, err := os.ReadFile(filepath.Join(workspace, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "hello new agent\n" {
		t.Fatalf("unexpected updated file %q", string(updated))
	}
	traceData, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.TrimSpace(string(traceData)), "\n") + 1; got != 4 {
		t.Fatalf("expected 4 trace lines, got %d: %s", got, string(traceData))
	}
}

func TestCLINaturalModeAcceptsMarkdownPlanWithSpacePath(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "Assignment Question.pdf"), []byte("%PDF test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [
				{"message": {"role": "assistant", "content": "可以，先确认文件：\n\n1. list .\n2. stat \"Assignment Question.pdf\""}}
			]
		}`))
	}))
	defer server.Close()

	cmd := exec.Command(
		"go",
		"run",
		".",
		"-workspace", workspace,
		"-natural",
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
		"-prompt", "帮我看看 Assignment Question.pdf",
	)
	cmd.Env = append(os.Environ(), "OPENAI_API_KEY=test-key")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{"planned steps:", "stat \"Assignment Question.pdf\"", "Assignment Question.pdf"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestCLIVersionFlagPrintsBuildVersion(t *testing.T) {
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "liora")
	build := exec.Command("go", "build", "-ldflags", "-X main.version=test-release", "-o", binary, packageDir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(output))
	}

	output, err := exec.Command(binary, "-version").CombinedOutput()
	if err != nil {
		t.Fatalf("version command failed: %v\n%s", err, string(output))
	}
	if !strings.Contains(string(output), "test-release") {
		t.Fatalf("expected version output to contain build version, got:\n%s", string(output))
	}
}

func TestCLINaturalModeUsesAnthropicProvider(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "app.txt"), []byte("hello old agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var gotAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		gotAPIKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"content": [
				{"type": "text", "text": "read app.txt\nreplace app.txt old new\ndiff"}
			]
		}`))
	}))
	defer server.Close()

	cmd := exec.Command(
		"go",
		"run",
		".",
		"-workspace", workspace,
		"-natural",
		"-llm-provider", "anthropic",
		"-llm-base-url", server.URL,
		"-llm-api-key", "anthropic-key",
		"-llm-model", "claude-test",
		"-prompt", "把 old 改成 new",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	if gotAPIKey != "anthropic-key" {
		t.Fatalf("unexpected anthropic key header %q", gotAPIKey)
	}
	updated, err := os.ReadFile(filepath.Join(workspace, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "hello new agent\n" {
		t.Fatalf("unexpected updated file %q", string(updated))
	}
}

func TestCLIInteractiveModeRunsTurns(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "app.txt"), []byte("hello old agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(toolLoopHandler(
		"Replaced old with new in app.txt.",
		fakeToolStep{name: "read", args: `{"path":"app.txt"}`},
		fakeToolStep{name: "replace", args: `{"path":"app.txt","old_text":"old","new_text":"new"}`},
	))
	defer server.Close()

	cmd := exec.Command(
		"go",
		"run",
		".",
		"-workspace", workspace,
		"-interactive",
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	cmd.Env = append(os.Environ(), "OPENAI_API_KEY=test-key", "LIORA_HOME="+t.TempDir())
	cmd.Stdin = strings.NewReader("把 old 改成 new\n/exit\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{"Liora", "You", "把 old 改成 new", "Assistant", "Replaced old with new in app.txt.", "Diff", "Next", "Bye"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"Plan", "Tools", "Task - started"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("interactive output should hide internal %q, got:\n%s", avoid, rendered)
		}
	}
	updated, err := os.ReadFile(filepath.Join(workspace, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "hello old agent\n" {
		t.Fatalf("interactive patch mode should not mutate before apply, got %q", string(updated))
	}
}

func TestCLIInteractiveCanDisablePatchMode(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "app.txt"), []byte("hello old agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(toolLoopHandler(
		"Replaced old with new in app.txt.",
		fakeToolStep{name: "replace", args: `{"path":"app.txt","old_text":"old","new_text":"new"}`},
	))
	defer server.Close()

	cmd := exec.Command(
		"go",
		"run",
		".",
		"-workspace", workspace,
		"-interactive",
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	cmd.Env = append(os.Environ(), "OPENAI_API_KEY=test-key", "LIORA_PATCH_MODE=0", "LIORA_HOME="+t.TempDir())
	cmd.Stdin = strings.NewReader("把 old 改成 new\n/exit\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	updated, err := os.ReadFile(filepath.Join(workspace, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "hello new agent\n" {
		t.Fatalf("disabled patch mode should mutate workspace, got %q", string(updated))
	}
}

func TestCLIDefaultsToInteractiveCurrentDirectory(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "app.txt"), []byte("hello old agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "liora")
	build := exec.Command("go", "build", "-o", binary, packageDir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(output))
	}

	server := httptest.NewServer(toolLoopHandler(
		"Read app.txt in the current directory.",
		fakeToolStep{name: "read", args: `{"path":"app.txt"}`},
	))
	defer server.Close()

	cmd := exec.Command(
		binary,
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "OPENAI_API_KEY=test-key", "LIORA_HOME="+t.TempDir())
	cmd.Stdin = strings.NewReader("看一下当前目录\n/timeline\n/exit\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{"Liora", "local agent workbench", "liora >", "Timeline session_", "user: 看一下当前目录", "tool.result"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected default daemon-backed TUI output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestCLIDaemonModeServesHealth(t *testing.T) {
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "liora")
	build := exec.Command("go", "build", "-o", binary, packageDir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(output))
	}
	addr := freeLocalAddr(t)
	cmd := exec.Command(binary, "-daemon", "-daemon-addr", addr, "-workspace", t.TempDir())
	cmd.Env = append(os.Environ(), "LIORA_LLM_API_KEY=test-key", "LIORA_HOME="+t.TempDir())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	var body string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			data, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body = string(data)
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("daemon did not serve healthz, got %q", body)
	}
}

func TestCLIInteractiveCanStreamThroughDaemon(t *testing.T) {
	workspace := t.TempDir()
	for _, path := range []string{"README.md", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(workspace, path), []byte("hello\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "liora")
	build := exec.Command("go", "build", "-o", binary, packageDir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(output))
	}
	server := httptest.NewServer(toolLoopHandler(
		"Listed the workspace directory.",
		fakeToolStep{name: "list", args: `{"path":"."}`},
	))
	defer server.Close()

	addr := freeLocalAddr(t)
	home := t.TempDir()
	daemonCmd := exec.Command(
		binary,
		"-daemon",
		"-daemon-addr", addr,
		"-workspace", workspace,
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	daemonCmd.Env = append(os.Environ(), "LIORA_HOME="+home, "OPENAI_API_KEY=test-key")
	if err := daemonCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = daemonCmd.Process.Kill()
		_ = daemonCmd.Wait()
	})
	waitForDaemon(t, addr)

	tuiCmd := exec.Command(
		binary,
		"-workspace", workspace,
		"-interactive",
		"-tui-daemon",
		"-daemon-addr", addr,
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	tuiCmd.Env = append(os.Environ(), "LIORA_HOME="+home, "OPENAI_API_KEY=test-key")
	tuiCmd.Stdin = strings.NewReader("看看目录\n/exit\n")
	output, err := tuiCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{"Assistant", "Listed the workspace directory."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected daemon-backed TUI output to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"Status", "Planning task", "Plan", "- list .", "Tools", "README.md", "notes.txt", "completed"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("daemon-backed TUI output should hide internal %q, got:\n%s", avoid, rendered)
		}
	}
}

func TestCLIInteractiveDaemonApplyCommand(t *testing.T) {
	workspace := t.TempDir()
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "liora")
	build := exec.Command("go", "build", "-o", binary, packageDir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(output))
	}
	server := httptest.NewServer(toolLoopHandler(
		"Wrote notes.txt.",
		fakeToolStep{name: "write", args: `{"path":"notes.txt","content":"hello\n"}`},
	))
	defer server.Close()

	addr := freeLocalAddr(t)
	home := t.TempDir()
	daemonCmd := exec.Command(
		binary,
		"-daemon",
		"-daemon-addr", addr,
		"-workspace", workspace,
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	daemonCmd.Env = append(os.Environ(), "LIORA_HOME="+home, "OPENAI_API_KEY=test-key", "LIORA_PATCH_MODE=1")
	if err := daemonCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = daemonCmd.Process.Kill()
		_ = daemonCmd.Wait()
	})
	waitForDaemon(t, addr)

	tuiCmd := exec.Command(
		binary,
		"-workspace", workspace,
		"-interactive",
		"-tui-daemon",
		"-daemon-addr", addr,
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	tuiCmd.Env = append(os.Environ(), "LIORA_HOME="+home, "OPENAI_API_KEY=test-key")
	tuiCmd.Stdin = strings.NewReader("创建 notes\n/apply\n/exit\n")
	output, err := tuiCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{"Diff", "Next", "Applied task", "Files:", "notes.txt"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected daemon apply output to contain %q, got:\n%s", want, rendered)
		}
	}
	data, err := os.ReadFile(filepath.Join(workspace, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("unexpected applied file %q", string(data))
	}
}

func TestCLIInteractiveDaemonTaskHistoryCommands(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "liora")
	build := exec.Command("go", "build", "-o", binary, packageDir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(output))
	}
	server := httptest.NewServer(toolLoopHandler(
		"Listed the workspace directory.",
		fakeToolStep{name: "list", args: `{"path":"."}`},
	))
	defer server.Close()

	addr := freeLocalAddr(t)
	home := t.TempDir()
	daemonCmd := exec.Command(
		binary,
		"-daemon",
		"-daemon-addr", addr,
		"-workspace", workspace,
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	daemonCmd.Env = append(os.Environ(), "LIORA_HOME="+home, "OPENAI_API_KEY=test-key")
	if err := daemonCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = daemonCmd.Process.Kill()
		_ = daemonCmd.Wait()
	})
	waitForDaemon(t, addr)

	tuiCmd := exec.Command(
		binary,
		"-workspace", workspace,
		"-interactive",
		"-tui-daemon",
		"-daemon-addr", addr,
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	tuiCmd.Env = append(os.Environ(), "LIORA_HOME="+home, "OPENAI_API_KEY=test-key")
	tuiCmd.Stdin = strings.NewReader("看看目录\n/tasks\n/last\n/exit\n")
	output, err := tuiCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{"System", "completed", "Task task_", "Events:", "task.plan_ready", "tool.result", "task.completed"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected history output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestCLIInteractiveDaemonCancelWhileTaskRuns(t *testing.T) {
	workspace := t.TempDir()
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "liora")
	build := exec.Command("go", "build", "-o", binary, packageDir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(output))
	}
	server := httptest.NewServer(toolLoopHandler(
		"Ran the slow command.",
		fakeToolStep{name: "run", args: `{"command":"sleep 10"}`},
	))
	defer server.Close()

	addr := freeLocalAddr(t)
	home := t.TempDir()
	daemonCmd := exec.Command(
		binary,
		"-daemon",
		"-daemon-addr", addr,
		"-workspace", workspace,
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	daemonCmd.Env = append(os.Environ(), "LIORA_HOME="+home, "OPENAI_API_KEY=test-key")
	if err := daemonCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = daemonCmd.Process.Kill()
		_ = daemonCmd.Wait()
	})
	waitForDaemon(t, addr)

	tuiCmd := exec.Command(
		binary,
		"-workspace", workspace,
		"-interactive",
		"-tui-daemon",
		"-daemon-addr", addr,
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	tuiCmd.Env = append(os.Environ(), "LIORA_HOME="+home, "OPENAI_API_KEY=test-key")
	tuiCmd.Stdin = strings.NewReader("run slow command\n/cancel\n/exit\n")
	output, err := tuiCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{"Cancelled task", "cancelled", "Bye"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected cancel output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestCLIInteractiveDirectoryListingShowsMultipleEntries(t *testing.T) {
	workspace := t.TempDir()
	for _, path := range []string{"README.md", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(workspace, path), []byte("hello\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "liora")
	build := exec.Command("go", "build", "-o", binary, packageDir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(output))
	}

	server := httptest.NewServer(toolLoopHandler(
		"Listed the workspace directory.",
		fakeToolStep{name: "list", args: `{"path":"."}`},
	))
	defer server.Close()

	cmd := exec.Command(
		binary,
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "OPENAI_API_KEY=test-key", "LIORA_HOME="+t.TempDir())
	cmd.Stdin = strings.NewReader("你帮我看看这个文件夹里有什么东西\n/exit\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{"Assistant", "Listed the workspace directory."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"Plan", "- list .", "README.md", "notes.txt", "Tools"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("interactive directory listing should hide internal %q, got:\n%s", avoid, rendered)
		}
	}
}

func waitForDaemon(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon did not become healthy at %s", addr)
}

func freeLocalAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return fmt.Sprintf("127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port)
}

func TestCLIScriptModeRunsMCPTool(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	serverBin := buildFakeMCPServer(t)
	configData, err := json.Marshal(map[string]any{
		"servers": map[string]any{
			"fake": map[string]any{
				"command": serverBin,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "mcp.json"), configData, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(
		"go",
		"run",
		".",
		"-workspace", workspace,
		"-prompt", `mcp fake echo {"text":"hello from mcp"}`,
	)
	cmd.Env = append(os.Environ(), "LIORA_HOME="+home)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	if !strings.Contains(string(output), "hello from mcp") {
		t.Fatalf("expected MCP output, got:\n%s", string(output))
	}
}

func buildFakeMCPServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(`package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

func main() {
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
			_ = encoder.Encode(map[string]any{"jsonrpc":"2.0","id":id,"result":map[string]any{"protocolVersion":"2025-06-18","capabilities":map[string]any{"tools":map[string]any{}}}})
		case "tools/list":
			_ = encoder.Encode(map[string]any{"jsonrpc":"2.0","id":id,"result":map[string]any{"tools":[]map[string]any{{"name":"echo","description":"Echo text","inputSchema":map[string]any{"type":"object"}}}}})
		case "tools/call":
			params, _ := req["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			_ = encoder.Encode(map[string]any{"jsonrpc":"2.0","id":id,"result":map[string]any{"content":[]map[string]any{{"type":"text","text":fmt.Sprint(args["text"])}}}})
		default:
			_ = encoder.Encode(map[string]any{"jsonrpc":"2.0","id":id,"error":map[string]any{"code":-32601,"message":"method not found"}})
		}
	}
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "fake-mcp")
	cmd := exec.Command("go", "build", "-o", binary, source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake MCP server failed: %v\n%s", err, string(output))
	}
	return binary
}
