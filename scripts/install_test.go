package scripts_test

import (
	"os"
	"strings"
	"testing"
)

func TestInstallScriptBuildsLioraBinary(t *testing.T) {
	data, err := os.ReadFile("install-local.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{`go build`, `cmd/coding-agent`, `liora`, `.local/bin`} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected install script to contain %q, got:\n%s", want, content)
		}
	}
}
