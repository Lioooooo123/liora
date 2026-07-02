package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/store"
	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

func TestServerArtifactPageServesPagedStoreArtifact(t *testing.T) {
	storeRoot := t.TempDir()
	artifactRel := filepath.Join("artifacts", "sessions", "session-1", "tasks", "task-1", "tool-results", "out.txt")
	artifactPath := filepath.Join(storeRoot, artifactRel)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("line-1\nline-2\nline-3\nline-4\nline-5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(Config{Store: store.New(storeRoot)}))
	defer server.Close()

	uri := "artifact://" + filepath.ToSlash(artifactRel)
	resp, err := http.Get(server.URL + "/v1/artifacts/page?uri=" + url.QueryEscape(uri) + "&page=2&page_size=2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}
	var page taskpkg.ArtifactPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.URI != uri || page.Page != 2 || page.PageSize != 2 || page.TotalLines != 5 || page.TotalPages != 3 || !page.HasPrev || !page.HasNext {
		t.Fatalf("unexpected artifact page metadata %#v", page)
	}
	if got := strings.Join(page.Lines, "\n"); got != "line-3\nline-4" {
		t.Fatalf("unexpected page lines %q", got)
	}
}

func TestServerArtifactPageRejectsUnsafeRequests(t *testing.T) {
	storeRoot := t.TempDir()
	server := httptest.NewServer(NewServer(Config{Store: store.New(storeRoot)}))
	defer server.Close()

	cases := []struct {
		name   string
		query  string
		status int
	}{
		{name: "malformed uri", query: "uri=" + url.QueryEscape("file:///tmp/out.txt"), status: http.StatusBadRequest},
		{name: "traversal", query: "uri=" + url.QueryEscape("artifact://artifacts/../secrets.txt"), status: http.StatusBadRequest},
		{name: "missing", query: "uri=" + url.QueryEscape("artifact://artifacts/sessions/missing/tasks/task/tool-results/out.txt"), status: http.StatusNotFound},
		{name: "invalid page", query: "uri=" + url.QueryEscape("artifact://artifacts/sessions/s/tasks/t/tool-results/out.txt") + "&page=bad", status: http.StatusBadRequest},
		{name: "invalid page size", query: "uri=" + url.QueryEscape("artifact://artifacts/sessions/s/tasks/t/tool-results/out.txt") + "&page_size=0", status: http.StatusBadRequest},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(server.URL + "/v1/artifacts/page?" + tt.query)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.status {
				t.Fatalf("expected status %d, got %d", tt.status, resp.StatusCode)
			}
		})
	}
}
