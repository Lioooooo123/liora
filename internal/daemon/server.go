package daemon

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Lioooooo123/liora/internal/apply"
	"github.com/Lioooooo123/liora/internal/capabilities"
	mcppkg "github.com/Lioooooo123/liora/internal/mcp"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

var errTaskAlreadyRunning = errors.New("task is already running")

type Config struct {
	Repository *taskpkg.Repository
	Runner     *taskpkg.Runner
	Store      *store.Store
	AuthToken  string
	Background BackgroundLimits
	Foreground ForegroundLimits
}

type BackgroundLimits struct {
	MaxConcurrent int
	MaxActive     int
}

type ForegroundLimits struct {
	MaxConcurrent   int
	MaxActive       int
	MaxPerWorkspace int
}

func NewServer(config Config) http.Handler {
	return newServer(config).routes()
}

func newServer(config Config) *server {
	s := &server{
		repo:       config.Repository,
		runner:     config.Runner,
		store:      config.Store,
		authToken:  strings.TrimSpace(config.AuthToken),
		running:    map[string]context.CancelFunc{},
		background: normalizeBackgroundLimits(config.Background),
		foreground: normalizeForegroundLimits(config.Foreground),
	}
	if s.repo != nil {
		_, _ = s.repo.MarkLostBackgroundTasks(context.Background(), "daemon restarted without a running handle")
		_, _ = s.repo.ExplainRestartState(context.Background(), "daemon restarted without a running handle")
	}
	if s.runner != nil && s.store != nil {
		s.runner.SetStore(s.store)
	}
	return s
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/capabilities", s.handleCapabilities)
	mux.HandleFunc("/v1/memories", s.requireCapability(s.handleMemories))
	mux.HandleFunc("/v1/memories/", s.requireCapability(s.handleMemory))
	mux.HandleFunc("/v1/permission-rules", s.requireCapability(s.handlePermissionRules))
	mux.HandleFunc("/v1/permission-rules/", s.requireCapability(s.handlePermissionRule))
	mux.HandleFunc("/v1/workbench", s.requireCapability(s.handleWorkbench))
	mux.HandleFunc("/v1/artifacts/page", s.requireCapability(s.handleArtifactPage))
	mux.HandleFunc("/v1/timeline/search", s.requireCapability(s.handleTimelineSearch))
	mux.HandleFunc("/v1/threads", s.requireCapability(s.handleThreads))
	mux.HandleFunc("/v1/threads/", s.requireCapability(s.handleThread))
	mux.HandleFunc("/v1/sessions", s.requireCapability(s.handleSessions))
	mux.HandleFunc("/v1/sessions/", s.requireCapability(s.handleSession))
	mux.HandleFunc("/v1/tasks", s.requireCapability(s.handleTasks))
	mux.HandleFunc("/v1/tasks/", s.requireCapability(s.handleTask))
	return mux
}

type server struct {
	repo       *taskpkg.Repository
	runner     *taskpkg.Runner
	store      *store.Store
	authToken  string
	runningMu  sync.Mutex
	running    map[string]context.CancelFunc
	background BackgroundLimits
	foreground ForegroundLimits
}

const (
	eventStreamFallbackInterval = 5 * time.Second
	taskEventStreamBatchLimit   = 16
)

func normalizeBackgroundLimits(limits BackgroundLimits) BackgroundLimits {
	if limits.MaxConcurrent <= 0 {
		limits.MaxConcurrent = 4
	}
	if limits.MaxActive <= 0 {
		limits.MaxActive = 32
	}
	if limits.MaxActive < limits.MaxConcurrent {
		limits.MaxActive = limits.MaxConcurrent
	}
	return limits
}

func normalizeForegroundLimits(limits ForegroundLimits) ForegroundLimits {
	if limits.MaxConcurrent <= 0 {
		limits.MaxConcurrent = 4
	}
	if limits.MaxActive <= 0 {
		limits.MaxActive = 64
	}
	if limits.MaxActive < limits.MaxConcurrent {
		limits.MaxActive = limits.MaxConcurrent
	}
	if limits.MaxPerWorkspace <= 0 {
		limits.MaxPerWorkspace = limits.MaxConcurrent
	}
	return limits
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) requireCapability(next http.HandlerFunc) http.HandlerFunc {
	if strings.TrimSpace(s.authToken) == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, errors.New("daemon capability token is required"))
			return
		}
		next(w, r)
	}
}

