package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Lioooooo123/liora/internal/store"
	"github.com/Lioooooo123/liora/internal/task"
)

type scheduleTriggerRequest struct {
	SessionID      string                     `json:"session_id,omitempty"`
	ThreadID       string                     `json:"thread_id,omitempty"`
	Risk           task.AutomationRisk        `json:"risk,omitempty"`
	Source         string                     `json:"source,omitempty"`
	CatchUpPolicy  task.ScheduleCatchUpPolicy `json:"catch_up_policy,omitempty"`
	MissedRuns     int                        `json:"missed_runs,omitempty"`
	MaxCatchUpRuns int                        `json:"max_catch_up_runs,omitempty"`
}

func (s *server) handleSchedules(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusInternalServerError, errors.New("store is not configured"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		schedules, err := s.store.ListSchedules(store.ScheduleListOptions{
			Workspace:       r.URL.Query().Get("workspace"),
			Limit:           limit,
			IncludeDisabled: truthyQuery(r.URL.Query().Get("include_disabled")),
		})
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, schedules)
	case http.MethodPost:
		var request store.CreateScheduleRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		schedule, err := s.store.CreateSchedule(request)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, schedule)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func (s *server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusInternalServerError, errors.New("store is not configured"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/schedules/")
	path = strings.TrimSuffix(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 && parts[0] != "" {
		s.handleScheduleResource(w, r, parts[0])
		return
	}
	if len(parts) != 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown schedule route %q", r.URL.Path))
		return
	}
	switch parts[1] {
	case "pause", "resume":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		schedule, err := s.store.SetScheduleEnabled(parts[0], parts[1] == "resume")
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, schedule)
	case "trigger":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		s.handleScheduleTrigger(w, r, parts[0])
	default:
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown schedule route %q", r.URL.Path))
	}
}

func (s *server) handleScheduleResource(w http.ResponseWriter, r *http.Request, scheduleID string) {
	switch r.Method {
	case http.MethodGet:
		schedule, err := s.store.GetSchedule(scheduleID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, schedule)
	case http.MethodPatch:
		var request store.UpdateScheduleRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		schedule, err := s.store.UpdateSchedule(scheduleID, request)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, schedule)
	case http.MethodDelete:
		if err := s.store.DeleteSchedule(scheduleID); err != nil {
			writeStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func (s *server) handleScheduleTrigger(w http.ResponseWriter, r *http.Request, scheduleID string) {
	if s.store == nil {
		writeError(w, http.StatusBadRequest, errors.New("schedule store is not configured"))
		return
	}
	if s.repo == nil {
		writeError(w, http.StatusBadRequest, errors.New("task repository is not configured"))
		return
	}
	var request scheduleTriggerRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	createRequest, err := s.scheduleTriggerCreateRequest(r, scheduleID, request)
	if err != nil {
		writeScheduleTriggerError(w, err)
		return
	}
	s.handleTaskCreateWithRequest(w, r, createRequest)
}

func (s *server) scheduleTriggerCreateRequest(r *http.Request, scheduleID string, request scheduleTriggerRequest) (task.CreateRequest, error) {
	risk, err := normalizeScheduleTriggerRisk(request.Risk)
	if err != nil {
		return task.CreateRequest{}, err
	}
	normalizedSchedule, err := task.NormalizeScheduleMetadata(task.OriginSchedule, task.ScheduleMetadata{
		ID:             scheduleID,
		CatchUpPolicy:  request.CatchUpPolicy,
		MissedRuns:     request.MissedRuns,
		MaxCatchUpRuns: request.MaxCatchUpRuns,
	})
	if err != nil {
		return task.CreateRequest{}, err
	}
	schedule, err := s.store.GetSchedule(scheduleID)
	if err != nil {
		return task.CreateRequest{}, err
	}
	if !schedule.Enabled {
		return task.CreateRequest{}, fmt.Errorf("schedule %q is disabled", schedule.ID)
	}
	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID != "" {
		session, err := s.repo.GetSession(r.Context(), sessionID)
		if err != nil {
			return task.CreateRequest{}, err
		}
		if session.Workspace != schedule.Workspace {
			return task.CreateRequest{}, fmt.Errorf("session %q belongs to workspace %q, not %q", session.ID, session.Workspace, schedule.Workspace)
		}
	}
	threadID := strings.TrimSpace(request.ThreadID)
	create := task.CreateRequest{
		Workspace: schedule.Workspace,
		Prompt:    schedule.Prompt,
		SessionID: sessionID,
		Natural:   true,
		RunAsync:  true,
		Queue:     true,
		Origin:    task.OriginSchedule,
		Automation: task.AutomationMetadata{
			Kind:    task.AutomationKindSchedule,
			Risk:    risk,
			Source:  firstNonEmpty(strings.TrimSpace(request.Source), "schedule:"+schedule.ID),
			Trigger: "schedule",
		},
		Schedule: normalizedSchedule,
	}
	if threadID != "" {
		create.ThreadID = &threadID
	}
	return create, nil
}

func normalizeScheduleTriggerRisk(risk task.AutomationRisk) (task.AutomationRisk, error) {
	switch risk {
	case "":
		return task.AutomationRiskSafe, nil
	case task.AutomationRiskSafe, task.AutomationRiskDangerous:
		return risk, nil
	default:
		return "", fmt.Errorf("unknown automation risk %q", risk)
	}
}

func writeScheduleTriggerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, err)
	case strings.Contains(err.Error(), "disabled"):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusBadRequest, err)
	}
}
