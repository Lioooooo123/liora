package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
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
