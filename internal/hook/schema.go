package hook

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Lioooooo123/liora/internal/permission"
)

func ensureHookTables(ctx context.Context, db *sql.DB) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS hook_runs (
			id TEXT PRIMARY KEY,
			hook_id TEXT NOT NULL DEFAULT '',
			event TEXT NOT NULL DEFAULT '',
			workspace TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			exit_code INTEGER NOT NULL DEFAULT 0,
			stdout TEXT NOT NULL DEFAULT '',
			stderr TEXT NOT NULL DEFAULT '',
			output_truncated INTEGER NOT NULL DEFAULT 0,
			replay_of_run_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hook_runs_hook_created ON hook_runs(hook_id, created_at)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure hook tables: %w", err)
		}
	}
	return nil
}

func scanHook(scanner interface{ Scan(dest ...any) error }) (Hook, error) {
	var hook Hook
	var enabled int
	var createdAt, updatedAt string
	if err := scanner.Scan(&hook.ID, &hook.Event, &hook.Command, &enabled, &createdAt, &updatedAt); err != nil {
		return Hook{}, err
	}
	hook.Enabled = enabled != 0
	hook.CreatedAt = parseTime(createdAt)
	hook.UpdatedAt = parseTime(updatedAt)
	classified, _ := permission.Classify("hook", string(hook.Event)+" "+hook.Command, false)
	hook.Risk = classified.Risk
	hook.Reason = classified.Reason
	return hook, nil
}

func scanRun(scanner interface{ Scan(dest ...any) error }) (RunRecord, error) {
	var run RunRecord
	var event, status, createdAt string
	var truncated int
	if err := scanner.Scan(&run.ID, &run.HookID, &event, &run.Workspace, &run.Payload, &status, &run.ExitCode, &run.Stdout, &run.Stderr, &truncated, &run.ReplayOfRunID, &createdAt); err != nil {
		return RunRecord{}, err
	}
	run.Event = Event(event)
	run.Status = RunStatus(status)
	run.OutputTruncated = truncated != 0
	run.CreatedAt = parseTime(createdAt)
	return run, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func newHookID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
}
