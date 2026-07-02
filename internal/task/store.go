package task

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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

const (
	transcriptEntrySchemaVersion = 9
	maxTaskRelationLabelRunes    = 64
)

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db, subscribers: map[string][]chan struct{}{}}
}

func normalizeTaskRelationLabel(field string, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len([]rune(trimmed)) > maxTaskRelationLabelRunes {
		return "", fmt.Errorf("%s must be at most %d characters", field, maxTaskRelationLabelRunes)
	}
	return trimmed, nil
}

func (r *Repository) ensureThreadInWorkspaceTx(ctx context.Context, querier sessionQuerier, field string, id string, workspace string) error {
	if id == "" {
		return nil
	}
	var threadWorkspace, archivedAt string
	err := querier.QueryRowContext(ctx, `
		SELECT workspace, archived_at
		FROM conversation_threads
		WHERE id = ?
	`, id).Scan(&threadWorkspace, &archivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s %q: thread not found", field, id)
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(archivedAt) != "" {
		return fmt.Errorf("%s %q is archived", field, id)
	}
	if threadWorkspace != workspace {
		return fmt.Errorf("%s %q belongs to workspace %q, not %q", field, id, threadWorkspace, workspace)
	}
	return nil
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
	origin, automation, err := NormalizeAutomationMetadata(request)
	if err != nil {
		return Task{}, err
	}
	schedule, err := NormalizeScheduleMetadata(origin, request.Schedule)
	if err != nil {
		return Task{}, err
	}
	parentTaskID := strings.TrimSpace(request.ParentTaskID)
	parentThreadID := strings.TrimSpace(request.ParentThreadID)
	childThreadID := strings.TrimSpace(request.ChildThreadID)
	subagentName, err := normalizeTaskRelationLabel("subagent_name", request.SubagentName)
	if err != nil {
		return Task{}, err
	}
	role, err := normalizeTaskRelationLabel("role", request.Role)
	if err != nil {
		return Task{}, err
	}
	requestModelConfig := request.ModelConfig
	scope := normalizeTaskScope(request.Scope)
	approvalGrants := normalizeStringList(request.ApprovalGrants)
	now := time.Now().UTC()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	if err := r.ensureThreadInWorkspaceTx(ctx, tx, "parent_thread_id", parentThreadID, workspace); err != nil {
		return Task{}, err
	}
	if err := r.ensureThreadInWorkspaceTx(ctx, tx, "child_thread_id", childThreadID, workspace); err != nil {
		return Task{}, err
	}
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
	inheritedScopeFromParent := false
	if parentTaskID != "" {
		parent, err := r.getTaskTx(ctx, tx, parentTaskID)
		if err != nil {
			return Task{}, fmt.Errorf("parent task %q: %w", parentTaskID, err)
		}
		if workspace != parent.Workspace {
			return Task{}, fmt.Errorf("child workspace %q must match parent workspace %q", workspace, parent.Workspace)
		}
		if request.AutoApproveParent {
			return Task{}, fmt.Errorf("child task cannot approve parent permissions")
		}
		if len(approvalGrants) > 0 {
			return Task{}, fmt.Errorf("child task approval grants are not allowed")
		}
		scope = defaultChildScope(parent.Scope, scope)
		if err := validateChildScopeWithinParent(parent.Scope, scope); err != nil {
			return Task{}, err
		}
		if requestModelConfig == nil && parent.ModelConfig != nil {
			inherited := *parent.ModelConfig
			inherited.Source = "parent_task"
			requestModelConfig = &inherited
		}
		inheritedScopeFromParent = true
	}
	scopeJSON, err := marshalTaskScope(scope)
	if err != nil {
		return Task{}, err
	}
	approvalGrantsJSON, err := marshalStringList(approvalGrants)
	if err != nil {
		return Task{}, err
	}
	modelConfig := normalizeModelConfig(requestModelConfig)
	task := Task{
		ID:                       newID("task"),
		SessionID:                sessionID,
		Title:                    titleFromPrompt(prompt),
		UserInput:                prompt,
		Natural:                  request.Natural,
		Status:                   StatusDraft,
		Workspace:                workspace,
		Origin:                   origin,
		Automation:               automation,
		ScheduleID:               schedule.ID,
		Schedule:                 schedule,
		ApprovalGranted:          false,
		ParentTaskID:             parentTaskID,
		ParentThreadID:           parentThreadID,
		ChildThreadID:            childThreadID,
		SubagentName:             subagentName,
		Role:                     role,
		Scope:                    scope,
		InheritedScopeFromParent: inheritedScopeFromParent,
		ApprovalGrants:           approvalGrants,
		ModelConfig:              modelConfig,
		CreatedAt:                now,
		UpdatedAt:                now,
	}
	_, err = tx.ExecContext(ctx, `
				INSERT INTO tasks (id, session_id, title, user_input, natural, status, workspace, origin, automation_kind, automation_risk, automation_source, automation_trigger, schedule_id, schedule_catch_up_policy, schedule_missed_runs, schedule_max_catch_up_runs, schedule_catch_up_runs, approval_granted, parent_task_id, parent_thread_id, child_thread_id, subagent_name, role, scope_json, inherited_scope_from_parent, approval_grants_json, model_provider, model_name, model_base_url, model_profile, model_source, created_at, updated_at, completed_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
			`, task.ID, task.SessionID, task.Title, task.UserInput, boolInt(task.Natural), string(task.Status), task.Workspace, string(task.Origin), string(task.Automation.Kind), string(task.Automation.Risk), task.Automation.Source, task.Automation.Trigger, task.Schedule.ID, string(task.Schedule.CatchUpPolicy), task.Schedule.MissedRuns, task.Schedule.MaxCatchUpRuns, task.Schedule.CatchUpRuns, boolInt(task.ApprovalGranted), task.ParentTaskID, task.ParentThreadID, task.ChildThreadID, task.SubagentName, task.Role, scopeJSON, boolInt(task.InheritedScopeFromParent), approvalGrantsJSON, modelConfigValue(modelConfig, "provider"), modelConfigValue(modelConfig, "model"), modelConfigValue(modelConfig, "base_url"), modelConfigValue(modelConfig, "profile"), modelConfigValue(modelConfig, "source"), formatTime(task.CreatedAt), formatTime(task.UpdatedAt))
	if err != nil {
		return Task{}, err
	}
	if parentTaskID != "" {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO subagent_relations (id, parent_task_id, subagent_task_id, relation, schema_version, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, newID("subagent"), parentTaskID, task.ID, "child_task", 5, formatTime(now))
		if err != nil {
			return Task{}, err
		}
	}
	messageID := newID("msg")
	_, err = tx.ExecContext(ctx, `
			INSERT INTO session_messages (id, session_id, role, content, task_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, messageID, task.SessionID, "user", task.UserInput, task.ID, formatTime(now))
	if err != nil {
		return Task{}, err
	}
	err = r.insertTranscriptEntryTx(ctx, tx, TimelineItem{
		ID:        messageID,
		SessionID: task.SessionID,
		TaskID:    task.ID,
		Kind:      "message",
		Role:      "user",
		Content:   task.UserInput,
		CreatedAt: now,
	})
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
	return r.getTaskTx(ctx, r.db, id)
}

func (r *Repository) UpdateTaskModelConfig(ctx context.Context, id string, config ModelConfig) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("task id is required")
	}
	normalized := normalizeModelConfig(&config)
	_, err := r.db.ExecContext(ctx, `
		UPDATE tasks
		SET model_provider = ?, model_name = ?, model_base_url = ?, model_profile = ?, model_source = ?
		WHERE id = ?
	`, modelConfigValue(normalized, "provider"), modelConfigValue(normalized, "model"), modelConfigValue(normalized, "base_url"), modelConfigValue(normalized, "profile"), modelConfigValue(normalized, "source"), id)
	return err
}

func (r *Repository) getTaskTx(ctx context.Context, querier sessionQuerier, id string) (Task, error) {
	var task Task
	var createdAt, updatedAt string
	var completedAt sql.NullString
	var scopeJSON, approvalGrantsJSON string
	var modelProvider, modelName, modelBaseURL, modelProfile, modelSource string
	var natural, approvalGranted, inheritedScopeFromParent int
	err := querier.QueryRowContext(ctx, `
					SELECT id, session_id, title, user_input, natural, status, workspace, origin, automation_kind, automation_risk, automation_source, automation_trigger, schedule_id, schedule_catch_up_policy, schedule_missed_runs, schedule_max_catch_up_runs, schedule_catch_up_runs, approval_granted, parent_task_id, parent_thread_id, child_thread_id, subagent_name, role, scope_json, inherited_scope_from_parent, approval_grants_json, model_provider, model_name, model_base_url, model_profile, model_source, created_at, updated_at, completed_at
				FROM tasks
				WHERE id = ?
				`, id).Scan(&task.ID, &task.SessionID, &task.Title, &task.UserInput, &natural, &task.Status, &task.Workspace, &task.Origin, &task.Automation.Kind, &task.Automation.Risk, &task.Automation.Source, &task.Automation.Trigger, &task.Schedule.ID, &task.Schedule.CatchUpPolicy, &task.Schedule.MissedRuns, &task.Schedule.MaxCatchUpRuns, &task.Schedule.CatchUpRuns, &approvalGranted, &task.ParentTaskID, &task.ParentThreadID, &task.ChildThreadID, &task.SubagentName, &task.Role, &scopeJSON, &inheritedScopeFromParent, &approvalGrantsJSON, &modelProvider, &modelName, &modelBaseURL, &modelProfile, &modelSource, &createdAt, &updatedAt, &completedAt)
	if err != nil {
		return Task{}, err
	}
	task.Natural = natural != 0
	task.ApprovalGranted = approvalGranted != 0
	task.ScheduleID = task.Schedule.ID
	task.InheritedScopeFromParent = inheritedScopeFromParent != 0
	task.Scope = unmarshalTaskScope(scopeJSON)
	task.ApprovalGrants = unmarshalStringList(approvalGrantsJSON)
	task.ModelConfig = normalizeModelConfig(&ModelConfig{Provider: modelProvider, Model: modelName, BaseURL: modelBaseURL, Profile: modelProfile, Source: modelSource})
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
				SELECT id, session_id, title, user_input, natural, status, workspace, origin, automation_kind, automation_risk, automation_source, automation_trigger, schedule_id, schedule_catch_up_policy, schedule_missed_runs, schedule_max_catch_up_runs, schedule_catch_up_runs, approval_granted, parent_task_id, parent_thread_id, child_thread_id, subagent_name, role, scope_json, inherited_scope_from_parent, approval_grants_json, model_provider, model_name, model_base_url, model_profile, model_source, created_at, updated_at, completed_at
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
				SELECT id, session_id, title, user_input, natural, status, workspace, origin, automation_kind, automation_risk, automation_source, automation_trigger, schedule_id, schedule_catch_up_policy, schedule_missed_runs, schedule_max_catch_up_runs, schedule_catch_up_runs, approval_granted, parent_task_id, parent_thread_id, child_thread_id, subagent_name, role, scope_json, inherited_scope_from_parent, approval_grants_json, model_provider, model_name, model_base_url, model_profile, model_source, created_at, updated_at, completed_at
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
				SELECT id, session_id, title, user_input, natural, status, workspace, origin, automation_kind, automation_risk, automation_source, automation_trigger, schedule_id, schedule_catch_up_policy, schedule_missed_runs, schedule_max_catch_up_runs, schedule_catch_up_runs, approval_granted, parent_task_id, parent_thread_id, child_thread_id, subagent_name, role, scope_json, inherited_scope_from_parent, approval_grants_json, model_provider, model_name, model_base_url, model_profile, model_source, created_at, updated_at, completed_at
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

func (r *Repository) EnsureSession(ctx context.Context, id string, title string, workspace string) (Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Session{}, fmt.Errorf("session id is required")
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return Session{}, fmt.Errorf("workspace is required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "New session"
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	session, err := r.getSessionTx(ctx, tx, id)
	if err == nil {
		if session.Workspace != workspace {
			return Session{}, fmt.Errorf("session workspace %q must match requested workspace %q", session.Workspace, workspace)
		}
		return session, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return Session{}, err
	}
	now := time.Now().UTC()
	session = Session{
		ID:        id,
		Title:     titleFromPrompt(title),
		Workspace: workspace,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sessions (id, title, workspace, last_task_id, created_at, updated_at)
		VALUES (?, ?, ?, '', ?, ?)
	`, session.ID, session.Title, session.Workspace, formatTime(session.CreatedAt), formatTime(session.UpdatedAt))
	if err != nil {
		return Session{}, err
	}
	return session, tx.Commit()
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
	materialized, err := r.TranscriptEntries(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	if len(materialized) > 0 {
		return materialized, nil
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

func (r *Repository) TranscriptEntries(ctx context.Context, sessionID string, limit int) ([]TimelineItem, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, session_id, task_id, kind, role, type, title, content, tool, tool_call_id, tool_result_id, input, output, target, status, diff, risk, reason, provider, model, profile, created_at
		FROM (
			SELECT id, session_id, task_id, kind, role, type, title, content, tool, tool_call_id, tool_result_id, input, output, target, status, diff, risk, reason, provider, model, profile, created_at
			FROM transcript_entries
			WHERE session_id = ?
			ORDER BY created_at DESC, id DESC
			LIMIT ?
		)
		ORDER BY created_at ASC, id ASC
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []TimelineItem
	for rows.Next() {
		item, err := scanTranscriptEntry(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type transcriptExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type transcriptScanner interface {
	Scan(dest ...any) error
}

func (r *Repository) insertTranscriptEntryTx(ctx context.Context, exec transcriptExec, item TimelineItem) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("transcript entry id is required")
	}
	if strings.TrimSpace(item.SessionID) == "" {
		return fmt.Errorf("transcript entry session id is required")
	}
	if strings.TrimSpace(item.Kind) == "" {
		return fmt.Errorf("transcript entry kind is required")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	item = normalizeTranscriptEntryForStorage(item)
	_, err := exec.ExecContext(ctx, `
		INSERT INTO transcript_entries (
			id, session_id, task_id, kind, role, type, title, content, tool, tool_call_id, tool_result_id, input, output, target,
			status, diff, risk, reason, provider, model, profile, schema_version, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			session_id = excluded.session_id,
			task_id = excluded.task_id,
			kind = excluded.kind,
			role = excluded.role,
			type = excluded.type,
			title = excluded.title,
			content = excluded.content,
			tool = excluded.tool,
			tool_call_id = excluded.tool_call_id,
			tool_result_id = excluded.tool_result_id,
			input = excluded.input,
			output = excluded.output,
			target = excluded.target,
			status = excluded.status,
			diff = excluded.diff,
			risk = excluded.risk,
			reason = excluded.reason,
			provider = excluded.provider,
			model = excluded.model,
			profile = excluded.profile,
			schema_version = excluded.schema_version,
			created_at = excluded.created_at
	`, item.ID, item.SessionID, item.TaskID, item.Kind, item.Role, item.Type, item.Title, item.Content, item.Tool, item.ToolCallID, item.ToolResultID, item.Input, item.Output, item.Target, item.Status, item.Diff, item.Risk, item.Reason, item.Provider, item.Model, item.Profile, transcriptEntrySchemaVersion, formatTime(item.CreatedAt))
	return err
}

func (r *Repository) insertApprovalItemTx(ctx context.Context, exec transcriptExec, id string, taskID string, payload EventPayload, now time.Time) error {
	id = strings.TrimSpace(firstNonEmpty(payload.ID, id))
	taskID = strings.TrimSpace(taskID)
	if id == "" {
		return fmt.Errorf("approval item id is required")
	}
	if taskID == "" {
		return fmt.Errorf("approval item task id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	toolName := strings.TrimSpace(payload.Tool)
	_, err := exec.ExecContext(ctx, `
		INSERT INTO approval_items (
			id, task_id, tool_call_id, tool, args_preview, risk, command_preview, diff_preview, reason,
			status, decision, decided_by, resolved_at, schema_version, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', '', '', '', 12, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			task_id = excluded.task_id,
			tool_call_id = excluded.tool_call_id,
			tool = excluded.tool,
			args_preview = excluded.args_preview,
			risk = excluded.risk,
			command_preview = excluded.command_preview,
			diff_preview = excluded.diff_preview,
			reason = excluded.reason,
			status = excluded.status,
			decision = excluded.decision,
			decided_by = excluded.decided_by,
			resolved_at = excluded.resolved_at,
			schema_version = excluded.schema_version,
			updated_at = excluded.updated_at
	`, id, taskID, strings.TrimSpace(payload.ToolCallID), toolName, previewText(payload.Input), strings.TrimSpace(payload.Risk), commandPreview(payload), previewText(payload.Diff), strings.TrimSpace(payload.Reason), formatTime(now), formatTime(now))
	return err
}

func (r *Repository) ApprovalItemForTask(ctx context.Context, taskID string) (ApprovalItem, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, task_id, tool_call_id, tool, args_preview, risk, command_preview, diff_preview, reason,
			status, decision, decided_by, resolved_at, created_at, updated_at
		FROM approval_items
		WHERE task_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, strings.TrimSpace(taskID))
	item, err := scanApprovalItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ApprovalItem{}, false, nil
		}
		return ApprovalItem{}, false, err
	}
	return item, true, nil
}

func (r *Repository) resolveApprovalItemTx(ctx context.Context, exec transcriptExec, taskID string, decision string, decidedBy string, now time.Time) error {
	taskID = strings.TrimSpace(taskID)
	decision = strings.TrimSpace(decision)
	decidedBy = strings.TrimSpace(decidedBy)
	if decidedBy == "" {
		decidedBy = "user"
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := exec.ExecContext(ctx, `
		UPDATE approval_items
		SET status = 'resolved', decision = ?, decided_by = ?, resolved_at = ?, updated_at = ?
		WHERE id = (
			SELECT id FROM approval_items
			WHERE task_id = ? AND status = 'pending' AND decision = ''
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		)
	`, decision, decidedBy, formatTime(now), formatTime(now), taskID)
	return err
}

type approvalItemScanner interface {
	Scan(dest ...any) error
}

func scanApprovalItem(scanner approvalItemScanner) (ApprovalItem, error) {
	var item ApprovalItem
	var resolvedAt, createdAt, updatedAt string
	if err := scanner.Scan(&item.ID, &item.TaskID, &item.ToolCallID, &item.ToolName, &item.ArgsPreview, &item.Risk, &item.CommandPreview, &item.DiffPreview, &item.Reason, &item.Status, &item.Decision, &item.DecidedBy, &resolvedAt, &createdAt, &updatedAt); err != nil {
		return ApprovalItem{}, err
	}
	if strings.TrimSpace(resolvedAt) != "" {
		parsed := parseTime(resolvedAt)
		item.ResolvedAt = &parsed
	}
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, nil
}

func commandPreview(payload EventPayload) string {
	input := strings.TrimSpace(payload.Input)
	if input == "" {
		return ""
	}
	return previewText(input)
}

func previewText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 240 {
		return value
	}
	return strings.TrimSpace(value[:240]) + "..."
}

func normalizeTranscriptEntryForStorage(item TimelineItem) TimelineItem {
	switch item.Kind {
	case "tool_result":
		path := artifactPathFromText(item.Output)
		if path == "" && len([]rune(item.Output)) > maxInlineContextFieldRunes {
			path = "inline:tool_result:" + item.ID
		}
		if path != "" {
			item.Target = firstNonEmpty(item.Target, path)
			if strings.TrimSpace(item.Content) == "" {
				item.Content = firstContextLine(item.Output)
			}
		}
	case "artifact":
		item.Target = firstNonEmpty(item.Target, item.Output)
	}
	item.Content = truncateContextField(item.Content)
	item.Input = truncateContextField(item.Input)
	item.Output = truncateContextField(item.Output)
	item.Diff = truncateContextField(item.Diff)
	return item
}

func scanTranscriptEntry(scanner transcriptScanner) (TimelineItem, error) {
	var item TimelineItem
	var createdAt string
	if err := scanner.Scan(
		&item.ID,
		&item.SessionID,
		&item.TaskID,
		&item.Kind,
		&item.Role,
		&item.Type,
		&item.Title,
		&item.Content,
		&item.Tool,
		&item.ToolCallID,
		&item.ToolResultID,
		&item.Input,
		&item.Output,
		&item.Target,
		&item.Status,
		&item.Diff,
		&item.Risk,
		&item.Reason,
		&item.Provider,
		&item.Model,
		&item.Profile,
		&createdAt,
	); err != nil {
		return TimelineItem{}, err
	}
	item.CreatedAt = parseTime(createdAt)
	return item, nil
}

const (
	defaultContextItemLimit    = 80
	maxContextItemLimit        = 200
	defaultContextTokenBudget  = 8000
	minContextTokenBudget      = 128
	maxContextTokenBudget      = 200000
	maxInlineContextFieldRunes = 1600
	maxContextTodos            = 5
	maxContextMemories         = 5
)

func (r *Repository) ContextEnvelope(ctx context.Context, sessionID string, request ContextRequest) (ContextEnvelope, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ContextEnvelope{}, fmt.Errorf("session id is required")
	}
	session, err := r.GetSession(ctx, sessionID)
	if err != nil {
		return ContextEnvelope{}, err
	}
	itemLimit := normalizeContextItemLimit(request.ItemLimit)
	tokenBudget := normalizeContextTokenBudget(request.TokenBudget)
	timeline, err := r.Timeline(ctx, sessionID, itemLimit+1)
	if err != nil {
		return ContextEnvelope{}, err
	}
	truncated := false
	if len(timeline) > itemLimit {
		timeline = timeline[len(timeline)-itemLimit:]
		truncated = true
	}
	transcript, artifactRefs := compactContextTimeline(timeline)
	transcript = filterContextTranscript(transcript)
	artifactRefs = contextArtifactRefs(transcript, artifactRefs)
	availableTranscript := len(transcript)
	availableArtifactRefs := len(artifactRefs)
	todos, availableTodos, err := r.contextTodos(ctx, sessionID)
	if err != nil {
		return ContextEnvelope{}, err
	}
	memories, availableMemories, err := r.contextMemories(ctx, session.Workspace, maxContextMemories)
	if err != nil {
		return ContextEnvelope{}, err
	}
	estimated, buckets := estimatePackedContextBudget(transcript, artifactRefs, memories, todos)
	for estimated > tokenBudget {
		nextTranscript, nextArtifactRefs, nextMemories, nextTodos, ok := trimPackedContext(transcript, artifactRefs, memories, todos)
		if !ok {
			break
		}
		transcript, artifactRefs, memories, todos = nextTranscript, nextArtifactRefs, nextMemories, nextTodos
		truncated = true
		estimated, buckets = estimatePackedContextBudget(transcript, artifactRefs, memories, todos)
	}
	if estimated > tokenBudget {
		truncated = true
	}
	summaries, transcriptBoundaries := contextMetadata(transcript)
	compactBoundaries, err := r.CompactBoundaries(ctx, sessionID, itemLimit)
	if err != nil {
		return ContextEnvelope{}, err
	}
	if len(compactBoundaries) == 0 {
		compactBoundaries = transcriptBoundaries
	}
	return ContextEnvelope{
		Session: session,
		Budget: ContextBudget{
			MaxTokens:       tokenBudget,
			EstimatedTokens: estimated,
			ItemLimit:       itemLimit,
			Truncated:       truncated,
			Buckets:         buckets,
		},
		Transcript:        transcript,
		Todos:             todos,
		Memories:          memories,
		Summaries:         summaries,
		ArtifactRefs:      artifactRefs,
		CompactBoundaries: compactBoundaries,
		Pack: contextPackSources(contextPackCounts{
			TranscriptAvailable: availableTranscript,
			TodoAvailable:       availableTodos,
			MemoryAvailable:     availableMemories,
			ArtifactAvailable:   availableArtifactRefs,
		}, transcript, todos, memories, artifactRefs),
		Diagnostics: contextDiagnostics(transcript, todos, memories, artifactRefs),
		GeneratedAt: time.Now().UTC(),
	}, nil
}

func trimPackedContext(transcript []TimelineItem, artifactRefs []ContextArtifactRef, memories []ContextMemory, todos []Todo) ([]TimelineItem, []ContextArtifactRef, []ContextMemory, []Todo, bool) {
	if len(transcript) > 1 {
		transcript = transcript[1:]
		return transcript, contextArtifactRefs(transcript, nil), memories, todos, true
	}
	if len(artifactRefs) > 0 {
		return transcript, artifactRefs[:len(artifactRefs)-1], memories, todos, true
	}
	if len(todos) > 0 {
		return transcript, artifactRefs, memories, todos[:len(todos)-1], true
	}
	if len(memories) > 0 {
		return transcript, artifactRefs, memories[:len(memories)-1], todos, true
	}
	return transcript, artifactRefs, memories, todos, false
}

func filterContextTranscript(items []TimelineItem) []TimelineItem {
	filtered := items[:0]
	for _, item := range items {
		if item.Kind == "todo" && item.Status == TodoStatusDone {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func (r *Repository) contextTodos(ctx context.Context, sessionID string) ([]Todo, int, error) {
	all, err := r.TodosBySession(ctx, sessionID)
	if err != nil {
		return nil, 0, err
	}
	selected := make([]Todo, 0, len(all))
	for _, todo := range all {
		if todo.Status == TodoStatusDone {
			continue
		}
		selected = append(selected, todo)
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if left, right := todoContextPriorityRank(selected[i].Priority), todoContextPriorityRank(selected[j].Priority); left != right {
			return left < right
		}
		if !selected[i].UpdatedAt.Equal(selected[j].UpdatedAt) {
			return selected[i].UpdatedAt.After(selected[j].UpdatedAt)
		}
		return selected[i].ID < selected[j].ID
	})
	if len(selected) > maxContextTodos {
		selected = selected[:maxContextTodos]
	}
	return selected, len(all), nil
}

func todoContextPriorityRank(priority string) int {
	switch priority {
	case TodoPriorityCritical:
		return 0
	case TodoPriorityHigh:
		return 1
	case TodoPriorityNormal:
		return 2
	default:
		return 3
	}
}

func (r *Repository) contextMemories(ctx context.Context, workspace string, limit int) ([]ContextMemory, int, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, 0, nil
	}
	now := formatTime(time.Now().UTC())
	var available int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM memories
		WHERE enabled = 1
			AND workspace = ?
			AND (expires_at IS NULL OR expires_at = '' OR expires_at > ?)
	`, workspace, now).Scan(&available); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = maxContextMemories
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, text, kind, source, workspace, importance, created_at, updated_at, expires_at
		FROM memories
		WHERE enabled = 1
			AND workspace = ?
			AND (expires_at IS NULL OR expires_at = '' OR expires_at > ?)
		ORDER BY importance DESC, updated_at DESC, id DESC
		LIMIT ?
	`, workspace, now, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	memories := make([]ContextMemory, 0, limit)
	for rows.Next() {
		memory, err := scanContextMemory(rows)
		if err != nil {
			return nil, 0, err
		}
		memories = append(memories, memory)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return memories, available, nil
}

func scanContextMemory(scanner interface {
	Scan(dest ...any) error
}) (ContextMemory, error) {
	var memory ContextMemory
	var createdAt, updatedAt string
	var expiresAt sql.NullString
	if err := scanner.Scan(&memory.ID, &memory.Text, &memory.Kind, &memory.Source, &memory.Workspace, &memory.Importance, &createdAt, &updatedAt, &expiresAt); err != nil {
		return ContextMemory{}, err
	}
	memory.CreatedAt = parseTime(createdAt)
	memory.UpdatedAt = parseTime(updatedAt)
	if expiresAt.Valid && strings.TrimSpace(expiresAt.String) != "" {
		parsed := parseTime(expiresAt.String)
		memory.ExpiresAt = &parsed
	}
	return memory, nil
}

func normalizeContextItemLimit(limit int) int {
	if limit <= 0 {
		return defaultContextItemLimit
	}
	if limit > maxContextItemLimit {
		return maxContextItemLimit
	}
	return limit
}

func normalizeContextTokenBudget(budget int) int {
	if budget <= 0 {
		return defaultContextTokenBudget
	}
	if budget < minContextTokenBudget {
		return minContextTokenBudget
	}
	if budget > maxContextTokenBudget {
		return maxContextTokenBudget
	}
	return budget
}

func compactContextTimeline(items []TimelineItem) ([]TimelineItem, []ContextArtifactRef) {
	transcript := make([]TimelineItem, 0, len(items))
	var refs []ContextArtifactRef
	for _, item := range items {
		item, ref, hasRef := compactContextItem(item)
		transcript = append(transcript, item)
		if hasRef {
			refs = append(refs, ref)
		}
	}
	return transcript, refs
}

func compactContextItem(item TimelineItem) (TimelineItem, ContextArtifactRef, bool) {
	ref := ContextArtifactRef{TaskID: item.TaskID, Tool: item.Tool, CreatedAt: item.CreatedAt}
	switch item.Kind {
	case "tool_result":
		ref.Path = firstNonEmpty(item.Target, artifactPathFromText(item.Output))
		if ref.Path == "" && len([]rune(item.Output)) > maxInlineContextFieldRunes {
			ref.Path = "inline:tool_result:" + item.ID
		}
		if ref.Path != "" {
			ref.Summary = firstContextLine(item.Output)
			item.Output = truncateContextField(item.Output)
			return item, ref, true
		}
	case "artifact":
		ref.Path = firstNonEmpty(item.Target, item.Output)
		ref.Summary = item.Content
		return item, ref, true
	}
	item.Content = truncateContextField(item.Content)
	item.Input = truncateContextField(item.Input)
	item.Output = truncateContextField(item.Output)
	item.Diff = truncateContextField(item.Diff)
	return item, ContextArtifactRef{}, false
}

func contextArtifactRefs(items []TimelineItem, initial []ContextArtifactRef) []ContextArtifactRef {
	refs := append([]ContextArtifactRef{}, initial...)
	for _, item := range items {
		if item.Kind != "tool_result" {
			continue
		}
		path := firstNonEmpty(item.Target, artifactPathFromText(item.Output))
		if path == "" {
			continue
		}
		refs = append(refs, ContextArtifactRef{
			TaskID:    item.TaskID,
			Tool:      item.Tool,
			Path:      path,
			Summary:   firstContextLine(item.Output),
			CreatedAt: item.CreatedAt,
		})
	}
	refs = append(refs, artifactRefsFromTimeline(items)...)
	return dedupeArtifactRefs(refs)
}

func truncateContextField(value string) string {
	runes := []rune(value)
	if len(runes) <= maxInlineContextFieldRunes {
		return value
	}
	return string(runes[:maxInlineContextFieldRunes]) + "\n... truncated for context budget"
}

func artifactPathFromText(value string) string {
	for _, field := range strings.Fields(value) {
		cleaned := strings.Trim(field, ".,;:()[]{}\"'")
		if strings.Contains(cleaned, ".liora/tool-results") || strings.HasPrefix(cleaned, "file:") || strings.HasPrefix(cleaned, "artifact://") {
			return strings.TrimPrefix(cleaned, "file:")
		}
	}
	if strings.Contains(strings.ToLower(value), "truncated") {
		return "inline:truncated-tool-result"
	}
	return ""
}

func firstContextLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	line, _, _ := strings.Cut(value, "\n")
	return truncateContextField(line)
}

func estimateContextBudget(items []TimelineItem, artifactRefs []ContextArtifactRef) (int, []ContextBudgetBucket) {
	return estimatePackedContextBudget(items, artifactRefs, nil, nil)
}

func estimatePackedContextBudget(items []TimelineItem, artifactRefs []ContextArtifactRef, memories []ContextMemory, todos []Todo) (int, []ContextBudgetBucket) {
	buckets := []ContextBudgetBucket{
		{Name: "system"},
		{Name: "user"},
		{Name: "transcript"},
		{Name: "memory"},
		{Name: "tool_result"},
		{Name: "artifact_preview"},
	}
	index := make(map[string]int, len(buckets))
	for i, bucket := range buckets {
		index[bucket.Name] = i
	}
	for _, item := range items {
		bucket := contextBudgetBucketName(item)
		tokens := 4 + estimateTokens(contextItemBudgetText(item))
		buckets[index[bucket]].EstimatedTokens += tokens
		buckets[index[bucket]].Items++
	}
	for _, ref := range artifactRefs {
		tokens := 2 + estimateTokens(strings.Join([]string{ref.Tool, ref.Path, ref.Summary}, "\n"))
		buckets[index["artifact_preview"]].EstimatedTokens += tokens
		buckets[index["artifact_preview"]].Items++
	}
	for _, memory := range memories {
		tokens := 4 + estimateTokens(contextMemoryBudgetText(memory))
		buckets[index["memory"]].EstimatedTokens += tokens
		buckets[index["memory"]].Items++
	}
	for _, todo := range todos {
		tokens := 4 + estimateTokens(contextTodoBudgetText(todo))
		buckets[index["transcript"]].EstimatedTokens += tokens
		buckets[index["transcript"]].Items++
	}
	total := 0
	for _, bucket := range buckets {
		total += bucket.EstimatedTokens
	}
	return total, buckets
}

func contextBudgetBucketName(item TimelineItem) string {
	if item.Kind == "message" && item.Role == "user" {
		return "user"
	}
	if item.Kind == "tool_result" {
		return "tool_result"
	}
	if item.Kind == "artifact" {
		return "artifact_preview"
	}
	return "transcript"
}

func contextItemBudgetText(item TimelineItem) string {
	return strings.Join([]string{
		item.Kind,
		item.Role,
		item.Type,
		item.Title,
		item.Content,
		item.Tool,
		item.Input,
		item.Output,
		item.Target,
		item.Status,
		item.Diff,
		item.Risk,
		item.Reason,
	}, "\n")
}

func contextMemoryBudgetText(memory ContextMemory) string {
	return strings.Join([]string{
		memory.Kind,
		memory.Source,
		memory.Workspace,
		memory.Text,
		fmt.Sprintf("%d", memory.Importance),
	}, "\n")
}

func contextTodoBudgetText(todo Todo) string {
	return strings.Join([]string{
		todo.ID,
		todo.Status,
		todo.Priority,
		todo.Content,
		todo.SourceTaskID,
	}, "\n")
}

func estimateContextTokens(items []TimelineItem) int {
	total, _ := estimateContextBudget(items, nil)
	return total
}

func estimateTokens(value string) int {
	runes := len([]rune(value))
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}

func contextMetadata(items []TimelineItem) ([]ContextSummary, []ContextCompactBoundary) {
	var summaries []ContextSummary
	var boundaries []ContextCompactBoundary
	for _, item := range items {
		if item.Kind == "message" && item.Role == "assistant" {
			summaries = append(summaries, ContextSummary{TaskID: item.TaskID, Content: item.Content, CreatedAt: item.CreatedAt})
		}
		if item.Kind == "compact_boundary" {
			boundaries = append(boundaries, ContextCompactBoundary{TaskID: item.TaskID, Summary: item.Content, CreatedAt: item.CreatedAt})
		}
	}
	return summaries, boundaries
}

type contextPackCounts struct {
	TranscriptAvailable int
	TodoAvailable       int
	MemoryAvailable     int
	ArtifactAvailable   int
}

func contextPackSources(counts contextPackCounts, transcript []TimelineItem, todos []Todo, memories []ContextMemory, artifactRefs []ContextArtifactRef) ContextPack {
	return ContextPack{Sources: []ContextPackSource{
		{
			Name:            "transcript",
			Selected:        len(transcript),
			Available:       counts.TranscriptAvailable,
			EstimatedTokens: estimateTimelineItemsTokens(transcript),
			Truncated:       len(transcript) < counts.TranscriptAvailable,
		},
		{
			Name:            "todo",
			Selected:        len(todos),
			Available:       counts.TodoAvailable,
			EstimatedTokens: estimateTodosTokens(todos),
			Truncated:       len(todos) < counts.TodoAvailable,
		},
		{
			Name:            "memory",
			Selected:        len(memories),
			Available:       counts.MemoryAvailable,
			EstimatedTokens: estimateMemoriesTokens(memories),
			Truncated:       len(memories) < counts.MemoryAvailable,
		},
		{
			Name:            "artifact_preview",
			Selected:        len(artifactRefs),
			Available:       counts.ArtifactAvailable,
			EstimatedTokens: estimateArtifactRefsTokens(artifactRefs),
			Truncated:       len(artifactRefs) < counts.ArtifactAvailable,
		},
	}}
}

func contextDiagnostics(transcript []TimelineItem, todos []Todo, memories []ContextMemory, artifactRefs []ContextArtifactRef) []ContextDiagnostic {
	diagnostics := make([]ContextDiagnostic, 0, len(transcript)+len(todos)+len(memories)+len(artifactRefs))
	for _, item := range transcript {
		source, reason := contextTimelineDiagnosticSource(item)
		diagnostics = append(diagnostics, ContextDiagnostic{
			Source:          source,
			ItemID:          item.ID,
			ItemKind:        item.Kind,
			Reason:          reason,
			Summary:         contextTimelineDiagnosticSummary(item),
			EstimatedTokens: 4 + estimateTokens(contextItemBudgetText(item)),
			CreatedAt:       item.CreatedAt,
		})
	}
	for _, todo := range todos {
		diagnostics = append(diagnostics, ContextDiagnostic{
			Source:          "todo",
			ItemID:          todo.ID,
			ItemKind:        todo.Status,
			Reason:          "open todo selected by priority and recent update for the current session",
			Summary:         todo.Content,
			EstimatedTokens: 4 + estimateTokens(contextTodoBudgetText(todo)),
			CreatedAt:       todo.UpdatedAt,
		})
	}
	for _, memory := range memories {
		diagnostics = append(diagnostics, ContextDiagnostic{
			Source:          "memory",
			ItemID:          memory.ID,
			ItemKind:        memory.Kind,
			Reason:          "enabled unexpired memory matched the current workspace and was selected by importance and recency",
			Summary:         memory.Text,
			EstimatedTokens: 4 + estimateTokens(contextMemoryBudgetText(memory)),
			CreatedAt:       memory.UpdatedAt,
		})
	}
	for _, ref := range artifactRefs {
		diagnostics = append(diagnostics, ContextDiagnostic{
			Source:          "artifact_preview",
			ItemID:          ref.Path,
			ItemKind:        "artifact_preview",
			Reason:          "artifact preview selected from transcript tool output or artifact reference instead of inlining full content",
			Summary:         firstNonEmpty(ref.Summary, ref.Path),
			EstimatedTokens: 2 + estimateTokens(strings.Join([]string{ref.Tool, ref.Path, ref.Summary}, "\n")),
			CreatedAt:       ref.CreatedAt,
		})
	}
	return diagnostics
}

func contextTimelineDiagnosticSource(item TimelineItem) (string, string) {
	switch item.Kind {
	case "tool_result":
		return "tool_result", "tool result retained from the current session transcript with bounded inline output"
	case "tool_call":
		return "transcript", "tool call kept as recent session history for continuity"
	case "message":
		if item.Role == "user" {
			return "transcript", "recent user message kept as session history"
		}
		if item.Role == "assistant" {
			return "transcript", "recent assistant response kept as session history and summary material"
		}
	case "compact_boundary":
		return "transcript", "compact boundary kept to explain where earlier transcript was summarized"
	}
	return "transcript", "recent session transcript item selected within item and token budget"
}

func contextTimelineDiagnosticSummary(item TimelineItem) string {
	switch item.Kind {
	case "tool_result":
		return firstContextLine(firstNonEmpty(item.Output, item.Target, item.Content))
	case "tool_call":
		return firstContextLine(firstNonEmpty(item.Tool+" "+item.Input, item.Content))
	case "diff":
		return firstContextLine(item.Diff)
	default:
		return firstContextLine(firstNonEmpty(item.Content, item.Output, item.Target, item.Status, item.Reason))
	}
}

func estimateTimelineItemsTokens(items []TimelineItem) int {
	total := 0
	for _, item := range items {
		total += 4 + estimateTokens(contextItemBudgetText(item))
	}
	return total
}

func estimateArtifactRefsTokens(refs []ContextArtifactRef) int {
	total := 0
	for _, ref := range refs {
		total += 2 + estimateTokens(strings.Join([]string{ref.Tool, ref.Path, ref.Summary}, "\n"))
	}
	return total
}

func estimateMemoriesTokens(memories []ContextMemory) int {
	total := 0
	for _, memory := range memories {
		total += 4 + estimateTokens(contextMemoryBudgetText(memory))
	}
	return total
}

func estimateTodosTokens(todos []Todo) int {
	total := 0
	for _, todo := range todos {
		total += 4 + estimateTokens(contextTodoBudgetText(todo))
	}
	return total
}

func artifactRefsFromTimeline(items []TimelineItem) []ContextArtifactRef {
	var refs []ContextArtifactRef
	for _, item := range items {
		if item.Kind != "artifact" {
			continue
		}
		refs = append(refs, ContextArtifactRef{
			TaskID:    item.TaskID,
			Tool:      item.Tool,
			Path:      firstNonEmpty(item.Target, item.Output),
			Summary:   item.Content,
			CreatedAt: item.CreatedAt,
		})
	}
	return refs
}

func dedupeArtifactRefs(refs []ContextArtifactRef) []ContextArtifactRef {
	seen := make(map[string]struct{}, len(refs))
	deduped := make([]ContextArtifactRef, 0, len(refs))
	for _, ref := range refs {
		key := strings.Join([]string{ref.TaskID, ref.Tool, ref.Path, ref.Summary}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, ref)
	}
	return deduped
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (r *Repository) SearchTimeline(ctx context.Context, workspace string, query string, limit int) ([]TimelineItem, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("timeline search query is required")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	workspace = strings.TrimSpace(workspace)
	pattern := "%" + escapeLike(strings.ToLower(query)) + "%"
	searchFields := []string{
		"te.id",
		"te.session_id",
		"te.task_id",
		"te.kind",
		"te.role",
		"te.type",
		"te.title",
		"te.content",
		"te.tool",
		"te.tool_call_id",
		"te.tool_result_id",
		"te.input",
		"te.output",
		"te.target",
		"te.status",
		"te.diff",
		"te.risk",
		"te.reason",
		"te.provider",
		"te.model",
		"te.profile",
	}
	predicates := make([]string, 0, len(searchFields))
	args := []any{workspace, workspace}
	for _, field := range searchFields {
		predicates = append(predicates, "lower(coalesce("+field+", '')) LIKE ? ESCAPE '\\'")
		args = append(args, pattern)
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `
		SELECT te.id, te.session_id, te.task_id, te.kind, te.role, te.type, te.title, te.content, te.tool, te.tool_call_id, te.tool_result_id, te.input, te.output, te.target, te.status, te.diff, te.risk, te.reason, te.provider, te.model, te.profile, te.created_at
		FROM transcript_entries te
		JOIN sessions s ON s.id = te.session_id
		WHERE (? = '' OR s.workspace = ?)
		  AND (`+strings.Join(predicates, "\n\t\t\tOR ")+`)
		ORDER BY te.created_at DESC, te.id DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var matches []TimelineItem
	for rows.Next() {
		item, err := scanTranscriptEntry(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, item)
	}
	return matches, rows.Err()
}

func escapeLike(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch r {
		case '\\', '%', '_':
			builder.WriteRune('\\')
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func (r *Repository) listBySessionAsc(ctx context.Context, sessionID string) ([]Task, error) {
	rows, err := r.db.QueryContext(ctx, `
			SELECT id, session_id, title, user_input, natural, status, workspace, origin, automation_kind, automation_risk, automation_source, automation_trigger, schedule_id, schedule_catch_up_policy, schedule_missed_runs, schedule_max_catch_up_runs, schedule_catch_up_runs, approval_granted, parent_task_id, parent_thread_id, child_thread_id, subagent_name, role, scope_json, inherited_scope_from_parent, approval_grants_json, model_provider, model_name, model_base_url, model_profile, model_source, created_at, updated_at, completed_at
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
		Provider:  payload.Provider,
		Model:     payload.Model,
		Profile:   payload.Profile,
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
		item.ToolCallID = payload.ToolCallID
		item.ToolResultID = payload.ToolResultID
		item.Input = payload.Input
	case EventToolResult:
		item.Kind = "tool_result"
		item.Tool = payload.Tool
		item.ToolCallID = payload.ToolCallID
		item.ToolResultID = payload.ToolResultID
		item.Input = payload.Input
		item.Output = payload.Output
		item.Status = payload.Status
	case EventTodoUpdated:
		item.Kind = "todo"
		item.Status = payload.Status
		item.Content = payload.Message
	case EventTranscriptEntry:
		item.Kind = "transcript"
		item.Role = payload.Kind
		item.Content = payload.Message
	case EventArtifactReference:
		item.Kind = "artifact"
		item.Tool = payload.Tool
		item.Target = firstNonEmpty(payload.Path, payload.Target)
		item.Output = item.Target
		item.Content = payload.Message
	case EventCompactBoundary:
		item.Kind = "compact_boundary"
		item.Content = payload.Message
		item.Status = payload.Status
	case EventHookRun:
		item.Kind = "hook"
		item.Status = payload.Status
		item.Content = payload.Message
		item.Reason = payload.Reason
	case EventScheduleTriggered:
		item.Kind = "schedule"
		item.Status = payload.Status
		item.Content = payload.Message
	case EventSubagentStarted, EventSubagentCompleted:
		item.Kind = "subagent"
		item.Status = payload.Status
		item.Content = payload.Message
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
	case EventUserInputRequest, EventUserInputReceived:
		item.Kind = "user_input"
		item.Status = payload.Status
		item.Content = payload.Message
	case EventPermissionApproved, EventPermissionDenied:
		item.Kind = "approval"
		item.Status = payload.Status
		item.Content = payload.Message
	case EventTaskQueued, EventReplanning, EventCompleted, EventCancelled, EventError:
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	if _, err := r.getSessionTx(ctx, tx, sessionID); err != nil {
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
	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_messages (id, session_id, role, content, task_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, message.ID, message.SessionID, message.Role, message.Content, message.TaskID, formatTime(message.CreatedAt))
	if err != nil {
		return Message{}, err
	}
	err = r.insertTranscriptEntryTx(ctx, tx, TimelineItem{
		ID:        message.ID,
		SessionID: message.SessionID,
		TaskID:    message.TaskID,
		Kind:      "message",
		Role:      message.Role,
		Content:   message.Content,
		CreatedAt: message.CreatedAt,
	})
	if err != nil {
		return Message{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatTime(now), sessionID)
	if err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return message, nil
}

func (r *Repository) UpdateStatus(ctx context.Context, id string, status Status) error {
	now := time.Now().UTC()
	var completedAt any
	if status == StatusCompleted || status == StatusFailed || status == StatusCancelled || status == StatusStale {
		completedAt = formatTime(now)
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, updated_at = ?, completed_at = COALESCE(?, completed_at)
		WHERE id = ?
	`, string(status), formatTime(now), completedAt, id)
	return err
}

func (r *Repository) GrantApproval(ctx context.Context, id string, decidedBy ...string) error {
	now := time.Now().UTC()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, updated_at = ?, completed_at = NULL
		WHERE id = ?
	`, string(StatusDraft), formatTime(now), id)
	if err != nil {
		return err
	}
	if err := r.resolveApprovalItemTx(ctx, tx, id, "approved", firstNonEmpty(decidedBy...), now); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) DenyApproval(ctx context.Context, id string, reason string, decidedBy ...string) error {
	now := time.Now().UTC()
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "permission denied"
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, updated_at = ?, completed_at = COALESCE(?, completed_at)
		WHERE id = ?
	`, string(StatusCancelled), formatTime(now), formatTime(now), id)
	if err != nil {
		return err
	}
	if err := r.resolveApprovalItemTx(ctx, tx, id, "denied", firstNonEmpty(decidedBy...), now); err != nil {
		return err
	}
	if err := r.appendEventTx(ctx, tx, id, EventPermissionDenied, EventPayload{
		Message: reason,
		Status:  string(StatusCancelled),
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.notifyEventSubscribers(id)
	return nil
}

func (r *Repository) Cancel(ctx context.Context, id string, reason string) error {
	now := time.Now().UTC()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "cancelled"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, updated_at = ?, completed_at = COALESCE(?, completed_at)
		WHERE id = ?
	`, string(StatusCancelled), formatTime(now), formatTime(now), id); err != nil {
		return err
	}
	if err := r.resolveApprovalItemTx(ctx, tx, id, "cancelled", "user", now); err != nil {
		return err
	}
	if err := r.appendEventTx(ctx, tx, id, EventCancelled, EventPayload{
		Message: reason,
		Status:  string(StatusCancelled),
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.notifyEventSubscribers(id)
	return nil
}

func (r *Repository) AppendEvent(ctx context.Context, taskID string, eventType EventType, payload EventPayload) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task id is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := r.appendEventTx(ctx, tx, taskID, eventType, payload); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
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

func (r *Repository) SubscribeEventsAny(ctx context.Context, taskIDs []string) (<-chan struct{}, func()) {
	ids := uniqueNonEmpty(taskIDs)
	ctx, cancel := context.WithCancel(ctx)
	notify := make(chan struct{})
	var once sync.Once
	unsubscribers := make([]func(), 0, len(ids))
	for _, taskID := range ids {
		taskCh, unsubscribe := r.SubscribeEvents(ctx, taskID)
		unsubscribers = append(unsubscribers, unsubscribe)
		go func() {
			select {
			case <-ctx.Done():
			case <-taskCh:
				once.Do(func() {
					close(notify)
				})
			}
		}()
	}
	unsubscribe := func() {
		cancel()
		for _, unsubscribe := range unsubscribers {
			unsubscribe()
		}
	}
	return notify, unsubscribe
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

func uniqueNonEmpty(values []string) []string {
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

func defaultChildScope(parent TaskScope, child TaskScope) TaskScope {
	parent = normalizeTaskScope(parent)
	child = normalizeTaskScope(child)
	if len(child.Paths) == 0 {
		child.Paths = append([]string(nil), parent.Paths...)
	}
	return child
}

func normalizeTaskScope(scope TaskScope) TaskScope {
	return TaskScope{
		Paths:           normalizeStringList(scope.Paths),
		NetworkHosts:    normalizeStringList(scope.NetworkHosts),
		MCPServers:      normalizeStringList(scope.MCPServers),
		MCPTools:        normalizeStringList(scope.MCPTools),
		ApprovalActions: normalizeStringList(scope.ApprovalActions),
	}
}

func normalizeStringList(values []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}

func validateChildScopeWithinParent(parent TaskScope, child TaskScope) error {
	parent = normalizeTaskScope(parent)
	child = normalizeTaskScope(child)
	if err := validateChildPathsWithinParent(parent.Paths, child.Paths); err != nil {
		return err
	}
	if err := validateChildListWithinParent("network host", parent.NetworkHosts, child.NetworkHosts); err != nil {
		return err
	}
	if err := validateChildListWithinParent("MCP server", parent.MCPServers, child.MCPServers); err != nil {
		return err
	}
	if err := validateChildListWithinParent("MCP tool", parent.MCPTools, child.MCPTools); err != nil {
		return err
	}
	if err := validateChildListWithinParent("approval action", parent.ApprovalActions, child.ApprovalActions); err != nil {
		return err
	}
	return nil
}

func validateChildPathsWithinParent(parentPaths []string, childPaths []string) error {
	for _, child := range childPaths {
		if !pathWithinAnyParent(parentPaths, child) {
			return fmt.Errorf("child path %q is outside parent scope", child)
		}
	}
	return nil
}

func pathWithinAnyParent(parentPaths []string, child string) bool {
	child = filepath.Clean(child)
	for _, parent := range parentPaths {
		parent = filepath.Clean(parent)
		if child == parent {
			return true
		}
		if parent == string(filepath.Separator) {
			return strings.HasPrefix(child, parent)
		}
		if strings.HasPrefix(child, parent+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func validateChildListWithinParent(label string, parentValues []string, childValues []string) error {
	parent := map[string]struct{}{}
	for _, value := range parentValues {
		parent[value] = struct{}{}
	}
	for _, value := range childValues {
		if _, ok := parent[value]; !ok {
			return fmt.Errorf("child %s %q is outside parent scope", label, value)
		}
	}
	return nil
}

func marshalTaskScope(scope TaskScope) (string, error) {
	bytes, err := json.Marshal(normalizeTaskScope(scope))
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func unmarshalTaskScope(value string) TaskScope {
	value = strings.TrimSpace(value)
	if value == "" {
		return TaskScope{}
	}
	var scope TaskScope
	if err := json.Unmarshal([]byte(value), &scope); err != nil {
		return TaskScope{}
	}
	return normalizeTaskScope(scope)
}

func marshalStringList(values []string) (string, error) {
	bytes, err := json.Marshal(normalizeStringList(values))
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func unmarshalStringList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(value), &values); err != nil {
		return nil
	}
	return normalizeStringList(values)
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
		var scopeJSON, approvalGrantsJSON string
		var modelProvider, modelName, modelBaseURL, modelProfile, modelSource string
		var natural, approvalGranted, inheritedScopeFromParent int
		if err := rows.Scan(&task.ID, &task.SessionID, &task.Title, &task.UserInput, &natural, &task.Status, &task.Workspace, &task.Origin, &task.Automation.Kind, &task.Automation.Risk, &task.Automation.Source, &task.Automation.Trigger, &task.Schedule.ID, &task.Schedule.CatchUpPolicy, &task.Schedule.MissedRuns, &task.Schedule.MaxCatchUpRuns, &task.Schedule.CatchUpRuns, &approvalGranted, &task.ParentTaskID, &task.ParentThreadID, &task.ChildThreadID, &task.SubagentName, &task.Role, &scopeJSON, &inheritedScopeFromParent, &approvalGrantsJSON, &modelProvider, &modelName, &modelBaseURL, &modelProfile, &modelSource, &createdAt, &updatedAt, &completedAt); err != nil {
			return nil, err
		}
		task.Natural = natural != 0
		task.ApprovalGranted = approvalGranted != 0
		task.ScheduleID = task.Schedule.ID
		task.Scope = unmarshalTaskScope(scopeJSON)
		task.InheritedScopeFromParent = inheritedScopeFromParent != 0
		task.ApprovalGrants = unmarshalStringList(approvalGrantsJSON)
		task.ModelConfig = normalizeModelConfig(&ModelConfig{Provider: modelProvider, Model: modelName, BaseURL: modelBaseURL, Profile: modelProfile, Source: modelSource})
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

func normalizeModelConfig(config *ModelConfig) *ModelConfig {
	if config == nil {
		return nil
	}
	normalized := ModelConfig{
		Provider: strings.TrimSpace(config.Provider),
		Model:    strings.TrimSpace(config.Model),
		BaseURL:  strings.TrimSpace(config.BaseURL),
		Profile:  strings.TrimSpace(config.Profile),
		Source:   strings.TrimSpace(config.Source),
	}
	if normalized.Provider == "" && normalized.Model == "" && normalized.BaseURL == "" && normalized.Profile == "" && normalized.Source == "" {
		return nil
	}
	return &normalized
}

func modelConfigValue(config *ModelConfig, field string) string {
	if config == nil {
		return ""
	}
	switch field {
	case "provider":
		return config.Provider
	case "model":
		return config.Model
	case "base_url":
		return config.BaseURL
	case "profile":
		return config.Profile
	case "source":
		return config.Source
	default:
		return ""
	}
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
