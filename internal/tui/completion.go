package tui

import (
	"context"
	"strings"
)

const maxCompletionItems = 8

type Completion struct {
	Value       string
	Label       string
	Description string
}

type CompletionProvider interface {
	Completions(ctx context.Context, line string) ([]Completion, error)
}

type CompletionProviderFunc func(ctx context.Context, line string) ([]Completion, error)

func (f CompletionProviderFunc) Completions(ctx context.Context, line string) ([]Completion, error) {
	return f(ctx, line)
}

type builtinCompletionProvider struct{}

func (builtinCompletionProvider) Completions(_ context.Context, line string) ([]Completion, error) {
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "/") || strings.Contains(line, " ") {
		return nil, nil
	}
	return []Completion{
		{Value: "/help", Label: "/help", Description: "show commands"},
		{Value: "/diff", Label: "/diff", Description: "review current patch"},
		{Value: "/apply", Label: "/apply", Description: "apply current patch"},
		{Value: "/skills", Label: "/skills", Description: "list installed skills"},
		{Value: "/skill ", Label: "/skill <name>", Description: "read an installed skill"},
		{Value: "/mcp", Label: "/mcp", Description: "list MCP tools"},
		{Value: "/memory", Label: "/memory", Description: "manage memory"},
		{Value: "/exit", Label: "/exit", Description: "quit"},
	}, nil
}

func completionLabel(item Completion) string {
	if strings.TrimSpace(item.Label) != "" {
		return strings.TrimSpace(item.Label)
	}
	return strings.TrimSpace(item.Value)
}

func completionDescription(item Completion) string {
	return strings.TrimSpace(item.Description)
}

func mergeCompletions(line string, groups ...[]Completion) []Completion {
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "/") {
		return nil
	}
	seen := map[string]struct{}{}
	var merged []Completion
	for _, group := range groups {
		for _, item := range group {
			value := strings.TrimRight(item.Value, "\r\n")
			if strings.TrimSpace(value) == "" || !strings.HasPrefix(value, line) {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			item.Value = value
			if strings.TrimSpace(item.Label) == "" {
				item.Label = value
			}
			seen[value] = struct{}{}
			merged = append(merged, item)
			if len(merged) >= maxCompletionItems {
				return merged
			}
		}
	}
	return merged
}
