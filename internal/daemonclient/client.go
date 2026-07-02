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
	"github.com/Lioooooo123/liora/internal/store"
	"github.com/Lioooooo123/liora/internal/task"
)

type Client struct {
	baseURL         string
	httpClient      *http.Client
	capabilityToken string
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

type TaskStreamEvent struct {
	TaskID string
	StreamEvent
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

func WithCapabilityToken(token string) Option {
	return func(client *Client) {
		client.capabilityToken = strings.TrimSpace(token)
	}
}

func (c *Client) authorize(request *http.Request) {
	if strings.TrimSpace(c.capabilityToken) == "" {
		return
	}
	request.Header.Set("Authorization", "Bearer "+c.capabilityToken)
	request.Header.Set("X-Liora-Capability", c.capabilityToken)
}

func (c *Client) Health(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	c.authorize(request)
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

func (c *Client) ListMemories(ctx context.Context, limit int) ([]store.Memory, error) {
	return c.ListMemoriesWithOptions(ctx, store.MemoryListOptions{Limit: limit})
}

func (c *Client) SearchMemories(ctx context.Context, query string, limit int) ([]store.Memory, error) {
	return c.ListMemoriesWithOptions(ctx, store.MemoryListOptions{Query: query, Limit: limit})
}

func (c *Client) SearchMemoriesWithOptions(ctx context.Context, query string, limit int, includeDisabled bool) ([]store.Memory, error) {
	return c.ListMemoriesWithOptions(ctx, store.MemoryListOptions{Query: query, Limit: limit, IncludeDisabled: includeDisabled})
}

func (c *Client) ListMemoriesWithOptions(ctx context.Context, options store.MemoryListOptions) ([]store.Memory, error) {
	if err := ensureNonNegativeLimit(options.Limit); err != nil {
		return nil, err
	}
	values := url.Values{}
	if strings.TrimSpace(options.Query) != "" {
		values.Set("q", options.Query)
	}
	if strings.TrimSpace(options.Workspace) != "" {
		values.Set("workspace", options.Workspace)
	}
	if options.Limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", options.Limit))
	}
	if options.IncludeDisabled {
		values.Set("include_disabled", "true")
	}
	if options.IncludeExpired {
		values.Set("include_expired", "true")
	}
	path := "/v1/memories"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var result []store.Memory
	if err := c.getJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) AddMemory(ctx context.Context, text string) (store.Memory, error) {
	return c.CreateMemory(ctx, store.CreateMemoryRequest{Text: text})
}

func (c *Client) CreateMemory(ctx context.Context, request store.CreateMemoryRequest) (store.Memory, error) {
	if err := validateCreateMemoryRequest(request); err != nil {
		return store.Memory{}, err
	}
	var result store.Memory
	if err := c.postJSON(ctx, "/v1/memories", request, &result, http.StatusCreated); err != nil {
		return store.Memory{}, err
	}
	return result, nil
}

func (c *Client) GetMemory(ctx context.Context, id string) (store.Memory, error) {
	escapedID, err := pathID("memory id", id)
	if err != nil {
		return store.Memory{}, err
	}
	var result store.Memory
	if err := c.getJSON(ctx, "/v1/memories/"+escapedID, &result); err != nil {
		return store.Memory{}, err
	}
	return result, nil
}

func (c *Client) UpdateMemory(ctx context.Context, id string, request store.UpdateMemoryRequest) (store.Memory, error) {
	escapedID, err := pathID("memory id", id)
	if err != nil {
		return store.Memory{}, err
	}
	if err := validateUpdateMemoryRequest(request); err != nil {
		return store.Memory{}, err
	}
	var result store.Memory
	if err := c.patchJSON(ctx, "/v1/memories/"+escapedID, request, &result, http.StatusOK); err != nil {
		return store.Memory{}, err
	}
	return result, nil
}

func (c *Client) SetMemoryEnabled(ctx context.Context, id string, enabled bool) (store.Memory, error) {
	escapedID, err := pathID("memory id", id)
	if err != nil {
		return store.Memory{}, err
	}
	action := "disable"
	if enabled {
		action = "enable"
	}
	var result store.Memory
	if err := c.postJSON(ctx, "/v1/memories/"+escapedID+"/"+action, struct{}{}, &result, http.StatusOK); err != nil {
		return store.Memory{}, err
	}
	return result, nil
}

func (c *Client) DeleteMemory(ctx context.Context, id string) error {
	return c.DeleteMemoryForWorkspace(ctx, id, "")
}

func (c *Client) DeleteMemoryForWorkspace(ctx context.Context, id string, workspace string) error {
	escapedID, err := pathID("memory id", id)
	if err != nil {
		return err
	}
	path := "/v1/memories/" + escapedID
	if strings.TrimSpace(workspace) != "" {
		values := url.Values{}
		values.Set("workspace", workspace)
		path += "?" + values.Encode()
	}
	return c.deleteJSON(ctx, path, http.StatusNoContent)
}

func (c *Client) CreatePermissionRule(ctx context.Context, request store.CreatePermissionRuleRequest) (store.PermissionRule, error) {
	var result store.PermissionRule
	if err := c.postJSON(ctx, "/v1/permission-rules", request, &result, http.StatusCreated); err != nil {
		return store.PermissionRule{}, err
	}
	return result, nil
}

func (c *Client) ListPermissionRules(ctx context.Context, options store.PermissionRuleListOptions) ([]store.PermissionRule, error) {
	if err := ensureNonNegativeLimit(options.Limit); err != nil {
		return nil, err
	}
	values := url.Values{}
	if strings.TrimSpace(options.Workspace) != "" {
		values.Set("workspace", options.Workspace)
	}
	if strings.TrimSpace(options.SessionID) != "" {
		values.Set("session_id", options.SessionID)
	}
	if options.Limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", options.Limit))
	}
	if options.IncludeDisabled {
		values.Set("include_disabled", "true")
	}
	path := "/v1/permission-rules"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var result []store.PermissionRule
	if err := c.getJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) DeletePermissionRule(ctx context.Context, id string) error {
	escapedID, err := pathID("permission rule id", id)
	if err != nil {
		return err
	}
	return c.deleteJSON(ctx, "/v1/permission-rules/"+escapedID, http.StatusNoContent)
}

func (c *Client) CreateConversationThread(ctx context.Context, request store.CreateConversationThreadRequest) (store.ConversationThread, error) {
	if strings.TrimSpace(request.Workspace) == "" {
		return store.ConversationThread{}, fmt.Errorf("workspace is required")
	}
	var result store.ConversationThread
	if err := c.postJSON(ctx, "/v1/threads", request, &result, http.StatusCreated); err != nil {
		return store.ConversationThread{}, err
	}
	return result, nil
}

func (c *Client) ListConversationThreads(ctx context.Context, workspace string, limit int) ([]store.ConversationThread, error) {
	return c.ListConversationThreadsWithOptions(ctx, store.ConversationThreadListOptions{Workspace: workspace, Limit: limit})
}

func (c *Client) ListConversationThreadsWithOptions(ctx context.Context, options store.ConversationThreadListOptions) ([]store.ConversationThread, error) {
	limit := options.Limit
	if err := ensureNonNegativeLimit(limit); err != nil {
		return nil, err
	}
	values := url.Values{}
	if strings.TrimSpace(options.Workspace) != "" {
		values.Set("workspace", options.Workspace)
	}
	if limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", limit))
	}
	if options.IncludeArchived {
		values.Set("include_archived", "true")
	}
	path := "/v1/threads"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var result []store.ConversationThread
	if err := c.getJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) GetConversationThread(ctx context.Context, threadID string) (store.ConversationThread, error) {
	escapedID, err := pathID("thread id", threadID)
	if err != nil {
		return store.ConversationThread{}, err
	}
	var result store.ConversationThread
	if err := c.getJSON(ctx, "/v1/threads/"+escapedID, &result); err != nil {
		return store.ConversationThread{}, err
	}
	return result, nil
}

