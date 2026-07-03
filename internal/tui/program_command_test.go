package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestBuiltinCommandCompletionsFindImplementedCommands_whenTypingPrefixes(t *testing.T) {
	// Given
	provider := builtinCompletionProvider{}
	tests := []struct {
		name  string
		line  string
		wants []string
	}{
		{"prompt context", "/prompt", []string{"/prompt-context"}},
		{"compact", "/comp", []string{"/compact"}},
		{"threads", "/thread", []string{"/threads", "/thread-new", "/thread-send", "/thread-inbox"}},
		{"permissions", "/perm", []string{"/permissions", "/permission-rule"}},
		{"continue alias", "/cont", []string{"/continue"}},
		{"last task", "/last", []string{"/last"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			items, err := provider.Completions(context.Background(), tt.line)
			if err != nil {
				t.Fatal(err)
			}
			got := mergeCompletions(tt.line, items)

			// Then
			for _, want := range tt.wants {
				if !hasCompletionCommand(got, want) {
					t.Fatalf("expected completion %q for %q, got %#v", want, tt.line, got)
				}
			}
		})
	}
}

func TestHelpTextIncludesImplementedCommands_whenRendered(t *testing.T) {
	// Given
	rendered := helpText()

	// Then
	for _, want := range []string{"/compact", "/prompt-context", "/threads", "/thread-new", "/permission-rule", "/continue", "/last", "/model"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected help to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestProgramCtrlCCancelsRunningTask_whenTaskIsRunning(t *testing.T) {
	// Given
	seen := ""
	handler := CommandHandlerFunc(func(_ context.Context, line string) (string, bool, error) {
		seen = line
		return "Cancelled task.", true, nil
	})
	m := newModel(context.Background(), Config{Commands: handler}, fakeStreamingSubmitter{})
	m.running = true

	// When
	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))

	// Then
	if cmd == nil {
		t.Fatal("expected ctrl+c to run /cancel while a task is running")
	}
	if updated.(*model).quitting {
		t.Fatal("ctrl+c should not quit while a cancellable task is running")
	}
	msg := cmd()
	result, ok := msg.(commandResultMsg)
	if !ok {
		t.Fatalf("expected commandResultMsg, got %T", msg)
	}
	if seen != "/cancel" || !result.handled {
		t.Fatalf("expected ctrl+c to submit /cancel, seen=%q result=%#v", seen, result)
	}
}

func TestProgramEscapeCancelsRunningTask_whenNoCompletionPalette(t *testing.T) {
	// Given
	seen := ""
	handler := CommandHandlerFunc(func(_ context.Context, line string) (string, bool, error) {
		seen = line
		return "Cancelled task.", true, nil
	})
	m := newModel(context.Background(), Config{Commands: handler}, fakeStreamingSubmitter{})
	m.running = true

	// When
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	// Then
	if cmd == nil {
		t.Fatal("expected esc to run /cancel while a task is running")
	}
	msg := cmd()
	result, ok := msg.(commandResultMsg)
	if !ok {
		t.Fatalf("expected commandResultMsg, got %T", msg)
	}
	if seen != "/cancel" || !result.handled {
		t.Fatalf("expected esc to submit /cancel, seen=%q result=%#v", seen, result)
	}
}

func hasCompletionCommand(items []Completion, value string) bool {
	for _, item := range items {
		if commandValueName(item.Value) == value {
			return true
		}
	}
	return false
}
