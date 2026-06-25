package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyUnifiedPatchUpdatesWorkspaceFile(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := `--- a/notes.txt
+++ b/notes.txt
@@ -1 +1 @@
-old
+new
`

	result, err := ApplyUnifiedPatch(workspace, patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0] != "notes.txt" {
		t.Fatalf("unexpected apply result %#v", result)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\n" {
		t.Fatalf("unexpected file content %q", string(data))
	}
}

func TestApplyUnifiedPatchRejectsPathTraversal(t *testing.T) {
	workspace := t.TempDir()
	patch := `--- a/../escape.txt
+++ b/../escape.txt
@@ -0,0 +1 @@
+owned
`
	_, err := ApplyUnifiedPatch(workspace, patch)
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected outside workspace error, got %v", err)
	}
}

func TestCreatePatchFromWorkspaceDiff(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch, err := CreatePatch(workspace, []FileChange{{Path: "notes.txt", Before: "old\n", After: "new\n"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--- a/notes.txt", "+++ b/notes.txt", "-old", "+new"} {
		if !strings.Contains(patch, want) {
			t.Fatalf("expected patch to contain %q, got:\n%s", want, patch)
		}
	}
}
