package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (r *Repository) Queue(ctx context.Context, id string) error {
	return r.UpdateStatus(ctx, id, StatusQueued)
}

func (r *Repository) HasActiveSessionTask(ctx context.Context, sessionID string, excludeID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false, fmt.Errorf("session id is required")
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT id
		FROM tasks
		WHERE session_id = ?
			AND id != ?
			AND status IN (?, ?, ?, ?)
		LIMIT 1
	`, sessionID, strings.TrimSpace(excludeID), string(StatusDraft), string(StatusPlanning), string(StatusRunning), string(StatusWaitingUser))
	var id string
	err := row.Scan(&id)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}

func (r *Repository) HasSessionQueueBlocker(ctx context.Context, sessionID string, excludeID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false, fmt.Errorf("session id is required")
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT id
		FROM tasks
		WHERE session_id = ?
			AND id != ?
			AND status IN (?, ?, ?, ?)
		LIMIT 1
	`, sessionID, strings.TrimSpace(excludeID), string(StatusQueued), string(StatusPlanning), string(StatusRunning), string(StatusWaitingUser))
	var id string
	err := row.Scan(&id)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}

func (r *Repository) NextQueuedTask(ctx context.Context, sessionID string) (Task, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Task{}, false, fmt.Errorf("session id is required")
	}
	row := r.db.QueryRowContext(ctx, `
			SELECT id, session_id, title, user_input, natural, status, workspace, origin, automation_kind, automation_risk, automation_source, automation_trigger, approval_granted, parent_task_id, model_provider, model_name, model_base_url, model_profile, model_source, created_at, updated_at, completed_at
		FROM tasks
		WHERE session_id = ? AND status = ?
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, sessionID, string(StatusQueued))
	task, err := scanTask(row)
	if err == nil {
		return task, true, nil
	}
	if err == sql.ErrNoRows {
		return Task{}, false, nil
	}
	return Task{}, false, err
}

func (r *Repository) ReceiveUserInput(ctx context.Context, id string, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("message is required")
	}
	task, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if _, err := r.AppendMessage(ctx, task.SessionID, "user", message, task.ID); err != nil {
		return err
	}
	if err := r.UpdateStatus(ctx, id, StatusDraft); err != nil {
		return err
	}
	return r.AppendEvent(ctx, id, EventUserInputReceived, EventPayload{
		Message: message,
		Status:  string(StatusDraft),
	})
}

func (r *Repository) LatestUserInput(ctx context.Context, id string) (string, bool, error) {
	events, err := r.Events(ctx, id, 0)
	if err != nil {
		return "", false, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != EventUserInputReceived {
			continue
		}
		payload, err := parseEventPayload(events[i])
		if err != nil {
			return "", false, err
		}
		message := strings.TrimSpace(payload.Message)
		return message, message != "", nil
	}
	return "", false, nil
}

func parseEventPayload(event Event) (EventPayload, error) {
	var payload EventPayload
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		return EventPayload{}, err
	}
	return payload, nil
}

type BackgroundCounts struct {
	Running int
	Active  int
}

type ForegroundCounts struct {
	Running int
	Active  int
}

func (r *Repository) CountBackgroundTasks(ctx context.Context) (BackgroundCounts, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM tasks
		WHERE origin IN (?, ?, ?, ?)
			AND status IN (?, ?, ?, ?, ?, ?)
		GROUP BY status
	`, string(OriginBackground), string(OriginSchedule), string(OriginHook), string(OriginSubagent), string(StatusQueued), string(StatusPlanning), string(StatusRunning), string(StatusWaitingUser), string(StatusLost), string(StatusRecovered))
	if err != nil {
		return BackgroundCounts{}, err
	}
	defer rows.Close()
	counts := BackgroundCounts{}
	for rows.Next() {
		var status Status
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return BackgroundCounts{}, err
		}
		counts.Active += count
		switch status {
		case StatusPlanning, StatusRunning:
			counts.Running += count
		}
	}
	if err := rows.Err(); err != nil {
		return BackgroundCounts{}, err
	}
	return counts, nil
}