func (s *server) authorized(r *http.Request) bool {
	token := strings.TrimSpace(r.Header.Get("X-Liora-Capability"))
	if token == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			token = strings.TrimSpace(auth[len("bearer "):])
		}
	}
	if token == "" || s.authToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.authToken)) == 1
}

func (s *server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	body := map[string]any{"tools": capabilities.BuiltinTools()}
	mcpTools, err := s.mcpTools(r.Context())
	if err != nil {
		body["mcp_error"] = err.Error()
	}
	if len(mcpTools) > 0 {
		body["mcp_tools"] = mcpTools
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *server) handleMemories(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusInternalServerError, errors.New("store is not configured"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		includeDisabled := truthyQuery(r.URL.Query().Get("include_disabled"))
		includeExpired := truthyQuery(r.URL.Query().Get("include_expired"))
		memories, err := s.store.ListMemoriesWithOptions(store.MemoryListOptions{
			Query:           r.URL.Query().Get("q"),
			Workspace:       r.URL.Query().Get("workspace"),
			Limit:           limit,
			IncludeDisabled: includeDisabled,
			IncludeExpired:  includeExpired,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, memories)
	case http.MethodPost:
		var request store.CreateMemoryRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		memory, err := s.store.CreateMemoryWithOptions(request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, memory)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func (s *server) handleMemory(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusInternalServerError, errors.New("store is not configured"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/memories/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, errors.New("memory id is required"))
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			memory, err := s.store.GetMemory(id)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, memory)
		case http.MethodPatch:
			var request store.UpdateMemoryRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			memory, err := s.store.UpdateMemory(id, request)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, memory)
		case http.MethodDelete:
			if err := s.store.DeleteMemoryForWorkspace(id, r.URL.Query().Get("workspace")); err != nil {
				writeStoreError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "GET, PATCH, DELETE")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		}
		return
	}
	if len(parts) == 2 && (parts[1] == "disable" || parts[1] == "enable") {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		memory, err := s.store.SetMemoryEnabled(id, parts[1] == "enable")
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, memory)
		return
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("unknown memory route %q", r.URL.Path))
}

