package hook

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Lioooooo123/liora/internal/store"
)

func (r *Registry) RecordRun(ctx context.Context, record RunRecord) (RunRecord, error) {
	record.HookID = strings.TrimSpace(record.HookID)
	if record.HookID == "" {
		return RunRecord{}, errors.New("hook run requires hook id")
	}
	db, err := r.open(ctx)
	if err != nil {
		return RunRecord{}, err
	}
	defer db.Close()
	if strings.TrimSpace(record.ID) == "" {
		record.ID = newHookID("hook_run")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO hook_runs (id, hook_id, event, workspace, payload_json, status, exit_code, stdout, stderr, output_truncated, replay_of_run_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.HookID, string(record.Event), record.Workspace, record.Payload, string(record.Status), record.ExitCode, record.Stdout, record.Stderr, boolInt(record.OutputTruncated), record.ReplayOfRunID, formatTime(record.CreatedAt))
	if err != nil {
		return RunRecord{}, fmt.Errorf("record hook run %s: %w", record.HookID, err)
	}
	return record, nil
}

func (r *Registry) ListRuns(ctx context.Context, options RunListOptions) ([]RunRecord, error) {
	db, err := r.open(ctx)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	query, args := hookRunsQuery(options)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list hook runs: %w", err)
	}
	defer rows.Close()
	runs := []RunRecord{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (r *Registry) LatestFailedRun(ctx context.Context, hookID string) (RunRecord, error) {
	db, err := r.open(ctx)
	if err != nil {
		return RunRecord{}, err
	}
	defer db.Close()
	run, err := scanRun(db.QueryRowContext(ctx, `
		SELECT id, hook_id, event, workspace, payload_json, status, exit_code, stdout, stderr, output_truncated, replay_of_run_id, created_at
		FROM hook_runs
		WHERE hook_id = ? AND status <> ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, strings.TrimSpace(hookID), string(RunStatusOK)))
	if err != nil {
		return RunRecord{}, fmt.Errorf("latest failed hook run %s: %w", strings.TrimSpace(hookID), err)
	}
	return run, nil
}

func SecuritySummary(ctx context.Context, persistentStore *store.Store) (map[string]int, int, error) {
	registry := NewRegistry(persistentStore)
	hooks, err := registry.List(ctx, true)
	if err != nil {
		return nil, 0, err
	}
	summary := map[string]int{}
	for _, hook := range hooks {
		if hook.Enabled {
			summary[hook.Risk]++
		}
	}
	runs, err := registry.ListRuns(ctx, RunListOptions{})
	if err != nil {
		return nil, 0, err
	}
	failures := 0
	for _, run := range runs {
		if run.Status != RunStatusOK {
			failures++
		}
	}
	return summary, failures, nil
}

func hookRunsQuery(options RunListOptions) (string, []any) {
	var clauses []string
	args := []any{}
	if hookID := strings.TrimSpace(options.HookID); hookID != "" {
		clauses = append(clauses, `hook_id = ?`)
		args = append(args, hookID)
	}
	query := `SELECT id, hook_id, event, workspace, payload_json, status, exit_code, stdout, stderr, output_truncated, replay_of_run_id, created_at FROM hook_runs`
	if len(clauses) > 0 {
		query += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	query += ` ORDER BY created_at ASC, id ASC`
	if options.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", options.Limit)
	}
	return query, args
}