func (r *Repository) NextQueuedBackgroundTask(ctx context.Context) (Task, bool, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id
		FROM tasks
		WHERE origin IN (?, ?, ?, ?)
			AND status = ?
		ORDER BY created_at ASC, id ASC
		LIMIT 100
	`, string(OriginBackground), string(OriginSchedule), string(OriginHook), string(OriginSubagent), string(StatusQueued))
	if err != nil {
		return Task{}, false, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return Task{}, false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return Task{}, false, err
	}
	for _, id := range ids {
		task, err := r.Get(ctx, id)
		if err != nil {
			return Task{}, false, err
		}
		if task.Origin == OriginSchedule || task.Origin == OriginSubagent {
			blocked, err := r.HasWorkspaceForegroundBlocker(ctx, task.Workspace, "")
			if err != nil {
				return Task{}, false, err
			}
			if blocked {
				continue
			}
		}
		return task, true, nil
	}
	return Task{}, false, nil
}

func (r *Repository) CountForegroundTasks(ctx context.Context) (ForegroundCounts, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM tasks
		WHERE origin = ?
			AND status IN (?, ?, ?, ?)
		GROUP BY status
	`, string(OriginForeground), string(StatusQueued), string(StatusPlanning), string(StatusRunning), string(StatusWaitingUser))
	if err != nil {
		return ForegroundCounts{}, err
	}
	defer rows.Close()
	counts := ForegroundCounts{}
	for rows.Next() {
		var status Status
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return ForegroundCounts{}, err
		}
		counts.Active += count
		switch status {
		case StatusPlanning, StatusRunning:
			counts.Running += count
		}
	}
	if err := rows.Err(); err != nil {
		return ForegroundCounts{}, err
	}
	return counts, nil
}

func (r *Repository) CountRunningForegroundTasksByWorkspace(ctx context.Context, workspace string) (int, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return 0, fmt.Errorf("workspace is required")
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM tasks
		WHERE workspace = ?
			AND origin = ?
			AND status IN (?, ?)
	`, workspace, string(OriginForeground), string(StatusPlanning), string(StatusRunning))
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *Repository) HasEarlierSessionQueueBlocker(ctx context.Context, task Task) (bool, error) {
	sessionID := strings.TrimSpace(task.SessionID)
	if sessionID == "" {
		return false, fmt.Errorf("session id is required")
	}
	createdAt := formatTime(task.CreatedAt)
	row := r.db.QueryRowContext(ctx, `
		SELECT id
		FROM tasks
		WHERE session_id = ?
			AND id != ?
			AND (
				status IN (?, ?, ?)
				OR (
					status = ?
					AND (created_at < ? OR (created_at = ? AND id < ?))
				)
			)
		LIMIT 1
	`, sessionID, strings.TrimSpace(task.ID), string(StatusPlanning), string(StatusRunning), string(StatusWaitingUser), string(StatusQueued), createdAt, createdAt, task.ID)
	var id string
	err := row.Scan(&id)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}

func (r *Repository) HasWorkspaceForegroundBlocker(ctx context.Context, workspace string, excludeID string) (bool, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return false, fmt.Errorf("workspace is required")
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT id
		FROM tasks
		WHERE workspace = ?
			AND origin = ?
			AND id != ?
			AND status IN (?, ?, ?, ?)
		LIMIT 1
	`, workspace, string(OriginForeground), strings.TrimSpace(excludeID), string(StatusQueued), string(StatusPlanning), string(StatusRunning), string(StatusWaitingUser))
	var id string
	err := row.Scan(&id)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}

func (r *Repository) NextEligibleQueuedForegroundTask(ctx context.Context) (Task, bool, error) {
	tasks, err := r.EligibleQueuedForegroundTasks(ctx, 100)
	if err != nil {
		return Task{}, false, err
	}
	if len(tasks) == 0 {
		return Task{}, false, nil
	}
	return tasks[0], true, nil
}

