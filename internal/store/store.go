package store

import (
	"bufio"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	Importance int        `json:"importance"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

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
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
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
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("memory text is required")
	}
	db, err := s.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	now := time.Now().UTC()
	_, err = db.Exec(`
		INSERT INTO memories (id, text, kind, mood, source, importance, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, newID(), text, "note", "", "manual", 3, formatTime(now), formatTime(now))
	return err
}

func (s *Store) ListMemories(limit int) ([]Memory, error) {
	db, err := s.OpenDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return queryMemories(db, "", limit)
}

func (s *Store) SearchMemories(query string, limit int) ([]Memory, error) {
	db, err := s.OpenDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	query = strings.TrimSpace(query)
	if query == "" {
		return queryMemories(db, "", limit)
	}
	return queryMemories(db, query, limit)
}

func (s *Store) ScanSkills(workspaceRoot string) ([]Skill, error) {
	var skills []Skill
	global, err := scanSkillDir(filepath.Join(s.root, "skills"), "global")
	if err != nil {
		return nil, err
	}
	skills = append(skills, global...)
	if strings.TrimSpace(workspaceRoot) != "" {
		local, err := scanSkillDir(filepath.Join(workspaceRoot, ".liora", "skills"), "workspace")
		if err != nil {
			return nil, err
		}
		skills = append(skills, local...)
	}
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Name == skills[j].Name {
			return skills[i].Source < skills[j].Source
		}
		return skills[i].Name < skills[j].Name
	})
	return skills, nil
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
	db, err := sql.Open("sqlite", s.path("liora.db"))
	if err != nil {
		return nil, err
	}
	if err := initDB(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateLegacyJSONL(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func initDB(db *sql.DB) error {
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			text TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'note',
			mood TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			importance INTEGER NOT NULL DEFAULT 3,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_used_at TEXT
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
			approval_granted INTEGER NOT NULL DEFAULT 0,
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
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN natural INTEGER NOT NULL DEFAULT 1`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN approval_granted INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_tasks_session_updated ON tasks(session_id, updated_at)`); err != nil {
		return err
	}
	return nil
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
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO memories (id, text, kind, mood, source, importance, created_at, updated_at, last_used_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, memory.Text, kind, memory.Mood, source, importance, formatTime(createdAt), formatTime(createdAt), formatOptionalTime(memory.LastUsedAt)); err != nil {
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

	var memories []Memory
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

func queryMemories(db *sql.DB, query string, limit int) ([]Memory, error) {
	limitClause := ""
	args := []any{}
	if strings.TrimSpace(query) != "" {
		limitClause = `WHERE lower(text) LIKE ?`
		args = append(args, "%"+strings.ToLower(query)+"%")
	}
	sqlText := `SELECT id, text, kind, mood, source, importance, created_at, updated_at, last_used_at FROM memories ` + limitClause + ` ORDER BY created_at DESC, id DESC`
	if limit > 0 {
		sqlText += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var memories []Memory
	for rows.Next() {
		var memory Memory
		var createdAt, updatedAt string
		var lastUsedAt sql.NullString
		if err := rows.Scan(&memory.ID, &memory.Text, &memory.Kind, &memory.Mood, &memory.Source, &memory.Importance, &createdAt, &updatedAt, &lastUsedAt); err != nil {
			return nil, err
		}
		memory.CreatedAt = parseTime(createdAt)
		memory.UpdatedAt = parseTime(updatedAt)
		if lastUsedAt.Valid && lastUsedAt.String != "" {
			parsed := parseTime(lastUsedAt.String)
			memory.LastUsedAt = &parsed
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

func (s *Store) ensureRoot() error {
	return os.MkdirAll(s.root, 0o700)
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

func scanSkillDir(root string, source string) ([]Skill, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		skill := parseSkill(entry.Name(), path, string(data), source)
		skills = append(skills, skill)
	}
	return skills, nil
}

func parseSkill(name string, path string, body string, source string) Skill {
	skill := Skill{Name: name, Title: name, Path: path, Body: body, Source: source}
	lines := strings.Split(body, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for _, line := range lines[1:] {
			line = strings.TrimSpace(line)
			if line == "---" {
				break
			}
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			switch strings.TrimSpace(strings.ToLower(key)) {
			case "name", "title":
				skill.Title = strings.TrimSpace(value)
			case "description":
				skill.Description = strings.TrimSpace(value)
			}
		}
		return skill
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			skill.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			break
		}
	}
	return skill
}
