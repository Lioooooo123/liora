package tuisession

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/daemon"
	"github.com/Lioooooo123/liora/internal/daemonclient"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

type nativeTodoGenerator struct {
	calls int
}

func (g *nativeTodoGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	return `todo_write {"todos":[{"id":"todo-plan","content":"Draft acceptance tests","status":"pending","priority":"high"}]}
todo_write {"todos":[{"id":"todo-plan","content":"Draft acceptance tests","status":"done","priority":"critical"}]}
todo_read`, nil
}

func (g *nativeTodoGenerator) SupportsTools() bool {
	return true
}

func (g *nativeTodoGenerator) GenerateWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (llm.Completion, error) {
	g.calls++
	switch g.calls {
	case 1:
		return llm.Completion{ToolCalls: []llm.ToolCall{{
			ID:        "call_todo_create",
			Name:      "todo_write",
			Arguments: `{"todos":[{"id":"todo-plan","content":"Draft acceptance tests","status":"pending","priority":"high"}]}`,
		}}}, nil
	case 2:
		return llm.Completion{ToolCalls: []llm.ToolCall{{
			ID:        "call_todo_update",
			Name:      "todo_write",
			Arguments: `{"todos":[{"id":"todo-plan","content":"Draft acceptance tests","status":"done","priority":"critical"}]}`,
		}}}, nil
	case 3:
		return llm.Completion{ToolCalls: []llm.ToolCall{{
			ID:        "call_todo_read",
			Name:      "todo_read",
			Arguments: `{}`,
		}}}, nil
	default:
		return llm.Completion{Content: "todo plan stored"}, nil
	}
}

func TestDaemonSubmitterPersistsTodoToolsAndShowsTodoAfterResume(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&nativeTodoGenerator{}))
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner, Store: persistentStore}))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	submitter := NewDaemonSubmitter(client, workspace, true, "", false)

	result, err := submitter.SubmitStream(t.Context(), "make a todo plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	taskID := findOnlyTaskID(t, repo)
	taskRecord, err := repo.Get(t.Context(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	todos, err := client.SessionTodos(t.Context(), taskRecord.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(todos) != 1 || todos[0].ID != "todo-plan" || todos[0].Status != "done" || todos[0].Priority != "critical" || todos[0].SourceTaskID != taskID || todos[0].UpdatedAt.IsZero() {
		t.Fatalf("unexpected daemon todos %#v", todos)
	}
	sawUpdatedAt := false
	for _, event := range result.Events {
		if event.Tool == "todo_read" && strings.Contains(event.Output, "updated_at") {
			sawUpdatedAt = true
		}
	}
	if !sawUpdatedAt {
		t.Fatalf("expected todo_read tool output to include updated_at")
	}

	resumed := NewDaemonSubmitter(client, workspace, true, "", false)
	output, handled, err := resumed.HandleCommand(t.Context(), "/todo")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Todos " + taskRecord.SessionID, "id=todo-plan", "status=done", "priority=critical", "source_task_id=" + taskID, "updated_at=", "content=Draft acceptance tests"} {
		if !handled || !strings.Contains(output, want) {
			t.Fatalf("expected /todo output to contain %q handled=%v output=%q", want, handled, output)
		}
	}
	t.Logf("/todo after resume:\n%s", output)
}

func TestDaemonSubmitterTodoCommandShowsEmptySessionState(t *testing.T) {
	workspace := t.TempDir()
	persistentStore := store.New(t.TempDir())
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Store: persistentStore}))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.CreateSession(t.Context(), taskpkg.CreateSessionRequest{Workspace: workspace, Title: "empty todos"})
	if err != nil {
		t.Fatal(err)
	}
	submitter := NewDaemonSubmitter(client, workspace, true, session.Session.ID, false)
	output, handled, err := submitter.HandleCommand(t.Context(), "/todo")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(output, "No todos found for session "+session.Session.ID) {
		t.Fatalf("expected empty todo output, handled=%v output=%q", handled, output)
	}
	t.Logf("/todo empty session:\n%s", output)

	usage, handled, err := submitter.HandleCommand(t.Context(), "/todo now")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(usage, "Usage: /todo") {
		t.Fatalf("expected /todo usage, handled=%v output=%q", handled, usage)
	}
	t.Logf("/todo usage:\n%s", usage)
}

