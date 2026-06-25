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
	for _, want := range []string{"-daemon", "LIORA_PATCH_MODE=1", "/healthz", "/v1/tasks", "/diff", "/apply"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected daemon smoke script to contain %q, got:\n%s", want, content)
		}
	}
}
