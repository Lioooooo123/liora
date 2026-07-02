package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreSchemaReportMigratesOldDatabaseFixtureIdempotently(t *testing.T) {
	// Given
	root := t.TempDir()
	dbPath := filepath.Join(root, "liora.db")
	loadSQLFixture(t, dbPath, "old-liora-v0.1.sql")
	store := New(root)

	// When
	first, err := store.SchemaReport()
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SchemaReport()
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if first.State != "ok" || second.State != "ok" {
		t.Fatalf("expected ok schema reports, got first=%#v second=%#v", first, second)
	}
	if first.CurrentVersion != CurrentSchemaVersion || second.CurrentVersion != CurrentSchemaVersion {
		t.Fatalf("expected current schema version %d, got first=%d second=%d", CurrentSchemaVersion, first.CurrentVersion, second.CurrentVersion)
	}
	if first.MigrationStatus != "complete" || second.MigrationStatus != "complete" {
		t.Fatalf("expected complete migrations, got first=%q second=%q", first.MigrationStatus, second.MigrationStatus)
	}
	assertSchemaTablePresent(t, first, "memories")
	assertSchemaTablePresent(t, first, "tasks")
	assertSchemaTablePresent(t, first, "sessions")
	assertSchemaTablePresent(t, first, "session_messages")
	assertSchemaTablePresent(t, first, "task_events")
	for _, table := range schemaVersionedSurfaceTables() {
		assertSchemaTablePresent(t, first, table)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM memories WHERE text = 'legacy sqlite memory'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected fixture memory to survive one time, got count=%d", count)
	}
	var version string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'liora_schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != fmt.Sprintf("%d", CurrentSchemaVersion) {
		t.Fatalf("expected schema version meta to be %d, got %q", CurrentSchemaVersion, version)
	}
	for _, table := range schemaVersionedSurfaceTables() {
		assertTableHasColumn(t, db, table, "schema_version")
	}
	assertTableHasColumn(t, db, "todos", "priority")
}

func TestStoreSchemaReportRunsMigrationFixtureMatrixIdempotently(t *testing.T) {
	for _, fixture := range migrationFixtures() {
		t.Run(fixture, func(t *testing.T) {
			root := t.TempDir()
			dbPath := filepath.Join(root, "liora.db")
			loadSQLFixture(t, dbPath, fixture)
			store := New(root)

			for i := 0; i < 2; i++ {
				report, err := store.SchemaReport()
				if err != nil {
					t.Fatal(err)
				}
				if report.State != "ok" || report.MigrationStatus != "complete" || report.CurrentVersion != CurrentSchemaVersion {
					t.Fatalf("unexpected fixture migration report on pass %d: %#v", i+1, report)
				}
			}

			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, table := range schemaVersionedSurfaceTables() {
				assertTableHasColumn(t, db, table, "schema_version")
			}
			var version int
			if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if version != CurrentSchemaVersion {
				t.Fatalf("expected fixture %s to migrate to schema version %d, got %d", fixture, CurrentSchemaVersion, version)
			}
		})
	}
}

