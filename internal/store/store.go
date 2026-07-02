package store

import (
	"bufio"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	root string
}

type Memory struct {
	ID         string     `json:"id"`
	Text       string     `json:"text"`
	Kind       string     `json:"kind"`
	Mood       string     `json:"mood,omitempty"`
	Source     string     `json:"source,omitempty"`
	Workspace  string     `json:"workspace,omitempty"`
	Redaction  string     `json:"redaction,omitempty"`
	Importance int        `json:"importance"`
	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type CreateMemoryRequest struct {
	Text       string     `json:"text"`
	Kind       string     `json:"kind,omitempty"`
	Source     string     `json:"source,omitempty"`
	Workspace  string     `json:"workspace,omitempty"`
	Importance int        `json:"importance,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type UpdateMemoryRequest struct {
	Text       *string    `json:"text,omitempty"`
	Kind       *string    `json:"kind,omitempty"`
	Source     *string    `json:"source,omitempty"`
	Workspace  *string    `json:"workspace,omitempty"`
	Importance *int       `json:"importance,omitempty"`
	Enabled    *bool      `json:"enabled,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type MemoryListOptions struct {
	Query           string
	Workspace       string
	Limit           int
	IncludeDisabled bool
	IncludeExpired  bool
}

type PermissionRuleAction string

const (
	PermissionRuleAlwaysAllow PermissionRuleAction = "always_allow"
	PermissionRuleAlwaysDeny  PermissionRuleAction = "always_deny"
	PermissionRuleAlwaysAsk   PermissionRuleAction = "always_ask"
)

type PermissionRule struct {
	ID        string               `json:"id"`
	Action    PermissionRuleAction `json:"action"`
	Workspace string               `json:"workspace,omitempty"`
	SessionID string               `json:"session_id,omitempty"`
	Tool      string               `json:"tool,omitempty"`
	Risk      string               `json:"risk,omitempty"`
	Reason    string               `json:"reason,omitempty"`
	Enabled   bool                 `json:"enabled"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
}

type CreatePermissionRuleRequest struct {
	Action    PermissionRuleAction `json:"action"`
	Workspace string               `json:"workspace,omitempty"`
	SessionID string               `json:"session_id,omitempty"`
	Tool      string               `json:"tool,omitempty"`
	Risk      string               `json:"risk,omitempty"`
	Reason    string               `json:"reason,omitempty"`
}

type PermissionRuleListOptions struct {
	Workspace       string
	SessionID       string
	Limit           int
	IncludeDisabled bool
}

type ConversationThread struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	Workspace   string             `json:"workspace"`
	LastTaskID  string             `json:"last_task_id,omitempty"`
	ModelConfig *ThreadModelConfig `json:"model_config,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	ArchivedAt  *time.Time         `json:"archived_at,omitempty"`
}

type ThreadModelConfig struct {
	ThreadID              string `json:"thread_id"`
	Provider              string `json:"provider"`
	Model                 string `json:"model"`
	BaseURL               string `json:"base_url,omitempty"`
	Profile               string `json:"profile,omitempty"`
	InheritedFromThreadID string `json:"inherited_from_thread_id,omitempty"`
}

type WorkspaceModelConfig struct {
	Workspace string `json:"workspace"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	BaseURL   string `json:"base_url,omitempty"`
	Profile   string `json:"profile,omitempty"`
}

type UpdateThreadModelConfigRequest struct {
	Provider              string `json:"provider,omitempty"`
	Model                 string `json:"model,omitempty"`
	BaseURL               string `json:"base_url,omitempty"`
	Profile               string `json:"profile,omitempty"`
	InheritedFromThreadID string `json:"inherited_from_thread_id,omitempty"`
}

type UpdateWorkspaceModelConfigRequest struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	Profile  string `json:"profile,omitempty"`
}

type CreateConversationThreadRequest struct {
	Workspace string `json:"workspace"`
	Title     string `json:"title,omitempty"`
}

type UpdateConversationThreadRequest struct {
	Title    *string `json:"title,omitempty"`
	Archived *bool   `json:"archived,omitempty"`
}

type ConversationThreadListOptions struct {
	Workspace       string
	Limit           int
	IncludeArchived bool
}

type CrossThreadArtifactRef struct {
	Path    string `json:"path"`
	Summary string `json:"summary,omitempty"`
}

type CrossThreadMessage struct {
	ID                       string                   `json:"id"`
	FromThreadID             string                   `json:"from_thread_id"`
	ToThreadID               string                   `json:"to_thread_id"`
	FromWorkspace            string                   `json:"from_workspace"`
	ToWorkspace              string                   `json:"to_workspace"`
	TaskID                   string                   `json:"task_id,omitempty"`
	Role                     string                   `json:"role"`
	Content                  string                   `json:"content"`
	Summary                  string                   `json:"summary,omitempty"`
	ExplicitContent          string                   `json:"explicit_content,omitempty"`
	ArtifactRefs             []CrossThreadArtifactRef `json:"artifact_refs,omitempty"`
	CrossWorkspaceAuthorized bool                     `json:"cross_workspace_authorized,omitempty"`
	CrossWorkspaceAuthReason string                   `json:"cross_workspace_auth_reason,omitempty"`
	IncludesPrompt           bool                     `json:"includes_prompt"`
	IncludesSecret           bool                     `json:"includes_secret"`
	IncludesMemory           bool                     `json:"includes_memory"`
	IncludesApprovalRule     bool                     `json:"includes_approval_rule"`
	CreatedAt                time.Time                `json:"created_at"`
}

type CreateCrossThreadMessageRequest struct {
	FromThreadID             string                   `json:"from_thread_id"`
	ToThreadID               string                   `json:"to_thread_id"`
	TaskID                   string                   `json:"task_id,omitempty"`
	Summary                  string                   `json:"summary,omitempty"`
	ExplicitContent          string                   `json:"explicit_content,omitempty"`
	ArtifactRefs             []CrossThreadArtifactRef `json:"artifact_refs,omitempty"`
	CrossWorkspaceAuthorized bool                     `json:"cross_workspace_authorized,omitempty"`
	CrossWorkspaceAuthReason string                   `json:"cross_workspace_auth_reason,omitempty"`
}

var ErrCrossWorkspaceAuthorizationRequired = errors.New("cross-workspace handoff requires explicit authorization")

type Skill struct {
	Name        string
	Title       string
	Description string
	Path        string
	Body        string
	Source      string
}

type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers"`
}

type MCPServerConfig struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Source      string            `json:"source,omitempty"`
	Version     string            `json:"version,omitempty"`
	Permissions []string          `json:"permissions,omitempty"`
}

func DefaultRoot() string {
	if value := strings.TrimSpace(os.Getenv("LIORA_HOME")); value != "" {
		return value
	}
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		return filepath.Join(configHome, "liora")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".liora"
	}
	return filepath.Join(home, ".config", "liora")
}