func (s *server) handlePermissionRules(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusInternalServerError, errors.New("store is not configured"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		rules, err := s.store.ListPermissionRules(store.PermissionRuleListOptions{
			Workspace:       r.URL.Query().Get("workspace"),
			SessionID:       r.URL.Query().Get("session_id"),
			Limit:           limit,
			IncludeDisabled: truthyQuery(r.URL.Query().Get("include_disabled")),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, rules)
	case http.MethodPost:
		var request store.CreatePermissionRuleRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		rule, err := s.store.CreatePermissionRule(request)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, rule)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func (s *server) handlePermissionRule(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusInternalServerError, errors.New("store is not configured"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/permission-rules/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, errors.New("permission rule id is required"))
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if err := s.store.DeletePermissionRule(parts[0]); err != nil {
			writeStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "DELETE")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func truthyQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "all":
		return true
	default:
		return false
	}
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if errors.Is(err, store.ErrCrossWorkspaceAuthorizationRequired) {
		writeError(w, http.StatusForbidden, err)
		return
	}
	writeError(w, http.StatusBadRequest, err)
}

func (s *server) handleThreads(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusInternalServerError, errors.New("store is not configured"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		threads, err := s.store.ListConversationThreadsWithOptions(store.ConversationThreadListOptions{
			Workspace:       r.URL.Query().Get("workspace"),
			Limit:           limit,
			IncludeArchived: truthyQuery(r.URL.Query().Get("include_archived")),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, threads)
	case http.MethodPost:
		var request store.CreateConversationThreadRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		thread, err := s.store.CreateConversationThread(request)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, thread)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func (s *server) handleThread(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusInternalServerError, errors.New("store is not configured"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/threads/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown thread route %q", r.URL.Path))
		return
	}
	threadID := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			thread, err := s.store.GetConversationThread(threadID)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, thread)
		case http.MethodPatch:
			var request store.UpdateConversationThreadRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			thread, err := s.store.UpdateConversationThread(threadID, request)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, thread)
		default:
			w.Header().Set("Allow", "GET, PATCH")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		}
		return
	}
	if len(parts) == 2 && parts[1] == "model" {
		s.handleThreadModel(w, r, threadID)
		return
	}
	if len(parts) != 2 || parts[1] != "messages" {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown thread route %q", r.URL.Path))
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		messages, err := s.store.ListCrossThreadMessages(threadID, limit)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, messages)
	case http.MethodPost:
		var request store.CreateCrossThreadMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(request.ToThreadID) != "" && strings.TrimSpace(request.ToThreadID) != threadID {
			writeError(w, http.StatusBadRequest, errors.New("to_thread_id must match route thread id"))
			return
		}
		request.ToThreadID = threadID
		message, err := s.store.CreateCrossThreadMessage(request)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if err := s.recordCrossThreadMessageTranscript(r.Context(), message); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, message)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func (s *server) handleThreadModel(w http.ResponseWriter, r *http.Request, threadID string) {
	switch r.Method {
	case http.MethodGet:
		config, ok, err := s.store.GetThreadModelConfig(threadID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, sql.ErrNoRows)
			return
		}
		writeJSON(w, http.StatusOK, config)
	case http.MethodPut, http.MethodPatch:
		var request store.UpdateThreadModelConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		config, err := s.store.UpdateThreadModelConfig(threadID, request)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, config)
	case http.MethodDelete:
		if err := s.store.DeleteThreadModelConfig(threadID); err != nil {
			writeStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT, PATCH, DELETE")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func (s *server) recordCrossThreadMessageTranscript(ctx context.Context, message store.CrossThreadMessage) error {
	if s.repo == nil || s.store == nil {
		return nil
	}
	fromThread, err := s.store.GetConversationThread(message.FromThreadID)
	if err != nil {
		return err
	}
	toThread, err := s.store.GetConversationThread(message.ToThreadID)
	if err != nil {
		return err
	}
	if _, err := s.repo.EnsureSession(ctx, fromThread.ID, fromThread.Title, fromThread.Workspace); err != nil {
		return err
	}
	if _, err := s.repo.EnsureSession(ctx, toThread.ID, toThread.Title, toThread.Workspace); err != nil {
		return err
	}
	sentContent := crossThreadTranscriptContent("thread_message.sent", message, message.ToThreadID)
	if _, err := s.repo.AppendMessage(ctx, message.FromThreadID, "thread_message.sent", sentContent, message.TaskID); err != nil {
		return err
	}
	receivedContent := crossThreadTranscriptContent("thread_message.received", message, message.FromThreadID)
	if _, err := s.repo.AppendMessage(ctx, message.ToThreadID, "thread_message.received", receivedContent, message.TaskID); err != nil {
		return err
	}
	linkContent := crossThreadTranscriptContent("thread_link.created", message, message.ToThreadID)
	if _, err := s.repo.AppendMessage(ctx, message.FromThreadID, "thread_link.created", linkContent, message.TaskID); err != nil {
		return err
	}
	linkContent = crossThreadTranscriptContent("thread_link.created", message, message.FromThreadID)
	if _, err := s.repo.AppendMessage(ctx, message.ToThreadID, "thread_link.created", linkContent, message.TaskID); err != nil {
		return err
	}
	return nil
}

func crossThreadTranscriptContent(event string, message store.CrossThreadMessage, peerThreadID string) string {
	parts := []string{
		event,
		"message_id=" + message.ID,
		"peer_thread_id=" + peerThreadID,
	}
	if strings.TrimSpace(message.Summary) != "" {
		parts = append(parts, "summary="+message.Summary)
	}
	if strings.TrimSpace(message.TaskID) != "" {
		parts = append(parts, "task_id="+message.TaskID)
	}
	if strings.TrimSpace(message.ExplicitContent) != "" {
		parts = append(parts, "content="+message.ExplicitContent)
	}
	if len(message.ArtifactRefs) > 0 {
		parts = append(parts, fmt.Sprintf("artifact_refs=%d", len(message.ArtifactRefs)))
	}
	if message.CrossWorkspaceAuthorized {
		parts = append(parts, "cross_workspace_authorized=true")
	}
	return strings.Join(parts, "\n")
}

