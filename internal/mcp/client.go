package mcp

import (
	"context"
	"errors"
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
