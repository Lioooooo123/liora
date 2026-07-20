package daemon

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

func TestLatestWaitingRequestSurvivesDenseEventLogAndPreservesOrder(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "dense waiting request lookup",
		Natural:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventPermissionRequest, taskpkg.EventPayload{Message: "approve?"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1100; i++ {
		if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventAssistantDelta, taskpkg.EventPayload{Message: "delta"}); err != nil {
			t.Fatal(err)
		}
	}

	s := &server{repo: repo}
	eventType, payload, err := s.latestWaitingRequest(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if eventType != taskpkg.EventPermissionRequest || payload.Message != "approve?" {
		t.Fatalf("unexpected waiting request type=%q payload=%#v", eventType, payload)
	}

	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventUserInputRequest, taskpkg.EventPayload{Message: "Which file?"}); err != nil {
		t.Fatal(err)
	}
	eventType, payload, err = s.latestWaitingRequest(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if eventType != taskpkg.EventUserInputRequest || payload.Message != "Which file?" {
		t.Fatalf("unexpected newest waiting request type=%q payload=%#v", eventType, payload)
	}
}

func BenchmarkLatestWaitingRequestDenseEventLog(b *testing.B) {
	db, err := store.New(b.TempDir()).OpenDB()
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(b.Context(), taskpkg.CreateRequest{
		Workspace: b.TempDir(),
		Prompt:    "benchmark waiting request lookup",
		Natural:   true,
	})
	if err != nil {
		b.Fatal(err)
	}

	tx, err := db.BeginTx(b.Context(), nil)
	if err != nil {
		b.Fatal(err)
	}
	insert := func(id string, eventType taskpkg.EventType, payload string) {
		b.Helper()
		if _, err := tx.ExecContext(b.Context(), `
			INSERT INTO task_events (id, task_id, type, payload_json, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, id, task.ID, string(eventType), payload, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			b.Fatal(err)
		}
	}
	insert("benchmark-wait", taskpkg.EventUserInputRequest, `{"message":"Which file?","status":"waiting_user"}`)
	deltaPayload := `{"message":"` + strings.Repeat("x", 8*1024) + `"}`
	for i := 0; i < 999; i++ {
		insert(fmt.Sprintf("benchmark-delta-%04d", i), taskpkg.EventAssistantDelta, deltaPayload)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}

	s := &server{repo: repo}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eventType, payload, err := s.latestWaitingRequest(b.Context(), task.ID)
		if err != nil {
			b.Fatal(err)
		}
		if eventType != taskpkg.EventUserInputRequest || payload.Message != "Which file?" {
			b.Fatalf("unexpected waiting request type=%q payload=%#v", eventType, payload)
		}
	}
}
