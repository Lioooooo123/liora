package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Lioooooo123/liora/internal/apply"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

type Config struct {
	Repository *taskpkg.Repository
	Runner     *taskpkg.Runner
}

func NewServer(config Config) http.Handler {
	server := &server{repo: config.Repository, runner: config.Runner}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.handleHealth)
	mux.HandleFunc("/v1/tasks", server.handleTasks)
	mux.HandleFunc("/v1/tasks/", server.handleTask)
	return mux
}

type server struct {
	repo   *taskpkg.Repository
	runner *taskpkg.Runner
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
			go func() {
				_ = s.runner.Run(context.Background(), taskID)
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
	if len(parts) == 3 && parts[1] == "events" && parts[2] == "stream" {
		s.writeEventStream(w, r, taskID)
		return
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("unknown task route %q", r.URL.Path))
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
	events, err := s.repo.Events(r.Context(), taskID, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	for _, event := range events {
		fmt.Fprintf(w, "event: %s\n", event.Type)
		fmt.Fprintf(w, "id: %s\n", event.ID)
		fmt.Fprintf(w, "data: %s\n\n", event.Payload)
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
