package task

import (
	"context"
	"strings"
)

func (r *Repository) MarkInterruptedForegroundTasks(ctx context.Context, reason string) (int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT tasks.id
		FROM tasks
		JOIN task_events ON task_events.task_id = tasks.id
		WHERE origin = ?
			AND status IN (?, ?)
			AND task_events.type IN (?, ?, ?, ?, ?, ?, ?, ?)
	`, string(OriginForeground), string(StatusPlanning), string(StatusRunning), string(EventTaskQueued), string(EventPlanning), string(EventSandboxWorkspace), string(EventSandboxRun), string(EventPlanReady), string(EventReplanning), string(EventToolCall), string(EventToolResult))
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
		reason = "foreground task lost its daemon runner"
	}
	for _, id := range ids {
		if err := r.UpdateStatus(ctx, id, StatusFailed); err != nil {
			return 0, err
		}
		if err := r.AppendEvent(ctx, id, EventError, EventPayload{
			Message: reason,
			Status:  string(StatusFailed),
			Reason:  "restart_recovery",
		}); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}
