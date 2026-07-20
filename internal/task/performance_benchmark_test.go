package task

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/store"
)

func TestLatestUserInputSurvivesDenseEventLog(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	task, err := repo.Create(t.Context(), CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "dense event input lookup",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), task.ID, EventUserInputReceived, EventPayload{Message: "notes.txt"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1100; i++ {
		if err := repo.AppendEvent(t.Context(), task.ID, EventAssistantDelta, EventPayload{Message: "delta"}); err != nil {
			t.Fatal(err)
		}
	}

	answer, ok, err := repo.LatestUserInput(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || answer != "notes.txt" {
		t.Fatalf("unexpected latest input ok=%v answer=%q", ok, answer)
	}
}

func BenchmarkRepositoryLatestUserInputDenseEventLog(b *testing.B) {
	db, err := store.New(b.TempDir()).OpenDB()
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	repo := NewRepository(db)
	task, err := repo.Create(b.Context(), CreateRequest{
		Workspace: b.TempDir(),
		Prompt:    "benchmark input lookup",
		Natural:   true,
	})
	if err != nil {
		b.Fatal(err)
	}

	tx, err := db.BeginTx(b.Context(), nil)
	if err != nil {
		b.Fatal(err)
	}
	insert := func(id string, eventType EventType, payload string) {
		b.Helper()
		if _, err := tx.ExecContext(b.Context(), `
			INSERT INTO task_events (id, task_id, type, payload_json, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, id, task.ID, string(eventType), payload, formatTime(time.Now().UTC())); err != nil {
			b.Fatal(err)
		}
	}
	insert("benchmark-input", EventUserInputReceived, `{"message":"notes.txt","status":"draft"}`)
	deltaPayload := `{"message":"` + strings.Repeat("x", 8*1024) + `"}`
	for i := 0; i < 999; i++ {
		insert(fmt.Sprintf("benchmark-delta-%04d", i), EventAssistantDelta, deltaPayload)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		answer, ok, err := repo.LatestUserInput(b.Context(), task.ID)
		if err != nil {
			b.Fatal(err)
		}
		if !ok || answer != "notes.txt" {
			b.Fatalf("unexpected latest input ok=%v answer=%q", ok, answer)
		}
	}
}
