package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

type WorkspaceMode string

const (
	WorkspaceModeDirect WorkspaceMode = "direct"
	WorkspaceModeCopy   WorkspaceMode = "copy"
)

type WorkspaceSession struct {
	Source  string        `json:"source"`
	Root    string        `json:"root"`
	Mode    WorkspaceMode `json:"mode"`
	cleanup func()
}

func PrepareWorkspace(source string, mode WorkspaceMode) (WorkspaceSession, error) {
	if mode == "" || mode == WorkspaceModeDirect {
		return WorkspaceSession{Source: source, Root: source, Mode: WorkspaceModeDirect}, nil
	}
	tempRoot, err := os.MkdirTemp("", "liora-task-*")
	if err != nil {
		return WorkspaceSession{}, err
	}
	session := WorkspaceSession{
		Source:  source,
		Root:    tempRoot,
		Mode:    WorkspaceModeCopy,
		cleanup: func() { _ = os.RemoveAll(tempRoot) },
	}
	if err := copyWorkspace(source, tempRoot); err != nil {
		session.Cleanup()
		return WorkspaceSession{}, err
	}
	return session, nil
}

func (s WorkspaceSession) Cleanup() {
	if s.cleanup != nil {
		s.cleanup()
	}
}

func copyWorkspace(source string, targetRoot string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		if shouldSkipCopy(entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(targetRoot, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func shouldSkipCopy(entry os.DirEntry) bool {
	name := entry.Name()
	if name == ".git" || name == "node_modules" || name == "vendor" {
		return true
	}
	return strings.HasPrefix(name, ".liora-task-")
}
