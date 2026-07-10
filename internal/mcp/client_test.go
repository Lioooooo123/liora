package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClientListsAndCallsTools(t *testing.T) {
	if os.Getenv("LIORA_FAKE_MCP_SERVER") == "1" {
		runFakeMCPServer()
		return
	}
	cfg := ServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestClientListsAndCallsTools"},
		Env:     map[string]string{"LIORA_FAKE_MCP_SERVER": "1"},
	}
	client := NewClient(cfg)

	tools, err := client.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" || tools[0].Description != "Echo text" {
		t.Fatalf("unexpected tools %#v", tools)
	}

	output, err := client.CallTool(t.Context(), "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("unexpected tool output %q", output)
	}
}

func TestClientReusesInitializedSession_whenListingThenCalling(t *testing.T) {
	// Given
	if os.Getenv("LIORA_FAKE_MCP_SERVER") == "1" {
		runFakeMCPServer()
		return
	}
	countPath := filepath.Join(t.TempDir(), "initializes.txt")
	cfg := ServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestClientReusesInitializedSession_whenListingThenCalling"},
		Env: map[string]string{
			"LIORA_FAKE_MCP_SERVER":     "1",
			"LIORA_FAKE_MCP_COUNT_FILE": countPath,
		},
	}
	client := NewClient(cfg)
	if closer, ok := any(client).(interface{ Close() }); ok {
		t.Cleanup(closer.Close)
	}

	// When
	if _, err := client.ListTools(t.Context()); err != nil {
		t.Fatal(err)
	}
	output, err := client.CallTool(t.Context(), "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if !strings.Contains(output, "hello") {
		t.Fatalf("unexpected tool output %q", output)
	}
	data, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "initialize\n"); got != 1 {
		t.Fatalf("expected one initialized MCP session, got %d entries:\n%s", got, string(data))
	}
}

func TestClientFiltersSecretsFromServerEnv(t *testing.T) {
	if os.Getenv("LIORA_FAKE_MCP_SERVER") == "1" {
		runFakeMCPServer()
		return
	}
	t.Setenv("LIORA_LLM_API_KEY", "super-secret")
	t.Setenv("OPENAI_API_KEY", "another-secret")
	t.Setenv("LIORA_DAEMON_TOKEN", "daemon-token")
	t.Setenv("HARMLESS_VAR", "keep-me")

	dumpPath := filepath.Join(t.TempDir(), "env.txt")
	cfg := ServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestClientFiltersSecretsFromServerEnv"},
		Env: map[string]string{
			"LIORA_FAKE_MCP_SERVER":   "1",
			"LIORA_FAKE_MCP_ENV_DUMP": dumpPath,
			"CONFIGURED_VAR":          "configured-value",
		},
	}
	client := NewClient(cfg)
	if _, err := client.ListTools(t.Context()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	env := string(data)
	for _, secret := range []string{"LIORA_LLM_API_KEY=", "OPENAI_API_KEY=", "LIORA_DAEMON_TOKEN="} {
		if strings.Contains(env, secret) {
			t.Fatalf("secret %q leaked to MCP server environment:\n%s", secret, env)
		}
	}
	if !strings.Contains(env, "PATH=") {
		t.Fatalf("expected PATH to be inherited by MCP server:\n%s", env)
	}
	if !strings.Contains(env, "HARMLESS_VAR=keep-me") {
		t.Fatalf("expected non-secret env to be inherited:\n%s", env)
	}
	if !strings.Contains(env, "CONFIGURED_VAR=configured-value") {
		t.Fatalf("expected configured env to be applied:\n%s", env)
	}
}

func TestManagerListsConfiguredServers(t *testing.T) {
	if os.Getenv("LIORA_FAKE_MCP_SERVER") == "1" {
		runFakeMCPServer()
		return
	}
	manager := NewManager(Config{Servers: map[string]ServerConfig{
		"fake": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestManagerListsConfiguredServers"},
			Env:     map[string]string{"LIORA_FAKE_MCP_SERVER": "1"},
		},
	}})
	tools, err := manager.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Server != "fake" || tools[0].Name != "echo" {
		t.Fatalf("unexpected manager tools %#v", tools)
	}
}

func TestManagerSkipsDisabledServersAndRejectsCalls(t *testing.T) {
	// Given
	enabled := false
	manager := NewManager(Config{Servers: map[string]ServerConfig{
		"disabled": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestManagerSkipsDisabledServersAndRejectsCalls"},
			Env:     map[string]string{"LIORA_FAKE_MCP_SERVER": "fail"},
			Enabled: &enabled,
		},
	}})

	// When
	tools, listErr := manager.ListToolsDetailed(t.Context())
	_, callErr := manager.Call(t.Context(), "disabled", "echo", map[string]any{"text": "blocked"})

	// Then
	if listErr != nil {
		t.Fatalf("disabled server should not be started during list, got %v", listErr)
	}
	if len(tools) != 0 {
		t.Fatalf("disabled server exposed tools: %#v", tools)
	}
	if callErr == nil || !strings.Contains(callErr.Error(), "disabled") {
		t.Fatalf("expected disabled call error, got %v", callErr)
	}
}