func (r *Repository) EligibleQueuedForegroundTasks(ctx context.Context, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id
		FROM tasks
		WHERE origin = ? AND status = ?
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, string(OriginForeground), string(StatusQueued), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tasks := make([]Task, 0, len(ids))
	for _, id := range ids {
		task, err := r.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		blocked, err := r.HasEarlierSessionQueueBlocker(ctx, task)
		if err != nil {
			return nil, err
		}
		if !blocked {
			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}

func (r *Repository) MarkLostBackgroundTasks(ctx context.Context, reason string) (int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id
		FROM tasks
		WHERE origin IN (?, ?, ?, ?)
			AND status IN (?, ?)
	`, string(OriginBackground), string(OriginSchedule), string(OriginHook), string(OriginSubagent), string(StatusPlanning), string(StatusRunning))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "background task lost its daemon runner"
	}
	for _, id := range ids {
		if err := r.UpdateStatus(ctx, id, StatusLost); err != nil {
			return 0, err
		}
		if err := r.AppendEvent(ctx, id, EventError, EventPayload{
			Message: reason,
			Status:  string(StatusLost),
			Reason:  "lost",
		}); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func (r *Repository) RecoverLostTask(ctx context.Context, id string, reason string) error {
	task, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if task.Status != StatusLost {
		return fmt.Errorf("task is not lost: %s", task.Status)
	}
	if !IsBackgroundOrigin(task.Origin) {
		return fmt.Errorf("task origin %q is not a background-controlled origin", task.Origin)
	}
	if err := r.UpdateStatus(ctx, id, StatusRecovered); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Lost background task recovered."
	}
	return r.AppendEvent(ctx, id, EventTaskQueued, EventPayload{
		Message: reason,
		Status:  string(StatusRecovered),
	})
}

func (r *Repository) MarkExpiredTasksStale(ctx context.Context, now time.Time, reason string) (int, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id
		FROM tasks
		WHERE status IN (?, ?, ?, ?)
		ORDER BY updated_at ASC, id ASC
	`, string(StatusQueued), string(StatusPlanning), string(StatusRunning), string(StatusWaitingUser))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "task expired before it could continue"
	}
	marked := 0
	for _, id := range ids {
		expiresAt, ok, err := r.latestExpiry(ctx, id)
		if err != nil {
			return marked, err
		}
		if !ok || now.Before(expiresAt) {
			continue
		}
		if err := r.markExpiredTaskStale(ctx, id, reason, expiresAt, now); err != nil {
			return marked, err
		}
		marked++
	}
	return marked, nil
}

