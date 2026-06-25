package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const shellTimeout = 30 * time.Second

type Workspace struct {
	root    string
	touched map[string]*string
}

type SearchMatch struct {
	Path    string
	Line    int
	Content string
}

type ShellResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func NewWorkspace(root string) (*Workspace, error) {
	if root == "" {
		return nil, errors.New("workspace root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root is not a directory: %s", root)
	}
	return &Workspace{root: abs, touched: make(map[string]*string)}, nil
}

func (w *Workspace) Root() string {
	return w.root
}

func (w *Workspace) ReadFile(path string) (string, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (w *Workspace) List(path string) ([]string, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (w *Workspace) WriteFile(path string, content string) error {
	abs, err := w.resolve(path)
	if err != nil {
		return err
	}
	if err := w.rememberOriginal(abs); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), 0o600)
}

func (w *Workspace) Replace(path string, oldText string, newText string) error {
	content, err := w.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(content, oldText) {
		return fmt.Errorf("old text not found in %s", path)
	}
	return w.WriteFile(path, strings.ReplaceAll(content, oldText, newText))
}

func (w *Workspace) Search(query string) ([]SearchMatch, error) {
	if query == "" {
		return nil, errors.New("search query is required")
	}
	var matches []SearchMatch
	err := filepath.WalkDir(w.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(w.root, path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, query) {
				matches = append(matches, SearchMatch{
					Path:    filepath.ToSlash(rel),
					Line:    i + 1,
					Content: line,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path == matches[j].Path {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].Path < matches[j].Path
	})
	return matches, nil
}

func (w *Workspace) RunShell(command string) (ShellResult, error) {
	if strings.TrimSpace(command) == "" {
		return ShellResult{}, errors.New("shell command is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = w.root
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := ShellResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: cmd.ProcessState.ExitCode(),
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.ExitCode = -1
		return result, fmt.Errorf("shell command timed out after %s", shellTimeout)
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func (w *Workspace) GitDiff() (string, error) {
	result, err := w.RunShell("git diff -- .")
	if err != nil {
		if result.Stdout != "" || result.Stderr != "" {
			return w.fallbackDiff()
		}
		return w.fallbackDiff()
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return w.fallbackDiff()
	}
	return result.Stdout, nil
}

func (w *Workspace) resolve(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", path)
	}
	abs := filepath.Clean(filepath.Join(w.root, path))
	rel, err := filepath.Rel(w.root, abs)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return abs, nil
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", fmt.Errorf("path outside workspace: %s", path)
	}
	return abs, nil
}

func (w *Workspace) fallbackDiff() (string, error) {
	var paths []string
	for path := range w.touched {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var builder strings.Builder
	for _, path := range paths {
		original := w.touched[path]
		before := ""
		hadBefore := false
		if original != nil {
			before = *original
			hadBefore = true
		}
		abs, err := w.resolve(path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(abs)
		hasAfter := err == nil
		after := string(data)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if hadBefore && hasAfter && before == after {
			continue
		}
		builder.WriteString("--- a/")
		builder.WriteString(path)
		builder.WriteString("\n+++ b/")
		builder.WriteString(path)
		builder.WriteString("\n")
		if hadBefore {
			for _, line := range splitLines(before) {
				builder.WriteString("-")
				builder.WriteString(line)
				builder.WriteString("\n")
			}
		}
		if hasAfter {
			for _, line := range splitLines(after) {
				builder.WriteString("+")
				builder.WriteString(line)
				builder.WriteString("\n")
			}
		}
	}
	return builder.String(), nil
}

func (w *Workspace) rememberOriginal(abs string) error {
	rel, err := filepath.Rel(w.root, abs)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if _, ok := w.touched[rel]; ok {
		return nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			w.touched[rel] = nil
			return nil
		}
		return err
	}
	if bytes.Contains(data, []byte{0}) {
		w.touched[rel] = nil
		return nil
	}
	content := string(data)
	w.touched[rel] = &content
	return nil
}

func splitLines(content string) []string {
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}