func TestManagerListToolsReturnsAvailableToolsWhenOneServerFails(t *testing.T) {
	manager := NewManager(Config{Servers: map[string]ServerConfig{
		"bad": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestManagerListToolsReturnsAvailableToolsWhenOneServerFails"},
			Env:     map[string]string{"LIORA_FAKE_MCP_SERVER": "fail"},
		},
		"fake": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestManagerListToolsReturnsAvailableToolsWhenOneServerFails"},
			Env:     map[string]string{"LIORA_FAKE_MCP_SERVER": "1"},
		},
	}})

	tools, err := manager.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Server != "fake" || tools[0].Name != "echo" {
		t.Fatalf("unexpected manager tools %#v", tools)
	}
}

func TestManagerListToolsDetailedReportsPartialErrors(t *testing.T) {
	manager := NewManager(Config{Servers: map[string]ServerConfig{
		"bad": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestManagerListToolsDetailedReportsPartialErrors"},
			Env:     map[string]string{"LIORA_FAKE_MCP_SERVER": "fail"},
		},
		"fake": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestManagerListToolsDetailedReportsPartialErrors"},
			Env:     map[string]string{"LIORA_FAKE_MCP_SERVER": "1"},
		},
	}})

	tools, err := manager.ListToolsDetailed(t.Context())
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("expected partial error from bad server, got %v", err)
	}
	if len(tools) != 1 || tools[0].Server != "fake" || tools[0].Name != "echo" {
		t.Fatalf("unexpected manager tools %#v", tools)
	}
}

func TestManagerListToolsDetailedTimesOutHungServers(t *testing.T) {
	oldTimeout := listToolsTimeout
	listToolsTimeout = 100 * time.Millisecond
	defer func() { listToolsTimeout = oldTimeout }()
	manager := NewManager(Config{Servers: map[string]ServerConfig{
		"fake": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestManagerListToolsDetailedTimesOutHungServers"},
			Env:     map[string]string{"LIORA_FAKE_MCP_SERVER": "1"},
		},
		"hung": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestManagerListToolsDetailedTimesOutHungServers"},
			Env:     map[string]string{"LIORA_FAKE_MCP_SERVER": "hang"},
		},
	}})

	started := time.Now()
	tools, err := manager.ListToolsDetailed(t.Context())
	elapsed := time.Since(started)
	if err == nil || !strings.Contains(err.Error(), "hung") {
		t.Fatalf("expected partial timeout error from hung server, got %v", err)
	}
	if len(tools) != 1 || tools[0].Server != "fake" {
		t.Fatalf("unexpected manager tools %#v", tools)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("expected hung server timeout to be bounded, took %s", elapsed)
	}
}

func TestManagerListToolsRunsServersConcurrently(t *testing.T) {
	manager := NewManager(Config{Servers: map[string]ServerConfig{
		"first": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestManagerListToolsRunsServersConcurrently"},
			Env:     map[string]string{"LIORA_FAKE_MCP_SERVER": "1", "LIORA_FAKE_MCP_DELAY_MS": "250"},
		},
		"second": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestManagerListToolsRunsServersConcurrently"},
			Env:     map[string]string{"LIORA_FAKE_MCP_SERVER": "1", "LIORA_FAKE_MCP_DELAY_MS": "250"},
		},
	}})

	started := time.Now()
	tools, err := manager.ListTools(t.Context())
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected two tools, got %#v", tools)
	}
	if elapsed >= 450*time.Millisecond {
		t.Fatalf("expected concurrent tools/list, took %s", elapsed)
	}
}

func runFakeMCPServer() {
	if dump := os.Getenv("LIORA_FAKE_MCP_ENV_DUMP"); dump != "" {
		_ = os.WriteFile(dump, []byte(strings.Join(os.Environ(), "\n")), 0o600)
	}
	mode := os.Getenv("LIORA_FAKE_MCP_SERVER")
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
			if countPath := os.Getenv("LIORA_FAKE_MCP_COUNT_FILE"); countPath != "" {
				f, err := os.OpenFile(countPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
				if err == nil {
					_, _ = f.WriteString("initialize\n")
					_ = f.Close()
				}
			}
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
			if mode == "hang" {
				time.Sleep(10 * time.Second)
				continue
			}
			if delayMS, err := strconv.Atoi(os.Getenv("LIORA_FAKE_MCP_DELAY_MS")); err == nil && delayMS > 0 {
				time.Sleep(time.Duration(delayMS) * time.Millisecond)
			}
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
			params, _ := req["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": fmt.Sprint(args["text"])}},
				},
			})
		default:
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}
	os.Exit(0)
}

func TestMain(m *testing.M) {
	if os.Getenv("LIORA_FAKE_MCP_SERVER") == "fail" {
		os.Exit(7)
	}
	if os.Getenv("LIORA_FAKE_MCP_SERVER") == "1" || os.Getenv("LIORA_FAKE_MCP_SERVER") == "hang" {
		runFakeMCPServer()
		return
	}
	os.Exit(m.Run())
}