func (c *Client) UpdateConversationThread(ctx context.Context, threadID string, request store.UpdateConversationThreadRequest) (store.ConversationThread, error) {
	escapedID, err := pathID("thread id", threadID)
	if err != nil {
		return store.ConversationThread{}, err
	}
	var result store.ConversationThread
	if err := c.patchJSON(ctx, "/v1/threads/"+escapedID, request, &result, http.StatusOK); err != nil {
		return store.ConversationThread{}, err
	}
	return result, nil
}

func (c *Client) GetThreadModelConfig(ctx context.Context, threadID string) (store.ThreadModelConfig, error) {
	escapedID, err := pathID("thread id", threadID)
	if err != nil {
		return store.ThreadModelConfig{}, err
	}
	var result store.ThreadModelConfig
	if err := c.getJSON(ctx, "/v1/threads/"+escapedID+"/model", &result); err != nil {
		return store.ThreadModelConfig{}, err
	}
	return result, nil
}

func (c *Client) UpdateThreadModelConfig(ctx context.Context, threadID string, request store.UpdateThreadModelConfigRequest) (store.ThreadModelConfig, error) {
	escapedID, err := pathID("thread id", threadID)
	if err != nil {
		return store.ThreadModelConfig{}, err
	}
	var result store.ThreadModelConfig
	if err := c.patchJSON(ctx, "/v1/threads/"+escapedID+"/model", request, &result, http.StatusOK); err != nil {
		return store.ThreadModelConfig{}, err
	}
	return result, nil
}

