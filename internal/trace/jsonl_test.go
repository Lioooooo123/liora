package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
