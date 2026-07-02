package hook

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lioooooo123/liora/internal/store"
)

func TestRunnerRecordsFailureAndReplaysWithSecretFreeEnv(t *testing.T) {
	// Given
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("API_KEY", "secret-from-parent-env")
	t.Setenv("LIORA_API_TOKEN", "secret-token")
	persistentStore := store.New(home)
	registry := NewRegistry(persistentStore)
	command := "printf '%s\\n' \"$API_KEY|$LIORA_API_TOKEN|$LIORA_HOOK_EVENT|$LIORA_HOOK_PAYLOAD\" >> env.log; echo hook-stderr >&2; exit 7"
	if _, err := registry.Save(t.Context(), SaveRequest{
		ID:      "pre-run",
		Event:   EventPreToolUse,
		Command: command,
		Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(registry, RunnerConfig{Timeout: time.Second, OutputLimit: 1024})

	// When
	payload := `{"tool":"run","input":"rm -rf build"}`
	err := runner.Run(t.Context(), EventPreToolUse, RunInput{Workspace: workspace, Payload: payload})

	// Then
	var runErr *RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("expected hook run error, got %v", err)
	}
	runs, err := registry.ListRuns(t.Context(), RunListOptions{HookID: "pre-run", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != RunStatusFailed || runs[0].ExitCode != 7 {
		t.Fatalf("expected failed run with exit code 7, got %#v", runs)
	}
	envLog, err := os.ReadFile(filepath.Join(workspace, "env.log"))
	if err != nil {
		t.Fatal(err)
	}
	renderedEnv := string(envLog)
	for _, forbidden := range []string{"secret-from-parent-env", "secret-token"} {
		if strings.Contains(renderedEnv, forbidden) {
			t.Fatalf("hook inherited forbidden secret %q in env log %q", forbidden, renderedEnv)
		}
	}
	if !strings.Contains(renderedEnv, string(EventPreToolUse)) || !strings.Contains(renderedEnv, payload) {
		t.Fatalf("expected event and payload to be passed to hook, got %q", renderedEnv)
	}

	// When
	err = runner.ReplayLatestFailure(t.Context(), "pre-run")

	// Then
	if !errors.As(err, &runErr) {
		t.Fatalf("expected replay to rerun failed hook, got %v", err)
	}
	runs, err = registry.ListRuns(t.Context(), RunListOptions{HookID: "pre-run", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ReplayOfRunID != "" || runs[1].ReplayOfRunID != runs[0].ID {
		t.Fatalf("expected replay run linked to original failure, got %#v", runs)
	}
}

func TestRunnerSkipsDisabledHooksAndCapsOutput(t *testing.T) {
	// Given
	workspace := t.TempDir()
	registry := NewRegistry(store.New(t.TempDir()))
	if _, err := registry.Save(t.Context(), SaveRequest{
		ID:      "disabled",
		Event:   EventTaskComplete,
		Command: "echo should-not-run > disabled.txt",
		Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save(t.Context(), SaveRequest{
		ID:      "noisy",
		Event:   EventTaskComplete,
		Command: "printf '1234567890'; exit 1",
		Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(registry, RunnerConfig{Timeout: time.Second, OutputLimit: 4})

	// When
	err := runner.Run(t.Context(), EventTaskComplete, RunInput{Workspace: workspace, Payload: `{}`})

	// Then
	var runErr *RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("expected noisy hook to fail, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "disabled.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disabled hook should not run, stat err=%v", err)
	}
	runs, err := registry.ListRuns(t.Context(), RunListOptions{HookID: "noisy", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Stdout != "1234" || !runs[0].OutputTruncated {
		t.Fatalf("expected capped noisy output, got %#v", runs)
	}
}