func (s *server) mcpTools(ctx context.Context) ([]capabilities.MCPToolSpec, error) {
	if s.store == nil {
		return nil, nil
	}
	config, err := s.store.LoadMCPConfig()
	if err != nil {
		return nil, err
	}
	if len(config.Servers) == 0 {
		return nil, nil
	}
	servers := make(map[string]mcppkg.ServerConfig, len(config.Servers))
	for name, server := range config.Servers {
		servers[name] = mcppkg.ServerConfig{
			Command: server.Command,
			Args:    server.Args,
			Env:     server.Env,
		}
	}
	tools, err := mcppkg.NewManager(mcppkg.Config{Servers: servers}).ListToolsDetailed(ctx)
	specs := make([]capabilities.MCPToolSpec, 0, len(tools))
	for _, tool := range tools {
		specs = append(specs, capabilities.MCPToolSpec{
			Server:      tool.Server,
			Name:        tool.Name,
			Usage:       "mcp " + tool.Server + " " + tool.Name + " <json arguments>",
			Description: tool.Description,
			Kind:        capabilities.ToolExternal,
			InputSchema: tool.InputSchema,
		})
	}
	return specs, err
}

func (s *server) handleWorkbench(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	workspace := r.URL.Query().Get("workspace")
	sessions, err := s.repo.ListSessionsByWorkspace(r.Context(), workspace, 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	tasks, err := s.repo.ListByWorkspace(r.Context(), workspace, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	workbench := taskpkg.Workbench{
		Workspace:   workspace,
		Sessions:    sessions,
		RecentTasks: tasks,
	}
	for _, task := range tasks {
		if isActiveStatus(task.Status) {
			workbench.ActiveTasks = append(workbench.ActiveTasks, task)
		}
		if task.Status == taskpkg.StatusQueued {
			workbench.QueuedTasks = append(workbench.QueuedTasks, task)
		}
		if task.Status == taskpkg.StatusWaitingUser {
			eventType, request, err := s.latestWaitingRequest(r.Context(), task.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			switch eventType {
			case taskpkg.EventPermissionRequest:
				approval := taskpkg.PendingApproval{
					Task:    task,
					Request: request,
				}
				if item, ok, err := s.repo.ApprovalItemForTask(r.Context(), task.ID); err != nil {
					writeError(w, http.StatusInternalServerError, err)
					return
				} else if ok {
					approval.Item = item
				}
				workbench.PendingApprovals = append(workbench.PendingApprovals, approval)
			case taskpkg.EventUserInputRequest:
				workbench.PendingUserInputs = append(workbench.PendingUserInputs, taskpkg.PendingInput{
					Task:    task,
					Request: request,
				})
			}
		}
	}
	if s.store != nil {
		threads, err := s.store.ListConversationThreads(workspace, 100)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		workbench.Threads = threadWorkbenches(threads, tasks, workbench.PendingApprovals, workbench.PendingUserInputs)
	}
	writeJSON(w, http.StatusOK, workbench)
}

func isActiveStatus(status taskpkg.Status) bool {
	switch status {
	case taskpkg.StatusDraft, taskpkg.StatusQueued, taskpkg.StatusPlanning, taskpkg.StatusRunning, taskpkg.StatusWaitingUser, taskpkg.StatusLost, taskpkg.StatusRecovered:
		return true
	default:
		return false
	}
}

func threadWorkbenches(threads []store.ConversationThread, tasks []taskpkg.Task, approvals []taskpkg.PendingApproval, inputs []taskpkg.PendingInput) []taskpkg.ThreadWorkbench {
	summaries := make([]taskpkg.ThreadWorkbench, 0, len(threads))
	for _, thread := range threads {
		summary := taskpkg.ThreadWorkbench{
			ID:                  thread.ID,
			Title:               thread.Title,
			Workspace:           thread.Workspace,
			LastTaskID:          thread.LastTaskID,
			ModelConfig:         threadWorkbenchModelConfig(thread.ModelConfig),
			TranscriptSessionID: thread.ID,
			ContextSessionID:    thread.ID,
			Lifecycle:           "idle",
		}
		for _, task := range tasks {
			if task.SessionID != thread.ID {
				continue
			}
			summary.RecentTasks = append(summary.RecentTasks, task)
			if isActiveStatus(task.Status) {
				summary.ActiveTasks = append(summary.ActiveTasks, task)
			}
			if task.Status == taskpkg.StatusQueued {
				summary.QueuedTasks = append(summary.QueuedTasks, task)
			}
		}
		for _, approval := range approvals {
			if approval.Task.SessionID == thread.ID {
				summary.PendingApprovals = append(summary.PendingApprovals, approval)
			}
		}
		for _, input := range inputs {
			if input.Task.SessionID == thread.ID {
				summary.PendingUserInputs = append(summary.PendingUserInputs, input)
			}
		}
		summary.Lifecycle = threadLifecycle(summary)
		summaries = append(summaries, summary)
	}
	return summaries
}

func threadWorkbenchModelConfig(config *store.ThreadModelConfig) *taskpkg.ThreadModelConfig {
	if config == nil {
		return nil
	}
	return &taskpkg.ThreadModelConfig{
		ThreadID:              config.ThreadID,
		Provider:              config.Provider,
		Model:                 config.Model,
		BaseURL:               config.BaseURL,
		Profile:               config.Profile,
		InheritedFromThreadID: config.InheritedFromThreadID,
	}
}

func threadLifecycle(thread taskpkg.ThreadWorkbench) string {
	if len(thread.PendingApprovals) > 0 || len(thread.PendingUserInputs) > 0 {
		return string(taskpkg.StatusWaitingUser)
	}
	for _, task := range thread.ActiveTasks {
		if task.Status != taskpkg.StatusQueued {
			return "active"
		}
	}
	if len(thread.QueuedTasks) > 0 {
		return string(taskpkg.StatusQueued)
	}
	for _, task := range thread.RecentTasks {
		switch task.Status {
		case taskpkg.StatusCompleted, taskpkg.StatusCancelled, taskpkg.StatusFailed, taskpkg.StatusStale:
			return string(task.Status)
		}
	}
	return "idle"
}

func (s *server) handleTimelineSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	query := r.URL.Query().Get("q")
	workspace := r.URL.Query().Get("workspace")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.repo.SearchTimeline(r.Context(), workspace, query, limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		workspace := r.URL.Query().Get("workspace")
		tasks, err := s.repo.ListByWorkspace(r.Context(), workspace, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, tasks)
	case http.MethodPost:
		s.handleTaskCreate(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func (s *server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		workspace := r.URL.Query().Get("workspace")
		sessions, err := s.repo.ListSessionsByWorkspace(r.Context(), workspace, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, sessions)
	case http.MethodPost:
		var request taskpkg.CreateSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		session, err := s.repo.CreateSession(r.Context(), request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, taskpkg.CreateSessionResponse{Session: session})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, errors.New("session id is required"))
		return
	}
	sessionID := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		session, err := s.repo.GetSession(r.Context(), sessionID)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, session)
		return
	}
	if len(parts) == 2 && parts[1] == "messages" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		messages, err := s.repo.Messages(r.Context(), sessionID, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, messages)
		return
	}
	if len(parts) == 2 && parts[1] == "tasks" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		tasks, err := s.repo.ListBySession(r.Context(), sessionID, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, tasks)
		return
	}
	if len(parts) == 2 && parts[1] == "timeline" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		timeline, err := s.repo.Timeline(r.Context(), sessionID, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, timeline)
		return
	}
	if len(parts) == 2 && parts[1] == "todos" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		todos, err := s.repo.TodosBySession(r.Context(), sessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, todos)
		return
	}
	if len(parts) == 2 && parts[1] == "context" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		limit, err := optionalPositiveInt(r.URL.Query().Get("limit"), "limit")
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		tokenBudget, err := optionalPositiveInt(r.URL.Query().Get("token_budget"), "token_budget")
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		envelope, err := s.repo.ContextEnvelope(r.Context(), sessionID, taskpkg.ContextRequest{ItemLimit: limit, TokenBudget: tokenBudget})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, envelope)
		return
	}
	if len(parts) == 2 && parts[1] == "compact" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		var request taskpkg.CompactRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		result, err := s.repo.CompactSession(r.Context(), sessionID, request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("unknown session route %q", r.URL.Path))
}

