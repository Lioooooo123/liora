package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareWorkspaceCopyModeCopiesAndCleansTaskWorkspace(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "node_modules", "pkg", "ignored.txt"), []byte("skip\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	session, err := PrepareWorkspace(source, WorkspaceModeCopy)
	if err != nil {
		t.Fatal(err)
	}
	if session.Root == source {
		t.Fatal("copy mode should use a temporary workspace")
	}
	if session.Source != source || session.Mode != WorkspaceModeCopy {
		t.Fatalf("unexpected session %#v", session)
	}
	if _, err := os.Stat(filepath.Join(session.Root, "notes.txt")); err != nil {
		t.Fatalf("expected copied file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(session.Root, "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("expected node_modules to be skipped, stat err: %v", err)
	}

	session.Cleanup()
	if _, err := os.Stat(session.Root); !os.IsNotExist(err) {
		t.Fatalf("expected cleanup to remove temp workspace, stat err: %v", err)
	}
}

func TestPrepareWorkspaceDirectModeUsesSource(t *testing.T) {
	source := t.TempDir()
	session, err := PrepareWorkspace(source, WorkspaceModeDirect)
	if err != nil {
		t.Fatal(err)
	}
	if session.Root != source || session.Source != source || session.Mode != WorkspaceModeDirect {
		t.Fatalf("unexpected direct session %#v", session)
	}
	session.Cleanup()
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("direct cleanup should not remove source: %v", err)
	}
}
