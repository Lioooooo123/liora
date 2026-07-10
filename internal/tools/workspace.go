package tools

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const shellTimeout = 30 * time.Second
const maxReadLines = 1000
const maxReadBytes = 100 * 1024
const maxLineLength = 2000
const maxSearchMatches = 250
const maxShellOutputBytes = 512 * 1024
const maxDocumentBytes = 512 * 1024

type Workspace struct {
	root    string
	touched map[string]*string
}

type SearchMatch struct {
	Path    string
	Line    int
	Content string
}

type FileStat struct {
	Path  string
	Size  int64
	Mode  string
	IsDir bool
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
	return w.ReadFileRange(path, 1, maxReadLines)
}

func (w *Workspace) ReadFileRange(path string, startLine int, lineCount int) (string, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	file, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if startLine < 1 {
		startLine = 1
	}
	if lineCount <= 0 || lineCount > maxReadLines {
		lineCount = maxReadLines
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxLineLength*4)
	var builder strings.Builder
	currentLine := 0
	writtenLines := 0
	truncated := false
	for scanner.Scan() {
		currentLine++
		if currentLine < startLine {
			continue
		}
		if writtenLines >= lineCount {
			break
		}
		line := scanner.Text()
		if strings.ContainsRune(line, '\x00') {
			return "", fmt.Errorf("%s is not readable as text", path)
		}
		if len(line) > maxLineLength {
			line = line[:maxLineLength] + "[...truncated]"
			truncated = true
		}
		next := strconv.Itoa(currentLine) + "\t" + line + "\n"
		if builder.Len()+len(next) > maxReadBytes {
			truncated = true
			break
		}
		builder.WriteString(next)
		writtenLines++
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if truncated {
		builder.WriteString("[...truncated]\n")
	}
	return builder.String(), nil
}

func (w *Workspace) ReadDocumentRange(path string, startLine int, lineCount int) (string, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	ext := strings.ToLower(filepath.Ext(abs))
	var content string
	switch ext {
	case ".pdf":
		content, err = extractPDFText(abs)
	case ".docx":
		content, err = extractDOCXText(abs)
	default:
		return "", fmt.Errorf("document supports .pdf and .docx, got %s", ext)
	}
	if err != nil {
		return "", err
	}
	return formatNumberedRange(content, startLine, lineCount), nil
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

func (w *Workspace) AppendFile(path string, content string) error {
	existing, err := w.ReadFileRaw(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	return w.WriteFile(path, existing+content)
}

func (w *Workspace) Replace(path string, oldText string, newText string) error {
	return w.Edit(path, oldText, newText, true)
}

func (w *Workspace) Edit(path string, oldText string, newText string, replaceAll bool) error {
	if oldText == "" {
		return errors.New("old text is required")
	}
	if oldText == newText {
		return errors.New("old text and new text are identical")
	}
	content, err := w.ReadFileRaw(path)
	if err != nil {
		return err
	}
	count := strings.Count(content, oldText)
	if count == 0 {
		return fmt.Errorf("old text not found in %s", path)
	}
	if !replaceAll && count > 1 {
		return fmt.Errorf("old text is not unique in %s (%d occurrences)", path, count)
	}
	if replaceAll {
		return w.WriteFile(path, strings.ReplaceAll(content, oldText, newText))
	}
	return w.WriteFile(path, strings.Replace(content, oldText, newText, 1))
}

func (w *Workspace) Search(query string) ([]SearchMatch, error) {
	if query == "" {
		return nil, errors.New("search query is required")
	}
	if matches, ok, err := w.searchWithRipgrep(query); ok || err != nil {
		return matches, err
	}
	return w.searchWithWalk(query)
}

func (w *Workspace) Glob(pattern string, root string, includeDirs bool) ([]string, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, errors.New("glob pattern is required")
	}
	if root == "" {
		root = "."
	}
	if matches, ok, err := w.globWithRipgrep(pattern, root); ok || err != nil {
		if !includeDirs {
			filtered := matches[:0]
			for _, match := range matches {
				abs, err := w.resolve(match)
				if err != nil {
					return nil, err
				}
				info, err := os.Stat(abs)
				if err == nil && !info.IsDir() {
					filtered = append(filtered, match)
				}
			}
			matches = filtered
		}
		return matches, nil
	}
	return w.globWithWalk(pattern, root, includeDirs)
}

func (w *Workspace) Tree(root string, maxDepth int) ([]string, error) {
	if root == "" {
		root = "."
	}
	if maxDepth <= 0 || maxDepth > 6 {
		maxDepth = 2
	}
	absRoot, err := w.resolve(root)
	if err != nil {
		return nil, err
	}
	var lines []string
	err = filepath.WalkDir(absRoot, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relToRoot, err := filepath.Rel(absRoot, current)
		if err != nil {
			return err
		}
		if relToRoot == "." {
			return nil
		}
		if shouldSkipEntry(entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		depth := strings.Count(relToRoot, string(filepath.Separator)) + 1
		if depth > maxDepth {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := filepath.ToSlash(relToRoot)
		if entry.IsDir() {
			name += "/"
		}
		lines = append(lines, strings.Repeat("  ", depth-1)+name)
		if len(lines) >= 300 {
			return filepath.SkipAll
		}
		return nil
	})
	return lines, err
}

func (w *Workspace) Stat(path string) (FileStat, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return FileStat{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return FileStat{}, err
	}
	rel, _ := filepath.Rel(w.root, abs)
	return FileStat{
		Path:  filepath.ToSlash(rel),
		Size:  info.Size(),
		Mode:  info.Mode().String(),
		IsDir: info.IsDir(),
	}, nil
}

func (w *Workspace) Mkdir(path string) error {
	abs, err := w.resolve(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0o755)
}

func (w *Workspace) Delete(path string) error {
	abs, err := w.resolve(path)
	if err != nil {
		return err
	}
	if err := w.rememberOriginal(abs); err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.Remove(abs)
	}
	return os.Remove(abs)
}

func (w *Workspace) ReadFileRaw(path string) (string, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	if bytes.Contains(data, []byte{0}) {
		return "", fmt.Errorf("%s is not readable as text", path)
	}
	return string(data), nil
}

func (w *Workspace) searchWithWalk(query string) ([]SearchMatch, error) {
	var matches []SearchMatch
	err := filepath.WalkDir(w.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if shouldSkipEntry(entry) {
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
				if len(matches) >= maxSearchMatches {
					return filepath.SkipAll
				}
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
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	result := ShellResult{
		Stdout:   truncateString(stdout.String(), maxShellOutputBytes),
		Stderr:   truncateString(stderr.String(), maxShellOutputBytes),
		ExitCode: exitCode,
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

func (w *Workspace) searchWithRipgrep(query string) ([]SearchMatch, bool, error) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		return nil, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := []string{
		"--line-number", "--color", "never", "--no-heading", "-F",
		"--glob", "!.git/**",
		"--glob", "!.env",
		"--glob", "!.env.*",
		query, ".",
	}
	cmd := exec.CommandContext(ctx, rg, args...)
	cmd.Dir = w.root
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, true, nil
		}
		return nil, true, err
	}
	var matches []SearchMatch
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		lineNo, _ := strconv.Atoi(parts[1])
		matchPath := strings.TrimPrefix(filepath.ToSlash(parts[0]), "./")
		matches = append(matches, SearchMatch{Path: matchPath, Line: lineNo, Content: parts[2]})
		if len(matches) >= maxSearchMatches {
			break
		}
	}
	return matches, true, nil
}

func (w *Workspace) globWithRipgrep(pattern string, root string) ([]string, bool, error) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		return nil, false, nil
	}
	absRoot, err := w.resolve(root)
	if err != nil {
		return nil, true, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, rg, "--files", "--color", "never", "--glob", pattern, "--glob", "!.git/**", "--glob", "!.env", "--glob", "!.env.*", ".")
	cmd.Dir = absRoot
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, true, nil
		}
		return nil, true, err
	}
	var matches []string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		abs := filepath.Join(absRoot, filepath.FromSlash(line))
		rel, err := filepath.Rel(w.root, abs)
		if err != nil {
			return nil, true, err
		}
		matches = append(matches, filepath.ToSlash(rel))
		if len(matches) >= 100 {
			break
		}
	}
	sort.Strings(matches)
	return matches, true, nil
}