func TestStoreSchemaReportCreatesFreshVersionedSurfaces(t *testing.T) {
	root := t.TempDir()
	report, err := New(root).SchemaReport()
	if err != nil {
		t.Fatal(err)
	}
	if report.State != "ok" || report.MigrationStatus != "complete" || report.CurrentVersion != CurrentSchemaVersion {
		t.Fatalf("unexpected fresh schema report: %#v", report)
	}
	for _, table := range append([]string{"memories", "tasks", "sessions", "session_messages", "task_events"}, schemaVersionedSurfaceTables()...) {
		assertSchemaTablePresent(t, report, table)
	}

	db, err := sql.Open("sqlite", filepath.Join(root, "liora.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range schemaVersionedSurfaceTables() {
		assertTableHasColumn(t, db, table, "schema_version")
	}
	for _, kind := range []string{"note", "preference", "rule", "automation", "credential_hint"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM memory_types WHERE kind = ? AND schema_version = ?`, kind, CurrentSchemaVersion).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("expected seeded memory type %q once, got %d", kind, count)
		}
	}
}

func TestSchemaTablesDefineRetentionPolicies(t *testing.T) {
	tables := schemaTables()
	if err := validateSchemaTableRetention(tables); err != nil {
		t.Fatal(err)
	}
	classes := map[RetentionPolicy]bool{}
	for _, table := range tables {
		classes[table.Retention] = true
	}
	for _, want := range []RetentionPolicy{RetentionLongTerm, RetentionCleanable, RetentionReferenceOnly} {
		if !classes[want] {
			t.Fatalf("expected retention class %s in %#v", want, tables)
		}
	}
}

func TestSchemaTableRetentionRejectsMissingOrUnknownPolicy(t *testing.T) {
	tests := []struct {
		name   string
		tables []SchemaTableReport
		want   string
	}{
		{
			name:   "missing policy",
			tables: []SchemaTableReport{{Name: "new_table", RetentionRationale: "new data"}},
			want:   "unknown retention policy",
		},
		{
			name:   "unknown policy",
			tables: []SchemaTableReport{{Name: "new_table", Retention: RetentionPolicy("foreverish"), RetentionRationale: "new data"}},
			want:   "unknown retention policy",
		},
		{
			name:   "missing rationale",
			tables: []SchemaTableReport{{Name: "new_table", Retention: RetentionLongTerm}},
			want:   "requires retention rationale",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSchemaTableRetention(tt.tables)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestStoreSchemaReportCompletesPartialSurfaceMigrationIdempotently(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "liora.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		PRAGMA user_version = 1;
		CREATE TABLE todos (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL DEFAULT '',
			parent_task_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			schema_version INTEGER NOT NULL DEFAULT 2,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO todos (id, title, schema_version) VALUES ('todo-existing', 'already migrated', 2);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store := New(root)
	for i := 0; i < 2; i++ {
		report, err := store.SchemaReport()
		if err != nil {
			t.Fatal(err)
		}
		if report.State != "ok" || report.MigrationStatus != "complete" || report.CurrentVersion != CurrentSchemaVersion {
			t.Fatalf("unexpected partial migration report on pass %d: %#v", i+1, report)
		}
	}

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM todos WHERE id = 'todo-existing'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected existing partial todo row to survive once, got %d", count)
	}
	for _, table := range schemaVersionedSurfaceTables() {
		assertTableHasColumn(t, db, table, "schema_version")
	}
}

func TestStoreSchemaReportReportsPartialMigrationFailureWithReadOnlyBackupAdvice(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "liora.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		PRAGMA user_version = 1;
		CREATE TABLE meta (
			name TEXT PRIMARY KEY,
			content TEXT NOT NULL
		);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := New(root).SchemaReport()
	if err != nil {
		t.Fatal(err)
	}
	if report.State != "error" || report.MigrationStatus != "error" || !report.Recoverable {
		t.Fatalf("expected recoverable migration error, got %#v", report)
	}
	assertRecoveryGuidance(t, report)
}

func TestStoreSchemaReportReportsCorruptDatabase(t *testing.T) {
	// Given
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "liora.db"), []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}

	// When
	report, err := New(root).SchemaReport()

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if report.State != "error" {
		t.Fatalf("expected error state, got %#v", report)
	}
	if !report.Recoverable {
		t.Fatalf("expected corrupt db report to be recoverable, got %#v", report)
	}
	assertRecoveryGuidance(t, report)
}

func TestStoreSchemaReportRejectsFutureDatabaseVersion(t *testing.T) {
	// Given
	root := t.TempDir()
	dbPath := filepath.Join(root, "liora.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	futureVersion := CurrentSchemaVersion + 1
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, futureVersion)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// When
	report, err := New(root).SchemaReport()

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if report.State != "error" {
		t.Fatalf("expected error state for future schema, got %#v", report)
	}
	if report.MigrationStatus != "error" {
		t.Fatalf("expected error migration status, got %#v", report)
	}
	if !report.Recoverable {
		t.Fatalf("expected future schema report to be recoverable, got %#v", report)
	}
	assertRecoveryGuidance(t, report)

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != futureVersion {
		t.Fatalf("expected future schema version to remain %d, got %d", futureVersion, version)
	}
}

func loadSQLFixture(t *testing.T, dbPath string, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
}

func migrationFixtures() []string {
	return []string{"old-liora-v0.1.sql"}
}

func assertSchemaTablePresent(t *testing.T, report SchemaReport, name string) {
	t.Helper()
	for _, table := range report.Tables {
		if table.Name == name {
			if !table.Present {
				t.Fatalf("expected table %s to be present in %#v", name, report.Tables)
			}
			return
		}
	}
	t.Fatalf("expected report to include table %s in %#v", name, report.Tables)
}

func schemaVersionedSurfaceTables() []string {
	return []string{
		"memory_types",
		"transcript_entries",
		"todos",
		"artifact_refs",
		"approval_items",
		"schedules",
		"hooks",
		"conversation_threads",
		"thread_model_bindings",
		"thread_relations",
		"cross_thread_messages",
		"subagent_relations",
		"compact_boundaries",
	}
}

func assertTableHasColumn(t *testing.T, db *sql.DB, table string, column string) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("expected table %s to have column %s", table, column)
}

func assertRecoveryGuidance(t *testing.T, report SchemaReport) {
	t.Helper()
	if report.Error == "" {
		t.Fatalf("expected a clean diagnostic error, got %#v", report)
	}
	for label, value := range map[string]string{
		"read-only backup advice": report.Error,
		"backup path":             report.BackupPath,
		"export command":          report.ExportCommand,
		"safe delete command":     report.SafeDeleteCommand,
	} {
		if !strings.Contains(value, "liora.db") && label != "read-only backup advice" {
			t.Fatalf("expected %s to mention liora.db, got %q in %#v", label, value, report)
		}
	}
	if !strings.Contains(report.Error, "read-only backup") {
		t.Fatalf("expected read-only backup advice, got %q", report.Error)
	}
	if !strings.Contains(report.BackupPath, "readonly-backup") {
		t.Fatalf("expected backup path, got %q", report.BackupPath)
	}
	if !strings.Contains(report.ExportCommand, "sqlite3") || !strings.Contains(report.ExportCommand, ".dump") {
		t.Fatalf("expected sqlite dump command, got %q", report.ExportCommand)
	}
	if !strings.Contains(report.SafeDeleteCommand, "disabled-after-backup") {
		t.Fatalf("expected safe delete/move command, got %q", report.SafeDeleteCommand)
	}
}
