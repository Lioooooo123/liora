package tui

import (
	"context"
	"strings"
)

const maxCompletionItems = 12

type Completion struct {
	Value       string
	Label       string
	Description string
	Kind        string
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
	return builtinCommandCompletions(line), nil
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
			if strings.TrimSpace(value) == "" || !completionMatches(line, item, value) {
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

func completionMatches(line string, item Completion, value string) bool {
	if strings.HasPrefix(value, line) {
		return true
	}
	if !strings.HasPrefix(line, "/") {
		return false
	}
	query := strings.TrimPrefix(line, "/")
	if strings.HasPrefix(query, "skill ") {
		query = strings.TrimSpace(strings.TrimPrefix(query, "skill "))
	}
	if query == "" {
		return strings.HasPrefix(value, "/")
	}
	for _, candidate := range completionSearchTerms(item, value) {
		if strings.HasPrefix(candidate, query) {
			return true
		}
	}
	return false
}

func completionSearchTerms(item Completion, value string) []string {
	terms := []string{
		strings.TrimPrefix(strings.TrimSpace(completionLabel(item)), "/"),
		strings.TrimPrefix(strings.TrimSpace(value), "/"),
	}
	if skillName, ok := strings.CutPrefix(strings.TrimSpace(value), "/skill "); ok {
		terms = append(terms, strings.TrimSpace(skillName))
	}
	return terms
}
