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
	for _, want := range []string{"-tui-daemon", "Timeline session_", "tool.result", "/tools", "MCP tools", "mcp fake echo <json arguments>", "/timeline", "/cancel", "Cancelled task", "Fake", "chat"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected tui smoke script to contain %q, got:\n%s", want, content)
		}
	}
}

func TestCodingEvalScriptCoversTaskQualityBaseline(t *testing.T) {
	data, err := os.ReadFile("coding-eval.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"Fake", "LIORA_PATCH_MODE=1", "LIORA_PERMISSION=prompt", "natural", "run_async", "multi-file", "config/settings.txt", "docs/guide.txt", "docx-case", "assignment.docx", "Assignment Brief", "mcp-case", "fake_mcp.py", "mcp echo: hello from eval", "external", "replan-case", "task.replanning", "missing-replan.txt", "600000", "truncated", "task.diff", "task.patch_applied", "/timeline", "permission.requested", "permission.approved", "permission.denied", "task.cancelled", "coding eval ok"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected coding eval script to contain %q, got:\n%s", want, content)
		}
	}
}
