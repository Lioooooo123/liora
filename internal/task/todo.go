package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	TodoStatusPending    = "pending"
	TodoStatusInProgress = "in_progress"
	TodoStatusDone       = "done"
	TodoStatusCancelled  = "cancelled"

	TodoPriorityLow      = "low"
	TodoPriorityNormal   = "normal"
	TodoPriorityHigh     = "high"
	TodoPriorityCritical = "critical"
)

func (r *Repository) WriteTodos(ctx context.Context, request TodoWriteRequest) ([]Todo, error) {
	sessionID := strings.TrimSpace(request.SessionID)
	sourceTaskID := strings.TrimSpace(request.SourceTaskID)
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if sourceTaskID == "" {
		return nil, fmt.Errorf("source task id is required")
	}
	if len(request.Todos) == 0 {
		return nil, fmt.Errorf("todos are required")
	}
	now := time.Now().UTC()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := r.getSessionTx(ctx, tx, sessionID); err != nil {
		return nil, err
	}
	sourceTask, err := r.getTaskTx(ctx, tx, sourceTaskID)
	if err != nil {
		return nil, fmt.Errorf("source task %q: %w", sourceTaskID, err)
	}
	if sourceTask.SessionID != sessionID {
		return nil, fmt.Errorf("source task %q does not belong to session %q", sourceTaskID, sessionID)
	}
	items := make([]TodoWriteItem, 0, len(request.Todos))
	for index, item := range request.Todos {
		normalized, err := normalizeTodoWriteItem(item, sourceTaskID)
		if err != nil {
			return nil, fmt.Errorf("todo %d: %w", index+1, err)
		}
		items = append(items, normalized)
	}
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		existingSession, ok, err := r.todoSessionTx(ctx, tx, item.ID)
		if err != nil {
			return nil, err
		}
		if ok && existingSession != sessionID {
			return nil, fmt.Errorf("todo %q belongs to session %q", item.ID, existingSession)
		}
	}
	written := make([]Todo, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		action := "update"
		if id == "" {
			id = newID("todo")
			action = "create"
		} else if exists, err := r.todoExistsTx(ctx, tx, id); err != nil {
			return nil, err
		} else if !exists {
			action = "create"
		}
		createdAt := now
		if action == "update" {
			existing, err := r.getTodoByIDTx(ctx, tx, id)
			if err != nil {
				return nil, err
			}
			createdAt = existing.CreatedAt
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO todos (id, task_id, parent_task_id, status, title, priority, schema_version, created_at, updated_at)
			VALUES (?, ?, '', ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				task_id = excluded.task_id,
				status = excluded.status,
				title = excluded.title,
				priority = excluded.priority,
				updated_at = excluded.updated_at
		`, id, sourceTaskID, item.Status, item.Content, item.Priority, 9, formatTime(createdAt), formatTime(now)); err != nil {
			return nil, err
		}
		todo := Todo{
			ID:           id,
			SessionID:    sessionID,
			SourceTaskID: sourceTaskID,
			Content:      item.Content,
			Status:       item.Status,
			Priority:     item.Priority,
			CreatedAt:    createdAt,
			UpdatedAt:    now,
		}
		written = append(written, todo)
		if err := r.appendEventTx(ctx, tx, sourceTaskID, EventTodoUpdated, EventPayload{
			ID:           id,
			Action:       action,
			Message:      item.Content,
			Status:       item.Status,
			Priority:     item.Priority,
			SourceTaskID: sourceTaskID,
		}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	r.notifyEventSubscribers(sourceTaskID)
	return written, nil
}

func (r *Repository) TodosBySession(ctx context.Context, sessionID string) ([]Todo, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if _, err := r.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT todos.id, tasks.session_id, todos.task_id, todos.title, todos.status, todos.priority, todos.created_at, todos.updated_at
		FROM todos
		JOIN tasks ON tasks.id = todos.task_id
		WHERE tasks.session_id = ?
		ORDER BY todos.updated_at ASC, todos.id ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var todos []Todo
	for rows.Next() {
		todo, err := scanTodo(rows)
		if err != nil {
			return nil, err
		}
		todos = append(todos, todo)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return todos, nil
}

func normalizeTodoWriteItem(item TodoWriteItem, sourceTaskID string) (TodoWriteItem, error) {
	sourceTaskID = strings.TrimSpace(sourceTaskID)
	item.ID = strings.TrimSpace(item.ID)
	item.SourceTaskID = strings.TrimSpace(item.SourceTaskID)
	if item.SourceTaskID != "" && item.SourceTaskID != sourceTaskID {
		return TodoWriteItem{}, fmt.Errorf("source_task_id %q does not match current task %q", item.SourceTaskID, sourceTaskID)
	}
	item.SourceTaskID = sourceTaskID
	item.Content = strings.TrimSpace(item.Content)
	if item.Content == "" {
		return TodoWriteItem{}, fmt.Errorf("content is required")
	}
	status, err := normalizeTodoStatus(item.Status)
	if err != nil {
		return TodoWriteItem{}, err
	}
	priority, err := normalizeTodoPriority(item.Priority)
	if err != nil {
		return TodoWriteItem{}, err
	}
	item.Status = status
	item.Priority = priority
	return item, nil
}

func normalizeTodoStatus(status string) (string, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return TodoStatusPending, nil
	}
	switch status {
	case TodoStatusPending, TodoStatusInProgress, TodoStatusDone, TodoStatusCancelled:
		return status, nil
	default:
		return "", fmt.Errorf("invalid status %q", status)
	}
}

func normalizeTodoPriority(priority string) (string, error) {
	priority = strings.ToLower(strings.TrimSpace(priority))
	if priority == "" {
		return TodoPriorityNormal, nil
	}
	switch priority {
	case TodoPriorityLow, TodoPriorityNormal, TodoPriorityHigh, TodoPriorityCritical:
		return priority, nil
	default:
		return "", fmt.Errorf("invalid priority %q", priority)
	}
}

func (r *Repository) todoSessionTx(ctx context.Context, tx *sql.Tx, todoID string) (string, bool, error) {
	var sessionID string
	err := tx.QueryRowContext(ctx, `
		SELECT tasks.session_id
		FROM todos
		JOIN tasks ON tasks.id = todos.task_id
		WHERE todos.id = ?
	`, strings.TrimSpace(todoID)).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return sessionID, true, nil
}

func (r *Repository) todoExistsTx(ctx context.Context, tx *sql.Tx, todoID string) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM todos WHERE id = ?`, strings.TrimSpace(todoID)).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *Repository) getTodoByIDTx(ctx context.Context, tx *sql.Tx, todoID string) (Todo, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT todos.id, tasks.session_id, todos.task_id, todos.title, todos.status, todos.priority, todos.created_at, todos.updated_at
		FROM todos
		JOIN tasks ON tasks.id = todos.task_id
		WHERE todos.id = ?
	`, strings.TrimSpace(todoID))
	return scanTodo(row)
}

func (r *Repository) appendEventTx(ctx context.Context, tx *sql.Tx, taskID string, eventType EventType, payload EventPayload) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task id is required")
	}
	if err := ValidateEvent(eventType, payload); err != nil {
		return err
	}
	payload = NormalizeEventPayload(eventType, payload)
	payloadBytes, err := marshalPayload(payload)
	if err != nil {
		return err
	}
	eventID := newID("evt")
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO task_events (id, task_id, type, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, eventID, taskID, string(eventType), payloadBytes, formatTime(now))
	if err != nil {
		return err
	}
	if eventType == EventPermissionRequest {
		if err := r.insertApprovalItemTx(ctx, tx, eventID, taskID, payload, now); err != nil {
			return err
		}
	}
	task, err := r.getTaskTx(ctx, tx, taskID)
	if err != nil {
		return err
	}
	item, ok := timelineItemFromEvent(task.SessionID, task, Event{
		ID:        eventID,
		TaskID:    taskID,
		Type:      eventType,
		Payload:   payloadBytes,
		CreatedAt: now,
	})
	if !ok {
		return nil
	}
	if err := r.insertTranscriptEntryTx(ctx, tx, item); err != nil {
		return err
	}
	return r.insertCompactBoundaryTx(ctx, tx, item, payload)
}

type todoScanner interface {
	Scan(dest ...any) error
}

func scanTodo(scanner todoScanner) (Todo, error) {
	var todo Todo
	var createdAt, updatedAt string
	if err := scanner.Scan(&todo.ID, &todo.SessionID, &todo.SourceTaskID, &todo.Content, &todo.Status, &todo.Priority, &createdAt, &updatedAt); err != nil {
		return Todo{}, err
	}
	todo.CreatedAt = parseTime(createdAt)
	todo.UpdatedAt = parseTime(updatedAt)
	return todo, nil
}

func marshalPayload(payload EventPayload) (string, error) {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
