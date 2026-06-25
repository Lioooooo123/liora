package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync/atomic"
)

type Config struct {
	Servers map[string]ServerConfig `json:"servers"`
}

type ServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type Tool struct {
	Server      string
	Name        string
	Description string
	InputSchema map[string]any
}

type Client struct {
	config ServerConfig
	nextID atomic.Int64
}

type Manager struct {
	config Config
}

func NewClient(config ServerConfig) *Client {
	return &Client{config: config}
}

func NewManager(config Config) *Manager {
	if config.Servers == nil {
		config.Servers = map[string]ServerConfig{}
	}
	return &Manager{config: config}
}

func (m *Manager) ListTools(ctx context.Context) ([]Tool, error) {
	var names []string
	for name := range m.config.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	var tools []Tool
	for _, name := range names {
		client := NewClient(m.config.Servers[name])
		serverTools, err := client.ListTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, tool := range serverTools {
			tool.Server = name
			tools = append(tools, tool)
		}
	}
	return tools, nil
}

func (m *Manager) Call(ctx context.Context, server string, tool string, args map[string]any) (string, error) {
	config, ok := m.config.Servers[server]
	if !ok {
		return "", fmt.Errorf("unknown MCP server %q", server)
	}
	return NewClient(config).CallTool(ctx, tool, args)
}

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	session, err := c.start(ctx)
	if err != nil {
		return nil, err
	}
	defer session.close()
	if err := session.initialize(ctx); err != nil {
		return nil, err
	}
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := session.request(ctx, "tools/list", nil, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	session, err := c.start(ctx)
	if err != nil {
		return "", err
	}
	defer session.close()
	if err := session.initialize(ctx); err != nil {
		return "", err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	params := map[string]any{"name": name, "arguments": args}
	if err := session.request(ctx, "tools/call", params, &result); err != nil {
		return "", err
	}
	var lines []string
	for _, content := range result.Content {
		if content.Type == "" || content.Type == "text" {
			lines = append(lines, content.Text)
		}
	}
	output := strings.Join(lines, "\n")
	if result.IsError {
		return output, errors.New(output)
	}
	return output, nil
}

type session struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	encoder *json.Encoder
	nextID  *atomic.Int64
}

func (c *Client) start(ctx context.Context) (*session, error) {
	if strings.TrimSpace(c.config.Command) == "" {
		return nil, errors.New("MCP server command is required")
	}
	cmd := exec.CommandContext(ctx, c.config.Command, c.config.Args...)
	cmd.Env = os.Environ()
	for key, value := range c.config.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &session{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		encoder: json.NewEncoder(stdin),
		nextID:  &c.nextID,
	}, nil
}

func (s *session) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "liora", "version": "0.1.0"},
	}
	var result map[string]any
	if err := s.request(ctx, "initialize", params, &result); err != nil {
		return err
	}
	return s.encoder.Encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
}

func (s *session) request(ctx context.Context, method string, params any, result any) error {
	id := s.nextID.Add(1)
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if err := s.encoder.Encode(msg); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, err := s.stdout.ReadBytes('\n')
		if err != nil {
			return err
		}
		var response struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(line, &response); err != nil {
			return err
		}
		if response.ID != id {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf("MCP %s failed: %s", method, response.Error.Message)
		}
		if result != nil {
			return json.Unmarshal(response.Result, result)
		}
		return nil
	}
}

func (s *session) close() {
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
}
