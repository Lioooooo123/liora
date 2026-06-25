package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
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

func runFakeMCPServer() {
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
	if os.Getenv("LIORA_FAKE_MCP_SERVER") == "1" {
		runFakeMCPServer()
		return
	}
	os.Exit(m.Run())
}
