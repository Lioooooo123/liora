package apply

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FileChange struct {
	Path   string
	Before string
	After  string
}

type ApplyResult struct {
	Files []string `json:"files"`
}

func CreatePatch(workspace string, changes []FileChange) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("workspace is required")
	}
	var builder strings.Builder
	for _, change := range changes {
		path, err := cleanRelPath(workspace, change.Path)
		if err != nil {
			return "", err
		}
		builder.WriteString("--- a/")
		builder.WriteString(path)
		builder.WriteString("\n+++ b/")
		builder.WriteString(path)
		builder.WriteString("\n")
		beforeLines := splitPatchLines(change.Before)
		afterLines := splitPatchLines(change.After)
		builder.WriteString(fmt.Sprintf("@@ -1,%d +1,%d @@\n", max(1, len(beforeLines)), max(1, len(afterLines))))
		for _, line := range beforeLines {
			builder.WriteString("-")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
		for _, line := range afterLines {
			builder.WriteString("+")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}
	return builder.String(), nil
}

func ApplyUnifiedPatch(workspace string, patch string) (ApplyResult, error) {
	if strings.TrimSpace(workspace) == "" {
		return ApplyResult{}, errors.New("workspace is required")
	}
	files, err := parsePatch(workspace, patch)
	if err != nil {
		return ApplyResult{}, err
	}
	var applied []string
	for _, file := range files {
		abs, err := resolve(workspace, file.path)
		if err != nil {
			return ApplyResult{}, err
		}
		current := ""
		if data, err := os.ReadFile(abs); err == nil {
			current = string(data)
		} else if !os.IsNotExist(err) {
			return ApplyResult{}, err
		}
		updated, err := applyFilePatch(current, file.lines)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("%s: %w", file.path, err)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return ApplyResult{}, err
		}
		if err := os.WriteFile(abs, []byte(updated), 0o600); err != nil {
			return ApplyResult{}, err
		}
		applied = append(applied, file.path)
	}
	sort.Strings(applied)
	return ApplyResult{Files: applied}, nil
}

type parsedFile struct {
	path  string
	lines []string
}

func parsePatch(workspace string, patch string) ([]parsedFile, error) {
	var files []parsedFile
	var current *parsedFile
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			path = strings.TrimPrefix(path, "b/")
			cleaned, err := cleanRelPath(workspace, path)
			if err != nil {
				return nil, err
			}
			files = append(files, parsedFile{path: cleaned})
			current = &files[len(files)-1]
		case strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "@@"):
			continue
		case current != nil && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-")):
			current.lines = append(current.lines, line)
		}
	}
	if len(files) == 0 {
		return nil, errors.New("patch contains no files")
	}
	return files, nil
}

func applyFilePatch(current string, patchLines []string) (string, error) {
	var before []string
	var after []string
	for _, line := range patchLines {
		if line == `\ No newline at end of file` {
			continue
		}
		if line == "" {
			continue
		}
		body := line[1:]
		switch line[0] {
		case ' ':
			before = append(before, body)
			after = append(after, body)
		case '-':
			before = append(before, body)
		case '+':
			after = append(after, body)
		}
	}
	beforeText := joinPatchLines(before)
	afterText := joinPatchLines(after)
	if beforeText == "" {
		return current + afterText, nil
	}
	if !strings.Contains(current, beforeText) {
		return "", errors.New("patch context does not match")
	}
	return strings.Replace(current, beforeText, afterText, 1), nil
}

func cleanRelPath(workspace string, path string) (string, error) {
	abs, err := resolve(workspace, path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(filepath.Clean(workspace), abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func resolve(workspace string, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", path)
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	abs := filepath.Clean(filepath.Join(root, filepath.FromSlash(path)))
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path outside workspace: %s", path)
	}
	if err := ensureRealPathWithinRoot(root, abs); err != nil {
		return "", err
	}
	return abs, nil
}

// ensureRealPathWithinRoot verifies that abs, after resolving symlinks in its
// deepest existing ancestor, still lives inside root. A lexical check alone
// cannot catch a symlink inside the workspace pointing outside it, because the
// cleaned path stays within root while the OS follows the link to the real
// target. The leaf may not exist yet (a new file), so resolve the nearest
// existing ancestor instead of abs itself.
func ensureRealPathWithinRoot(root, abs string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	probe := abs
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	realProbe, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(realRoot, realProbe)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path outside workspace: %s", abs)
	}
	return nil
}

func splitPatchLines(content string) []string {
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func joinPatchLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
