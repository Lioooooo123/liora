package trace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteJSONLUsesOwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file mode semantics differ on windows")
	}
	dir := filepath.Join(t.TempDir(), "traces")
	path := filepath.Join(dir, "trace.jsonl")
	if err := WriteJSONL(path, []Event{{Tool: "run", Input: "echo $SECRET", Output: "value", Status: StatusOK}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("trace file mode = %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("trace dir mode = %v, want 0700", di.Mode().Perm())
	}
}

func TestWriteJSONLStoresTraceEvents(t *testing.T) {
	events := []Event{
		{Tool: "read", Input: "app.txt", Output: "hello", Status: StatusOK},
		{Tool: "run", Input: "go test ./...", Output: "ok", Status: StatusOK},
	}
	path := filepath.Join(t.TempDir(), "trace.jsonl")

	if err := WriteJSONL(path, events); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 jsonl lines, got %d: %s", len(lines), string(data))
	}
	if !strings.Contains(lines[0], `"tool":"read"`) || !strings.Contains(lines[1], `"tool":"run"`) {
		t.Fatalf("unexpected jsonl content: %s", string(data))
	}
}
