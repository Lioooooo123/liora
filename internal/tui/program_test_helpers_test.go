package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func assertVisibleLinesWithinWidth(t *testing.T, content string, width int) {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("expected line width <= %d, got %d for %q\n%s", width, got, line, content)
		}
	}
}
