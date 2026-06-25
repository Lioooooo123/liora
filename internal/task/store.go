package task

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Repository struct {
	db            *sql.DB
	subscribersMu sync.Mutex
	subscribers   map[string][]chan struct{}
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db, subscribers: map[string][]chan struct{}{}}
}

func (r *Repository) Create(ctx context.Context, request CreateRequest) (Task, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return Task{}, fmt.Errorf("prompt is required")
	}
	workspace := strings.TrimSpace(request.Workspace)
	if workspace == "" {
		return Task{}, fmt.Errorf("workspace is required")
	}
	now := time.Now().UTC()
	task := Task{
		ID:        newID("task"),
		Title:     titleFromPrompt(prompt),
		UserInput: prompt,
		Natural:   request.Natural,
		Status:    StatusDraft,
		Workspace: workspace,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO tasks (id, title, user_input, natural, status, workspace, created_at, updated_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, task.ID, task.Title, task.UserInput, boolInt(task.Natural), string(task.Status), task.Workspace, formatTime(task.CreatedAt), formatTime(task.UpdatedAt))
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

func (r *Repository) Get(ctx context.Context, id string) (Task, error) {
	var task Task
	var createdAt, updatedAt string
	var completedAt sql.NullString
	var natural int
	err := r.db.QueryRowContext(ctx, `
		SELECT id, title, user_input, natural, status, workspace, created_at, updated_at, completed_at
		FROM tasks
		WHERE id = ?
	`, id).Scan(&task.ID, &task.Title, &task.UserInput, &natural, &task.Status, &task.Workspace, &createdAt, &updatedAt, &completedAt)
	if err != nil {
		return Task{}, err
	}
	task.Natural = natural != 0
	task.CreatedAt = parseTime(createdAt)
	task.UpdatedAt = parseTime(updatedAt)
	if completedAt.Valid && completedAt.String != "" {
		parsed := parseTime(completedAt.String)
		task.CompletedAt = &parsed
	}
	return task, nil
}

func (r *Repository) List(ctx context.Context, limit int) ([]Task, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, user_input, natural, status, workspace, created_at, updated_at, completed_at
		FROM tasks
		ORDER BY updated_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

func (r *Repository) UpdateStatus(ctx context.Context, id string, status Status) error {
	now := time.Now().UTC()
	var completedAt any
	if status == StatusCompleted || status == StatusFailed || status == StatusCancelled {
		completedAt = formatTime(now)
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, updated_at = ?, completed_at = COALESCE(?, completed_at)
		WHERE id = ?
	`, string(status), formatTime(now), completedAt, id)
	return err
}

func (r *Repository) Cancel(ctx context.Context, id string, reason string) error {
	if err := r.UpdateStatus(ctx, id, StatusCancelled); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "cancelled"
	}
	return r.AppendEvent(ctx, id, EventCancelled, EventPayload{
		Message: reason,
		Status:  string(StatusCancelled),
	})
}

func (r *Repository) AppendEvent(ctx context.Context, taskID string, eventType EventType, payload EventPayload) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO task_events (id, task_id, type, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, newID("evt"), taskID, string(eventType), string(payloadBytes), formatTime(now))
	if err != nil {
		return err
	}
	r.notifyEventSubscribers(taskID)
	return nil
}

func (r *Repository) SubscribeEvents(ctx context.Context, taskID string) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	r.subscribersMu.Lock()
	r.subscribers[taskID] = append(r.subscribers[taskID], ch)
	r.subscribersMu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		r.removeEventSubscriber(taskID, ch)
	})
	unsubscribe := func() {
		if stop() {
			r.removeEventSubscriber(taskID, ch)
		}
	}
	return ch, unsubscribe
}

func (r *Repository) notifyEventSubscribers(taskID string) {
	r.subscribersMu.Lock()
	subscribers := r.subscribers[taskID]
	delete(r.subscribers, taskID)
	r.subscribersMu.Unlock()
	for _, ch := range subscribers {
		close(ch)
	}
}

func (r *Repository) removeEventSubscriber(taskID string, ch chan struct{}) {
	r.subscribersMu.Lock()
	defer r.subscribersMu.Unlock()
	subscribers := r.subscribers[taskID]
	for i, subscriber := range subscribers {
		if subscriber == ch {
			subscribers = append(subscribers[:i], subscribers[i+1:]...)
			break
		}
	}
	if len(subscribers) == 0 {
		delete(r.subscribers, taskID)
		return
	}
	r.subscribers[taskID] = subscribers
}

func (r *Repository) Events(ctx context.Context, taskID string, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, task_id, type, payload_json, created_at
		FROM task_events
		WHERE task_id = ?
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var event Event
		var createdAt string
		if err := rows.Scan(&event.ID, &event.TaskID, &event.Type, &event.Payload, &createdAt); err != nil {
			return nil, err
		}
		event.CreatedAt = parseTime(createdAt)
		events = append(events, event)
	}
	return events, rows.Err()
}

func scanTasks(rows *sql.Rows) ([]Task, error) {
	var tasks []Task
	for rows.Next() {
		var task Task
		var createdAt, updatedAt string
		var completedAt sql.NullString
		var natural int
		if err := rows.Scan(&task.ID, &task.Title, &task.UserInput, &natural, &task.Status, &task.Workspace, &createdAt, &updatedAt, &completedAt); err != nil {
			return nil, err
		}
		task.Natural = natural != 0
		task.CreatedAt = parseTime(createdAt)
		task.UpdatedAt = parseTime(updatedAt)
		if completedAt.Valid && completedAt.String != "" {
			parsed := parseTime(completedAt.String)
			task.CompletedAt = &parsed
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func titleFromPrompt(prompt string) string {
	prompt = strings.Join(strings.Fields(prompt), " ")
	runes := []rune(prompt)
	if len(runes) > 32 {
		return string(runes[:32]) + "..."
	}
	return prompt
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func newID(prefix string) string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(data[:])
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
