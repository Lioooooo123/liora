package tui

import (
	"context"
	"strings"
	"testing"
)

func TestFullScreenChromeSessionContextHintsStayCompactOnNarrowTerminals(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{
		Workspace: "/tmp/project-with-a-long-directory-name",
		Model:     "deepseek-v4-pro",
		Core:      "embedded daemon",
		Safety:    "patch-first",
	}, fakeStreamingSubmitter{})
	model.resize(42, 18)

	// When
	view := model.View()

	// Then
	for _, want := range []string{"session", "/sessions", "/resume-latest", "/new-session", "context", "/context", "status", "/status"} {
		if !strings.Contains(view.Content, want) {
			t.Fatalf("expected narrow workbench to contain compact hint %q, got:\n%s", want, view.Content)
		}
	}
	assertVisibleLinesWithinWidth(t, view.Content, 42)
}
