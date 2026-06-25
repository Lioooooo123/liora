package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [
				{"message": {"role": "assistant", "content": "read app.txt\nreplace app.txt old new\ndiff"}}
			]
		}`))
	}))
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
	cmd.Env = append(os.Environ(), "OPENAI_API_KEY=test-key")
	cmd.Stdin = strings.NewReader("把 old 改成 new\n/exit\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{"Liora", "Plan", "Tools", "Summary", "Bye"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "You") {
		t.Fatalf("interactive output should not duplicate user input, got:\n%s", rendered)
	}
	updated, err := os.ReadFile(filepath.Join(workspace, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "hello new agent\n" {
		t.Fatalf("unexpected updated file %q", string(updated))
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [
				{"message": {"role": "assistant", "content": "read app.txt\ndiff"}}
			]
		}`))
	}))
	defer server.Close()

	cmd := exec.Command(
		binary,
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "OPENAI_API_KEY=test-key")
	cmd.Stdin = strings.NewReader("看一下当前目录\n/exit\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	if !strings.Contains(rendered, "Liora") || !strings.Contains(rendered, "agent >") {
		t.Fatalf("expected default interactive TUI, got:\n%s", rendered)
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
	cmd.Env = append(os.Environ(), "LIORA_LLM_API_KEY=test-key")
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [
				{"message": {"role": "assistant", "content": "list ."}}
			]
		}`))
	}))
	defer server.Close()

	cmd := exec.Command(
		binary,
		"-llm-base-url", server.URL,
		"-llm-model", "test-model",
	)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "OPENAI_API_KEY=test-key")
	cmd.Stdin = strings.NewReader("你帮我看看这个文件夹里有什么东西\n/exit\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{"Plan", "- list .", "README.md", "notes.txt"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, rendered)
		}
	}
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
