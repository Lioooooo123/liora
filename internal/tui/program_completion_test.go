package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

type fakeCompletionProvider struct {
	items []Completion
}

func (f fakeCompletionProvider) Completions(_ context.Context, _ string) ([]Completion, error) {
	return f.items, nil
}

func TestProgramShowsSkillCompletions_whenTypingSkillSlashCommand(t *testing.T) {
	// Given
	m := newModel(context.Background(), Config{
		Workspace: "/tmp/project",
		Completions: fakeCompletionProvider{items: []Completion{
			{Value: "/skill review", Label: "/skill review", Description: "Review code changes"},
			{Value: "/skill tests", Label: "/skill tests", Description: "Generate tests"},
		}},
	}, fakeStreamingSubmitter{})
	m.resize(96, 20)
	m.input.SetValue("/skill re")

	// When
	m.refreshCompletions()
	panel := m.inputPanelView()

	// Then
	for _, want := range []string{"/skill review", "Review code changes"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("expected skill completion panel to contain %q, got:\n%s", want, panel)
		}
	}
	if strings.Contains(panel, "/skill tests") {
		t.Fatalf("completion panel should filter by typed skill prefix, got:\n%s", panel)
	}
}

func TestProgramSlashPaletteShowsSkillCompletions_whenTypingSlash(t *testing.T) {
	// Given
	m := newModel(context.Background(), Config{
		Workspace: "/tmp/project",
		Completions: fakeCompletionProvider{items: []Completion{
			{Value: "/skill review", Label: "review", Description: "Review code changes"},
			{Value: "/skill tests", Label: "tests", Description: "Generate tests"},
		}},
	}, fakeStreamingSubmitter{})
	m.resize(96, 20)
	m.input.SetValue("/")

	// When
	m.refreshCompletions()
	panel := m.inputPanelView()

	// Then
	for _, want := range []string{"command layer", "Skills", "/help", "review", "Review code changes"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("expected slash palette to contain %q, got:\n%s", want, panel)
		}
	}
}

func TestProgramSlashPaletteFitsNarrowWidth_whenTypingSlash(t *testing.T) {
	// Given
	m := newModel(context.Background(), Config{
		Workspace: "/tmp/project",
		Completions: fakeCompletionProvider{items: []Completion{
			{Value: "/skill review", Label: "review", Description: "Review Chinese markdown and terminal wrapping"},
		}},
	}, fakeStreamingSubmitter{})
	m.resize(42, 18)
	m.input.SetValue("/")

	// When
	m.refreshCompletions()
	panel := m.inputPanelView()

	// Then
	if !strings.Contains(panel, "command layer") {
		t.Fatalf("expected slash palette to label the command layer, got:\n%s", panel)
	}
	assertVisibleLinesWithinWidth(t, panel, 42)
}

func TestProgramTabAppliesSkillCompletion_whenSlashPrefixMatchesSkillName(t *testing.T) {
	// Given
	m := newModel(context.Background(), Config{
		Workspace: "/tmp/project",
		Completions: fakeCompletionProvider{items: []Completion{
			{Value: "/skill review", Label: "review", Description: "Review code changes"},
			{Value: "/skill tests", Label: "tests", Description: "Generate tests"},
		}},
	}, fakeStreamingSubmitter{})
	m.resize(96, 20)
	m.input.SetValue("/re")
	m.refreshCompletions()

	// When
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := updated.(*model).input.Value()

	// Then
	if got != "/skill review" {
		t.Fatalf("expected /re tab to complete to /skill review, got %q", got)
	}
}

func TestProgramShowsAllSkillCompletions_whenSkillCommandHasTrailingSpace(t *testing.T) {
	// Given
	m := newModel(context.Background(), Config{
		Workspace: "/tmp/project",
		Completions: fakeCompletionProvider{items: []Completion{
			{Value: "/skill review", Label: "/skill review", Description: "Review code changes"},
			{Value: "/skill tests", Label: "/skill tests", Description: "Generate tests"},
		}},
	}, fakeStreamingSubmitter{})
	m.resize(96, 20)
	m.input.SetValue("/skill ")

	// When
	m.refreshCompletions()
	panel := m.inputPanelView()

	// Then
	for _, want := range []string{"/skill review", "/skill tests"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("expected trailing-space skill completion panel to contain %q, got:\n%s", want, panel)
		}
	}
}

func TestProgramTabAppliesSkillCompletion_whenSingleSkillMatches(t *testing.T) {
	// Given
	m := newModel(context.Background(), Config{
		Workspace: "/tmp/project",
		Completions: fakeCompletionProvider{items: []Completion{
			{Value: "/skill review", Label: "/skill review", Description: "Review code changes"},
		}},
	}, fakeStreamingSubmitter{})
	m.resize(96, 20)
	m.input.SetValue("/skill re")
	m.refreshCompletions()

	// When
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := updated.(*model).input.Value()

	// Then
	if got != "/skill review" {
		t.Fatalf("expected tab to complete skill command, got %q", got)
	}
}
