package tuisession

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/daemon"
	"github.com/Lioooooo123/liora/internal/daemonclient"
	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/tools"
)

type artifactSandboxExecutor struct {
	stdout string
}

func (e artifactSandboxExecutor) Run(_ context.Context, _ string, _ string) (tools.ShellResult, error) {
	return tools.ShellResult{Stdout: e.stdout, ExitCode: 0}, nil
}

type nativeArtifactGenerator struct {
	calls int
}

func (g *nativeArtifactGenerator) Generate(_ context.Context, _ []llm.Message) (string, error) {
	return "run emit-large-artifact", nil
}

func (g *nativeArtifactGenerator) SupportsTools() bool {
	return true
}

func (g *nativeArtifactGenerator) GenerateWithTools(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (llm.Completion, error) {
	g.calls++
	if g.calls == 1 {
		return llm.Completion{
			ToolCalls: []llm.ToolCall{{
				ID:        "call_large_artifact",
				Name:      "run",
				Arguments: `{"command":"emit-large-artifact"}`,
			}},
		}, nil
	}
	return llm.Completion{Content: "large artifact stored"}, nil
}

func TestDaemonSubmitterPagesLargeArtifactWithoutInliningFullContext(t *testing.T) {
	workspace := t.TempDir()
	storeRoot := t.TempDir()
	persistentStore := store.New(storeRoot)
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	largeOutput := largeArtifactOutput(7000)
	runner := taskpkg.NewRunner(repo, llm.NewPlanner(&nativeArtifactGenerator{}))
	runner.SetSandbox(artifactSandboxExecutor{stdout: largeOutput})
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Runner: runner, Store: persistentStore}))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	submitter := NewDaemonSubmitter(client, workspace, true, "", false)

	if _, err := submitter.SubmitStream(t.Context(), "produce large artifact", nil); err != nil {
		t.Fatal(err)
	}
	taskID := findOnlyTaskID(t, repo)
	taskRecord, err := repo.Get(t.Context(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	artifactURI := taskArtifactURI(t, repo, taskID)
	if !strings.HasPrefix(artifactURI, "artifact://artifacts/sessions/"+taskRecord.SessionID+"/tasks/"+taskID+"/tool-results/") {
		t.Fatalf("unexpected artifact uri %q", artifactURI)
	}

	firstPage, handled, err := submitter.HandleCommand(t.Context(), "/artifact "+artifactURI+" 1 2")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Artifact " + artifactURI, "Page 1/", "page_size=2", "artifact-line-0001", "artifact-line-0002"} {
		if !handled || !strings.Contains(firstPage, want) {
			t.Fatalf("expected first artifact page to contain %q handled=%v output=%q", want, handled, firstPage)
		}
	}
	if strings.Contains(firstPage, "artifact-line-0003") {
		t.Fatalf("first artifact page should not inline the next page, got %q", firstPage)
	}
	t.Logf("artifact page 1:\n%s", firstPage)

	secondPage, handled, err := submitter.HandleCommand(t.Context(), "/artifact "+artifactURI+" 2 2")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Page 2/", "has_prev=true", "artifact-line-0003", "artifact-line-0004"} {
		if !handled || !strings.Contains(secondPage, want) {
			t.Fatalf("expected second artifact page to contain %q handled=%v output=%q", want, handled, secondPage)
		}
	}
	t.Logf("artifact page 2:\n%s", secondPage)

	envelope, err := client.SessionContext(t.Context(), taskRecord.SessionID, taskpkg.ContextRequest{ItemLimit: 50, TokenBudget: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.ArtifactRefs) == 0 || envelope.ArtifactRefs[0].Path != artifactURI {
		t.Fatalf("expected context artifact ref %q, got %#v", artifactURI, envelope.ArtifactRefs)
	}
	contextOutput, handled, err := submitter.HandleCommand(t.Context(), "/context 50 4096")
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(contextOutput, artifactURI) || !strings.Contains(contextOutput, "Artifacts:") {
		t.Fatalf("expected context command to show bounded artifact reference, handled=%v output=%q", handled, contextOutput)
	}
	if contextContains(envelope, "artifact-line-7000-tail-marker") || strings.Contains(contextOutput, "artifact-line-7000-tail-marker") {
		t.Fatalf("context should not inline the full artifact tail; envelope=%#v output=%q", envelope.Transcript, contextOutput)
	}
	t.Logf("bounded context:\n%s", contextOutput)
}

func TestDaemonSubmitterArtifactCommandRejectsInvalidInputsWithoutSessionState(t *testing.T) {
	storeRoot := t.TempDir()
	persistentStore := store.New(storeRoot)
	db, err := persistentStore.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := taskpkg.NewRepository(db)
	server := httptest.NewServer(daemon.NewServer(daemon.Config{Repository: repo, Store: persistentStore}))
	defer server.Close()
	submitter := newTestSubmitter(t, server.URL, t.TempDir(), true)

	commands := []string{
		"/artifact file:///tmp/out.txt",
		"/artifact artifact://artifacts/../secrets.txt",
		"/artifact artifact://artifacts/sessions/missing/tasks/task/tool-results/out.txt",
		"/artifact artifact://artifacts/sessions/s/tasks/t/tool-results/out.txt zero",
		"/artifact artifact://artifacts/sessions/s/tasks/t/tool-results/out.txt 1 zero",
	}
	for _, command := range commands {
		output, handled, err := submitter.HandleCommand(t.Context(), command)
		if !handled {
			t.Fatalf("expected %q to be handled", command)
		}
		if err == nil {
			t.Fatalf("expected %q to fail, got output %q", command, output)
		}
		t.Logf("%s -> %v", command, err)
	}
	sessions, err := repo.ListSessions(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("artifact command failures should not create sessions, got %#v", sessions)
	}
}

func largeArtifactOutput(lines int) string {
	var builder strings.Builder
	for i := 1; i <= lines; i++ {
		if i == lines {
			fmt.Fprintf(&builder, "artifact-line-%04d-tail-marker\n", i)
			continue
		}
		fmt.Fprintf(&builder, "artifact-line-%04d\n", i)
	}
	return builder.String()
}

func taskArtifactURI(t *testing.T, repo *taskpkg.Repository, taskID string) string {
	t.Helper()
	events, err := repo.Events(t.Context(), taskID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != taskpkg.EventArtifactReference {
			continue
		}
		var payload taskpkg.EventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Path
	}
	t.Fatalf("expected artifact reference event in %#v", events)
	return ""
}

func contextContains(envelope taskpkg.ContextEnvelope, needle string) bool {
	for _, item := range envelope.Transcript {
		if strings.Contains(item.Output, needle) || strings.Contains(item.Content, needle) {
			return true
		}
	}
	return false
}