func (c *Client) DeleteThreadModelConfig(ctx context.Context, threadID string) error {
	escapedID, err := pathID("thread id", threadID)
	if err != nil {
		return err
	}
	return c.deleteJSON(ctx, "/v1/threads/"+escapedID+"/model", http.StatusNoContent)
}

func (c *Client) CreateCrossThreadMessage(ctx context.Context, toThreadID string, request store.CreateCrossThreadMessageRequest) (store.CrossThreadMessage, error) {
	escapedID, err := pathID("thread id", toThreadID)
	if err != nil {
		return store.CrossThreadMessage{}, err
	}
	if strings.TrimSpace(request.FromThreadID) == "" {
		return store.CrossThreadMessage{}, fmt.Errorf("from_thread_id is required")
	}
	var result store.CrossThreadMessage
	if err := c.postJSON(ctx, "/v1/threads/"+escapedID+"/messages", request, &result, http.StatusCreated); err != nil {
		return store.CrossThreadMessage{}, err
	}
	return result, nil
}

func (c *Client) ListCrossThreadMessages(ctx context.Context, toThreadID string, limit int) ([]store.CrossThreadMessage, error) {
	escapedID, err := pathID("thread id", toThreadID)
	if err != nil {
		return nil, err
	}
	if err := ensureNonNegativeLimit(limit); err != nil {
		return nil, err
	}
	path := "/v1/threads/" + escapedID + "/messages"
	if limit > 0 {
		values := url.Values{}
		values.Set("limit", fmt.Sprintf("%d", limit))
		path += "?" + values.Encode()
	}
	var result []store.CrossThreadMessage
	if err := c.getJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) DisableMemory(ctx context.Context, id string) error {
	_, err := c.SetMemoryEnabled(ctx, id, false)
	return err
}

func (c *Client) EnableMemory(ctx context.Context, id string) error {
	_, err := c.SetMemoryEnabled(ctx, id, true)
	return err
}

