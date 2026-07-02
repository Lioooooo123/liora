package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Lioooooo123/liora/internal/capabilities"
	"github.com/Lioooooo123/liora/internal/llm"
)

const (
	maxModelToolOutputChars = 50_000
	toolOutputPreviewChars  = 2_000
)

func (l *ToolLoop) currentDiff() string {
	diff, err := l.agent.workspace.GitDiff()
	if err != nil {
		return ""
	}
	return diff
}

func loopToolSchemas() []llm.ToolSchema {
	specs := capabilities.ToolSchemas()
	schemas := make([]llm.ToolSchema, 0, len(specs))
	for _, spec := range specs {
		schemas = append(schemas, llm.ToolSchema{
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  spec.InputSchema,
		})
	}
	return schemas
}

func isReadOnlyTool(name string) bool {
	for _, spec := range capabilities.BuiltinTools() {
		if spec.Name == name {
			return spec.Kind == capabilities.ToolReadOnly
		}
	}
	return false
}

func renderToolCalls(calls []llm.ToolCall) string {
	lines := make([]string, 0, len(calls))
	for _, call := range calls {
		line := call.Name
		if input := toolInput(call); input != "" {
			line += " " + input
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func toolInput(call llm.ToolCall) string {
	args, err := parseToolArgs(call.Arguments)
	if err != nil {
		return strings.TrimSpace(call.Arguments)
	}
	switch call.Name {
	case "run":
		return argString(args, "command")
	case "mcp":
		return strings.TrimSpace(argString(args, "server") + " " + argString(args, "tool"))
	case "search":
		return argString(args, "query")
	case "skill":
		return argString(args, "name")
	case "glob":
		return strings.TrimSpace(argString(args, "pattern") + " " + argString(args, "path"))
	case "diff":
		return ""
	case "todo_write":
		return argTodoSummary(args)
	case "todo_read":
		return ""
	case "Task":
		return strings.TrimSpace(argString(args, "subagent_name") + " " + argString(args, "prompt"))
	case "TaskOutput":
		return argString(args, "task_id")
	case "TaskStop":
		return strings.TrimSpace(argString(args, "task_id") + " " + argString(args, "reason"))
	case "write", "append":
		return strings.TrimSpace(argString(args, "path") + " " + argString(args, "content"))
	case "edit":
		input := strings.TrimSpace(argString(args, "path") + " " + argString(args, "old_text") + " " + argString(args, "new_text"))
		if argBool(args, "all") {
			input += " all"
		}
		return strings.TrimSpace(input)
	case "replace":
		return strings.TrimSpace(argString(args, "path") + " " + argString(args, "old_text") + " " + argString(args, "new_text"))
	default:
		return argString(args, "path")
	}
}

func argTodoSummary(args map[string]any) string {
	todos, err := parseTodoItems(args)
	if err != nil || len(todos) == 0 {
		return ""
	}
	return todos[0].Content
}

func completionSummaryForLoop(content string, executed int) string {
	if trimmed := strings.TrimSpace(content); trimmed != "" {
		return trimmed
	}
	return completionSummary(executed)
}

func parseToolArgs(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

func argString(args map[string]any, key string) string {
	if value, ok := args[key]; ok {
		if text, ok := value.(string); ok {
			return text
		}
	}
	return ""
}

func argStringOr(args map[string]any, key string, fallback string) string {
	if value := argString(args, key); value != "" {
		return value
	}
	return fallback
}

func argInt(args map[string]any, key string, fallback int) int {
	value, ok := args[key]
	if !ok {
		return fallback
	}
	switch number := value.(type) {
	case float64:
		return int(number)
	case int:
		return number
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(number)); err == nil {
			return parsed
		}
	}
	return fallback
}

func argBool(args map[string]any, key string) bool {
	if value, ok := args[key]; ok {
		if flag, ok := value.(bool); ok {
			return flag
		}
	}
	return false
}

func (l *ToolLoop) budgetToolOutput(ctx context.Context, call llm.ToolCall, output string) string {
	if len(output) <= maxModelToolOutputChars || strings.Contains(output, "[...truncated]") {
		return output
	}
	outputPath, err := l.persistToolOutput(ctx, call, output)
	if err != nil {
		return output[:maxModelToolOutputChars] + "\n[...truncated]\n"
	}
	return renderPersistedToolOutput(call, output, outputPath)
}

func (l *ToolLoop) persistToolOutput(ctx context.Context, call llm.ToolCall, output string) (string, error) {
	if l.agent.outputs != nil {
		return l.agent.outputs.PersistToolOutput(ctx, call, output)
	}
	return workspaceToolOutputSink{root: l.agent.workspace.Root()}.PersistToolOutput(ctx, call, output)
}

type workspaceToolOutputSink struct {
	root string
}

func (s workspaceToolOutputSink) PersistToolOutput(_ context.Context, call llm.ToolCall, output string) (string, error) {
	relDir := filepath.ToSlash(filepath.Join(".liora", "tool-results"))
	absDir := filepath.Join(s.root, relDir)
	if err := os.MkdirAll(absDir, 0o700); err != nil {
		return "", err
	}
	relPath := filepath.ToSlash(filepath.Join(relDir, safeToolOutputFileStem(call)+"-"+randomHex(8)+".txt"))
	absPath := filepath.Join(s.root, filepath.FromSlash(relPath))
	if err := os.WriteFile(absPath, []byte(output), 0o600); err != nil {
		return "", err
	}
	return relPath, nil
}

func renderPersistedToolOutput(call llm.ToolCall, output string, outputPath string) string {
	lines := []string{
		fmt.Sprintf("Tool output exceeded %d characters; showing a preview only.", maxModelToolOutputChars),
		"tool_name: " + call.Name,
		"tool_call_id: " + call.ID,
		fmt.Sprintf("output_size_chars: %d", len(output)),
		fmt.Sprintf("output_size_bytes: %d", len([]byte(output))),
		"output_path: " + outputPath,
		"next_step: Use read with output_path to page through the full output.",
		"",
		"[preview]",
		output[:toolOutputPreviewChars],
	}
	return strings.Join(lines, "\n")
}

func safeToolOutputFileStem(call llm.ToolCall) string {
	label := call.Name + "-" + call.ID
	var builder strings.Builder
	lastWasUnderscore := false
	for _, r := range label {
		if builder.Len() >= 80 {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			builder.WriteRune(r)
			lastWasUnderscore = false
			continue
		}
		if !lastWasUnderscore {
			builder.WriteByte('_')
			lastWasUnderscore = true
		}
	}
	stem := strings.Trim(builder.String(), "_")
	if stem == "" {
		return "tool-result"
	}
	return stem
}

func randomHex(bytesCount int) string {
	data := make([]byte, bytesCount)
	if _, err := rand.Read(data); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(data)
}
