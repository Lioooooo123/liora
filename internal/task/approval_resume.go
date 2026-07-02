package task

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/permission"
)

func (r *Runner) completedToolLookup(taskID string) agent.CompletedToolLookup {
	return func(ctx context.Context, toolCallID string) (agent.CompletedToolResult, bool, error) {
		payload, ok, err := r.repo.CompletedToolResult(ctx, taskID, toolCallID)
		if err != nil || !ok {
			return agent.CompletedToolResult{}, ok, err
		}
		return agent.CompletedToolResult{Output: payload.Output}, true, nil
	}
}

func (r *Repository) HasApprovedApproval(ctx context.Context, taskID string, request permission.Request) (bool, error) {
	taskID = strings.TrimSpace(taskID)
	toolCallID := strings.TrimSpace(request.ToolCallID)
	if taskID == "" {
		return false, nil
	}
	if toolCallID != "" {
		return r.hasApprovedApprovalByCallID(ctx, taskID, toolCallID)
	}
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM approval_items
		WHERE task_id = ?
		  AND status = 'resolved'
		  AND decision = 'approved'
		  AND tool = ?
		  AND args_preview = ?
		  AND risk = ?
	`, taskID, strings.TrimSpace(request.Tool), previewText(request.Input), strings.TrimSpace(request.Risk)).Scan(&count)
	return count > 0, err
}

func (r *Repository) hasApprovedApprovalByCallID(ctx context.Context, taskID string, toolCallID string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM approval_items
		WHERE task_id = ?
		  AND status = 'resolved'
		  AND decision = 'approved'
		  AND tool_call_id = ?
	`, taskID, toolCallID).Scan(&count)
	return count > 0, err
}

func (r *Repository) CompletedToolResult(ctx context.Context, taskID string, toolCallID string) (EventPayload, bool, error) {
	taskID = strings.TrimSpace(taskID)
	toolCallID = strings.TrimSpace(toolCallID)
	if taskID == "" || toolCallID == "" {
		return EventPayload{}, false, nil
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT tool, tool_call_id, tool_result_id, input, output, status
		FROM transcript_entries
		WHERE task_id = ?
		  AND kind = 'tool_result'
		  AND status = 'ok'
		  AND tool_call_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, taskID, toolCallID)
	var payload EventPayload
	if err := row.Scan(&payload.Tool, &payload.ToolCallID, &payload.ToolResultID, &payload.Input, &payload.Output, &payload.Status); err != nil {
		if err == sql.ErrNoRows {
			return EventPayload{}, false, nil
		}
		return EventPayload{}, false, err
	}
	return payload, true, nil
}

func (r *Repository) CompletedToolResults(ctx context.Context, taskID string) ([]EventPayload, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT tool, tool_call_id, tool_result_id, input, output, status
		FROM transcript_entries
		WHERE task_id = ?
		  AND kind = 'tool_result'
		  AND status = 'ok'
		ORDER BY created_at ASC, id ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []EventPayload
	for rows.Next() {
		var payload EventPayload
		if err := rows.Scan(&payload.Tool, &payload.ToolCallID, &payload.ToolResultID, &payload.Input, &payload.Output, &payload.Status); err != nil {
			return nil, err
		}
		results = append(results, payload)
	}
	return results, rows.Err()
}

func completedToolSummary(results []EventPayload) string {
	if len(results) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Previously completed tool calls for this task. Do not repeat these tool calls; continue from the next unfinished action:\n")
	for _, result := range results {
		toolCallID := strings.TrimSpace(result.ToolCallID)
		if toolCallID == "" {
			toolCallID = "(unknown)"
		}
		fmt.Fprintf(&builder, "- %s %s input=%q status=%s\n", toolCallID, strings.TrimSpace(result.Tool), result.Input, strings.TrimSpace(result.Status))
	}
	return strings.TrimSpace(builder.String())
}
