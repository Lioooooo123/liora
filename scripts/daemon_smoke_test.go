package scripts_test

import (
	"os"
	"strings"
	"testing"
)

func TestDaemonSmokeScriptCoversDaemonAPI(t *testing.T) {
	data, err := os.ReadFile("daemon-smoke.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"-daemon", "LIORA_PATCH_MODE=1", "/healthz", "daemon did not become healthy", "/v1/tasks", "sandbox.workspace", "task.completed", "/diff", "/apply", "/cancel", "task.cancelled"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected daemon smoke script to contain %q, got:\n%s", want, content)
		}
	}
}

func TestTUISmokeScriptCoversDaemonBackedTUI(t *testing.T) {
	data, err := os.ReadFile("tui-smoke.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"-tui-daemon", "Timeline session_", "tool.result", "/timeline", "/cancel", "Cancelled task", "Fake", "chat"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected tui smoke script to contain %q, got:\n%s", want, content)
		}
	}
}
