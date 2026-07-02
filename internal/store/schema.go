package store

import (
	"database/sql"
	"fmt"
	"strings"
)

const CurrentSchemaVersion = 16

type RetentionPolicy string

const (
	RetentionLongTerm      RetentionPolicy = "long_term"
	RetentionCleanable     RetentionPolicy = "cleanable"
	RetentionReferenceOnly RetentionPolicy = "reference_only"
)

type SchemaReport struct {
	DBPath            string
	State             string
	CurrentVersion    int
	MigrationStatus   string
	Recoverable       bool
	Error             string
	BackupPath        string
	ExportCommand     string
	SafeDeleteCommand string
	Tables            []SchemaTableReport
}

type SchemaTableReport struct {
	Name               string
	Present            bool
	Retention          RetentionPolicy
	RetentionRationale string
}

func (s *Store) SchemaReport() (SchemaReport, error) {
	report := SchemaReport{
		DBPath:          s.path("liora.db"),
		State:           "ok",
		CurrentVersion:  CurrentSchemaVersion,
		MigrationStatus: "complete",
		Tables:          schemaTables(),
	}
	db, err := s.OpenDB()
	if err != nil {
		report.State = "error"
		report.MigrationStatus = "error"
		report.Recoverable = true
		report.Error = "database open or migration failed; make a read-only backup of liora.db before repair or deletion"
		report.BackupPath = report.DBPath + ".readonly-backup"
		report.ExportCommand = fmt.Sprintf("sqlite3 %q .dump > %q", report.DBPath, report.DBPath+".dump.sql")
		report.SafeDeleteCommand = fmt.Sprintf("mv %q %q", report.DBPath, report.DBPath+".disabled-after-backup")
		return report, nil
	}
	defer db.Close()

	version, err := schemaUserVersion(db)
	if err != nil {
		return SchemaReport{}, fmt.Errorf("read schema version: %w", err)
	}
	report.CurrentVersion = version
	for index := range report.Tables {
		present, err := tablePresent(db, report.Tables[index].Name)
		if err != nil {
			return SchemaReport{}, fmt.Errorf("inspect table %s: %w", report.Tables[index].Name, err)
		}
		report.Tables[index].Present = present
		if !present {
			report.MigrationStatus = "pending"
		}
	}
	if version < CurrentSchemaVersion {
		report.MigrationStatus = "pending"
	}
	return report, nil
}

func schemaTables() []SchemaTableReport {
	return []SchemaTableReport{
		{Name: "memories", Retention: RetentionLongTerm, RetentionRationale: "user personalization state"},
		{Name: "memory_types", Retention: RetentionLongTerm, RetentionRationale: "memory kind catalog"},
		{Name: "tasks", Retention: RetentionLongTerm, RetentionRationale: "task audit history"},
		{Name: "sessions", Retention: RetentionLongTerm, RetentionRationale: "conversation index"},
		{Name: "session_messages", Retention: RetentionLongTerm, RetentionRationale: "user-visible transcript source"},
		{Name: "task_events", Retention: RetentionLongTerm, RetentionRationale: "daemon audit event log"},
		{Name: "transcript_entries", Retention: RetentionCleanable, RetentionRationale: "derived transcript projection after compaction"},
		{Name: "todos", Retention: RetentionLongTerm, RetentionRationale: "task planning and progress state"},
		{Name: "artifact_refs", Retention: RetentionReferenceOnly, RetentionRationale: "stores artifact paths and summaries, not artifact bytes"},
		{Name: "approval_items", Retention: RetentionCleanable, RetentionRationale: "pending approval queue can prune terminal items"},
		{Name: "permission_rules", Retention: RetentionLongTerm, RetentionRationale: "user permission policy decisions"},
		{Name: "schedules", Retention: RetentionLongTerm, RetentionRationale: "user automation configuration"},
		{Name: "hooks", Retention: RetentionLongTerm, RetentionRationale: "workspace automation configuration"},
		{Name: "conversation_threads", Retention: RetentionLongTerm, RetentionRationale: "thread identity and navigation state"},
		{Name: "workspace_model_bindings", Retention: RetentionLongTerm, RetentionRationale: "workspace default model selection state"},
		{Name: "thread_model_bindings", Retention: RetentionLongTerm, RetentionRationale: "thread model selection state"},
		{Name: "thread_relations", Retention: RetentionLongTerm, RetentionRationale: "thread graph metadata"},
		{Name: "cross_thread_messages", Retention: RetentionLongTerm, RetentionRationale: "explicit handoff transcript"},
		{Name: "subagent_relations", Retention: RetentionLongTerm, RetentionRationale: "parent-child task audit graph"},
		{Name: "compact_boundaries", Retention: RetentionReferenceOnly, RetentionRationale: "stores compaction markers and summary references"},
	}
}

func validateSchemaTableRetention(tables []SchemaTableReport) error {
	for _, table := range tables {
		if strings.TrimSpace(table.Name) == "" {
			return fmt.Errorf("schema table name is required")
		}
		switch table.Retention {
		case RetentionLongTerm, RetentionCleanable, RetentionReferenceOnly:
		default:
			return fmt.Errorf("schema table %s has unknown retention policy %q", table.Name, table.Retention)
		}
		if strings.TrimSpace(table.RetentionRationale) == "" {
			return fmt.Errorf("schema table %s requires retention rationale", table.Name)
		}
	}
	return nil
}

func tablePresent(db *sql.DB, name string) (bool, error) {
	var found string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&found)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}

func schemaUserVersion(db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}
