package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

func (s *server) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	var request taskpkg.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	thread, threadBound, err := s.prepareThreadTask(r.Context(), &request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	origin, automation, err := taskpkg.NormalizeAutomationMetadata(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	schedule, err := taskpkg.NormalizeScheduleMetadata(origin, request.Schedule)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request.Schedule = schedule
	if request.RunAsync && s.runner == nil && !taskpkg.AutomationRequiresApproval(origin, automation) {
		writeError(w, http.StatusBadRequest, errors.New("runner is not configured"))
		return
	}
	if request.RunAsync && taskpkg.IsBackgroundOrigin(origin) {
		if err := s.ensureBackgroundCapacity(r.Context()); err != nil {
			writeError(w, http.StatusTooManyRequests, err)
			return
		}
	}
	if request.RunAsync && origin == taskpkg.OriginForeground {
		if err := s.ensureForegroundCapacity(r.Context()); err != nil {
			writeError(w, http.StatusTooManyRequests, err)
			return
		}
	}
	task, err := s.repo.Create(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if threadBound {
		if err := s.store.SetConversationThreadLastTask(thread.ID, task.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	_ = s.repo.AppendEvent(r.Context(), task.ID, taskpkg.EventTaskCreated, taskpkg.EventPayload{
		Message:      task.UserInput,
		Status:       string(task.Status),
		Origin:       string(task.Origin),
		Kind:         string(task.Automation.Kind),
		Risk:         string(task.Automation.Risk),
		Source:       task.Automation.Source,
		Trigger:      task.Automation.Trigger,
		ParentTaskID: task.ParentTaskID,
	})
	if task.Origin == taskpkg.OriginSchedule && schedule.ID != "" {
		_ = s.repo.AppendEvent(r.Context(), task.ID, taskpkg.EventScheduleTriggered, taskpkg.EventPayload{
			ID:            schedule.ID,
			Message:       "Schedule triggered.",
			Status:        string(task.Status),
			Origin:        string(task.Origin),
			Kind:          string(task.Automation.Kind),
			Source:        task.Automation.Source,
			Trigger:       task.Automation.Trigger,
			MissedRuns:    schedule.MissedRuns,
			CatchUpPolicy: string(schedule.CatchUpPolicy),
			CatchUpRuns:   schedule.CatchUpRuns,
			ExpiresAt:     taskpkg.ExpiresAtAfter(taskpkg.DefaultScheduleTriggerExpiry),
		})
	}
	if isDangerousAutomation(task) {
		if err := s.pauseDangerousAutomation(r.Context(), task); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		task, err = s.repo.Get(r.Context(), task.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusAccepted, taskpkg.CreateResponse{Task: task})
		return
	}
	if task.Origin == taskpkg.OriginForeground {
		queued, err := s.queueBehindSessionBlocker(r.Context(), task, "Foreground turn queued behind the active session turn.")
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		if queued {
			task, err = s.repo.Get(r.Context(), task.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, http.StatusAccepted, taskpkg.CreateResponse{Task: task})
			return
		}
	}
	if request.RunAsync {
		status, err := s.enqueueOrStart(r, task, request.Queue)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		task, err = s.repo.Get(r.Context(), task.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, status, taskpkg.CreateResponse{Task: task})
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
}

func isDangerousAutomation(task taskpkg.Task) bool {
	return taskpkg.AutomationRequiresApproval(task.Origin, task.Automation)
}

func (s *server) prepareThreadTask(ctx context.Context, request *taskpkg.CreateRequest) (storeThread conversationThreadBinding, bound bool, err error) {
	if request.ThreadID == nil {
		return conversationThreadBinding{}, false, nil
	}
	threadID := strings.TrimSpace(*request.ThreadID)
	if threadID == "" {
		return conversationThreadBinding{}, false, errors.New("thread_id is required")
	}
	if s.store == nil {
		return conversationThreadBinding{}, false, errors.New("store is not configured")
	}
	thread, err := s.store.GetConversationThread(threadID)
	if err != nil {
		return conversationThreadBinding{}, false, err
	}
	if thread.ArchivedAt != nil {
		return conversationThreadBinding{}, false, fmt.Errorf("thread %q is archived", thread.ID)
	}
	workspace := strings.TrimSpace(request.Workspace)
	if workspace == "" {
		return conversationThreadBinding{}, false, errors.New("workspace is required")
	}
	if workspace != thread.Workspace {
		return conversationThreadBinding{}, false, fmt.Errorf("thread %q belongs to workspace %q, not %q", thread.ID, thread.Workspace, workspace)
	}
	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID != "" && sessionID != thread.ID {
		return conversationThreadBinding{}, false, errors.New("session_id must match thread_id")
	}
	if _, err := s.repo.EnsureSession(ctx, thread.ID, thread.Title, thread.Workspace); err != nil {
		return conversationThreadBinding{}, false, err
	}
	request.SessionID = thread.ID
	if request.ModelConfig == nil && thread.ModelConfig != nil {
		config := *thread.ModelConfig
		if strings.TrimSpace(config.InheritedFromThreadID) != "" && (strings.TrimSpace(config.Provider) == "" || strings.TrimSpace(config.Model) == "") {
			if inherited, err := s.store.GetConversationThread(config.InheritedFromThreadID); err == nil && inherited.Workspace == thread.Workspace && inherited.ModelConfig != nil {
				config.Provider = inherited.ModelConfig.Provider
				config.Model = inherited.ModelConfig.Model
				config.BaseURL = inherited.ModelConfig.BaseURL
				if strings.TrimSpace(config.Profile) == "" {
					config.Profile = inherited.ModelConfig.Profile
				}
			}
		}
		request.ModelConfig = &taskpkg.ModelConfig{
			Provider: config.Provider,
			Model:    config.Model,
			BaseURL:  config.BaseURL,
			Profile:  config.Profile,
			Source:   "thread_binding",
		}
	}
	return conversationThreadBinding{ID: thread.ID}, true, nil
}

type conversationThreadBinding struct {
	ID string
}

func (s *server) pauseDangerousAutomation(ctx context.Context, task taskpkg.Task) error {
	if err := s.repo.UpdateStatus(ctx, task.ID, taskpkg.StatusWaitingUser); err != nil {
		return err
	}
	return s.repo.AppendEvent(ctx, task.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{
		Message:      "Dangerous automation requires user approval before starting.",
		Status:       string(taskpkg.StatusWaitingUser),
		Risk:         string(task.Automation.Risk),
		Origin:       string(task.Origin),
		Kind:         string(task.Automation.Kind),
		Source:       task.Automation.Source,
		Trigger:      task.Automation.Trigger,
		ParentTaskID: task.ParentTaskID,
		ExpiresAt:    taskpkg.ExpiresAtAfter(taskpkg.DefaultWaitExpiry),
	})
}

func (s *server) enqueueOrStart(r *http.Request, task taskpkg.Task, queue bool) (int, error) {
	if queued, err := s.queueBackgroundIfLimited(r.Context(), task, "Background task queued by the concurrency limit."); err != nil || queued {
		if err != nil {
			return 0, err
		}
		return http.StatusAccepted, nil
	}
	if queued, err := s.queueForegroundIfLimited(r.Context(), task, "Foreground task queued by the scheduler concurrency limit."); err != nil || queued {
		if err != nil {
			return 0, err
		}
		return http.StatusAccepted, nil
	}
	if queue {
		queued, err := s.queueBehindSessionBlocker(r.Context(), task, "Task queued behind the active session turn.")
		if err != nil {
			return 0, err
		}
		if queued {
			return http.StatusAccepted, nil
		}
	}
	if err := s.startTaskAsync(task.ID); err != nil {
		return 0, err
	}
	return http.StatusAccepted, nil
}

func (s *server) ensureBackgroundCapacity(ctx context.Context) error {
	counts, err := s.repo.CountBackgroundTasks(ctx)
	if err != nil {
		return err
	}
	if counts.Active >= s.background.MaxActive {
		return fmt.Errorf("background task resource limit reached: active=%d max_active=%d", counts.Active, s.background.MaxActive)
	}
	return nil
}

func (s *server) ensureForegroundCapacity(ctx context.Context) error {
	counts, err := s.repo.CountForegroundTasks(ctx)
	if err != nil {
		return err
	}
	if counts.Active >= s.foreground.MaxActive {
		return fmt.Errorf("foreground task resource limit reached: active=%d max_active=%d", counts.Active, s.foreground.MaxActive)
	}
	return nil
}

func (s *server) queueBackgroundIfLimited(ctx context.Context, task taskpkg.Task, message string) (bool, error) {
	if !taskpkg.IsBackgroundOrigin(task.Origin) {
		return false, nil
	}
	if task.Origin == taskpkg.OriginSchedule || task.Origin == taskpkg.OriginSubagent {
		blocked, err := s.repo.HasWorkspaceForegroundBlocker(ctx, task.Workspace, task.ID)
		if err != nil {
			return false, err
		}
		if blocked {
			if err := s.repo.Queue(ctx, task.ID); err != nil {
				return false, err
			}
			_ = s.repo.AppendEvent(ctx, task.ID, taskpkg.EventTaskQueued, taskpkg.EventPayload{
				Message: "Background-controlled task queued behind workspace foreground work.",
				Status:  string(taskpkg.StatusQueued),
				Origin:  string(task.Origin),
				Kind:    string(task.Automation.Kind),
			})
			return true, nil
		}
	}
	counts, err := s.repo.CountBackgroundTasks(ctx)
	if err != nil {
		return false, err
	}
	if counts.Running < s.background.MaxConcurrent {
		return false, nil
	}
	if err := s.repo.Queue(ctx, task.ID); err != nil {
		return false, err
	}
	_ = s.repo.AppendEvent(ctx, task.ID, taskpkg.EventTaskQueued, taskpkg.EventPayload{
		Message: message,
		Status:  string(taskpkg.StatusQueued),
		Origin:  string(task.Origin),
		Kind:    string(task.Automation.Kind),
	})
	return true, nil
}

func (s *server) queueForegroundIfLimited(ctx context.Context, task taskpkg.Task, message string) (bool, error) {
	if task.Origin != taskpkg.OriginForeground {
		return false, nil
	}
	counts, err := s.repo.CountForegroundTasks(ctx)
	if err != nil {
		return false, err
	}
	if s.foregroundRunning(ctx, counts) < s.foreground.MaxConcurrent {
		workspaceRunning, err := s.foregroundWorkspaceRunning(ctx, task.Workspace)
		if err != nil {
			return false, err
		}
		if workspaceRunning < s.foreground.MaxPerWorkspace {
			return false, nil
		}
		message = "Foreground task queued by the workspace concurrency limit."
	}
	if err := s.repo.Queue(ctx, task.ID); err != nil {
		return false, err
	}
	_ = s.repo.AppendEvent(ctx, task.ID, taskpkg.EventTaskQueued, taskpkg.EventPayload{
		Message: message,
		Status:  string(taskpkg.StatusQueued),
		Origin:  string(task.Origin),
	})
	return true, nil
}

func (s *server) queueBehindSessionBlocker(ctx context.Context, task taskpkg.Task, message string) (bool, error) {
	active, err := s.repo.HasSessionQueueBlocker(ctx, task.SessionID, task.ID)
	if err != nil {
		return false, err
	}
	if !active {
		return false, nil
	}
	if err := s.repo.Queue(ctx, task.ID); err != nil {
		return false, err
	}
	_ = s.repo.AppendEvent(ctx, task.ID, taskpkg.EventTaskQueued, taskpkg.EventPayload{
		Message: message,
		Status:  string(taskpkg.StatusQueued),
	})
	return true, nil
}

func (s *server) handleTaskInput(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	var request taskpkg.InputRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(request.Message) == "" {
		writeError(w, http.StatusBadRequest, errors.New("message is required"))
		return
	}
	task, err := s.repo.Get(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if task.Status != taskpkg.StatusWaitingUser {
		writeError(w, http.StatusConflict, fmt.Errorf("task is not waiting for user input: %s", task.Status))
		return
	}
	if !s.latestWaitIsUserInput(r, taskID) {
		writeError(w, http.StatusConflict, errors.New("task is waiting for approval; use /approval instead"))
		return
	}
	if err := s.repo.ReceiveUserInput(r.Context(), taskID, request.Message); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if s.runner != nil {
		if err := s.startTaskAsync(taskID); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
	}
	task, err = s.repo.Get(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusAccepted, taskpkg.InputResponse{Task: task})
}

func (s *server) latestWaitIsUserInput(r *http.Request, taskID string) bool {
	eventType, _, err := s.latestWaitingRequest(r.Context(), taskID)
	return err == nil && eventType == taskpkg.EventUserInputRequest
}

func (s *server) latestWaitIsApproval(ctx context.Context, taskID string) bool {
	eventType, _, err := s.latestWaitingRequest(ctx, taskID)
	return err == nil && eventType == taskpkg.EventPermissionRequest
}

func (s *server) latestWaitingRequest(ctx context.Context, taskID string) (taskpkg.EventType, taskpkg.EventPayload, error) {
	events, err := s.repo.Events(ctx, taskID, 0)
	if err != nil {
		return "", taskpkg.EventPayload{}, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Type {
		case taskpkg.EventUserInputRequest, taskpkg.EventPermissionRequest:
			var payload taskpkg.EventPayload
			if err := json.Unmarshal([]byte(events[i].Payload), &payload); err != nil {
				return "", taskpkg.EventPayload{}, err
			}
			return events[i].Type, payload, nil
		}
	}
	return "", taskpkg.EventPayload{}, nil
}

func (s *server) startNextQueuedAfter(taskID string) {
	ctx := context.Background()
	task, err := s.repo.Get(ctx, taskID)
	if err != nil {
		return
	}
	switch task.Status {
	case taskpkg.StatusCompleted, taskpkg.StatusFailed, taskpkg.StatusCancelled:
	default:
		return
	}
	next, ok, err := s.repo.NextQueuedTask(ctx, task.SessionID)
	if err != nil || !ok {
		return
	}
	if next.Origin == taskpkg.OriginForeground {
		s.startNextForegroundQueued()
		return
	}
	if err := s.repo.ActivateQueuedTask(ctx, next); err != nil {
		return
	}
	_ = s.repo.AppendEvent(ctx, next.ID, taskpkg.EventTaskQueued, taskpkg.EventPayload{
		Message: "Queued task starting.",
		Status:  string(taskpkg.StatusDraft),
	})
	_ = s.startTaskAsync(next.ID)
}

func (s *server) startNextForegroundQueued() {
	ctx := context.Background()
	counts, err := s.repo.CountForegroundTasks(ctx)
	if err != nil || s.foregroundRunning(ctx, counts) >= s.foreground.MaxConcurrent {
		return
	}
	queued, err := s.repo.EligibleQueuedForegroundTasks(ctx, 100)
	if err != nil {
		return
	}
	var next taskpkg.Task
	ok := false
	for _, candidate := range queued {
		running, err := s.foregroundWorkspaceRunning(ctx, candidate.Workspace)
		if err != nil {
			return
		}
		if running >= s.foreground.MaxPerWorkspace {
			continue
		}
		next = candidate
		ok = true
		break
	}
	if !ok {
		return
	}
	if err := s.repo.ActivateQueuedTask(ctx, next); err != nil {
		return
	}
	_ = s.repo.AppendEvent(ctx, next.ID, taskpkg.EventTaskQueued, taskpkg.EventPayload{
		Message: "Queued foreground task starting.",
		Status:  string(taskpkg.StatusDraft),
		Origin:  string(next.Origin),
	})
	_ = s.startTaskAsync(next.ID)
}

func (s *server) foregroundRunning(ctx context.Context, counts taskpkg.ForegroundCounts) int {
	s.runningMu.Lock()
	ids := make([]string, 0, len(s.running))
	for id := range s.running {
		ids = append(ids, id)
	}
	s.runningMu.Unlock()
	handles := 0
	for _, id := range ids {
		task, err := s.repo.Get(ctx, id)
		if err == nil && task.Origin == taskpkg.OriginForeground {
			handles++
		}
	}
	if handles > counts.Running {
		return handles
	}
	return counts.Running
}

func (s *server) foregroundWorkspaceRunning(ctx context.Context, workspace string) (int, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return 0, errors.New("workspace is required")
	}
	count, err := s.repo.CountRunningForegroundTasksByWorkspace(ctx, workspace)
	if err != nil {
		return 0, err
	}
	s.runningMu.Lock()
	ids := make([]string, 0, len(s.running))
	for id := range s.running {
		ids = append(ids, id)
	}
	s.runningMu.Unlock()
	handles := 0
	for _, id := range ids {
		task, err := s.repo.Get(ctx, id)
		if err == nil && task.Origin == taskpkg.OriginForeground && task.Workspace == workspace {
			handles++
		}
	}
	if handles > count {
		return handles, nil
	}
	return count, nil
}

func (s *server) startNextBackgroundQueued() {
	ctx := context.Background()
	counts, err := s.repo.CountBackgroundTasks(ctx)
	if err != nil || counts.Running >= s.background.MaxConcurrent {
		return
	}
	next, ok, err := s.repo.NextQueuedBackgroundTask(ctx)
	if err != nil || !ok {
		return
	}
	if err := s.repo.ActivateQueuedTask(ctx, next); err != nil {
		return
	}
	_ = s.repo.AppendEvent(ctx, next.ID, taskpkg.EventTaskQueued, taskpkg.EventPayload{
		Message: "Queued background task starting.",
		Status:  string(taskpkg.StatusDraft),
		Origin:  string(next.Origin),
		Kind:    string(next.Automation.Kind),
	})
	_ = s.startTaskAsync(next.ID)
}
