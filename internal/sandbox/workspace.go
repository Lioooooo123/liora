package sandbox

import (
	"context"
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
	return PrepareWorkspaceWithContext(context.Background(), source, mode)
}

func PrepareWorkspaceWithContext(ctx context.Context, source string, mode WorkspaceMode) (WorkspaceSession, error) {
	if mode == "" || mode == WorkspaceModeDirect {
		return WorkspaceSession{Source: source, Root: source, Mode: WorkspaceModeDirect}, nil
	}
	if err := ctx.Err(); err != nil {
		return WorkspaceSession{}, err
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
	if err := copyWorkspace(ctx, source, tempRoot); err != nil {
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

func copyWorkspace(ctx context.Context, source string, targetRoot string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
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
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
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
	if skippedCopyDirs[name] {
		return true
	}
	return strings.HasPrefix(name, ".liora-task-")
}

var skippedCopyDirs = map[string]bool{
	".cache":        true,
	".git":          true,
	".gradle":       true,
	".mypy_cache":   true,
	".next":         true,
	".nuxt":         true,
	".pnpm-store":   true,
	".pytest_cache": true,
	".ruff_cache":   true,
	".turbo":        true,
	".venv":         true,
	".yarn":         true,
	"DerivedData":   true,
	"Pods":          true,
	"__pycache__":   true,
	"build":         true,
	"dist":          true,
	"node_modules":  true,
	"target":        true,
	"vendor":        true,
	"venv":          true,
}
