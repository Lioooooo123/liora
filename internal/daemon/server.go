package daemon

import (
	"context"
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

type Config struct {
	Repository *taskpkg.Repository
	Runner     *taskpkg.Runner
	Store      *store.Store
}

func NewServer(config Config) http.Handler {
	return newServer(config).routes()
}

func newServer(config Config) *server {
	return &server{
		repo:    config.Repository,
		runner:  config.Runner,
		store:   config.Store,
		running: map[string]context.CancelFunc{},
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/capabilities", s.handleCapabilities)
	mux.HandleFunc("/v1/memories", s.handleMemories)
	mux.HandleFunc("/v1/workbench", s.handleWorkbench)
	mux.HandleFunc("/v1/timeline/search", s.handleTimelineSearch)
	mux.HandleFunc("/v1/sessions", s.handleSessions)
	mux.HandleFunc("/v1/sessions/", s.handleSession)
	mux.HandleFunc("/v1/tasks", s.handleTasks)
	mux.HandleFunc("/v1/tasks/", s.handleTask)
	return mux
}

type server struct {
	repo      *taskpkg.Repository
	runner    *taskpkg.Runner
	store     *store.Store
	runningMu sync.Mutex
	running   map[string]context.CancelFunc
}

const eventStreamFallbackInterval = 5 * time.Second

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
	} else if len(mcpTools) > 0 {
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
		query := r.URL.Query().Get("q")
		var (
			memories []store.Memory
			err      error
		)
		if strings.TrimSpace(query) == "" {
			memories, err = s.store.ListMemories(limit)
		} else {
			memories, err = s.store.SearchMemories(query, limit)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, memories)
	case http.MethodPost:
		var request struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		memory, err := s.store.CreateMemory(request.Text)
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
	tools, err := mcppkg.NewManager(mcppkg.Config{Servers: servers}).ListTools(ctx)
	if err != nil {
		return nil, err
	}
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
	return specs, nil
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
		if task.Status == taskpkg.StatusWaitingUser {
			request, err := s.latestPermissionRequest(r.Context(), task.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			workbench.PendingApprovals = append(workbench.PendingApprovals, taskpkg.PendingApproval{
				Task:    task,
				Request: request,
			})
		}
	}
	writeJSON(w, http.StatusOK, workbench)
}

func (s *server) latestPermissionRequest(ctx context.Context, taskID string) (taskpkg.EventPayload, error) {
	events, err := s.repo.Events(ctx, taskID, 0)
	if err != nil {
		return taskpkg.EventPayload{}, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != taskpkg.EventPermissionRequest {
			continue
		}
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(events[i].Payload), &payload); err != nil {
			return taskpkg.EventPayload{}, err
		}
		return payload, nil
	}
	return taskpkg.EventPayload{}, nil
}

func isActiveStatus(status taskpkg.Status) bool {
	switch status {
	case taskpkg.StatusDraft, taskpkg.StatusPlanning, taskpkg.StatusRunning, taskpkg.StatusWaitingUser:
		return true
	default:
		return false
	}
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
		var request taskpkg.CreateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		task, err := s.repo.Create(r.Context(), request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		_ = s.repo.AppendEvent(r.Context(), task.ID, taskpkg.EventTaskCreated, taskpkg.EventPayload{Message: task.UserInput})
		if request.RunAsync {
			taskID := task.ID
			ctx, cancel := context.WithCancel(context.Background())
			s.registerRunning(taskID, cancel)
			go func() {
				defer s.unregisterRunning(taskID)
				_ = s.runner.Run(ctx, taskID)
			}()
			writeJSON(w, http.StatusAccepted, taskpkg.CreateResponse{Task: task})
			return
		}
		if s.runner != nil {
			if err := s.runner.Run(r.Context(), task.ID); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			task, err = s.repo.Get(r.Context(), task.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
		}
		writeJSON(w, http.StatusCreated, taskpkg.CreateResponse{Task: task})
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
	writeError(w, http.StatusNotFound, fmt.Errorf("unknown session route %q", r.URL.Path))
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
	if len(parts) == 2 && parts[1] == "approval" {
		s.handleTaskApproval(w, r, taskID)
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
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	switch strings.ToLower(strings.TrimSpace(request.Decision)) {
	case "approve", "approved", "yes":
		if err := s.repo.GrantApproval(r.Context(), taskID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		_ = s.repo.AppendEvent(r.Context(), taskID, taskpkg.EventPermissionApproved, taskpkg.EventPayload{
			Message: "Approval granted.",
			Status:  string(taskpkg.StatusDraft),
		})
		task, err := s.repo.Get(r.Context(), taskID)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		if s.runner != nil {
			ctx, cancel := context.WithCancel(context.Background())
			s.registerRunning(taskID, cancel)
			go func() {
				defer s.unregisterRunning(taskID)
				_ = s.runner.Run(ctx, taskID)
			}()
			writeJSON(w, http.StatusAccepted, task)
			return
		}
		writeJSON(w, http.StatusOK, task)
	case "deny", "denied", "no":
		if err := s.repo.DenyApproval(r.Context(), taskID, request.Reason); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		s.cancelRunning(taskID)
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
	task, err := s.repo.Get(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *server) registerRunning(taskID string, cancel context.CancelFunc) {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	s.running[taskID] = cancel
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
			lastSeq[taskID] = nextSeq
			if taskDone {
				done[taskID] = true
			}
		}
		if allDone || len(done) == len(taskIDs) {
			return
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
		case taskpkg.EventPermissionApproved:
			done = false
		case taskpkg.EventCompleted, taskpkg.EventCancelled, taskpkg.EventError, taskpkg.EventPermissionRequest:
			done = true
		}
	}
	if flusher != nil {
		flusher.Flush()
	}
	return done, lastSeq, nil
}

func (s *server) writeAvailableTaskEvents(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, taskID string, afterSeq int64) (bool, int64, error) {
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
		fmt.Fprintf(w, "data: %s\n\n", taskSSEData(taskID, event.Payload))
		switch event.Type {
		case taskpkg.EventPermissionApproved:
			done = false
		case taskpkg.EventCompleted, taskpkg.EventCancelled, taskpkg.EventError, taskpkg.EventPermissionRequest:
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
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
