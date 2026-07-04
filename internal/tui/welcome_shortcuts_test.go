package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderWelcomeShowsSessionAndContextShortcuts(t *testing.T) {
	// Given
	output := RenderWelcome(Config{
		Workspace: "/tmp/project",
		Model:     "deepseek-v4-pro",
	})

	// Then
	for _, want := range []string{"/sessions", "/context", "/status", "/resume-latest", "/new-session"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected welcome output to expose %q, got:\n%s", want, output)
		}
	}
}

func TestRenderWelcomeKeepsShortcutLinesNarrow(t *testing.T) {
	// Given
	output := RenderWelcome(Config{
		Workspace: "/tmp/project",
		Model:     "deepseek-v4-pro",
		Core:      "embedded daemon",
		Safety:    "patch-first",
	})

	// Then
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "/") && !strings.Contains(line, "Patch-first") {
			continue
		}
		if lipgloss.Width(line) > 42 {
			t.Fatalf("expected welcome shortcut line to fit 42 columns, got width %d:\n%s", lipgloss.Width(line), line)
		}
	}
}
