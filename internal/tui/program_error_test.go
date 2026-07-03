package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProgramSkipsDuplicateError_whenTaskErrorAlreadyStreamed(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(80, 20)
	model.submitLine("hi")
	errMessage := "Post http://127.0.0.1:9/chat/completions: connection refused"

	// When
	_, _ = model.Update(streamUpdateMsg{update: streamUpdate("task.error", eventPayload{Message: errMessage})})
	_, _ = model.Update(turnDoneMsg{err: errors.New(errMessage)})

	// Then
	view := model.View()
	if got := strings.Count(view.Content, errMessage); got != 1 {
		t.Fatalf("expected streamed task error to render once, got %d occurrences:\n%s", got, view.Content)
	}
}

func TestProgramShowsTurnDoneError_whenNoTaskErrorWasStreamed(t *testing.T) {
	// Given
	model := newModel(context.Background(), Config{Workspace: "/tmp/project"}, fakeStreamingSubmitter{})
	model.resize(80, 20)
	model.submitLine("hi")

	// When
	_, _ = model.Update(turnDoneMsg{err: errors.New("connection refused")})

	// Then
	view := model.View()
	for _, want := range []string{"Error", "connection refused"} {
		if !strings.Contains(view.Content, want) {
			t.Fatalf("expected turn completion error to contain %q, got:\n%s", want, view.Content)
		}
	}
}