func optionalPositiveInt(value string, field string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", field)
	}
	return parsed, nil
}

func (s *server) handleTask(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, errors.New("task id is required"))
		return
	}
	if len(parts) == 2 && parts[0] == "events" && parts[1] == "stream" {
		s.handleTasksEventStream(w, r)
		return
	}
	taskID := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		task, err := s.repo.Get(r.Context(), taskID)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
		return
	}
	if len(parts) == 2 && parts[1] == "events" {
		events, err := s.repo.Events(r.Context(), taskID, 0)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, events)
		return
	}
	if len(parts) == 2 && parts[1] == "diff" {
		s.handleTaskDiff(w, r, taskID)
		return
	}
	if len(parts) == 2 && parts[1] == "apply" {
		s.handleTaskApply(w, r, taskID)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" {
		s.handleTaskCancel(w, r, taskID)
		return
	}
	if len(parts) == 2 && parts[1] == "recover" {
		s.handleTaskRecover(w, r, taskID)
		return
	}
	if len(parts) == 2 && parts[1] == "approval" {
		s.handleTaskApproval(w, r, taskID)
		return
	}
	if len(parts) == 2 && parts[1] == "input" {
		s.handleTaskInput(w, r, taskID)
		return
	}
	if len(parts) == 3 && parts[1] == "events" && parts[2] == "stream" {
		s.writeEventStream(w, r, taskID)
		return
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("unknown task route %q", r.URL.Path))
}

