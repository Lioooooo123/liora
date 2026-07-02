package task

import (
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/store"
)

func TestRunnerTaskPromptIncludesPriorSessionContext_whenContinuingSession(t *testing.T) {
	// Given
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	first, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "看一下 README",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), first.ID, EventSummary, EventPayload{Message: "README 里写着 Liora 是本地 coding agent。"}); err != nil {
		t.Fatal(err)
	}
	second, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		SessionID: first.SessionID,
		Prompt:    "好吧",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(repo, nil)

	// When
	prompt, err := runner.taskPrompt(t.Context(), second)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Session context",
		"user: 看一下 README",
		"assistant: README 里写着 Liora 是本地 coding agent。",
		"Current user request:\n好吧",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected task prompt to contain %q, got:\n%s", want, prompt)
		}
	}
	if strings.Count(prompt, "好吧") != 1 {
		t.Fatalf("current request should appear once, got:\n%s", prompt)
	}
}
