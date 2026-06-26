package daemonclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Lioooooo123/liora/internal/capabilities"
	"github.com/Lioooooo123/liora/internal/task"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type Option func(*Client)

type Capabilities struct {
	Tools    []capabilities.ToolSpec    `json:"tools"`
	MCPTools []capabilities.MCPToolSpec `json:"mcp_tools,omitempty"`
	MCPError string                     `json:"mcp_error,omitempty"`
}

type StreamEvent struct {
	Type  task.EventType
	Event task.Event
}

type ApplyResult struct {
	Files []string `json:"files"`
}

func New(baseURL string, options ...Option) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("daemon base URL is required")
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("invalid daemon base URL: %w", err)
	}
	client := &Client{baseURL: baseURL, httpClient: http.DefaultClient}
	for _, option := range options {
		option(client)
	}
	if client.httpClient == nil {
		client.httpClient = http.DefaultClient
	}
	return client, nil
}

func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		client.httpClient = httpClient
	}
}

func (c *Client) Health(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return readAPIError(response)
	}
	return nil
}

func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var result Capabilities
	if err := c.getJSON(ctx, "/v1/capabilities", &result); err != nil {
		return Capabilities{}, err
	}
	return result, nil
}

func (c *Client) CreateTask(ctx context.Context, request task.CreateRequest) (task.CreateResponse, error) {
	var result task.CreateResponse
	if err := c.postJSON(ctx, "/v1/tasks", request, &result, http.StatusCreated, http.StatusAccepted); err != nil {
		return task.CreateResponse{}, err
	}
	return result, nil
}

func (c *Client) CreateSession(ctx context.Context, request task.CreateSessionRequest) (task.CreateSessionResponse, error) {
	var result task.CreateSessionResponse
	if err := c.postJSON(ctx, "/v1/sessions", request, &result, http.StatusCreated); err != nil {
		return task.CreateSessionResponse{}, err
	}
	return result, nil
}

func (c *Client) GetSession(ctx context.Context, sessionID string) (task.Session, error) {
	var result task.Session
	if err := c.getJSON(ctx, "/v1/sessions/"+url.PathEscape(sessionID), &result); err != nil {
		return task.Session{}, err
	}
	return result, nil
}

func (c *Client) ListSessions(ctx context.Context, limit int) ([]task.Session, error) {
	path := "/v1/sessions"
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	var result []task.Session
	if err := c.getJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) SessionMessages(ctx context.Context, sessionID string, limit int) ([]task.Message, error) {
	path := "/v1/sessions/" + url.PathEscape(sessionID) + "/messages"
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	var result []task.Message
	if err := c.getJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) SessionTasks(ctx context.Context, sessionID string, limit int) ([]task.Task, error) {
	path := "/v1/sessions/" + url.PathEscape(sessionID) + "/tasks"
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	var result []task.Task
	if err := c.getJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) SessionTimeline(ctx context.Context, sessionID string, limit int) ([]task.TimelineItem, error) {
	path := "/v1/sessions/" + url.PathEscape(sessionID) + "/timeline"
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	var result []task.TimelineItem
	if err := c.getJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) GetTask(ctx context.Context, taskID string) (task.Task, error) {
	var result task.Task
	if err := c.getJSON(ctx, "/v1/tasks/"+url.PathEscape(taskID), &result); err != nil {
		return task.Task{}, err
	}
	return result, nil
}

func (c *Client) ListTasks(ctx context.Context, limit int) ([]task.Task, error) {
	path := "/v1/tasks"
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	var result []task.Task
	if err := c.getJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) Events(ctx context.Context, taskID string) ([]task.Event, error) {
	var result []task.Event
	if err := c.getJSON(ctx, "/v1/tasks/"+url.PathEscape(taskID)+"/events", &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) Diff(ctx context.Context, taskID string) (string, error) {
	var result struct {
		Diff string `json:"diff"`
	}
	if err := c.getJSON(ctx, "/v1/tasks/"+url.PathEscape(taskID)+"/diff", &result); err != nil {
		return "", err
	}
	return result.Diff, nil
}

func (c *Client) Apply(ctx context.Context, taskID string, patch string) (ApplyResult, error) {
	var result ApplyResult
	body := struct {
		Patch string `json:"patch"`
	}{Patch: patch}
	if err := c.postJSON(ctx, "/v1/tasks/"+url.PathEscape(taskID)+"/apply", body, &result, http.StatusOK); err != nil {
		return ApplyResult{}, err
	}
	return result, nil
}

func (c *Client) Cancel(ctx context.Context, taskID string, reason string) (task.Task, error) {
	var result task.Task
	body := struct {
		Reason string `json:"reason"`
	}{Reason: reason}
	if err := c.postJSON(ctx, "/v1/tasks/"+url.PathEscape(taskID)+"/cancel", body, &result, http.StatusOK); err != nil {
		return task.Task{}, err
	}
	return result, nil
}

func (c *Client) Approve(ctx context.Context, taskID string) (task.Task, error) {
	return c.submitApproval(ctx, taskID, "approve", "")
}

func (c *Client) Deny(ctx context.Context, taskID string, reason string) (task.Task, error) {
	return c.submitApproval(ctx, taskID, "deny", reason)
}

func (c *Client) submitApproval(ctx context.Context, taskID string, decision string, reason string) (task.Task, error) {
	var result task.Task
	body := struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason,omitempty"`
	}{Decision: decision, Reason: reason}
	if err := c.postJSON(ctx, "/v1/tasks/"+url.PathEscape(taskID)+"/approval", body, &result, http.StatusOK, http.StatusAccepted); err != nil {
		return task.Task{}, err
	}
	return result, nil
}

func (c *Client) StreamEvents(ctx context.Context, taskID string) (<-chan StreamEvent, <-chan error) {
	events := make(chan StreamEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/tasks/"+url.PathEscape(taskID)+"/events/stream", nil)
		if err != nil {
			errs <- err
			return
		}
		response, err := c.httpClient.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			errs <- err
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			errs <- readAPIError(response)
			return
		}
		if err := scanSSE(ctx, response.Body, events); err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return readAPIError(response)
	}
	return json.NewDecoder(response.Body).Decode(out)
}

func (c *Client) postJSON(ctx context.Context, path string, input any, out any, allowedStatus ...int) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if !statusAllowed(response.StatusCode, allowedStatus) {
		return readAPIError(response)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}

func scanSSE(ctx context.Context, reader io.Reader, events chan<- StreamEvent) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	var eventType string
	var eventID string
	var data strings.Builder
	flush := func() error {
		if eventType == "" && data.Len() == 0 {
			return nil
		}
		payload := strings.TrimSuffix(data.String(), "\n")
		if eventType == "task.error" {
			return fmt.Errorf("daemon stream error: %s", payload)
		}
		if !json.Valid([]byte(payload)) {
			return fmt.Errorf("decode stream event %q: invalid JSON payload", eventType)
		}
		event := task.Event{
			ID:      eventID,
			Type:    task.EventType(eventType),
			Payload: payload,
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case events <- StreamEvent{Type: task.EventType(eventType), Event: event}:
		}
		eventType = ""
		eventID = ""
		data.Reset()
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "id:") {
			eventID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			data.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	return flush()
}

func statusAllowed(status int, allowed []int) bool {
	for _, candidate := range allowed {
		if status == candidate {
			return true
		}
	}
	return false
}

func readAPIError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
		return fmt.Errorf("daemon API %s: %s", response.Status, payload.Error)
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		text = response.Status
	}
	return fmt.Errorf("daemon API %s: %s", response.Status, text)
}