func (r *Repository) markExpiredTaskStale(ctx context.Context, id string, reason string, expiresAt time.Time, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, updated_at = ?, completed_at = COALESCE(?, completed_at)
		WHERE id = ?
	`, string(StatusStale), formatTime(now), formatTime(now), id); err != nil {
		return err
	}
	if err := r.resolveApprovalItemTx(ctx, tx, id, "expired", "system", now); err != nil {
		return err
	}
	if err := r.appendEventTx(ctx, tx, id, EventError, EventPayload{
		Message:   reason,
		Status:    string(StatusStale),
		Reason:    "stale",
		ExpiresAt: formatTime(expiresAt),
		StaleAt:   formatTime(now),
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.notifyEventSubscribers(id)
	return nil
}

func (r *Repository) latestExpiry(ctx context.Context, taskID string) (time.Time, bool, error) {
	events, err := r.Events(ctx, taskID, 0)
	if err != nil {
		return time.Time{}, false, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Type {
		case EventPermissionRequest, EventUserInputRequest, EventScheduleTriggered, EventHookRun:
		default:
			continue
		}
		payload, err := parseEventPayload(events[i])
		if err != nil {
			return time.Time{}, false, err
		}
		expiresAt := strings.TrimSpace(payload.ExpiresAt)
		if expiresAt == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, expiresAt)
		if err != nil {
			return time.Time{}, false, err
		}
		return parsed, true, nil
	}
	return time.Time{}, false, nil
}

func (r *Repository) ExplainRestartState(ctx context.Context, reason string) (RestartRecoveryCounts, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "daemon restarted and recovered persisted task state"
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id
		FROM tasks
		WHERE status IN (?, ?, ?)
		ORDER BY updated_at ASC, id ASC
	`, string(StatusQueued), string(StatusWaitingUser), string(StatusLost))
	if err != nil {
		return RestartRecoveryCounts{}, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return RestartRecoveryCounts{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return RestartRecoveryCounts{}, err
	}
	counts := RestartRecoveryCounts{}
	for _, id := range ids {
		task, err := r.Get(ctx, id)
		if err != nil {
			return counts, err
		}
		if ok, err := r.hasRestartRecoveryEvent(ctx, task.ID, task.Status); err != nil {
			return counts, err
		} else if ok {
			continue
		}
		payload := EventPayload{
			Message: "Task state recovered after daemon restart.",
			Status:  string(task.Status),
			Reason:  "restart_recovery",
			Origin:  string(task.Origin),
			Kind:    string(task.Automation.Kind),
			Source:  task.Automation.Source,
			Trigger: task.Automation.Trigger,
		}
		eventType := EventError
		switch task.Status {
		case StatusQueued:
			eventType = EventTaskQueued
			counts.Queued++
		case StatusWaitingUser:
			counts.WaitingUser++
		case StatusLost:
			payload.Message = reason
			counts.Lost++
		}
		if err := r.AppendEvent(ctx, task.ID, eventType, payload); err != nil {
			return counts, err
		}
	}
	return counts, nil
}

func (r *Repository) hasRestartRecoveryEvent(ctx context.Context, taskID string, status Status) (bool, error) {
	events, err := r.Events(ctx, taskID, 0)
	if err != nil {
		return false, err
	}
	for _, event := range events {
		payload, err := parseEventPayload(event)
		if err != nil {
			continue
		}
		if payload.Reason == "restart_recovery" && payload.Status == string(status) {
			return true, nil
		}
	}
	return false, nil
}

func scanTask(row scanner) (Task, error) {
	var task Task
	var createdAt, updatedAt string
	var completedAt sql.NullString
	var modelProvider, modelName, modelBaseURL, modelProfile, modelSource string
	var natural, approvalGranted int
	if err := row.Scan(&task.ID, &task.SessionID, &task.Title, &task.UserInput, &natural, &task.Status, &task.Workspace, &task.Origin, &task.Automation.Kind, &task.Automation.Risk, &task.Automation.Source, &task.Automation.Trigger, &approvalGranted, &task.ParentTaskID, &modelProvider, &modelName, &modelBaseURL, &modelProfile, &modelSource, &createdAt, &updatedAt, &completedAt); err != nil {
		return Task{}, err
	}
	task.Natural = natural != 0
	task.ApprovalGranted = approvalGranted != 0
	task.ModelConfig = normalizeModelConfig(&ModelConfig{Provider: modelProvider, Model: modelName, BaseURL: modelBaseURL, Profile: modelProfile, Source: modelSource})
	task.CreatedAt = parseTime(createdAt)
	task.UpdatedAt = parseTime(updatedAt)
	if completedAt.Valid && completedAt.String != "" {
		parsed := parseTime(completedAt.String)
		task.CompletedAt = &parsed
	}
	return task, nil
}

func (r *Repository) ActivateQueuedTask(ctx context.Context, task Task) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, updated_at = ?, completed_at = NULL
		WHERE id = ? AND status = ?
	`, string(StatusDraft), formatTime(now), task.ID, string(StatusQueued))
	return err
}

func IsBackgroundOrigin(origin Origin) bool {
	switch origin {
	case OriginBackground, OriginSchedule, OriginHook, OriginSubagent:
		return true
	default:
		return false
	}
}