func TestDaemonSubmitterTodoCommandSurvivesDaemonRestart(t *testing.T) {
	workspace := t.TempDir()
	storeRoot := t.TempDir()
	firstStore := store.New(storeRoot)
	firstDB, err := firstStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	firstRepo := taskpkg.NewRepository(firstDB)
	firstServer := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: firstRepo, Store: firstStore}))
	firstClient, err := daemonclient.New(firstServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	session, err := firstClient.CreateSession(t.Context(), taskpkg.CreateSessionRequest{Workspace: workspace, Title: "restart todos"})
	if err != nil {
		t.Fatal(err)
	}
	sourceTask, err := firstRepo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		SessionID: session.Session.ID,
		Prompt:    "seed restart todo",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstRepo.WriteTodos(t.Context(), taskpkg.TodoWriteRequest{
		SessionID:    session.Session.ID,
		SourceTaskID: sourceTask.ID,
		Todos: []taskpkg.TodoWriteItem{{
			ID:       "restart-plan",
			Content:  "Recover persisted todo after restart",
			Status:   taskpkg.TodoStatusInProgress,
			Priority: taskpkg.TodoPriorityHigh,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	firstServer.Close()
	if err := firstDB.Close(); err != nil {
		t.Fatal(err)
	}

	secondStore := store.New(storeRoot)
	secondDB, err := secondStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer secondDB.Close()
	secondRepo := taskpkg.NewRepository(secondDB)
	secondServer := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: secondRepo, Store: secondStore}))
	defer secondServer.Close()
	secondClient, err := daemonclient.New(secondServer.URL)
	if err != nil {
		t.Fatal(err)
	}

	restarted := NewDaemonSubmitter(secondClient, workspace, true, session.Session.ID, false)
	output, handled, err := restarted.HandleCommand(t.Context(), "/todo")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("/todo after daemon restart:\n%s", output)
	for _, want := range []string{"Todos " + session.Session.ID, "id=restart-plan", "status=in_progress", "priority=high", "source_task_id=" + sourceTask.ID, "updated_at=", "content=Recover persisted todo after restart"} {
		if !handled || !strings.Contains(output, want) {
			t.Fatalf("expected restarted /todo output to contain %q handled=%v output=%q", want, handled, output)
		}
	}

	emptySession, err := secondClient.CreateSession(t.Context(), taskpkg.CreateSessionRequest{Workspace: workspace, Title: "empty after restart"})
	if err != nil {
		t.Fatal(err)
	}
	emptySubmitter := NewDaemonSubmitter(secondClient, workspace, true, emptySession.Session.ID, false)
	emptyOutput, handled, err := emptySubmitter.HandleCommand(t.Context(), "/todo")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("/todo empty after daemon restart:\n%s", emptyOutput)
	if !handled || !strings.Contains(emptyOutput, "No todos found for session "+emptySession.Session.ID) || strings.Contains(emptyOutput, "restart-plan") {
		t.Fatalf("expected restarted empty session isolation, handled=%v output=%q", handled, emptyOutput)
	}
}

func TestDaemonSubmitterTranscriptAndTodoSurviveDaemonRestart(t *testing.T) {
	workspace := t.TempDir()
	storeRoot := t.TempDir()
	firstStore := store.New(storeRoot)
	firstDB, err := firstStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	firstRepo := taskpkg.NewRepository(firstDB)
	firstServer := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: firstRepo, Store: firstStore}))
	firstClient, err := daemonclient.New(firstServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	session, err := firstClient.CreateSession(t.Context(), taskpkg.CreateSessionRequest{Workspace: workspace, Title: "restart transcript"})
	if err != nil {
		t.Fatal(err)
	}
	sourceTask, err := firstRepo.Create(t.Context(), taskpkg.CreateRequest{
		Workspace: workspace,
		SessionID: session.Session.ID,
		Prompt:    "seed restart transcript",
		Natural:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstRepo.AppendMessage(t.Context(), session.Session.ID, "assistant", "assistant restart summary", sourceTask.ID); err != nil {
		t.Fatal(err)
	}
	for _, event := range []struct {
		eventType taskpkg.EventType
		payload   taskpkg.EventPayload
	}{
		{taskpkg.EventToolCall, taskpkg.EventPayload{Tool: "read", Input: "README.md"}},
		{taskpkg.EventToolResult, taskpkg.EventPayload{Tool: "read", Input: "README.md", Output: "read output after restart", Status: "ok"}},
		{taskpkg.EventDiff, taskpkg.EventPayload{Diff: "diff --git a/README.md b/README.md\n+after restart"}},
		{taskpkg.EventPermissionRequest, taskpkg.EventPayload{Tool: "shell", Input: "apply_patch", Message: "approval needed after restart", Status: string(taskpkg.StatusWaitingUser), Risk: "write", Reason: "workspace edit"}},
	} {
		if err := firstRepo.AppendEvent(t.Context(), sourceTask.ID, event.eventType, event.payload); err != nil {
			t.Fatalf("append %s: %v", event.eventType, err)
		}
	}
	if _, err := firstRepo.WriteTodos(t.Context(), taskpkg.TodoWriteRequest{
		SessionID:    session.Session.ID,
		SourceTaskID: sourceTask.ID,
		Todos: []taskpkg.TodoWriteItem{{
			ID:       "restart-all-plan",
			Content:  "Recover transcript and todo after restart",
			Status:   taskpkg.TodoStatusInProgress,
			Priority: taskpkg.TodoPriorityCritical,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	firstServer.Close()
	if err := firstDB.Close(); err != nil {
		t.Fatal(err)
	}

	secondStore := store.New(storeRoot)
	secondDB, err := secondStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer secondDB.Close()
	secondRepo := taskpkg.NewRepository(secondDB)
	secondServer := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: secondRepo, Store: secondStore}))
	defer secondServer.Close()
	secondClient, err := daemonclient.New(secondServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewDaemonSubmitter(secondClient, workspace, true, session.Session.ID, false)

	transcript, handled, err := restarted.HandleCommand(t.Context(), "/transcript 50")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("/transcript after daemon restart:\n%s", transcript)
	for _, want := range []string{"Transcript " + session.Session.ID, "Assistant", "assistant restart summary", "Tool call", "read README.md", "Tool result [ok]", "read output after restart", "Diff", "+after restart", "Approval", "approval needed after restart"} {
		if !handled || !strings.Contains(transcript, want) {
			t.Fatalf("expected restarted /transcript to contain %q handled=%v output=%q", want, handled, transcript)
		}
	}
	resumed, handled, err := restarted.HandleCommand(t.Context(), "/resume-session "+session.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Session " + session.Session.ID, "Context: transcript_items=", "assistant restart summary", "Tool result [ok]", "Approval"} {
		if !handled || !strings.Contains(resumed, want) {
			t.Fatalf("expected restarted /resume-session to contain %q handled=%v output=%q", want, handled, resumed)
		}
	}
	todos, handled, err := restarted.HandleCommand(t.Context(), "/todo")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Todos " + session.Session.ID, "id=restart-all-plan", "status=in_progress", "priority=critical", "content=Recover transcript and todo after restart"} {
		if !handled || !strings.Contains(todos, want) {
			t.Fatalf("expected restarted /todo to contain %q handled=%v output=%q", want, handled, todos)
		}
	}

	emptySession, err := secondClient.CreateSession(t.Context(), taskpkg.CreateSessionRequest{Workspace: workspace, Title: "empty transcript after restart"})
	if err != nil {
		t.Fatal(err)
	}
	emptySubmitter := NewDaemonSubmitter(secondClient, workspace, true, emptySession.Session.ID, false)
	emptyTranscript, handled, err := emptySubmitter.HandleCommand(t.Context(), "/transcript 10")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(emptyTranscript, "No transcript items found.") || strings.Contains(emptyTranscript, "assistant restart summary") || strings.Contains(emptyTranscript, "restart-all-plan") {
		t.Fatalf("expected empty restarted transcript isolation, handled=%v output=%q", handled, emptyTranscript)
	}
	emptyTodos, handled, err := emptySubmitter.HandleCommand(t.Context(), "/todo")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(emptyTodos, "No todos found for session "+emptySession.Session.ID) || strings.Contains(emptyTodos, "restart-all-plan") {
		t.Fatalf("expected empty restarted todo isolation, handled=%v output=%q", handled, emptyTodos)
	}
}