func New(root string) *Store {
	if root == "" {
		root = DefaultRoot()
	}
	return &Store{root: root}
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) Goal() (string, bool, error) {
	data, err := os.ReadFile(s.path("goal.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	goal := strings.TrimSpace(string(data))
	return goal, goal != "", nil
}

func (s *Store) SetGoal(goal string) error {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return errors.New("goal is required")
	}
	if err := s.ensureRoot(); err != nil {
		return err
	}
	return os.WriteFile(s.path("goal.txt"), []byte(goal+"\n"), 0o600)
}

func (s *Store) ClearGoal() error {
	err := os.Remove(s.path("goal.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) AddMemory(text string) error {
	_, err := s.CreateMemory(text)
	return err
}

func (s *Store) CreateMemory(text string) (Memory, error) {
	return s.CreateMemoryWithOptions(CreateMemoryRequest{Text: text})
}

func (s *Store) CreateMemoryWithOptions(request CreateMemoryRequest) (Memory, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return Memory{}, errors.New("memory text is required")
	}
	kind, err := normalizeMemoryKind(request.Kind)
	if err != nil {
		return Memory{}, err
	}
	source := strings.TrimSpace(request.Source)
	if source == "" {
		source = "manual"
	}
	workspace := strings.TrimSpace(request.Workspace)
	importance, err := normalizeMemoryImportance(request.Importance)
	if err != nil {
		return Memory{}, err
	}
	db, err := s.OpenDB()
	if err != nil {
		return Memory{}, err
	}
	defer db.Close()
	now := time.Now().UTC()
	text, redaction := redactPrivateMemoryText(text, kind)
	memory := Memory{
		ID:         newID(),
		Text:       text,
		Kind:       kind,
		Source:     source,
		Workspace:  workspace,
		Redaction:  redaction,
		Importance: importance,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  request.ExpiresAt,
	}
	_, err = db.Exec(`
			INSERT INTO memories (id, text, kind, mood, source, workspace, redaction, importance, enabled, created_at, updated_at, last_used_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
		`, memory.ID, memory.Text, memory.Kind, memory.Mood, memory.Source, memory.Workspace, memory.Redaction, memory.Importance, boolInt(memory.Enabled), formatTime(memory.CreatedAt), formatTime(memory.UpdatedAt), formatOptionalTime(memory.ExpiresAt))
	if err != nil {
		return Memory{}, err
	}
	return memory, nil
}

func (s *Store) ListMemories(limit int) ([]Memory, error) {
	db, err := s.OpenDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return queryMemories(db, MemoryListOptions{Limit: limit})
}

func (s *Store) SearchMemories(query string, limit int) ([]Memory, error) {
	return s.ListMemoriesWithOptions(MemoryListOptions{Query: query, Limit: limit})
}

func (s *Store) SearchMemoriesWithOptions(query string, limit int, includeDisabled bool) ([]Memory, error) {
	return s.ListMemoriesWithOptions(MemoryListOptions{Query: query, Limit: limit, IncludeDisabled: includeDisabled})
}

func (s *Store) ListMemoriesWithOptions(options MemoryListOptions) ([]Memory, error) {
	db, err := s.OpenDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return queryMemories(db, options)
}

func (s *Store) GetMemory(id string) (Memory, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Memory{}, errors.New("memory id is required")
	}
	db, err := s.OpenDB()
	if err != nil {
		return Memory{}, err
	}
	defer db.Close()
	return getMemory(db, id)
}

func (s *Store) UpdateMemory(id string, request UpdateMemoryRequest) (Memory, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Memory{}, errors.New("memory id is required")
	}
	db, err := s.OpenDB()
	if err != nil {
		return Memory{}, err
	}
	defer db.Close()
	memory, err := getMemory(db, id)
	if err != nil {
		return Memory{}, err
	}
	if request.Text != nil {
		text := strings.TrimSpace(*request.Text)
		if text == "" {
			return Memory{}, errors.New("memory text is required")
		}
		memory.Text, memory.Redaction = redactPrivateMemoryText(text, memory.Kind)
	}
	if request.Kind != nil {
		memory.Kind, err = normalizeMemoryKind(*request.Kind)
		if err != nil {
			return Memory{}, err
		}
		memory.Text, memory.Redaction = redactPrivateMemoryText(memory.Text, memory.Kind)
	}
	if request.Source != nil {
		memory.Source = strings.TrimSpace(*request.Source)
	}
	if request.Workspace != nil {
		memory.Workspace = strings.TrimSpace(*request.Workspace)
	}
	if request.Importance != nil {
		memory.Importance, err = normalizeMemoryImportance(*request.Importance)
		if err != nil {
			return Memory{}, err
		}
	}
	if request.Enabled != nil {
		memory.Enabled = *request.Enabled
	}
	if request.ExpiresAt != nil {
		memory.ExpiresAt = request.ExpiresAt
	}
	memory.UpdatedAt = time.Now().UTC()
	_, err = db.Exec(`
			UPDATE memories
			SET text = ?, kind = ?, source = ?, workspace = ?, redaction = ?, importance = ?, enabled = ?, updated_at = ?, expires_at = ?
			WHERE id = ?
		`, memory.Text, memory.Kind, memory.Source, memory.Workspace, memory.Redaction, memory.Importance, boolInt(memory.Enabled), formatTime(memory.UpdatedAt), formatOptionalTime(memory.ExpiresAt), memory.ID)
	if err != nil {
		return Memory{}, err
	}
	return memory, nil
}

func (s *Store) SetMemoryEnabled(id string, enabled bool) (Memory, error) {
	return s.UpdateMemory(id, UpdateMemoryRequest{Enabled: &enabled})
}

func (s *Store) DeleteMemory(id string) error {
	return s.DeleteMemoryForWorkspace(id, "")
}

