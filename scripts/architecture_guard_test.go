package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchitectureGuardScriptPassesForCurrentMainChain(t *testing.T) {
	cmd := exec.Command("bash", "architecture-guard.sh", "..")
	cmd.Dir = "."
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("architecture guard failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "architecture guard ok") {
		t.Fatalf("expected architecture guard success output, got:\n%s", output)
	}
}

func TestArchitectureGuardScriptRejectsFrameworkRuntimeTakeover(t *testing.T) {
	root := t.TempDir()
	writeGuardFixture(t, root)
	data, err := os.ReadFile("architecture-guard.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "architecture-guard.sh"), data, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", filepath.Join(root, "architecture-guard.sh"), root)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected architecture guard to reject forbidden runtime dependency, got success:\n%s", output)
	}
	if !strings.Contains(string(output), "forbidden main-runtime dependency") {
		t.Fatalf("expected forbidden dependency diagnostic, got:\n%s", output)
	}
}

func writeGuardFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"go.mod": `module fixture

require (
	charm.land/bubbletea/v2 v2.0.0
	modernc.org/sqlite v1.38.2
	github.com/cloudwego/eino v0.4.0
)
`,
		"apps/cli/main.go": `package main

import (
	"context"
	"net/http"

	"github.com/Lioooooo123/liora/internal/daemon"
	"github.com/Lioooooo123/liora/internal/daemonclient"
	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
	"github.com/Lioooooo123/liora/internal/tui"
)

func main() {
	_ = daemon.Server{}
	_ = taskpkg.Task{}
	_ = store.New
	_, _ = daemonclient.New("")
	_ = runDaemon(newDaemonHTTPServer(*daemonAddr, server))
	_ = startEmbeddedDaemon(persistentStore, planner, llmRegistry, sandboxExecutor, patchMode)
	_ = daemonclient.New(baseURL, clientOptions...)
	_ = tui.RunProgram(context.Background(), tuiConfig, daemonSession)
}
`,
		"internal/daemon/server.go":               `package daemon; var _ = "/v1/tasks handleTasksEventStream"`,
		"internal/daemonclient/client.go":         `package daemonclient; func StreamEvents() {}; func Apply() {}`,
		"internal/store/store.go":                 `package store; import _ "modernc.org/sqlite"`,
		"internal/task/task.go":                   `package task; type Task struct{}`,
		"internal/tui/program.go":                 `package tui; import _ "charm.land/bubbletea/v2"`,
		"internal/tui/tui.go":                     `package tui`,
		"internal/tuisession/daemon_submitter.go": `package tuisession; import _ "github.com/Lioooooo123/liora/internal/daemonclient"; var _ = "s.client.CreateTask s.client.StreamEvents"`,
		"packages/protocol/src/client.ts":         `export function createDaemonProtocolClient() {} export function parseTaskEventStream() {}`,
		"go.sum":                                  "",
		"package.json":                            "{}",
		"pnpm-lock.yaml":                          "",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
