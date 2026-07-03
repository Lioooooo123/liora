package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderSectionWrapsLongTokens_whenWidthIsConstrained(t *testing.T) {
	// Given
	var out strings.Builder
	width := 32
	body := "error: " + strings.Repeat("x", 80)

	// When
	renderSectionWithWidth(&out, "Error", body, width)

	// Then
	assertOutputFitsWidth(t, out.String(), width)
}

func TestRenderSectionWrapsMarkdownCode_whenWidthIsConstrained(t *testing.T) {
	// Given
	var out strings.Builder
	width := 34
	body := "## Result\n\n```text\n" + strings.Repeat("abcdef", 12) + "\n```"

	// When
	renderSectionWithWidth(&out, "Assistant", body, width)

	// Then
	assertOutputFitsWidth(t, out.String(), width)
}

func assertOutputFitsWidth(t *testing.T, output string, width int) {
	t.Helper()
	plain := terminalPlainText(output)
	for _, line := range strings.Split(plain, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("expected line width <= %d, got %d for %q\n%s", width, got, line, plain)
		}
	}
}
