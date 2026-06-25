package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/apply"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

type fakeGenerator struct {
	response string
}

func (f *fakeGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	return f.response, nil
}

func TestServerCreatesTaskAndServesEvents(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(NewServer(Config{
		Repository: repo,
		Runner:     taskpkg.NewRunner(repo, llm.NewPlanner(&fakeGenerator{response: "write notes.txt hello\nread notes.txt"})),
	}))
	defer server.Close()

	body := strings.NewReader(`{"workspace":` + quote(workspace) + `,"prompt":"创建 notes","natural":true}`)
	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected create status %d", resp.StatusCode)
	}
	var created taskpkg.CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Task.ID == "" || created.Task.Status != taskpkg.StatusCompleted {
		t.Fatalf("unexpected created task %#v", created.Task)
	}

	resp, err = http.Get(server.URL + "/v1/tasks/" + created.Task.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var events []taskpkg.Event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if !hasEvent(events, taskpkg.EventCompleted) {
		t.Fatalf("expected completed event, got %#v", events)
	}

	resp, err = http.Get(server.URL + "/v1/tasks/" + created.Task.ID + "/events/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sse strings.Builder
	if _, err := io.Copy(&sse, resp.Body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sse.String(), "event: task.completed") {
		t.Fatalf("expected completed SSE event, got:\n%s", sse.String())
	}
}

func TestServerServesDiffAndAppliesPatch(t *testing.T) {
	workspace := t.TempDir()
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		Prompt:    "patch notes",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	patch, err := apply.CreatePatch(workspace, []apply.FileChange{{Path: "notes.txt", Before: "", After: "hello\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventDiff, taskpkg.EventPayload{Diff: patch}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()
	resp, err := http.Get(server.URL + "/v1/tasks/" + task.ID + "/diff")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "+++ b/notes.txt") {
		t.Fatalf("unexpected diff response %s", string(data))
	}

	resp, err = http.Post(server.URL+"/v1/tasks/"+task.ID+"/apply", "application/json", strings.NewReader(`{"patch":`+quote(patch)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected apply status %d", resp.StatusCode)
	}
	applied, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(applied), "notes.txt") {
		t.Fatalf("unexpected apply response %s", string(applied))
	}
}

func TestEventStreamWaitsForNewEventsUntilTaskCompletes(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "slow task",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	done := make(chan string, 1)
	go func() {
		resp, err := http.Get(server.URL + "/v1/tasks/" + task.ID + "/events/stream")
		if err != nil {
			done <- err.Error()
			return
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			done <- err.Error()
			return
		}
		done <- string(data)
	}()

	time.Sleep(100 * time.Millisecond)
	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventSummary, taskpkg.EventPayload{Message: "later"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(t.Context(), task.ID, taskpkg.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), task.ID, taskpkg.EventCompleted, taskpkg.EventPayload{Status: string(taskpkg.StatusCompleted)}); err != nil {
		t.Fatal(err)
	}

	select {
	case body := <-done:
		if !strings.Contains(body, "event: task.summary") || !strings.Contains(body, "later") || !strings.Contains(body, "event: task.completed") {
			t.Fatalf("stream did not include later events:\n%s", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish after task completion")
	}
}

func TestServerCancelsTask(t *testing.T) {
	db, err := store.New(t.TempDir()).OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	task, err := repo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: t.TempDir(),
		Prompt:    "long task",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Repository: repo}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/tasks/"+task.ID+"/cancel", "application/json", strings.NewReader(`{"reason":"user clicked stop"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected cancel status %d", resp.StatusCode)
	}
	var cancelled taskpkg.Task
	if err := json.NewDecoder(resp.Body).Decode(&cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != taskpkg.StatusCancelled {
		t.Fatalf("unexpected cancelled task %#v", cancelled)
	}
	stream, err := http.Get(server.URL + "/v1/tasks/" + task.ID + "/events/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	data, err := io.ReadAll(stream.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "event: task.cancelled") || !strings.Contains(string(data), "user clicked stop") {
		t.Fatalf("unexpected cancel stream:\n%s", string(data))
	}
}

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func hasEvent(events []taskpkg.Event, eventType taskpkg.EventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
