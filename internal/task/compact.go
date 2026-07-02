package task

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	compactSkippedEmptyContext     = "empty_context"
	compactSkippedNoTask           = "no_task"
	compactSkippedWithinBudget     = "within_budget"
	compactSkippedAlreadyCompacted = "already_compacted"
	compactBoundarySchemaVersion   = 10
)

func (r *Repository) CompactSession(ctx context.Context, sessionID string, request CompactRequest) (CompactResult, error) {
	mode := request.Mode
	if mode == "" {
		mode = CompactModeManual
	}
	if mode != CompactModeManual && mode != CompactModeAuto {
		return CompactResult{}, fmt.Errorf("unknown compact mode %q", mode)
	}
	session, err := r.GetSession(ctx, sessionID)
	if err != nil {
		return CompactResult{}, err
	}
	itemLimit := normalizeContextItemLimit(request.ItemLimit)
	tokenBudget := normalizeContextTokenBudget(request.TokenBudget)
	timeline, err := r.Timeline(ctx, session.ID, itemLimit)
	if err != nil {
		return CompactResult{}, err
	}
	transcript, artifactRefs := compactContextTimeline(timeline)
	artifactRefs = contextArtifactRefs(transcript, artifactRefs)
	estimated, _ := estimateContextBudget(transcript, artifactRefs)
	result := CompactResult{
		Session:               session,
		Mode:                  mode,
		Reason:                strings.TrimSpace(request.Reason),
		TokenBudget:           tokenBudget,
		BeforeEstimatedTokens: estimated,
		AfterEstimatedTokens:  estimated,
		TranscriptItems:       len(transcript),
		GeneratedAt:           time.Now().UTC(),
	}
	if len(transcript) == 0 {
		result.SkippedReason = compactSkippedEmptyContext
		return result, nil
	}
	if strings.TrimSpace(session.LastTaskID) == "" {
		result.SkippedReason = compactSkippedNoTask
		return result, nil
	}
	if mode == CompactModeAuto {
		if estimated <= tokenBudget {
			result.SkippedReason = compactSkippedWithinBudget
			return result, nil
		}
		if !hasContextSinceLatestCompact(transcript) {
			result.SkippedReason = compactSkippedAlreadyCompacted
			return result, nil
		}
	}
	sourceStartID, sourceEndID := compactSourceRange(transcript)
	summary := compactBoundarySummary(mode, result.Reason, len(transcript), estimated, tokenBudget)
	if err := r.AppendEvent(ctx, session.LastTaskID, EventCompactBoundary, EventPayload{
		Message:         summary,
		Status:          string(mode),
		Reason:          result.Reason,
		TokenEstimate:   estimated,
		TokenBudget:     tokenBudget,
		SourceStartID:   sourceStartID,
		SourceEndID:     sourceEndID,
		SourceItemCount: len(transcript),
	}); err != nil {
		return CompactResult{}, err
	}
	envelope, err := r.ContextEnvelope(ctx, session.ID, ContextRequest{ItemLimit: itemLimit, TokenBudget: tokenBudget})
	if err != nil {
		return CompactResult{}, err
	}
	result.Compacted = true
	result.AfterEstimatedTokens = envelope.Budget.EstimatedTokens
	result.Boundary = latestCompactBoundary(envelope.CompactBoundaries)
	result.GeneratedAt = envelope.GeneratedAt
	return result, nil
}

func (r *Repository) CompactBoundaries(ctx context.Context, sessionID string, limit int) ([]ContextCompactBoundary, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id, summary, token_budget, token_estimate, source_start_id, source_end_id, source_item_count, created_at
		FROM (
			SELECT task_id, summary, token_budget, token_estimate, source_start_id, source_end_id, source_item_count, created_at
			FROM compact_boundaries
			WHERE session_id = ?
			ORDER BY created_at DESC, id DESC
			LIMIT ?
		)
		ORDER BY created_at ASC
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var boundaries []ContextCompactBoundary
	for rows.Next() {
		boundary, err := scanCompactBoundary(rows)
		if err != nil {
			return nil, err
		}
		boundaries = append(boundaries, boundary)
	}
	return boundaries, rows.Err()
}

func (r *Repository) insertCompactBoundaryTx(ctx context.Context, tx *sql.Tx, item TimelineItem, payload EventPayload) error {
	if item.Kind != "compact_boundary" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO compact_boundaries (
			id, session_id, task_id, summary, token_budget, token_estimate,
			source_start_id, source_end_id, source_item_count, schema_version, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			session_id = excluded.session_id,
			task_id = excluded.task_id,
			summary = excluded.summary,
			token_budget = excluded.token_budget,
			token_estimate = excluded.token_estimate,
			source_start_id = excluded.source_start_id,
			source_end_id = excluded.source_end_id,
			source_item_count = excluded.source_item_count,
			schema_version = excluded.schema_version,
			created_at = excluded.created_at
	`, item.ID, item.SessionID, item.TaskID, item.Content, payload.TokenBudget, payload.TokenEstimate, payload.SourceStartID, payload.SourceEndID, payload.SourceItemCount, compactBoundarySchemaVersion, formatTime(item.CreatedAt))
	return err
}

func scanCompactBoundary(scanner interface{ Scan(...any) error }) (ContextCompactBoundary, error) {
	var boundary ContextCompactBoundary
	var createdAt string
	if err := scanner.Scan(&boundary.TaskID, &boundary.Summary, &boundary.TokenBudget, &boundary.TokenEstimate, &boundary.SourceStartID, &boundary.SourceEndID, &boundary.SourceItemCount, &createdAt); err != nil {
		return ContextCompactBoundary{}, err
	}
	boundary.CreatedAt = parseTime(createdAt)
	return boundary, nil
}

func hasContextSinceLatestCompact(items []TimelineItem) bool {
	for i := len(items) - 1; i >= 0; i-- {
		switch items[i].Kind {
		case "compact_boundary":
			return false
		case "message", "transcript", "tool_call", "tool_result", "todo", "diff", "approval", "artifact", "hook", "schedule", "subagent", "user_input":
			return true
		}
	}
	return false
}

func compactSourceRange(items []TimelineItem) (string, string) {
	var startID, endID string
	for _, item := range items {
		if item.Kind == "compact_boundary" {
			continue
		}
		if startID == "" {
			startID = item.ID
		}
		endID = item.ID
	}
	return startID, endID
}

func compactBoundarySummary(mode CompactMode, reason string, items int, estimated int, budget int) string {
	label := "Manual compact"
	if mode == CompactModeAuto {
		label = "Auto compact"
	}
	var builder strings.Builder
	builder.WriteString(label)
	builder.WriteString(fmt.Sprintf(": summarized %d context items; estimated_tokens=%d; token_budget=%d", items, estimated, budget))
	if strings.TrimSpace(reason) != "" {
		builder.WriteString("; reason=")
		builder.WriteString(strings.TrimSpace(reason))
	}
	return builder.String()
}

func latestCompactBoundary(boundaries []ContextCompactBoundary) *ContextCompactBoundary {
	if len(boundaries) == 0 {
		return nil
	}
	latest := boundaries[len(boundaries)-1]
	return &latest
}