func (s *server) handleTasksEventStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	taskIDs := taskIDsFromQuery(r)
	if len(taskIDs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("task_id is required"))
		return
	}
	s.writeTaskEventStream(w, r, taskIDs)
}

func (s *server) handleTaskApproval(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	var request struct {
		Decision  string `json:"decision"`
		Reason    string `json:"reason"`
		DecidedBy string `json:"decided_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	switch strings.ToLower(strings.TrimSpace(request.Decision)) {
	case "approve", "approved", "yes":
		current, err := s.repo.Get(r.Context(), taskID)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		if current.Status != taskpkg.StatusWaitingUser {
			if s.isRunning(taskID) {
				writeError(w, http.StatusConflict, errTaskAlreadyRunning)
				return
			}
			writeError(w, http.StatusConflict, fmt.Errorf("task is not waiting for approval: %s", current.Status))
			return
		}
		if !s.latestWaitIsApproval(r.Context(), taskID) {
			writeError(w, http.StatusConflict, errors.New("task is waiting for user input; use /input instead"))
			return
		}
		if err := s.repo.GrantApproval(r.Context(), taskID, request.DecidedBy); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		task, err := s.repo.Get(r.Context(), taskID)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		if s.runner != nil {
			if queued, err := s.queueBackgroundIfLimited(r.Context(), task, "Approved background task queued by the concurrency limit."); err != nil {
				writeError(w, http.StatusConflict, err)
				return
			} else if queued {
				_ = s.repo.AppendEvent(r.Context(), taskID, taskpkg.EventPermissionApproved, taskpkg.EventPayload{
					Message: "Approval granted.",
					Status:  string(taskpkg.StatusQueued),
				})
				task, _ = s.repo.Get(r.Context(), taskID)
				writeJSON(w, http.StatusAccepted, task)
				return
			}
			if err := s.startTaskAsync(taskID); err != nil {
				writeError(w, http.StatusConflict, err)
				return
			}
			_ = s.repo.AppendEvent(r.Context(), taskID, taskpkg.EventPermissionApproved, taskpkg.EventPayload{
				Message: "Approval granted.",
				Status:  string(taskpkg.StatusDraft),
			})
			writeJSON(w, http.StatusAccepted, task)
			return
		}
		_ = s.repo.AppendEvent(r.Context(), taskID, taskpkg.EventPermissionApproved, taskpkg.EventPayload{
			Message: "Approval granted.",
			Status:  string(taskpkg.StatusDraft),
		})
		writeJSON(w, http.StatusOK, task)
	case "deny", "denied", "no":
		current, err := s.repo.Get(r.Context(), taskID)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		if current.Status != taskpkg.StatusWaitingUser {
			writeError(w, http.StatusConflict, fmt.Errorf("task is not waiting for approval: %s", current.Status))
			return
		}
		if !s.latestWaitIsApproval(r.Context(), taskID) {
			writeError(w, http.StatusConflict, errors.New("task is waiting for user input; use /input instead"))
			return
		}
		if err := s.repo.DenyApproval(r.Context(), taskID, request.Reason, request.DecidedBy); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		s.cancelRunning(taskID)
		s.startNextQueuedAfter(taskID)
		s.startNextForegroundQueued()
		task, err := s.repo.Get(r.Context(), taskID)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	default:
		writeError(w, http.StatusBadRequest, errors.New("decision must be approve or deny"))
	}
}

func (s *server) handleTaskCancel(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&request)
	}
	if err := s.repo.Cancel(r.Context(), taskID, request.Reason); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.cancelRunning(taskID)
	s.startNextQueuedAfter(taskID)
	s.startNextForegroundQueued()
	s.startNextBackgroundQueued()
	task, err := s.repo.Get(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *server) handleTaskRecover(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	if s.runner == nil {
		writeError(w, http.StatusBadRequest, errors.New("runner is not configured"))
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&request)
	}
	if err := s.repo.RecoverLostTask(r.Context(), taskID, request.Reason); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	task, err := s.repo.Get(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if queued, err := s.queueBackgroundIfLimited(r.Context(), task, "Recovered background task queued by the concurrency limit."); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	} else if queued {
		task, _ = s.repo.Get(r.Context(), taskID)
		writeJSON(w, http.StatusAccepted, task)
		return
	}
	if err := s.startTaskAsync(taskID); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	task, _ = s.repo.Get(r.Context(), taskID)
	writeJSON(w, http.StatusAccepted, task)
}

func (s *server) tryRegisterRunning(taskID string, cancel context.CancelFunc) bool {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	if s.running[taskID] != nil {
		return false
	}
	s.running[taskID] = cancel
	return true
}

func (s *server) startTaskAsync(taskID string) error {
	if s.runner == nil {
		return errors.New("runner is not configured")
	}
	ctx, cancel := context.WithCancel(context.Background())
	if !s.tryRegisterRunning(taskID, cancel) {
		cancel()
		return errTaskAlreadyRunning
	}
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				message := fmt.Sprintf("task runner panic: %v", recovered)
				_ = s.repo.UpdateStatus(context.Background(), taskID, taskpkg.StatusFailed)
				_ = s.repo.AppendEvent(context.Background(), taskID, taskpkg.EventError, taskpkg.EventPayload{
					Message: message,
					Status:  string(taskpkg.StatusFailed),
					Reason:  "panic_recovered",
				})
			}
			s.unregisterRunning(taskID)
			s.startNextQueuedAfter(taskID)
			s.startNextForegroundQueued()
			s.startNextBackgroundQueued()
		}()
		_ = s.runner.Run(ctx, taskID)
	}()
	return nil
}

func (s *server) unregisterRunning(taskID string) {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	delete(s.running, taskID)
}

func (s *server) cancelRunning(taskID string) {
	s.runningMu.Lock()
	cancel := s.running[taskID]
	s.runningMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *server) isRunning(taskID string) bool {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	return s.running[taskID] != nil
}

func (s *server) handleTaskDiff(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	events, err := s.repo.Events(r.Context(), taskID, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != taskpkg.EventDiff {
			continue
		}
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(events[i].Payload), &payload); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"diff": payload.Diff})
		return
	}
	writeError(w, http.StatusNotFound, errors.New("task has no diff"))
}

func (s *server) handleTaskApply(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	var request struct {
		Patch string `json:"patch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	task, err := s.repo.Get(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	result, err := apply.ApplyUnifiedPatch(task.Workspace, request.Patch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	_ = s.repo.AppendEvent(r.Context(), taskID, taskpkg.EventPatchApply, taskpkg.EventPayload{
		Message: "applied patch to " + strings.Join(result.Files, ", "),
		Diff:    request.Patch,
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *server) writeEventStream(w http.ResponseWriter, r *http.Request, taskID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	var lastSeq int64
	for {
		done, nextSeq, err := s.writeAvailableEvents(r.Context(), w, flusher, taskID, lastSeq)
		if err != nil {
			fmt.Fprintf(w, "event: task.error\ndata: %s\n\n", quoteSSEData(err.Error()))
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		if done {
			return
		}
		lastSeq = nextSeq
		notification, unsubscribe := s.repo.SubscribeEvents(r.Context(), taskID)
		timer := time.NewTimer(eventStreamFallbackInterval)
		select {
		case <-r.Context().Done():
			timer.Stop()
			unsubscribe()
			return
		case <-notification:
			timer.Stop()
			unsubscribe()
		case <-timer.C:
			unsubscribe()
		}
	}
}

func (s *server) writeTaskEventStream(w http.ResponseWriter, r *http.Request, taskIDs []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	lastSeq := make(map[string]int64, len(taskIDs))
	done := make(map[string]bool, len(taskIDs))
	for {
		allDone := true
		advanced := false
		for _, taskID := range taskIDs {
			if done[taskID] {
				continue
			}
			allDone = false
			taskDone, nextSeq, err := s.writeAvailableTaskEvents(r.Context(), w, flusher, taskID, lastSeq[taskID])
			if err != nil {
				fmt.Fprintf(w, "event: task.error\ndata: %s\n\n", taskSSEData(taskID, quoteSSEData(err.Error())))
				if flusher != nil {
					flusher.Flush()
				}
				return
			}
			if nextSeq != lastSeq[taskID] {
				advanced = true
			}
			lastSeq[taskID] = nextSeq
			if taskDone {
				done[taskID] = true
			}
		}
		if allDone || len(done) == len(taskIDs) {
			return
		}
		if advanced {
			continue
		}
		var pending []string
		for _, taskID := range taskIDs {
			if !done[taskID] {
				pending = append(pending, taskID)
			}
		}
		notification, unsubscribe := s.repo.SubscribeEventsAny(r.Context(), pending)
		timer := time.NewTimer(eventStreamFallbackInterval)
		select {
		case <-r.Context().Done():
			timer.Stop()
			unsubscribe()
			return
		case <-notification:
			timer.Stop()
			unsubscribe()
		case <-timer.C:
			unsubscribe()
		}
	}
}

func (s *server) writeAvailableEvents(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, taskID string, afterSeq int64) (bool, int64, error) {
	events, err := s.repo.EventsAfter(ctx, taskID, afterSeq, 0)
	if err != nil {
		return false, afterSeq, err
	}
	done := false
	lastSeq := afterSeq
	for _, event := range events {
		lastSeq = event.Seq
		fmt.Fprintf(w, "event: %s\n", event.Type)
		fmt.Fprintf(w, "id: %s\n", event.ID)
		fmt.Fprintf(w, "data: %s\n\n", event.Payload)
		switch event.Type {
		case taskpkg.EventPermissionApproved, taskpkg.EventUserInputReceived:
			done = false
		case taskpkg.EventCompleted, taskpkg.EventCancelled, taskpkg.EventError, taskpkg.EventPermissionRequest, taskpkg.EventUserInputRequest:
			done = true
		}
	}
	if flusher != nil {
		flusher.Flush()
	}
	return done, lastSeq, nil
}

func (s *server) writeAvailableTaskEvents(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, taskID string, afterSeq int64) (bool, int64, error) {
	events, err := s.repo.EventsAfter(ctx, taskID, afterSeq, taskEventStreamBatchLimit)
	if err != nil {
		return false, afterSeq, err
	}
	done := false
	lastSeq := afterSeq
	for _, event := range events {
		lastSeq = event.Seq
		fmt.Fprintf(w, "event: %s\n", event.Type)
		fmt.Fprintf(w, "id: %s\n", event.ID)
		fmt.Fprintf(w, "data: %s\n\n", taskSSEData(taskID, event.Payload))
		switch event.Type {
		case taskpkg.EventPermissionApproved:
			done = false
		case taskpkg.EventCompleted, taskpkg.EventCancelled, taskpkg.EventError:
			done = true
		}
	}
	if flusher != nil {
		flusher.Flush()
	}
	return done, lastSeq, nil
}

func taskSSEData(taskID string, payload string) string {
	data, _ := json.Marshal(struct {
		TaskID  string          `json:"task_id"`
		Payload json.RawMessage `json:"payload"`
	}{
		TaskID:  taskID,
		Payload: json.RawMessage(payload),
	})
	return string(data)
}

func taskIDsFromQuery(r *http.Request) []string {
	var ids []string
	ids = append(ids, r.URL.Query()["task_id"]...)
	for _, value := range r.URL.Query()["ids"] {
		for _, id := range strings.Split(value, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				ids = append(ids, id)
			}
		}
	}
	return uniqueStrings(ids)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func quoteSSEData(value string) string {
	data, _ := json.Marshal(map[string]string{"message": value})
	return string(data)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		err = errors.New("task not found")
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
