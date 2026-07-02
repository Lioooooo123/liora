package hook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Lioooooo123/liora/internal/store"
)

type Registry struct {
	store *store.Store
}

func NewRegistry(persistentStore *store.Store) *Registry {
	return &Registry{store: persistentStore}
}

func (r *Registry) Save(ctx context.Context, request SaveRequest) (Hook, error) {
	if r == nil || r.store == nil {
		return Hook{}, errors.New("hook registry store is required")
	}
	hook, err := normalizeHook(request)
	if err != nil {
		return Hook{}, err
	}
	db, err := r.open(ctx)
	if err != nil {
		return Hook{}, err
	}
	defer db.Close()
	now := time.Now().UTC()
	_, err = db.ExecContext(ctx, `
		INSERT INTO hooks (id, event, command, enabled, schema_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, 2, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			event = excluded.event,
			command = excluded.command,
			enabled = excluded.enabled,
			updated_at = excluded.updated_at
	`, hook.ID, string(hook.Event), hook.Command, boolInt(hook.Enabled), formatTime(now), formatTime(now))
	if err != nil {
		return Hook{}, fmt.Errorf("save hook %s: %w", hook.ID, err)
	}
	return r.Get(ctx, hook.ID)
}

func (r *Registry) Get(ctx context.Context, id string) (Hook, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Hook{}, errors.New("hook id is required")
	}
	db, err := r.open(ctx)
	if err != nil {
		return Hook{}, err
	}
	defer db.Close()
	hook, err := scanHook(db.QueryRowContext(ctx, `
		SELECT id, event, command, enabled, created_at, updated_at
		FROM hooks
		WHERE id = ?
	`, id))
	if err != nil {
		return Hook{}, fmt.Errorf("get hook %s: %w", id, err)
	}
	return hook, nil
}

func (r *Registry) List(ctx context.Context, includeDisabled bool) ([]Hook, error) {
	db, err := r.open(ctx)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	query := `SELECT id, event, command, enabled, created_at, updated_at FROM hooks`
	if !includeDisabled {
		query += ` WHERE enabled <> 0`
	}
	query += ` ORDER BY event, id`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list hooks: %w", err)
	}
	defer rows.Close()
	hooks := []Hook{}
	for rows.Next() {
		hook, err := scanHook(rows)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, hook)
	}
	return hooks, rows.Err()
}

func (r *Registry) SetEnabled(ctx context.Context, id string, enabled bool) (Hook, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Hook{}, errors.New("hook id is required")
	}
	db, err := r.open(ctx)
	if err != nil {
		return Hook{}, err
	}
	defer db.Close()
	result, err := db.ExecContext(ctx, `UPDATE hooks SET enabled = ?, updated_at = ? WHERE id = ?`, boolInt(enabled), formatTime(time.Now().UTC()), id)
	if err != nil {
		return Hook{}, fmt.Errorf("update hook %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Hook{}, fmt.Errorf("inspect hook update: %w", err)
	}
	if affected == 0 {
		return Hook{}, fmt.Errorf("hook %s: %w", id, sql.ErrNoRows)
	}
	return r.Get(ctx, id)
}

func (r *Registry) open(ctx context.Context) (*sql.DB, error) {
	db, err := r.store.OpenDB()
	if err != nil {
		return nil, fmt.Errorf("open hook db: %w", err)
	}
	if err := ensureHookTables(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
