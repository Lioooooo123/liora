package task

import (
	"context"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
)

type fakeGenerator struct {
	response string
}

func (f *fakeGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	return f.response, nil
}

func TestRunnerExecutesTaskAndPersistsEvents(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "创建 notes",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "write notes.txt hello\nread notes.txt"}))
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("unexpected task status %#v", got)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var eventTypes []EventType
	var combinedPayload strings.Builder
	for _, event := range events {
		eventTypes = append(eventTypes, event.Type)
		combinedPayload.WriteString(event.Payload)
		combinedPayload.WriteByte('\n')
	}
	for _, want := range []EventType{EventPlanning, EventPlanReady, EventToolCall, EventToolResult, EventSummary, EventCompleted} {
		if !containsEventType(eventTypes, want) {
			t.Fatalf("expected event %s in %#v", want, eventTypes)
		}
	}
	if !strings.Contains(combinedPayload.String(), "notes.txt") || !strings.Contains(combinedPayload.String(), "completed 2 steps") {
		t.Fatalf("unexpected event payloads:\n%s", combinedPayload.String())
	}
}

func TestRunnerExecutesScriptTaskWithoutPlanner(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: workspace,
		Prompt:    "write smoke.txt ok\nread smoke.txt",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: ""}))
	if err := runner.Run(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("unexpected task status %#v", got)
	}
	events, err := repo.Events(t.Context(), task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !containsEventType(eventTypes(events), EventToolResult) {
		t.Fatalf("expected tool result event, got %#v", events)
	}
}

func containsEventType(events []EventType, want EventType) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func eventTypes(events []Event) []EventType {
	types := make([]EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}
