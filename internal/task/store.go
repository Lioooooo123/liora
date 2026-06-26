package task

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID == "" {
		sessionID = newID("session")
		_, err := tx.ExecContext(ctx, `
			INSERT INTO sessions (id, title, workspace, last_task_id, created_at, updated_at)
			VALUES (?, ?, ?, '', ?, ?)
		`, sessionID, titleFromPrompt(prompt), workspace, formatTime(now), formatTime(now))
		if err != nil {
			return Task{}, err
		}
	} else if _, err := r.getSessionTx(ctx, tx, sessionID); err != nil {
		return Task{}, err
	}
	task := Task{
		ID:              newID("task"),
		SessionID:       sessionID,
		Title:           titleFromPrompt(prompt),
		UserInput:       prompt,
		Natural:         request.Natural,
		Status:          StatusDraft,
		Workspace:       workspace,
		ApprovalGranted: false,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO tasks (id, session_id, title, user_input, natural, status, workspace, approval_granted, created_at, updated_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, task.ID, task.SessionID, task.Title, task.UserInput, boolInt(task.Natural), string(task.Status), task.Workspace, boolInt(task.ApprovalGranted), formatTime(task.CreatedAt), formatTime(task.UpdatedAt))
	if err != nil {
		return Task{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_messages (id, session_id, role, content, task_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, newID("msg"), task.SessionID, "user", task.UserInput, task.ID, formatTime(now))
	if err != nil {
		return Task{}, err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE sessions
		SET last_task_id = ?, updated_at = ?
		WHERE id = ?
	`, task.ID, formatTime(now), task.SessionID)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (r *Repository) Get(ctx context.Context, id string) (Task, error) {
	var task Task
	var createdAt, updatedAt string
	var completedAt sql.NullString
	var natural, approvalGranted int
	err := r.db.QueryRowContext(ctx, `
		SELECT id, session_id, title, user_input, natural, status, workspace, approval_granted, created_at, updated_at, completed_at
		FROM tasks
		WHERE id = ?
	`, id).Scan(&task.ID, &task.SessionID, &task.Title, &task.UserInput, &natural, &task.Status, &task.Workspace, &approvalGranted, &createdAt, &updatedAt, &completedAt)
	if err != nil {
		return Task{}, err
	}
	task.Natural = natural != 0
	task.ApprovalGranted = approvalGranted != 0
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
		SELECT id, session_id, title, user_input, natural, status, workspace, approval_granted, created_at, updated_at, completed_at
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

func (r *Repository) ListByWorkspace(ctx context.Context, workspace string, limit int) ([]Task, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return r.List(ctx, limit)
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, session_id, title, user_input, natural, status, workspace, approval_granted, created_at, updated_at, completed_at
		FROM tasks
		WHERE workspace = ?
		ORDER BY updated_at DESC, id DESC
		LIMIT ?
	`, workspace, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

func (r *Repository) ListBySession(ctx context.Context, sessionID string, limit int) ([]Task, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, session_id, title, user_input, natural, status, workspace, approval_granted, created_at, updated_at, completed_at
		FROM tasks
		WHERE session_id = ?
		ORDER BY updated_at DESC, id DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

func (r *Repository) CreateSession(ctx context.Context, request CreateSessionRequest) (Session, error) {
	workspace := strings.TrimSpace(request.Workspace)
	if workspace == "" {
		return Session{}, fmt.Errorf("workspace is required")
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "New session"
	}
	now := time.Now().UTC()
	session := Session{
		ID:        newID("session"),
		Title:     titleFromPrompt(title),
		Workspace: workspace,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions (id, title, workspace, last_task_id, created_at, updated_at)
		VALUES (?, ?, ?, '', ?, ?)
	`, session.ID, session.Title, session.Workspace, formatTime(session.CreatedAt), formatTime(session.UpdatedAt))
	if err != nil {
		return Session{}, err
	}
	return session, nil
}

func (r *Repository) GetSession(ctx context.Context, id string) (Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Session{}, fmt.Errorf("session id is required")
	}
	return r.getSessionTx(ctx, r.db, id)
}

func (r *Repository) ListSessions(ctx context.Context, limit int) ([]Session, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, workspace, last_task_id, created_at, updated_at
		FROM sessions
		ORDER BY updated_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (r *Repository) ListSessionsByWorkspace(ctx context.Context, workspace string, limit int) ([]Session, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return r.ListSessions(ctx, limit)
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, workspace, last_task_id, created_at, updated_at
		FROM sessions
		WHERE workspace = ?
		ORDER BY updated_at DESC, id DESC
		LIMIT ?
	`, workspace, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (r *Repository) Messages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, session_id, role, content, task_id, created_at
		FROM session_messages
		WHERE session_id = ?
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []Message
	for rows.Next() {
		var message Message
		var createdAt string
		if err := rows.Scan(&message.ID, &message.SessionID, &message.Role, &message.Content, &message.TaskID, &createdAt); err != nil {
			return nil, err
		}
		message.CreatedAt = parseTime(createdAt)
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (r *Repository) Timeline(ctx context.Context, sessionID string, limit int) ([]TimelineItem, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	messages, err := r.Messages(ctx, sessionID, 0)
	if err != nil {
		return nil, err
	}
	tasks, err := r.listBySessionAsc(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	items := make([]TimelineItem, 0, len(messages)+len(tasks)*4)
	for _, message := range messages {
		items = append(items, TimelineItem{
			ID:        message.ID,
			SessionID: message.SessionID,
			TaskID:    message.TaskID,
			Kind:      "message",
			Role:      message.Role,
			Content:   message.Content,
			CreatedAt: message.CreatedAt,
		})
	}
	for _, task := range tasks {
		events, err := r.Events(ctx, task.ID, 0)
		if err != nil {
			return nil, err
		}
		items = append(items, timelineItemsFromEvents(sessionID, task, events)...)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items, nil
}

func (r *Repository) SearchTimeline(ctx context.Context, workspace string, query string, limit int) ([]TimelineItem, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("timeline search query is required")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	sessions, err := r.ListSessionsByWorkspace(ctx, workspace, 200)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(query)
	var matches []TimelineItem
	for _, session := range sessions {
		timeline, err := r.Timeline(ctx, session.ID, 500)
		if err != nil {
			return nil, err
		}
		for _, item := range timeline {
			if strings.Contains(strings.ToLower(timelineItemSearchText(item)), needle) {
				matches = append(matches, item)
			}
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].CreatedAt.Equal(matches[j].CreatedAt) {
			return matches[i].ID > matches[j].ID
		}
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func timelineItemSearchText(item TimelineItem) string {
	return strings.Join([]string{
		item.Kind,
		item.Role,
		item.Type,
		item.Title,
		item.Content,
		item.Tool,
		item.Input,
		item.Output,
		item.Status,
		item.Diff,
		item.Risk,
		item.Reason,
	}, "\n")
}

func (r *Repository) listBySessionAsc(ctx context.Context, sessionID string) ([]Task, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, session_id, title, user_input, natural, status, workspace, approval_granted, created_at, updated_at, completed_at
		FROM tasks
		WHERE session_id = ?
		ORDER BY created_at ASC, id ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

func timelineItemsFromEvents(sessionID string, task Task, events []Event) []TimelineItem {
	var items []TimelineItem
	for _, event := range events {
		item, ok := timelineItemFromEvent(sessionID, task, event)
		if ok {
			items = append(items, item)
		}
	}
	return items
}

func timelineItemFromEvent(sessionID string, task Task, event Event) (TimelineItem, bool) {
	var payload EventPayload
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		return TimelineItem{}, false
	}
	item := TimelineItem{
		ID:        event.ID,
		SessionID: sessionID,
		TaskID:    task.ID,
		Type:      string(event.Type),
		Title:     task.Title,
		CreatedAt: event.CreatedAt,
	}
	switch event.Type {
	case EventSummary:
		item.Kind = "message"
		item.Role = "assistant"
		item.Content = payload.Message
	case EventToolCall:
		item.Kind = "tool_call"
		item.Tool = payload.Tool
		item.Input = payload.Input
	case EventToolResult:
		item.Kind = "tool_result"
		item.Tool = payload.Tool
		item.Input = payload.Input
		item.Output = payload.Output
		item.Status = payload.Status
	case EventDiff:
		item.Kind = "diff"
		item.Diff = payload.Diff
	case EventPermissionRequest:
		item.Kind = "approval"
		item.Tool = payload.Tool
		item.Input = payload.Input
		item.Status = payload.Status
		item.Risk = payload.Risk
		item.Reason = payload.Reason
		item.Content = payload.Message
	case EventPermissionApproved, EventPermissionDenied:
		item.Kind = "approval"
		item.Status = payload.Status
		item.Content = payload.Message
	case EventReplanning, EventCompleted, EventCancelled, EventError:
		item.Kind = "status"
		item.Status = payload.Status
		item.Content = payload.Message
		item.Reason = payload.Reason
	default:
		return TimelineItem{}, false
	}
	return item, true
}

func (r *Repository) AppendMessage(ctx context.Context, sessionID string, role string, content string, taskID string) (Message, error) {
	sessionID = strings.TrimSpace(sessionID)
	role = strings.TrimSpace(role)
	content = strings.TrimSpace(content)
	taskID = strings.TrimSpace(taskID)
	if sessionID == "" {
		return Message{}, fmt.Errorf("session id is required")
	}
	if role == "" {
		return Message{}, fmt.Errorf("role is required")
	}
	if content == "" {
		return Message{}, fmt.Errorf("content is required")
	}
	if _, err := r.GetSession(ctx, sessionID); err != nil {
		return Message{}, err
	}
	now := time.Now().UTC()
	message := Message{
		ID:        newID("msg"),
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		TaskID:    taskID,
		CreatedAt: now,
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO session_messages (id, session_id, role, content, task_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, message.ID, message.SessionID, message.Role, message.Content, message.TaskID, formatTime(message.CreatedAt))
	if err != nil {
		return Message{}, err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatTime(now), sessionID)
	if err != nil {
		return Message{}, err
	}
	return message, nil
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

func (r *Repository) GrantApproval(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE tasks
		SET approval_granted = 1, status = ?, updated_at = ?, completed_at = NULL
		WHERE id = ?
	`, string(StatusDraft), formatTime(now), id)
	return err
}

func (r *Repository) DenyApproval(ctx context.Context, id string, reason string) error {
	if err := r.UpdateStatus(ctx, id, StatusCancelled); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "permission denied"
	}
	return r.AppendEvent(ctx, id, EventPermissionDenied, EventPayload{
		Message: reason,
		Status:  string(StatusCancelled),
	})
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
	return r.eventsAfter(ctx, taskID, 0, limit)
}

func (r *Repository) EventsAfter(ctx context.Context, taskID string, afterSeq int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	return r.eventsAfter(ctx, taskID, afterSeq, limit)
}

func (r *Repository) eventsAfter(ctx context.Context, taskID string, afterSeq int64, limit int) ([]Event, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT rowid, id, task_id, type, payload_json, created_at
		FROM task_events
		WHERE task_id = ? AND rowid > ?
		ORDER BY rowid ASC
		LIMIT ?
	`, taskID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var event Event
		var createdAt string
		if err := rows.Scan(&event.Seq, &event.ID, &event.TaskID, &event.Type, &event.Payload, &createdAt); err != nil {
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
		var natural, approvalGranted int
		if err := rows.Scan(&task.ID, &task.SessionID, &task.Title, &task.UserInput, &natural, &task.Status, &task.Workspace, &approvalGranted, &createdAt, &updatedAt, &completedAt); err != nil {
			return nil, err
		}
		task.Natural = natural != 0
		task.ApprovalGranted = approvalGranted != 0
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

type sessionQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *Repository) getSessionTx(ctx context.Context, querier sessionQuerier, id string) (Session, error) {
	row := querier.QueryRowContext(ctx, `
		SELECT id, title, workspace, last_task_id, created_at, updated_at
		FROM sessions
		WHERE id = ?
	`, id)
	return scanSession(row)
}

type scanner interface {
	Scan(...any) error
}

func scanSession(row scanner) (Session, error) {
	var session Session
	var createdAt, updatedAt string
	if err := row.Scan(&session.ID, &session.Title, &session.Workspace, &session.LastTaskID, &createdAt, &updatedAt); err != nil {
		return Session{}, err
	}
	session.CreatedAt = parseTime(createdAt)
	session.UpdatedAt = parseTime(updatedAt)
	return session, nil
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