func (w *Workspace) globWithWalk(pattern string, root string, includeDirs bool) ([]string, error) {
	absRoot, err := w.resolve(root)
	if err != nil {
		return nil, err
	}
	var matches []string
	err = filepath.WalkDir(absRoot, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if shouldSkipEntry(entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() && !includeDirs {
			return nil
		}
		rel, err := filepath.Rel(absRoot, current)
		if err != nil || rel == "." {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		ok, _ := path.Match(pattern, relSlash)
		if !ok {
			ok, _ = path.Match(pattern, path.Base(relSlash))
		}
		if ok {
			fullRel, _ := filepath.Rel(w.root, current)
			item := filepath.ToSlash(fullRel)
			if entry.IsDir() {
				item += "/"
			}
			matches = append(matches, item)
			if len(matches) >= 100 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	sort.Strings(matches)
	return matches, err
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
	if err := ensureRealPathWithinRoot(w.root, abs); err != nil {
		return "", err
	}
	return abs, nil
}

// ensureRealPathWithinRoot verifies that abs, after resolving symlinks in its
// deepest existing ancestor, still lives inside root. The lexical filepath.Rel
// check above cannot catch a symlink *inside* the workspace that points outside
// it: the cleaned path stays within root while the OS follows the link to the
// real target. The leaf may not exist yet (creating a file), so resolve the
// nearest existing ancestor instead of abs itself.
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

func shouldSkipEntry(entry os.DirEntry) bool {
	name := entry.Name()
	if name == ".git" || name == "node_modules" || name == "vendor" {
		return true
	}
	if name == ".env" || strings.HasPrefix(name, ".env.") {
		return true
	}
	return false
}

func truncateString(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes] + "\n[...truncated]\n"
}

func extractPDFText(abs string) (string, error) {
	pdftotext, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", fmt.Errorf("pdftotext is required to read PDF files")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, pdftotext, "-layout", "-enc", "UTF-8", abs, "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("PDF extraction timed out")
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("PDF extraction failed: %s", detail)
	}
	return truncateString(string(out), maxDocumentBytes), nil
}

func extractDOCXText(abs string) (string, error) {
	reader, err := zip.OpenReader(abs)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name == "word/document.xml" {
			content, err := readZipFileText(file)
			if err != nil {
				return "", err
			}
			return truncateString(docxXMLToText(content), maxDocumentBytes), nil
		}
	}
	return "", fmt.Errorf("word/document.xml not found in DOCX")
}

func readZipFileText(file *zip.File) (string, error) {
	reader, err := file.Open()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	var builder strings.Builder
	limited := io.LimitReader(reader, maxDocumentBytes+1)
	if _, err := io.Copy(&builder, limited); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func docxXMLToText(content string) string {
	decoder := xml.NewDecoder(strings.NewReader(content))
	var builder strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		switch value := token.(type) {
		case xml.CharData:
			builder.WriteString(string(value))
		case xml.EndElement:
			if value.Name.Local == "p" {
				builder.WriteString("\n")
			}
		case xml.StartElement:
			switch value.Name.Local {
			case "tab":
				builder.WriteString("\t")
			case "br", "cr":
				builder.WriteString("\n")
			}
		}
	}
	return strings.TrimSpace(builder.String()) + "\n"
}

func formatNumberedRange(content string, startLine int, lineCount int) string {
	if startLine < 1 {
		startLine = 1
	}
	if lineCount <= 0 || lineCount > maxReadLines {
		lineCount = maxReadLines
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var builder strings.Builder
	written := 0
	truncated := false
	for i, line := range lines {
		lineNo := i + 1
		if lineNo < startLine {
			continue
		}
		if written >= lineCount {
			truncated = true
			break
		}
		if len(line) > maxLineLength {
			line = line[:maxLineLength] + "[...truncated]"
			truncated = true
		}
		next := strconv.Itoa(lineNo) + "\t" + line + "\n"
		if builder.Len()+len(next) > maxReadBytes {
			truncated = true
			break
		}
		builder.WriteString(next)
		written++
	}
	if truncated {
		builder.WriteString("[...truncated]\n")
	}
	return builder.String()
}
