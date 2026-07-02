package mcp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
)

type Config struct {
	Servers map[string]ServerConfig `json:"servers"`
}

type ServerConfig struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Source      string            `json:"source,omitempty"`
	Version     string            `json:"version,omitempty"`
	Permissions []string          `json:"permissions,omitempty"`
}

type Tool struct {
	Server      string
	Name        string
	Description string
	InputSchema map[string]any
}

type Client struct {
	config  ServerConfig
	nextID  atomic.Int64
	mu      sync.Mutex
	session *session
}

type Manager struct {
	config  Config
	mu      sync.Mutex
	clients map[string]*Client
}

func NewClient(config ServerConfig) *Client {
	return &Client{config: config}
}

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := session.request(ctx, "tools/list", nil, &result); err != nil {
		c.resetSession()
		return nil, err
	}
	return result.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, err := c.ensureSession(ctx)
	if err != nil {
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
		c.resetSession()
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

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetSession()
}

func (c *Client) ensureSession(ctx context.Context) (*session, error) {
	if c.session != nil {
		return c.session, nil
	}
	session, err := c.start(ctx)
	if err != nil {
		return nil, err
	}
	if err := session.initialize(ctx); err != nil {
		session.close()
		return nil, err
	}
	c.session = session
	return session, nil
}

func (c *Client) resetSession() {
	if c.session == nil {
		return
	}
	c.session.close()
	c.session = nil
}

func (s ServerConfig) IsEnabled() bool {
	return s.Enabled == nil || *s.Enabled
}
