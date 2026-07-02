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
	"strings"
	"sync"
	"sync/atomic"
)

type session struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	encoder *json.Encoder
	nextID  *atomic.Int64
	once    sync.Once
}

type lineReadResult struct {
	line []byte
	err  error
}

func (c *Client) start(ctx context.Context) (*session, error) {
	if strings.TrimSpace(c.config.Command) == "" {
		return nil, errors.New("MCP server command is required")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	cmd := exec.Command(c.config.Command, c.config.Args...)
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
		line, err := s.readLine(ctx)
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

func (s *session) readLine(ctx context.Context) ([]byte, error) {
	result := make(chan lineReadResult, 1)
	go func() {
		line, err := s.stdout.ReadBytes('\n')
		result <- lineReadResult{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		s.close()
		return nil, ctx.Err()
	case read := <-result:
		return read.line, read.err
	}
}

func (s *session) close() {
	s.once.Do(func() {
		_ = s.stdin.Close()
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		_ = s.cmd.Wait()
	})
}
