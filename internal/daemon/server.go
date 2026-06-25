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
	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

type Config struct {
	Repository *taskpkg.Repository
	Runner     *taskpkg.Runner
}

func NewServer(config Config) http.Handler {
	return newServer(config).routes()
}

func newServer(config Config) *server {
	return &server{
		repo:    config.Repository,
		runner:  config.Runner,
		running: map[string]context.CancelFunc{},
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/capabilities", s.handleCapabilities)
	mux.HandleFunc("/v1/sessions", s.handleSessions)
	mux.HandleFunc("/v1/sessions/", s.handleSession)
	mux.HandleFunc("/v1/tasks", s.handleTasks)
	mux.HandleFunc("/v1/tasks/", s.handleTask)
	return mux
}

type server struct {
	repo      *taskpkg.Repository
	runner    *taskpkg.Runner
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
	writeJSON(w, http.StatusOK, map[string]any{
		"tools": capabilities.BuiltinTools(),
	})
}

func (s *server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		tasks, err := s.repo.List(r.Context(), limit)
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
		sessions, err := s.repo.ListSessions(r.Context(), limit)
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
	writeError(w, http.StatusNotFound, fmt.Errorf("unknown session route %q", r.URL.Path))
}

func (s *server) handleTask(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, errors.New("task id is required"))
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
	if len(parts) == 3 && parts[1] == "events" && parts[2] == "stream" {
		s.writeEventStream(w, r, taskID)
		return
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("unknown task route %q", r.URL.Path))
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
		if event.Type == taskpkg.EventCompleted || event.Type == taskpkg.EventCancelled || event.Type == taskpkg.EventError {
			done = true
		}
	}
	if flusher != nil {
		flusher.Flush()
	}
	return done, lastSeq, nil
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