func (s *Store) DeleteMemoryForWorkspace(id string, workspace string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("memory id is required")
	}
	db, err := s.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	var result sql.Result
	if strings.TrimSpace(workspace) == "" {
		result, err = db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	} else {
		result, err = db.Exec(`DELETE FROM memories WHERE id = ? AND workspace = ?`, id, strings.TrimSpace(workspace))
	}
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CreatePermissionRule(request CreatePermissionRuleRequest) (PermissionRule, error) {
	action, err := normalizePermissionRuleAction(request.Action)
	if err != nil {
		return PermissionRule{}, err
	}
	rule := PermissionRule{
		ID:        newID(),
		Action:    action,
		Workspace: strings.TrimSpace(request.Workspace),
		SessionID: strings.TrimSpace(request.SessionID),
		Tool:      strings.ToLower(strings.TrimSpace(request.Tool)),
		Risk:      strings.ToLower(strings.TrimSpace(request.Risk)),
		Reason:    strings.TrimSpace(request.Reason),
		Enabled:   true,
	}
	if err := validatePermissionRuleScope(rule); err != nil {
		return PermissionRule{}, err
	}
	db, err := s.OpenDB()
	if err != nil {
		return PermissionRule{}, err
	}
	defer db.Close()
	now := time.Now().UTC()
	rule.CreatedAt = now
	rule.UpdatedAt = now
	_, err = db.Exec(`
			INSERT INTO permission_rules (id, action, workspace, session_id, tool, risk, reason, enabled, schema_version, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, rule.ID, rule.Action, rule.Workspace, rule.SessionID, rule.Tool, rule.Risk, rule.Reason, boolInt(rule.Enabled), CurrentSchemaVersion, formatTime(rule.CreatedAt), formatTime(rule.UpdatedAt))
	if err != nil {
		return PermissionRule{}, err
	}
	return rule, nil
}

func (s *Store) ListPermissionRules(options PermissionRuleListOptions) ([]PermissionRule, error) {
	db, err := s.OpenDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return queryPermissionRules(db, options)
}

func (s *Store) DeletePermissionRule(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("permission rule id is required")
	}
	db, err := s.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := db.Exec(`DELETE FROM permission_rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CreateConversationThread(request CreateConversationThreadRequest) (ConversationThread, error) {
	workspace := strings.TrimSpace(request.Workspace)
	if workspace == "" {
		return ConversationThread{}, fmt.Errorf("workspace is required")
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "New thread"
	}
	now := time.Now().UTC()
	thread := ConversationThread{
		ID:        newID(),
		Title:     title,
		Workspace: workspace,
		CreatedAt: now,
		UpdatedAt: now,
	}
	db, err := s.OpenDB()
	if err != nil {
		return ConversationThread{}, err
	}
	defer db.Close()
	_, err = db.Exec(`
		INSERT INTO conversation_threads (id, title, workspace, last_task_id, schema_version, created_at, updated_at)
		VALUES (?, ?, ?, '', ?, ?, ?)
	`, thread.ID, thread.Title, thread.Workspace, CurrentSchemaVersion, formatTime(thread.CreatedAt), formatTime(thread.UpdatedAt))
	if err != nil {
		return ConversationThread{}, err
	}
	return thread, nil
}

func (s *Store) ListConversationThreads(workspace string, limit int) ([]ConversationThread, error) {
	return s.ListConversationThreadsWithOptions(ConversationThreadListOptions{Workspace: workspace, Limit: limit})
}

func (s *Store) ListConversationThreadsWithOptions(options ConversationThreadListOptions) ([]ConversationThread, error) {
	workspace := strings.TrimSpace(options.Workspace)
	limit := options.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	db, err := s.OpenDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var rows *sql.Rows
	archivedClause := "archived_at = ''"
	if options.IncludeArchived {
		archivedClause = "1 = 1"
	}
	if workspace == "" {
		rows, err = db.Query(`
			SELECT id, title, workspace, last_task_id, created_at, updated_at, archived_at
			FROM conversation_threads
			WHERE `+archivedClause+`
			ORDER BY updated_at DESC, id DESC
			LIMIT ?
		`, limit)
	} else {
		rows, err = db.Query(`
			SELECT id, title, workspace, last_task_id, created_at, updated_at, archived_at
			FROM conversation_threads
			WHERE workspace = ? AND `+archivedClause+`
			ORDER BY updated_at DESC, id DESC
			LIMIT ?
		`, workspace, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var threads []ConversationThread
	for rows.Next() {
		thread, err := scanConversationThread(rows)
		if err != nil {
			return nil, err
		}
		threads = append(threads, thread)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range threads {
		if err := attachThreadModelConfig(db, &threads[i]); err != nil {
			return nil, err
		}
	}
	return threads, nil
}

func (s *Store) UpdateConversationThread(id string, request UpdateConversationThreadRequest) (ConversationThread, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ConversationThread{}, fmt.Errorf("thread id is required")
	}
	titleChanged := request.Title != nil
	archiveChanged := request.Archived != nil
	if !titleChanged && !archiveChanged {
		return ConversationThread{}, fmt.Errorf("thread update requires title or archived")
	}
	db, err := s.OpenDB()
	if err != nil {
		return ConversationThread{}, err
	}
	defer db.Close()
	current, err := getConversationThread(db, id)
	if err != nil {
		return ConversationThread{}, err
	}
	title := current.Title
	if titleChanged {
		title = strings.TrimSpace(*request.Title)
		if title == "" {
			return ConversationThread{}, fmt.Errorf("thread title is required")
		}
	}
	archivedAt := ""
	if current.ArchivedAt != nil {
		archivedAt = formatTime(*current.ArchivedAt)
	}
	if archiveChanged {
		if *request.Archived {
			now := time.Now().UTC()
			archivedAt = formatTime(now)
		} else {
			archivedAt = ""
		}
	}
	now := time.Now().UTC()
	result, err := db.Exec(`
		UPDATE conversation_threads
		SET title = ?, archived_at = ?, updated_at = ?
		WHERE id = ?
	`, title, archivedAt, formatTime(now), id)
	if err != nil {
		return ConversationThread{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ConversationThread{}, err
	}
	if affected == 0 {
		return ConversationThread{}, sql.ErrNoRows
	}
	thread, err := getConversationThread(db, id)
	if err != nil {
		return ConversationThread{}, err
	}
	if err := attachThreadModelConfig(db, &thread); err != nil {
		return ConversationThread{}, err
	}
	return thread, nil
}

func (s *Store) GetConversationThread(id string) (ConversationThread, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ConversationThread{}, fmt.Errorf("thread id is required")
	}
	db, err := s.OpenDB()
	if err != nil {
		return ConversationThread{}, err
	}
	defer db.Close()
	thread, err := getConversationThread(db, id)
	if err != nil {
		return ConversationThread{}, err
	}
	if err := attachThreadModelConfig(db, &thread); err != nil {
		return ConversationThread{}, err
	}
	return thread, nil
}

func (s *Store) GetThreadModelConfig(threadID string) (ThreadModelConfig, bool, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ThreadModelConfig{}, false, fmt.Errorf("thread id is required")
	}
	db, err := s.OpenDB()
	if err != nil {
		return ThreadModelConfig{}, false, err
	}
	defer db.Close()
	if _, err := getConversationThread(db, threadID); err != nil {
		return ThreadModelConfig{}, false, err
	}
	return getThreadModelConfig(db, threadID)
}

func (s *Store) UpdateThreadModelConfig(threadID string, request UpdateThreadModelConfigRequest) (ThreadModelConfig, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ThreadModelConfig{}, fmt.Errorf("thread id is required")
	}
	request.Provider = strings.TrimSpace(request.Provider)
	request.Model = strings.TrimSpace(request.Model)
	request.BaseURL = strings.TrimSpace(request.BaseURL)
	request.Profile = strings.TrimSpace(request.Profile)
	request.InheritedFromThreadID = strings.TrimSpace(request.InheritedFromThreadID)
	if request.InheritedFromThreadID == "" && (request.Provider == "" || request.Model == "") {
		return ThreadModelConfig{}, fmt.Errorf("thread model binding requires provider and model or inherited_from_thread_id")
	}
	db, err := s.OpenDB()
	if err != nil {
		return ThreadModelConfig{}, err
	}
	defer db.Close()
	thread, err := getConversationThread(db, threadID)
	if err != nil {
		return ThreadModelConfig{}, err
	}
	if request.InheritedFromThreadID != "" {
		inherited, err := getConversationThread(db, request.InheritedFromThreadID)
		if err != nil {
			return ThreadModelConfig{}, err
		}
		if inherited.Workspace != thread.Workspace {
			return ThreadModelConfig{}, fmt.Errorf("inherited thread belongs to workspace %q, not %q", inherited.Workspace, thread.Workspace)
		}
	}
	now := time.Now().UTC()
	_, err = db.Exec(`
		INSERT INTO thread_model_bindings (thread_id, provider, model, base_url, profile, inherited_from_thread_id, schema_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(thread_id) DO UPDATE SET
			provider = excluded.provider,
			model = excluded.model,
			base_url = excluded.base_url,
			profile = excluded.profile,
			inherited_from_thread_id = excluded.inherited_from_thread_id,
			schema_version = excluded.schema_version,
			updated_at = excluded.updated_at
	`, threadID, request.Provider, request.Model, request.BaseURL, request.Profile, request.InheritedFromThreadID, CurrentSchemaVersion, formatTime(now), formatTime(now))
	if err != nil {
		return ThreadModelConfig{}, err
	}
	config, ok, err := getThreadModelConfig(db, threadID)
	if err != nil {
		return ThreadModelConfig{}, err
	}
	if !ok {
		return ThreadModelConfig{}, sql.ErrNoRows
	}
	return config, nil
}

func (s *Store) DeleteThreadModelConfig(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return fmt.Errorf("thread id is required")
	}
	db, err := s.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := db.Exec(`DELETE FROM thread_model_bindings WHERE thread_id = ?`, threadID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SetConversationThreadLastTask(threadID string, taskID string) error {
	threadID = strings.TrimSpace(threadID)
	taskID = strings.TrimSpace(taskID)
	if threadID == "" {
		return fmt.Errorf("thread id is required")
	}
	if taskID == "" {
		return fmt.Errorf("task id is required")
	}
	db, err := s.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	now := time.Now().UTC()
	result, err := db.Exec(`
		UPDATE conversation_threads
		SET last_task_id = ?, updated_at = ?
		WHERE id = ?
	`, taskID, formatTime(now), threadID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetWorkspaceModelConfig(workspace string) (WorkspaceModelConfig, bool, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return WorkspaceModelConfig{}, false, fmt.Errorf("workspace is required")
	}
	db, err := s.OpenDB()
	if err != nil {
		return WorkspaceModelConfig{}, false, err
	}
	defer db.Close()
	var config WorkspaceModelConfig
	err = db.QueryRow(`
		SELECT workspace, provider, model, base_url, profile
		FROM workspace_model_bindings
		WHERE workspace = ?
	`, workspace).Scan(&config.Workspace, &config.Provider, &config.Model, &config.BaseURL, &config.Profile)
	if err == sql.ErrNoRows {
		return WorkspaceModelConfig{}, false, nil
	}
	if err != nil {
		return WorkspaceModelConfig{}, false, err
	}
	return config, true, nil
}

func (s *Store) UpdateWorkspaceModelConfig(workspace string, request UpdateWorkspaceModelConfigRequest) (WorkspaceModelConfig, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return WorkspaceModelConfig{}, fmt.Errorf("workspace is required")
	}
	request.Provider = strings.TrimSpace(request.Provider)
	request.Model = strings.TrimSpace(request.Model)
	request.BaseURL = strings.TrimSpace(request.BaseURL)
	request.Profile = strings.TrimSpace(request.Profile)
	if request.Provider == "" || request.Model == "" {
		return WorkspaceModelConfig{}, fmt.Errorf("workspace model binding requires provider and model")
	}
	db, err := s.OpenDB()
	if err != nil {
		return WorkspaceModelConfig{}, err
	}
	defer db.Close()
	now := time.Now().UTC()
	_, err = db.Exec(`
		INSERT INTO workspace_model_bindings (workspace, provider, model, base_url, profile, schema_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace) DO UPDATE SET
			provider = excluded.provider,
			model = excluded.model,
			base_url = excluded.base_url,
			profile = excluded.profile,
			schema_version = excluded.schema_version,
			updated_at = excluded.updated_at
	`, workspace, request.Provider, request.Model, request.BaseURL, request.Profile, CurrentSchemaVersion, formatTime(now), formatTime(now))
	if err != nil {
		return WorkspaceModelConfig{}, err
	}
	config, ok, err := s.GetWorkspaceModelConfig(workspace)
	if err != nil {
		return WorkspaceModelConfig{}, err
	}
	if !ok {
		return WorkspaceModelConfig{}, sql.ErrNoRows
	}
	return config, nil
}

func (s *Store) CreateCrossThreadMessage(request CreateCrossThreadMessageRequest) (CrossThreadMessage, error) {
	fromThreadID := strings.TrimSpace(request.FromThreadID)
	toThreadID := strings.TrimSpace(request.ToThreadID)
	if fromThreadID == "" || toThreadID == "" {
		return CrossThreadMessage{}, fmt.Errorf("from_thread_id and to_thread_id are required")
	}
	summary := strings.TrimSpace(request.Summary)
	explicitContent := strings.TrimSpace(request.ExplicitContent)
	artifactRefs := normalizeCrossThreadArtifactRefs(request.ArtifactRefs)
	if summary == "" && explicitContent == "" && len(artifactRefs) == 0 {
		return CrossThreadMessage{}, fmt.Errorf("cross-thread handoff requires summary, explicit content, or artifact reference")
	}
	db, err := s.OpenDB()
	if err != nil {
		return CrossThreadMessage{}, err
	}
	defer db.Close()
	fromThread, err := getConversationThread(db, fromThreadID)
	if err != nil {
		return CrossThreadMessage{}, err
	}
	toThread, err := getConversationThread(db, toThreadID)
	if err != nil {
		return CrossThreadMessage{}, err
	}
	if fromThread.Workspace != toThread.Workspace && !request.CrossWorkspaceAuthorized {
		return CrossThreadMessage{}, ErrCrossWorkspaceAuthorizationRequired
	}
	artifactJSON, err := json.Marshal(artifactRefs)
	if err != nil {
		return CrossThreadMessage{}, err
	}
	now := time.Now().UTC()
	message := CrossThreadMessage{
		ID:                       newID(),
		FromThreadID:             fromThread.ID,
		ToThreadID:               toThread.ID,
		FromWorkspace:            fromThread.Workspace,
		ToWorkspace:              toThread.Workspace,
		TaskID:                   strings.TrimSpace(request.TaskID),
		Role:                     "handoff",
		Content:                  explicitContent,
		Summary:                  summary,
		ExplicitContent:          explicitContent,
		ArtifactRefs:             artifactRefs,
		CrossWorkspaceAuthorized: fromThread.Workspace != toThread.Workspace && request.CrossWorkspaceAuthorized,
		CrossWorkspaceAuthReason: strings.TrimSpace(request.CrossWorkspaceAuthReason),
		CreatedAt:                now,
	}
	_, err = db.Exec(`
		INSERT INTO cross_thread_messages (
			id, from_thread_id, to_thread_id, from_workspace, to_workspace, task_id, role, content,
			summary, explicit_content, artifact_refs_json, cross_workspace_authorized,
			cross_workspace_auth_reason, includes_prompt, includes_secret, includes_memory,
			includes_approval_rule, schema_version, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, ?, ?)
	`, message.ID, message.FromThreadID, message.ToThreadID, message.FromWorkspace, message.ToWorkspace, message.TaskID, message.Role, message.Content, message.Summary, message.ExplicitContent, string(artifactJSON), boolInt(message.CrossWorkspaceAuthorized), message.CrossWorkspaceAuthReason, CurrentSchemaVersion, formatTime(message.CreatedAt))
	if err != nil {
		return CrossThreadMessage{}, err
	}
	_, err = db.Exec(`
		INSERT INTO thread_relations (id, from_thread_id, to_thread_id, relation, schema_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, newID(), message.FromThreadID, message.ToThreadID, "message", CurrentSchemaVersion, formatTime(message.CreatedAt))
	if err != nil {
		return CrossThreadMessage{}, err
	}
	return message, nil
}

func (s *Store) ListCrossThreadMessages(toThreadID string, limit int) ([]CrossThreadMessage, error) {
	toThreadID = strings.TrimSpace(toThreadID)
	if toThreadID == "" {
		return nil, fmt.Errorf("to_thread_id is required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	db, err := s.OpenDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`
		SELECT id, from_thread_id, to_thread_id, from_workspace, to_workspace, task_id, role, content,
			summary, explicit_content, artifact_refs_json, cross_workspace_authorized,
			cross_workspace_auth_reason, includes_prompt, includes_secret, includes_memory,
			includes_approval_rule, created_at
		FROM cross_thread_messages
		WHERE to_thread_id = ?
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, toThreadID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []CrossThreadMessage
	for rows.Next() {
		message, err := scanCrossThreadMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) ExportMemories(options MemoryListOptions) ([]byte, error) {
	memories, err := s.ListMemoriesWithOptions(options)
	if err != nil {
		return nil, err
	}
	payload, err := json.MarshalIndent(memories, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func (s *Store) SaveMCPConfig(config MCPConfig) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path("mcp.json"), append(data, '\n'), 0o600)
}

func (s *Store) LoadMCPConfig() (MCPConfig, error) {
	data, err := os.ReadFile(s.path("mcp.json"))
	if errors.Is(err, os.ErrNotExist) {
		return MCPConfig{Servers: map[string]MCPServerConfig{}}, nil
	}
	if err != nil {
		return MCPConfig{}, err
	}
	var config MCPConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return MCPConfig{}, err
	}
	if config.Servers == nil {
		config.Servers = map[string]MCPServerConfig{}
	}
	return config, nil
}

func (s *Store) OpenDB() (*sql.DB, error) {
	if err := s.ensureRoot(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(s.path("liora.db")))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := initDB(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateLegacyJSONL(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(s.path("liora.db"), 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		db.Close()
		return nil, err
	}
	return db, nil
}

func sqliteDSN(path string) string {
	dsn := url.URL{Scheme: "file", Path: path}
	query := dsn.Query()
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "busy_timeout(10000)")
	dsn.RawQuery = query.Encode()
	return dsn.String()
}

func initDB(db *sql.DB) error {
	version, err := schemaUserVersion(db)
	if err != nil {
		return err
	}
	if version > CurrentSchemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, CurrentSchemaVersion)
	}
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=10000`,
		`CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			text TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'note',
			mood TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			workspace TEXT NOT NULL DEFAULT '',
			redaction TEXT NOT NULL DEFAULT '',
			importance INTEGER NOT NULL DEFAULT 3,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_used_at TEXT,
			expires_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_created_at ON memories(created_at)`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
				id TEXT PRIMARY KEY,
				session_id TEXT NOT NULL DEFAULT '',
				title TEXT NOT NULL,
				user_input TEXT NOT NULL,
				natural INTEGER NOT NULL DEFAULT 1,
				status TEXT NOT NULL,
				workspace TEXT NOT NULL,
				origin TEXT NOT NULL DEFAULT 'foreground',
					automation_kind TEXT NOT NULL DEFAULT '',
					automation_risk TEXT NOT NULL DEFAULT '',
					automation_source TEXT NOT NULL DEFAULT '',
					automation_trigger TEXT NOT NULL DEFAULT '',
					schedule_id TEXT NOT NULL DEFAULT '',
					schedule_catch_up_policy TEXT NOT NULL DEFAULT '',
					schedule_missed_runs INTEGER NOT NULL DEFAULT 0,
					schedule_max_catch_up_runs INTEGER NOT NULL DEFAULT 0,
					schedule_catch_up_runs INTEGER NOT NULL DEFAULT 0,
					approval_granted INTEGER NOT NULL DEFAULT 0,
					parent_task_id TEXT NOT NULL DEFAULT '',
					parent_thread_id TEXT NOT NULL DEFAULT '',
					child_thread_id TEXT NOT NULL DEFAULT '',
					subagent_name TEXT NOT NULL DEFAULT '',
					role TEXT NOT NULL DEFAULT '',
						scope_json TEXT NOT NULL DEFAULT '{}',
						inherited_scope_from_parent INTEGER NOT NULL DEFAULT 0,
						approval_grants_json TEXT NOT NULL DEFAULT '[]',
						model_provider TEXT NOT NULL DEFAULT '',
						model_name TEXT NOT NULL DEFAULT '',
						model_base_url TEXT NOT NULL DEFAULT '',
						model_profile TEXT NOT NULL DEFAULT '',
						model_source TEXT NOT NULL DEFAULT '',
						created_at TEXT NOT NULL,
						updated_at TEXT NOT NULL,
					completed_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_updated_at ON tasks(updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			workspace TEXT NOT NULL,
			last_task_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_workspace_updated ON sessions(workspace, updated_at)`,
		`CREATE TABLE IF NOT EXISTS session_messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			task_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_messages_session_created ON session_messages(session_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS task_events (
				id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				type TEXT NOT NULL,
				payload_json TEXT NOT NULL,
				created_at TEXT NOT NULL,
				FOREIGN KEY(task_id) REFERENCES tasks(id)
			)`,
		`CREATE INDEX IF NOT EXISTS idx_task_events_task_created ON task_events(task_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS memory_types (
				kind TEXT PRIMARY KEY,
				description TEXT NOT NULL DEFAULT '',
				schema_version INTEGER NOT NULL DEFAULT 2,
				created_at TEXT NOT NULL DEFAULT ''
			)`,
		`CREATE TABLE IF NOT EXISTS transcript_entries (
				id TEXT PRIMARY KEY,
				session_id TEXT NOT NULL DEFAULT '',
				task_id TEXT NOT NULL DEFAULT '',
				kind TEXT NOT NULL DEFAULT '',
				role TEXT NOT NULL DEFAULT '',
				type TEXT NOT NULL DEFAULT '',
				title TEXT NOT NULL DEFAULT '',
				content TEXT NOT NULL DEFAULT '',
				tool TEXT NOT NULL DEFAULT '',
				tool_call_id TEXT NOT NULL DEFAULT '',
				tool_result_id TEXT NOT NULL DEFAULT '',
				input TEXT NOT NULL DEFAULT '',
				output TEXT NOT NULL DEFAULT '',
				target TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT '',
				diff TEXT NOT NULL DEFAULT '',
				risk TEXT NOT NULL DEFAULT '',
				reason TEXT NOT NULL DEFAULT '',
				provider TEXT NOT NULL DEFAULT '',
				model TEXT NOT NULL DEFAULT '',
				profile TEXT NOT NULL DEFAULT '',
				schema_version INTEGER NOT NULL DEFAULT 2,
				created_at TEXT NOT NULL DEFAULT ''
			)`,
		`CREATE INDEX IF NOT EXISTS idx_transcript_entries_session_created ON transcript_entries(session_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS todos (
					id TEXT PRIMARY KEY,
					task_id TEXT NOT NULL DEFAULT '',
					parent_task_id TEXT NOT NULL DEFAULT '',
					status TEXT NOT NULL DEFAULT '',
					title TEXT NOT NULL DEFAULT '',
					priority TEXT NOT NULL DEFAULT 'normal',
					schema_version INTEGER NOT NULL DEFAULT 2,
					created_at TEXT NOT NULL DEFAULT '',
					updated_at TEXT NOT NULL DEFAULT ''
				)`,
		`CREATE INDEX IF NOT EXISTS idx_todos_task_updated ON todos(task_id, updated_at)`,
		`CREATE TABLE IF NOT EXISTS artifact_refs (
				id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL DEFAULT '',
				tool TEXT NOT NULL DEFAULT '',
				path TEXT NOT NULL DEFAULT '',
				summary TEXT NOT NULL DEFAULT '',
				schema_version INTEGER NOT NULL DEFAULT 2,
				created_at TEXT NOT NULL DEFAULT ''
			)`,
		`CREATE INDEX IF NOT EXISTS idx_artifact_refs_task_created ON artifact_refs(task_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS approval_items (
				id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL DEFAULT '',
				tool_call_id TEXT NOT NULL DEFAULT '',
				tool TEXT NOT NULL DEFAULT '',
				args_preview TEXT NOT NULL DEFAULT '',
				risk TEXT NOT NULL DEFAULT '',
				command_preview TEXT NOT NULL DEFAULT '',
				diff_preview TEXT NOT NULL DEFAULT '',
				reason TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT '',
				decision TEXT NOT NULL DEFAULT '',
				decided_by TEXT NOT NULL DEFAULT '',
				resolved_at TEXT NOT NULL DEFAULT '',
				schema_version INTEGER NOT NULL DEFAULT 12,
				created_at TEXT NOT NULL DEFAULT '',
				updated_at TEXT NOT NULL DEFAULT ''
			)`,
		`CREATE INDEX IF NOT EXISTS idx_approval_items_task_status ON approval_items(task_id, status)`,
		`CREATE TABLE IF NOT EXISTS permission_rules (
					id TEXT PRIMARY KEY,
					action TEXT NOT NULL DEFAULT '',
					workspace TEXT NOT NULL DEFAULT '',
					session_id TEXT NOT NULL DEFAULT '',
					tool TEXT NOT NULL DEFAULT '',
					risk TEXT NOT NULL DEFAULT '',
					reason TEXT NOT NULL DEFAULT '',
					enabled INTEGER NOT NULL DEFAULT 1,
					schema_version INTEGER NOT NULL DEFAULT 13,
					created_at TEXT NOT NULL DEFAULT '',
					updated_at TEXT NOT NULL DEFAULT ''
				)`,
		`CREATE INDEX IF NOT EXISTS idx_permission_rules_scope ON permission_rules(workspace, session_id, enabled)`,
		`CREATE TABLE IF NOT EXISTS schedules (
				id TEXT PRIMARY KEY,
				workspace TEXT NOT NULL DEFAULT '',
				trigger_kind TEXT NOT NULL DEFAULT 'cron',
				trigger TEXT NOT NULL DEFAULT '',
				prompt TEXT NOT NULL DEFAULT '',
				timezone TEXT NOT NULL DEFAULT 'Local',
				quiet_hours_start TEXT NOT NULL DEFAULT '',
				quiet_hours_end TEXT NOT NULL DEFAULT '',
				enabled INTEGER NOT NULL DEFAULT 1,
				schema_version INTEGER NOT NULL DEFAULT 16,
				created_at TEXT NOT NULL DEFAULT '',
				updated_at TEXT NOT NULL DEFAULT ''
			)`,
		`CREATE TABLE IF NOT EXISTS hooks (
				id TEXT PRIMARY KEY,
				event TEXT NOT NULL DEFAULT '',
				command TEXT NOT NULL DEFAULT '',
				enabled INTEGER NOT NULL DEFAULT 1,
				schema_version INTEGER NOT NULL DEFAULT 2,
				created_at TEXT NOT NULL DEFAULT '',
				updated_at TEXT NOT NULL DEFAULT ''
			)`,
		`CREATE TABLE IF NOT EXISTS conversation_threads (
					id TEXT PRIMARY KEY,
					title TEXT NOT NULL DEFAULT '',
					workspace TEXT NOT NULL DEFAULT '',
					last_task_id TEXT NOT NULL DEFAULT '',
					schema_version INTEGER NOT NULL DEFAULT 2,
					created_at TEXT NOT NULL DEFAULT '',
					updated_at TEXT NOT NULL DEFAULT '',
					archived_at TEXT NOT NULL DEFAULT ''
				)`,
		`CREATE INDEX IF NOT EXISTS idx_conversation_threads_workspace_updated ON conversation_threads(workspace, updated_at)`,
		`CREATE TABLE IF NOT EXISTS workspace_model_bindings (
						workspace TEXT PRIMARY KEY,
						provider TEXT NOT NULL DEFAULT '',
						model TEXT NOT NULL DEFAULT '',
						base_url TEXT NOT NULL DEFAULT '',
						profile TEXT NOT NULL DEFAULT '',
						schema_version INTEGER NOT NULL DEFAULT 2,
						created_at TEXT NOT NULL DEFAULT '',
						updated_at TEXT NOT NULL DEFAULT ''
				)`,
		`CREATE TABLE IF NOT EXISTS thread_model_bindings (
					thread_id TEXT PRIMARY KEY,
					provider TEXT NOT NULL DEFAULT '',
					model TEXT NOT NULL DEFAULT '',
					base_url TEXT NOT NULL DEFAULT '',
					profile TEXT NOT NULL DEFAULT '',
					inherited_from_thread_id TEXT NOT NULL DEFAULT '',
					schema_version INTEGER NOT NULL DEFAULT 2,
					created_at TEXT NOT NULL DEFAULT '',
					updated_at TEXT NOT NULL DEFAULT ''
			)`,
		`CREATE TABLE IF NOT EXISTS thread_relations (
				id TEXT PRIMARY KEY,
				from_thread_id TEXT NOT NULL DEFAULT '',
				to_thread_id TEXT NOT NULL DEFAULT '',
				relation TEXT NOT NULL DEFAULT '',
				schema_version INTEGER NOT NULL DEFAULT 2,
				created_at TEXT NOT NULL DEFAULT ''
			)`,
		`CREATE INDEX IF NOT EXISTS idx_thread_relations_from ON thread_relations(from_thread_id)`,
		`CREATE TABLE IF NOT EXISTS cross_thread_messages (
				id TEXT PRIMARY KEY,
				from_thread_id TEXT NOT NULL DEFAULT '',
				to_thread_id TEXT NOT NULL DEFAULT '',
				from_workspace TEXT NOT NULL DEFAULT '',
				to_workspace TEXT NOT NULL DEFAULT '',
				task_id TEXT NOT NULL DEFAULT '',
				role TEXT NOT NULL DEFAULT '',
				content TEXT NOT NULL DEFAULT '',
				summary TEXT NOT NULL DEFAULT '',
				explicit_content TEXT NOT NULL DEFAULT '',
				artifact_refs_json TEXT NOT NULL DEFAULT '[]',
				cross_workspace_authorized INTEGER NOT NULL DEFAULT 0,
				cross_workspace_auth_reason TEXT NOT NULL DEFAULT '',
				includes_prompt INTEGER NOT NULL DEFAULT 0,
				includes_secret INTEGER NOT NULL DEFAULT 0,
				includes_memory INTEGER NOT NULL DEFAULT 0,
				includes_approval_rule INTEGER NOT NULL DEFAULT 0,
				schema_version INTEGER NOT NULL DEFAULT 2,
				created_at TEXT NOT NULL DEFAULT ''
			)`,
		`CREATE INDEX IF NOT EXISTS idx_cross_thread_messages_to_created ON cross_thread_messages(to_thread_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS subagent_relations (
				id TEXT PRIMARY KEY,
				parent_task_id TEXT NOT NULL DEFAULT '',
				subagent_task_id TEXT NOT NULL DEFAULT '',
				relation TEXT NOT NULL DEFAULT '',
				schema_version INTEGER NOT NULL DEFAULT 2,
				created_at TEXT NOT NULL DEFAULT ''
			)`,
		`CREATE INDEX IF NOT EXISTS idx_subagent_relations_parent ON subagent_relations(parent_task_id)`,
		`CREATE TABLE IF NOT EXISTS compact_boundaries (
				id TEXT PRIMARY KEY,
				session_id TEXT NOT NULL DEFAULT '',
				task_id TEXT NOT NULL DEFAULT '',
				summary TEXT NOT NULL DEFAULT '',
				token_budget INTEGER NOT NULL DEFAULT 0,
				token_estimate INTEGER NOT NULL DEFAULT 0,
				source_start_id TEXT NOT NULL DEFAULT '',
				source_end_id TEXT NOT NULL DEFAULT '',
				source_item_count INTEGER NOT NULL DEFAULT 0,
				schema_version INTEGER NOT NULL DEFAULT 10,
				created_at TEXT NOT NULL DEFAULT ''
			)`,
		`CREATE INDEX IF NOT EXISTS idx_compact_boundaries_session_created ON compact_boundaries(session_id, created_at)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`ALTER TABLE memories ADD COLUMN kind TEXT NOT NULL DEFAULT 'note'`,
		`ALTER TABLE memories ADD COLUMN mood TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN source TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN workspace TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN redaction TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN importance INTEGER NOT NULL DEFAULT 3`,
		`ALTER TABLE memories ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE memories ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN last_used_at TEXT`,
		`ALTER TABLE memories ADD COLUMN expires_at TEXT`,
		`ALTER TABLE tasks ADD COLUMN natural INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE tasks ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN approval_granted INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN origin TEXT NOT NULL DEFAULT 'foreground'`,
		`ALTER TABLE tasks ADD COLUMN automation_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN automation_risk TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN automation_source TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN automation_trigger TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN schedule_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN schedule_catch_up_policy TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN schedule_missed_runs INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN schedule_max_catch_up_runs INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN schedule_catch_up_runs INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN parent_task_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN parent_thread_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN child_thread_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN subagent_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN role TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN scope_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE tasks ADD COLUMN inherited_scope_from_parent INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN approval_grants_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE tasks ADD COLUMN model_provider TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN model_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN model_base_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN model_profile TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN model_source TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE todos ADD COLUMN priority TEXT NOT NULL DEFAULT 'normal'`,
		`ALTER TABLE cross_thread_messages ADD COLUMN from_workspace TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cross_thread_messages ADD COLUMN to_workspace TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cross_thread_messages ADD COLUMN summary TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cross_thread_messages ADD COLUMN explicit_content TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cross_thread_messages ADD COLUMN artifact_refs_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE cross_thread_messages ADD COLUMN cross_workspace_authorized INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE cross_thread_messages ADD COLUMN cross_workspace_auth_reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cross_thread_messages ADD COLUMN includes_prompt INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE cross_thread_messages ADD COLUMN includes_secret INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE cross_thread_messages ADD COLUMN includes_memory INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE cross_thread_messages ADD COLUMN includes_approval_rule INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE conversation_threads ADD COLUMN archived_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE thread_model_bindings ADD COLUMN base_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN type TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN title TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN tool TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN tool_call_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN tool_result_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN input TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN output TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN target TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN diff TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN risk TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN provider TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transcript_entries ADD COLUMN profile TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE approval_items ADD COLUMN tool_call_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE approval_items ADD COLUMN args_preview TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE approval_items ADD COLUMN command_preview TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE approval_items ADD COLUMN diff_preview TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE approval_items ADD COLUMN decision TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE approval_items ADD COLUMN decided_by TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE approval_items ADD COLUMN resolved_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE compact_boundaries ADD COLUMN source_start_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE compact_boundaries ADD COLUMN source_end_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE compact_boundaries ADD COLUMN source_item_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE schedules ADD COLUMN workspace TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE schedules ADD COLUMN trigger_kind TEXT NOT NULL DEFAULT 'cron'`,
		`ALTER TABLE schedules ADD COLUMN timezone TEXT NOT NULL DEFAULT 'Local'`,
		`ALTER TABLE schedules ADD COLUMN quiet_hours_start TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE schedules ADD COLUMN quiet_hours_end TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.Exec(statement); err != nil && !isDuplicateColumn(err) {
			return err
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_tasks_session_updated ON tasks(session_id, updated_at)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_tasks_parent ON tasks(parent_task_id)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_tasks_parent_thread ON tasks(parent_thread_id)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_tasks_child_thread ON tasks(child_thread_id)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_workspace_created ON memories(workspace, created_at)`); err != nil {
		return err
	}
	for _, statement := range []string{
		fmt.Sprintf(`INSERT OR REPLACE INTO memory_types (kind, description, schema_version, created_at) VALUES ('note', 'General memory note', %d, '')`, CurrentSchemaVersion),
		fmt.Sprintf(`INSERT OR REPLACE INTO memory_types (kind, description, schema_version, created_at) VALUES ('preference', 'User preference used for personalization', %d, '')`, CurrentSchemaVersion),
		fmt.Sprintf(`INSERT OR REPLACE INTO memory_types (kind, description, schema_version, created_at) VALUES ('rule', 'User or workspace rule', %d, '')`, CurrentSchemaVersion),
		fmt.Sprintf(`INSERT OR REPLACE INTO memory_types (kind, description, schema_version, created_at) VALUES ('automation', 'Automation-related memory', %d, '')`, CurrentSchemaVersion),
		fmt.Sprintf(`INSERT OR REPLACE INTO memory_types (kind, description, schema_version, created_at) VALUES ('credential_hint', 'Credential location hint without secret material', %d, '')`, CurrentSchemaVersion),
	} {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, CurrentSchemaVersion)); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES ('liora_schema_version', ?)`, fmt.Sprintf("%d", CurrentSchemaVersion))
	return err
}

func isDuplicateColumn(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "duplicate column")
}

func (s *Store) migrateLegacyJSONL(db *sql.DB) error {
	var value string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = 'memory_jsonl_migrated'`).Scan(&value)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	memories, err := s.readLegacyMemories()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, memory := range memories {
		if strings.TrimSpace(memory.Text) == "" {
			continue
		}
		createdAt := memory.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		kind := memory.Kind
		if kind == "" {
			kind = "note"
		}
		importance := memory.Importance
		if importance == 0 {
			importance = 3
		}
		source := memory.Source
		if source == "" {
			source = "legacy-jsonl"
		}
		id := memory.ID
		if id == "" {
			id = newID()
		}
		enabled := memory.Enabled
		if !enabled {
			enabled = true
		}
		text, redaction := redactPrivateMemoryText(memory.Text, kind)
		if _, err := tx.Exec(`
				INSERT OR IGNORE INTO memories (id, text, kind, mood, source, workspace, redaction, importance, enabled, created_at, updated_at, last_used_at, expires_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, id, text, kind, memory.Mood, source, memory.Workspace, redaction, importance, boolInt(enabled), formatTime(createdAt), formatTime(createdAt), formatOptionalTime(memory.LastUsedAt), formatOptionalTime(memory.ExpiresAt)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES ('memory_jsonl_migrated', ?)`, formatTime(time.Now().UTC())); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) readLegacyMemories() ([]Memory, error) {
	file, err := os.Open(s.path("memory.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	memories := []Memory{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var memory Memory
		if err := json.Unmarshal([]byte(line), &memory); err != nil {
			return nil, err
		}
		memories = append(memories, memory)
	}
	return memories, scanner.Err()
}

func queryMemories(db *sql.DB, options MemoryListOptions) ([]Memory, error) {
	var clauses []string
	args := []any{}
	query := strings.TrimSpace(options.Query)
	if strings.TrimSpace(query) != "" {
		clauses = append(clauses, `lower(text) LIKE ?`)
		args = append(args, "%"+strings.ToLower(query)+"%")
	}
	if !options.IncludeDisabled {
		clauses = append(clauses, `enabled = 1`)
	}
	if strings.TrimSpace(options.Workspace) != "" {
		clauses = append(clauses, `workspace = ?`)
		args = append(args, strings.TrimSpace(options.Workspace))
	}
	if !options.IncludeExpired {
		clauses = append(clauses, `(expires_at IS NULL OR expires_at = '' OR expires_at > ?)`)
		args = append(args, formatTime(time.Now().UTC()))
	}
	sqlText := `SELECT id, text, kind, mood, source, workspace, redaction, importance, enabled, created_at, updated_at, last_used_at, expires_at FROM memories`
	if len(clauses) > 0 {
		sqlText += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	sqlText += ` ORDER BY created_at DESC, id DESC`
	if options.Limit > 0 {
		sqlText += fmt.Sprintf(" LIMIT %d", options.Limit)
	}
	rows, err := db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	memories := []Memory{}
	for rows.Next() {
		var memory Memory
		var createdAt, updatedAt string
		var lastUsedAt, expiresAt sql.NullString
		var enabled int
		if err := rows.Scan(&memory.ID, &memory.Text, &memory.Kind, &memory.Mood, &memory.Source, &memory.Workspace, &memory.Redaction, &memory.Importance, &enabled, &createdAt, &updatedAt, &lastUsedAt, &expiresAt); err != nil {
			return nil, err
		}
		memory.Enabled = enabled != 0
		memory.CreatedAt = parseTime(createdAt)
		memory.UpdatedAt = parseTime(updatedAt)
		if lastUsedAt.Valid && lastUsedAt.String != "" {
			parsed := parseTime(lastUsedAt.String)
			memory.LastUsedAt = &parsed
		}
		if expiresAt.Valid && expiresAt.String != "" {
			parsed := parseTime(expiresAt.String)
			memory.ExpiresAt = &parsed
		}
		memories = append(memories, memory)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(memories)-1; i < j; i, j = i+1, j-1 {
		memories[i], memories[j] = memories[j], memories[i]
	}
	return memories, nil
}

func queryPermissionRules(db *sql.DB, options PermissionRuleListOptions) ([]PermissionRule, error) {
	var clauses []string
	args := []any{}
	if !options.IncludeDisabled {
		clauses = append(clauses, `enabled = 1`)
	}
	if workspace := strings.TrimSpace(options.Workspace); workspace != "" {
		clauses = append(clauses, `(workspace = '' OR workspace = ?)`)
		args = append(args, workspace)
	}
	if sessionID := strings.TrimSpace(options.SessionID); sessionID != "" {
		clauses = append(clauses, `(session_id = '' OR session_id = ?)`)
		args = append(args, sessionID)
	}
	sqlText := `SELECT id, action, workspace, session_id, tool, risk, reason, enabled, created_at, updated_at FROM permission_rules`
	if len(clauses) > 0 {
		sqlText += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	sqlText += ` ORDER BY updated_at DESC, id DESC`
	if options.Limit > 0 {
		sqlText += fmt.Sprintf(" LIMIT %d", options.Limit)
	}
	rows, err := db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []PermissionRule
	for rows.Next() {
		rule, err := scanPermissionRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func scanPermissionRule(scanner interface {
	Scan(dest ...any) error
}) (PermissionRule, error) {
	var rule PermissionRule
	var enabled int
	var createdAt, updatedAt string
	if err := scanner.Scan(&rule.ID, &rule.Action, &rule.Workspace, &rule.SessionID, &rule.Tool, &rule.Risk, &rule.Reason, &enabled, &createdAt, &updatedAt); err != nil {
		return PermissionRule{}, err
	}
	rule.Enabled = enabled != 0
	rule.CreatedAt = parseTime(createdAt)
	rule.UpdatedAt = parseTime(updatedAt)
	return rule, nil
}

func normalizePermissionRuleAction(action PermissionRuleAction) (PermissionRuleAction, error) {
	switch PermissionRuleAction(strings.ToLower(strings.TrimSpace(string(action)))) {
	case PermissionRuleAlwaysAllow:
		return PermissionRuleAlwaysAllow, nil
	case PermissionRuleAlwaysDeny:
		return PermissionRuleAlwaysDeny, nil
	case PermissionRuleAlwaysAsk:
		return PermissionRuleAlwaysAsk, nil
	default:
		return "", fmt.Errorf("unknown permission rule action %q", action)
	}
}

func validatePermissionRuleScope(rule PermissionRule) error {
	if strings.TrimSpace(rule.Workspace) == "" && strings.TrimSpace(rule.SessionID) == "" && strings.TrimSpace(rule.Tool) == "" && strings.TrimSpace(rule.Risk) == "" {
		return errors.New("permission rule requires at least one scope field")
	}
	if err := validatePermissionRuleTool(rule.Tool); err != nil {
		return err
	}
	if err := validatePermissionRuleRisk(rule.Risk); err != nil {
		return err
	}
	return nil
}

func validatePermissionRuleTool(tool string) error {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return nil
	}
	switch tool {
	case "run", "write", "append", "edit", "replace", "mkdir", "delete", "mcp", "hook":
		return nil
	default:
		return fmt.Errorf("unknown permission rule tool %q", tool)
	}
}

func validatePermissionRuleRisk(risk string) error {
	risk = strings.TrimSpace(risk)
	if risk == "" {
		return nil
	}
	switch risk {
	case "write", "dangerous_shell", "network", "external", "hook_side_effect":
		return nil
	default:
		return fmt.Errorf("unknown permission rule risk %q", risk)
	}
}

func getMemory(db *sql.DB, id string) (Memory, error) {
	var memory Memory
	var createdAt, updatedAt string
	var lastUsedAt, expiresAt sql.NullString
	var enabled int
	err := db.QueryRow(`
		SELECT id, text, kind, mood, source, workspace, redaction, importance, enabled, created_at, updated_at, last_used_at, expires_at
		FROM memories
		WHERE id = ?
	`, id).Scan(&memory.ID, &memory.Text, &memory.Kind, &memory.Mood, &memory.Source, &memory.Workspace, &memory.Redaction, &memory.Importance, &enabled, &createdAt, &updatedAt, &lastUsedAt, &expiresAt)
	if err != nil {
		return Memory{}, err
	}
	memory.Enabled = enabled != 0
	memory.CreatedAt = parseTime(createdAt)
	memory.UpdatedAt = parseTime(updatedAt)
	if lastUsedAt.Valid && lastUsedAt.String != "" {
		parsed := parseTime(lastUsedAt.String)
		memory.LastUsedAt = &parsed
	}
	if expiresAt.Valid && expiresAt.String != "" {
		parsed := parseTime(expiresAt.String)
		memory.ExpiresAt = &parsed
	}
	return memory, nil
}

func scanConversationThread(scanner interface {
	Scan(dest ...any) error
}) (ConversationThread, error) {
	var thread ConversationThread
	var createdAt, updatedAt, archivedAt string
	if err := scanner.Scan(&thread.ID, &thread.Title, &thread.Workspace, &thread.LastTaskID, &createdAt, &updatedAt, &archivedAt); err != nil {
		return ConversationThread{}, err
	}
	thread.CreatedAt = parseTime(createdAt)
	thread.UpdatedAt = parseTime(updatedAt)
	if strings.TrimSpace(archivedAt) != "" {
		parsed := parseTime(archivedAt)
		thread.ArchivedAt = &parsed
	}
	return thread, nil
}

func getConversationThread(db *sql.DB, id string) (ConversationThread, error) {
	row := db.QueryRow(`
		SELECT id, title, workspace, last_task_id, created_at, updated_at, archived_at
		FROM conversation_threads
		WHERE id = ?
	`, id)
	return scanConversationThread(row)
}

func attachThreadModelConfig(db *sql.DB, thread *ConversationThread) error {
	config, ok, err := getThreadModelConfig(db, thread.ID)
	if err != nil {
		return err
	}
	if ok {
		thread.ModelConfig = &config
	}
	return nil
}

func getThreadModelConfig(db *sql.DB, threadID string) (ThreadModelConfig, bool, error) {
	row := db.QueryRow(`
		SELECT thread_id, provider, model, base_url, profile, inherited_from_thread_id
		FROM thread_model_bindings
		WHERE thread_id = ?
	`, threadID)
	var config ThreadModelConfig
	if err := row.Scan(&config.ThreadID, &config.Provider, &config.Model, &config.BaseURL, &config.Profile, &config.InheritedFromThreadID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ThreadModelConfig{}, false, nil
		}
		return ThreadModelConfig{}, false, err
	}
	return config, true, nil
}

func scanCrossThreadMessage(scanner interface {
	Scan(dest ...any) error
}) (CrossThreadMessage, error) {
	var message CrossThreadMessage
	var artifactRefsJSON, createdAt string
	var crossWorkspaceAuthorized, includesPrompt, includesSecret, includesMemory, includesApprovalRule int
	if err := scanner.Scan(
		&message.ID,
		&message.FromThreadID,
		&message.ToThreadID,
		&message.FromWorkspace,
		&message.ToWorkspace,
		&message.TaskID,
		&message.Role,
		&message.Content,
		&message.Summary,
		&message.ExplicitContent,
		&artifactRefsJSON,
		&crossWorkspaceAuthorized,
		&message.CrossWorkspaceAuthReason,
		&includesPrompt,
		&includesSecret,
		&includesMemory,
		&includesApprovalRule,
		&createdAt,
	); err != nil {
		return CrossThreadMessage{}, err
	}
	message.CrossWorkspaceAuthorized = crossWorkspaceAuthorized != 0
	message.IncludesPrompt = includesPrompt != 0
	message.IncludesSecret = includesSecret != 0
	message.IncludesMemory = includesMemory != 0
	message.IncludesApprovalRule = includesApprovalRule != 0
	message.CreatedAt = parseTime(createdAt)
	if strings.TrimSpace(artifactRefsJSON) != "" {
		if err := json.Unmarshal([]byte(artifactRefsJSON), &message.ArtifactRefs); err != nil {
			return CrossThreadMessage{}, err
		}
	}
	return message, nil
}

func normalizeCrossThreadArtifactRefs(refs []CrossThreadArtifactRef) []CrossThreadArtifactRef {
	normalized := make([]CrossThreadArtifactRef, 0, len(refs))
	for _, ref := range refs {
		path := strings.TrimSpace(ref.Path)
		if path == "" {
			continue
		}
		normalized = append(normalized, CrossThreadArtifactRef{
			Path:    path,
			Summary: strings.TrimSpace(ref.Summary),
		})
	}
	return normalized
}

func normalizeMemoryKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return "note", nil
	}
	switch kind {
	case "note", "preference", "rule", "automation", "credential_hint":
		return kind, nil
	default:
		return "", fmt.Errorf("unknown memory kind %q", kind)
	}
}

var (
	emailPattern       = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	secretPairPattern  = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|auth[_-]?token|token|secret|password|bearer)\s*[:=]\s*([^\s,;]+)`)
	secretTokenPattern = regexp.MustCompile(`\b(sk-[A-Za-z0-9_\-]{10,}|ghp_[A-Za-z0-9_]{10,}|xox[baprs]-[A-Za-z0-9\-]{10,})\b`)
)

func redactPrivateMemoryText(text string, kind string) (string, string) {
	redactions := map[string]bool{}
	redacted := secretPairPattern.ReplaceAllStringFunc(text, func(match string) string {
		redactions["secret"] = true
		parts := strings.FieldsFunc(match, func(r rune) bool {
			return r == ':' || r == '='
		})
		if len(parts) == 0 {
			return "[REDACTED_SECRET]"
		}
		return strings.TrimSpace(parts[0]) + "=[REDACTED_SECRET]"
	})
	redacted = secretTokenPattern.ReplaceAllStringFunc(redacted, func(string) string {
		redactions["secret"] = true
		return "[REDACTED_SECRET]"
	})
	redacted = emailPattern.ReplaceAllStringFunc(redacted, func(string) string {
		redactions["pii"] = true
		return "[REDACTED_EMAIL]"
	})
	if kind == "credential_hint" && !redactions["secret"] {
		if looksLikeCredentialMaterial(redacted) {
			redactions["secret"] = true
			redacted = "[REDACTED_SECRET]"
		}
	}
	return redacted, redactionSummary(redactions)
}

func looksLikeCredentialMaterial(text string) bool {
	compact := strings.TrimSpace(text)
	return len(compact) >= 24 && !strings.Contains(compact, " ") && strings.IndexFunc(compact, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' && r != '-'
	}) == -1
}

func redactionSummary(redactions map[string]bool) string {
	var parts []string
	for _, key := range []string{"secret", "pii"} {
		if redactions[key] {
			parts = append(parts, key)
		}
	}
	return strings.Join(parts, ",")
}

func normalizeMemoryImportance(importance int) (int, error) {
	if importance == 0 {
		return 3, nil
	}
	if importance < 1 || importance > 5 {
		return 0, fmt.Errorf("memory importance must be between 1 and 5")
	}
	return importance, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) ensureRoot() error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	return os.Chmod(s.root, 0o700)
}

func (s *Store) path(name string) string {
	return filepath.Join(s.root, name)
}

func newID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return fmt.Sprintf("mem_%d", time.Now().UnixNano())
	}
	return "mem_" + hex.EncodeToString(data[:])
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return formatTime(*t)
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