func (c *Client) Workbench(ctx context.Context, workspace string, limit int) (task.Workbench, error) {
	path, err := listPath("/v1/workbench", workspace, limit)
	if err != nil {
		return task.Workbench{}, err
	}
	var result task.Workbench
	if err := c.getJSON(ctx, path, &result); err != nil {
		return task.Workbench{}, err
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
	escapedID, err := pathID("session id", sessionID)
	if err != nil {
		return task.Session{}, err
	}
	var result task.Session
	if err := c.getJSON(ctx, "/v1/sessions/"+escapedID, &result); err != nil {
		return task.Session{}, err
	}
	return result, nil
}

func (c *Client) ListSessions(ctx context.Context, limit int) ([]task.Session, error) {
	return c.ListSessionsForWorkspace(ctx, "", limit)
}

func (c *Client) ListSessionsForWorkspace(ctx context.Context, workspace string, limit int) ([]task.Session, error) {
	path, err := listPath("/v1/sessions", workspace, limit)
	if err != nil {
		return nil, err
	}
	var result []task.Session
	if err := c.getJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) SessionMessages(ctx context.Context, sessionID string, limit int) ([]task.Message, error) {
	escapedID, err := pathID("session id", sessionID)
	if err != nil {
		return nil, err
	}
	if err := ensureNonNegativeLimit(limit); err != nil {
		return nil, err
	}
	path := "/v1/sessions/" + escapedID + "/messages"
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
	escapedID, err := pathID("session id", sessionID)
	if err != nil {
		return nil, err
	}
	if err := ensureNonNegativeLimit(limit); err != nil {
		return nil, err
	}
	path := "/v1/sessions/" + escapedID + "/tasks"
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
	escapedID, err := pathID("session id", sessionID)
	if err != nil {
		return nil, err
	}
	if err := ensureNonNegativeLimit(limit); err != nil {
		return nil, err
	}
	path := "/v1/sessions/" + escapedID + "/timeline"
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	var result []task.TimelineItem
	if err := c.getJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) SessionTodos(ctx context.Context, sessionID string) ([]task.Todo, error) {
	escapedID, err := pathID("session id", sessionID)
	if err != nil {
		return nil, err
	}
	var result []task.Todo
	if err := c.getJSON(ctx, "/v1/sessions/"+escapedID+"/todos", &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) SessionContext(ctx context.Context, sessionID string, request task.ContextRequest) (task.ContextEnvelope, error) {
	escapedID, err := pathID("session id", sessionID)
	if err != nil {
		return task.ContextEnvelope{}, err
	}
	if err := ensureNonNegativeLimit(request.ItemLimit); err != nil {
		return task.ContextEnvelope{}, fmt.Errorf("item limit: %w", err)
	}
	if err := ensureNonNegativeLimit(request.TokenBudget); err != nil {
		return task.ContextEnvelope{}, fmt.Errorf("token budget: %w", err)
	}
	values := url.Values{}
	if request.ItemLimit > 0 {
		values.Set("limit", fmt.Sprintf("%d", request.ItemLimit))
	}
	if request.TokenBudget > 0 {
		values.Set("token_budget", fmt.Sprintf("%d", request.TokenBudget))
	}
	path := "/v1/sessions/" + escapedID + "/context"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var result task.ContextEnvelope
	if err := c.getJSON(ctx, path, &result); err != nil {
		return task.ContextEnvelope{}, err
	}
	return result, nil
}

func (c *Client) CompactSession(ctx context.Context, sessionID string, request task.CompactRequest) (task.CompactResult, error) {
	escapedID, err := pathID("session id", sessionID)
	if err != nil {
		return task.CompactResult{}, err
	}
	if err := ensureNonNegativeLimit(request.ItemLimit); err != nil {
		return task.CompactResult{}, fmt.Errorf("item limit: %w", err)
	}
	if err := ensureNonNegativeLimit(request.TokenBudget); err != nil {
		return task.CompactResult{}, fmt.Errorf("token budget: %w", err)
	}
	var result task.CompactResult
	if err := c.postJSON(ctx, "/v1/sessions/"+escapedID+"/compact", request, &result, http.StatusOK); err != nil {
		return task.CompactResult{}, err
	}
	return result, nil
}

func (c *Client) ArtifactPage(ctx context.Context, request task.ArtifactPageRequest) (task.ArtifactPage, error) {
	uri := strings.TrimSpace(request.URI)
	if uri == "" {
		return task.ArtifactPage{}, fmt.Errorf("artifact uri is required")
	}
	if err := ensureNonNegativeLimit(request.Page); err != nil {
		return task.ArtifactPage{}, fmt.Errorf("page: %w", err)
	}
	if err := ensureNonNegativeLimit(request.PageSize); err != nil {
		return task.ArtifactPage{}, fmt.Errorf("page size: %w", err)
	}
	values := url.Values{}
	values.Set("uri", uri)
	if request.Page > 0 {
		values.Set("page", fmt.Sprintf("%d", request.Page))
	}
	if request.PageSize > 0 {
		values.Set("page_size", fmt.Sprintf("%d", request.PageSize))
	}
	var result task.ArtifactPage
	if err := c.getJSON(ctx, "/v1/artifacts/page?"+values.Encode(), &result); err != nil {
		return task.ArtifactPage{}, err
	}
	return result, nil
}

func (c *Client) SearchTimeline(ctx context.Context, workspace string, query string, limit int) ([]task.TimelineItem, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	if err := ensureNonNegativeLimit(limit); err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("q", query)
	if strings.TrimSpace(workspace) != "" {
		values.Set("workspace", workspace)
	}
	if limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", limit))
	}
	var result []task.TimelineItem
	if err := c.getJSON(ctx, "/v1/timeline/search?"+values.Encode(), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) GetTask(ctx context.Context, taskID string) (task.Task, error) {
	escapedID, err := pathID("task id", taskID)
	if err != nil {
		return task.Task{}, err
	}
	var result task.Task
	if err := c.getJSON(ctx, "/v1/tasks/"+escapedID, &result); err != nil {
		return task.Task{}, err
	}
	return result, nil
}

func (c *Client) ListTasks(ctx context.Context, limit int) ([]task.Task, error) {
	return c.ListTasksForWorkspace(ctx, "", limit)
}

func (c *Client) ListTasksForWorkspace(ctx context.Context, workspace string, limit int) ([]task.Task, error) {
	path, err := listPath("/v1/tasks", workspace, limit)
	if err != nil {
		return nil, err
	}
	var result []task.Task
	if err := c.getJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func listPath(base string, workspace string, limit int) (string, error) {
	if err := ensureNonNegativeLimit(limit); err != nil {
		return "", err
	}
	values := url.Values{}
	if limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", limit))
	}
	if strings.TrimSpace(workspace) != "" {
		values.Set("workspace", workspace)
	}
	if encoded := values.Encode(); encoded != "" {
		return base + "?" + encoded, nil
	}
	return base, nil
}

func pathID(label string, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	return url.PathEscape(id), nil
}

func ensureNonNegativeLimit(limit int) error {
	if limit < 0 {
		return fmt.Errorf("limit cannot be negative")
	}
	return nil
}

func validateCreateMemoryRequest(request store.CreateMemoryRequest) error {
	if strings.TrimSpace(request.Text) == "" {
		return fmt.Errorf("memory text is required")
	}
	if err := validateMemoryKind(request.Kind); err != nil {
		return err
	}
	return validateMemoryImportance(request.Importance)
}

func validateUpdateMemoryRequest(request store.UpdateMemoryRequest) error {
	if request.Text != nil && strings.TrimSpace(*request.Text) == "" {
		return fmt.Errorf("memory text is required")
	}
	if request.Kind != nil {
		if err := validateMemoryKind(*request.Kind); err != nil {
			return err
		}
	}
	if request.Importance != nil {
		return validateMemoryImportance(*request.Importance)
	}
	return nil
}

func validateMemoryKind(kind string) error {
	switch strings.TrimSpace(kind) {
	case "", "note", "preference", "rule", "automation", "credential_hint":
		return nil
	default:
		return fmt.Errorf("unknown memory kind %q", kind)
	}
}

func validateMemoryImportance(importance int) error {
	if importance == 0 {
		return nil
	}
	if importance < 1 || importance > 5 {
		return fmt.Errorf("memory importance must be between 1 and 5")
	}
	return nil
}

func (c *Client) Events(ctx context.Context, taskID string) ([]task.Event, error) {
	escapedID, err := pathID("task id", taskID)
	if err != nil {
		return nil, err
	}
	var result []task.Event
	if err := c.getJSON(ctx, "/v1/tasks/"+escapedID+"/events", &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) Diff(ctx context.Context, taskID string) (string, error) {
	escapedID, err := pathID("task id", taskID)
	if err != nil {
		return "", err
	}
	var result struct {
		Diff string `json:"diff"`
	}
	if err := c.getJSON(ctx, "/v1/tasks/"+escapedID+"/diff", &result); err != nil {
		return "", err
	}
	return result.Diff, nil
}

func (c *Client) Apply(ctx context.Context, taskID string, patch string) (ApplyResult, error) {
	escapedID, err := pathID("task id", taskID)
	if err != nil {
		return ApplyResult{}, err
	}
	var result ApplyResult
	body := struct {
		Patch string `json:"patch"`
	}{Patch: patch}
	if err := c.postJSON(ctx, "/v1/tasks/"+escapedID+"/apply", body, &result, http.StatusOK); err != nil {
		return ApplyResult{}, err
	}
	return result, nil
}

func (c *Client) Cancel(ctx context.Context, taskID string, reason string) (task.Task, error) {
	escapedID, err := pathID("task id", taskID)
	if err != nil {
		return task.Task{}, err
	}
	var result task.Task
	body := struct {
		Reason string `json:"reason"`
	}{Reason: reason}
	if err := c.postJSON(ctx, "/v1/tasks/"+escapedID+"/cancel", body, &result, http.StatusOK); err != nil {
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

func (c *Client) SendInput(ctx context.Context, taskID string, message string) (task.Task, error) {
	escapedID, err := pathID("task id", taskID)
	if err != nil {
		return task.Task{}, err
	}
	var result task.InputResponse
	body := task.InputRequest{Message: message}
	if err := c.postJSON(ctx, "/v1/tasks/"+escapedID+"/input", body, &result, http.StatusAccepted); err != nil {
		return task.Task{}, err
	}
	return result.Task, nil
}

func (c *Client) submitApproval(ctx context.Context, taskID string, decision string, reason string) (task.Task, error) {
	escapedID, err := pathID("task id", taskID)
	if err != nil {
		return task.Task{}, err
	}
	var result task.Task
	body := struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason,omitempty"`
	}{Decision: decision, Reason: reason}
	if err := c.postJSON(ctx, "/v1/tasks/"+escapedID+"/approval", body, &result, http.StatusOK, http.StatusAccepted); err != nil {
		return task.Task{}, err
	}
	return result, nil
}

func (c *Client) StreamEvents(ctx context.Context, taskID string) (<-chan StreamEvent, <-chan error) {
	events := make(chan StreamEvent)
	errs := make(chan error, 1)
	escapedID, err := pathID("task id", taskID)
	if err != nil {
		close(events)
		errs <- err
		close(errs)
		return events, errs
	}
	go func() {
		defer close(events)
		defer close(errs)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/tasks/"+escapedID+"/events/stream", nil)
		if err != nil {
			errs <- err
			return
		}
		c.authorize(request)
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

func (c *Client) StreamTaskEvents(ctx context.Context, taskIDs []string) (<-chan TaskStreamEvent, <-chan error) {
	events := make(chan TaskStreamEvent)
	errs := make(chan error, 1)
	ids := uniqueTaskIDs(taskIDs)
	if len(ids) == 0 {
		close(events)
		errs <- fmt.Errorf("task ids are required")
		close(errs)
		return events, errs
	}
	go func() {
		defer close(events)
		defer close(errs)
		values := url.Values{}
		for _, id := range ids {
			values.Add("task_id", id)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/tasks/events/stream?"+values.Encode(), nil)
		if err != nil {
			errs <- err
			return
		}
		c.authorize(request)
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
		if err := scanTaskSSE(ctx, response.Body, events); err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func uniqueTaskIDs(taskIDs []string) []string {
	seen := make(map[string]struct{}, len(taskIDs))
	ids := make([]string, 0, len(taskIDs))
	for _, id := range taskIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	c.authorize(request)
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
	return c.sendJSON(ctx, http.MethodPost, path, input, out, allowedStatus...)
}

func (c *Client) patchJSON(ctx context.Context, path string, input any, out any, allowedStatus ...int) error {
	return c.sendJSON(ctx, http.MethodPatch, path, input, out, allowedStatus...)
}

func (c *Client) deleteJSON(ctx context.Context, path string, allowedStatus ...int) error {
	return c.sendJSON(ctx, http.MethodDelete, path, struct{}{}, nil, allowedStatus...)
}

func (c *Client) sendJSON(ctx context.Context, method string, path string, input any, out any, allowedStatus ...int) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	c.authorize(request)
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
		if eventType == "task.error" && !json.Valid([]byte(payload)) {
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

func scanTaskSSE(ctx context.Context, reader io.Reader, events chan<- TaskStreamEvent) error {
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
		if !json.Valid([]byte(payload)) {
			return fmt.Errorf("decode task stream event %q: invalid JSON payload", eventType)
		}
		var envelope struct {
			TaskID  string          `json:"task_id"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			return fmt.Errorf("decode task stream event %q: %w", eventType, err)
		}
		if strings.TrimSpace(envelope.TaskID) == "" {
			return fmt.Errorf("decode task stream event %q: task_id is required", eventType)
		}
		if !json.Valid(envelope.Payload) {
			return fmt.Errorf("decode task stream event %q: invalid task payload", eventType)
		}
		event := task.Event{
			ID:      eventID,
			TaskID:  envelope.TaskID,
			Type:    task.EventType(eventType),
			Payload: string(envelope.Payload),
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case events <- TaskStreamEvent{TaskID: envelope.TaskID, StreamEvent: StreamEvent{Type: task.EventType(eventType), Event: event}}:
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
