package daemon

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	taskpkg "github.com/Lioooooo123/liora/internal/task"
)

const (
	defaultArtifactPageSize = 40
	maxArtifactPageSize     = 200
)

func (s *server) handleArtifactPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	if s.store == nil {
		writeError(w, http.StatusInternalServerError, errors.New("store is not configured"))
		return
	}
	uri := strings.TrimSpace(r.URL.Query().Get("uri"))
	page, err := optionalPositiveInt(r.URL.Query().Get("page"), "page")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	pageSize, err := optionalPositiveInt(r.URL.Query().Get("page_size"), "page_size")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	tail, err := optionalBool(r.URL.Query().Get("tail"), "tail")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = defaultArtifactPageSize
	}
	if pageSize > maxArtifactPageSize {
		pageSize = maxArtifactPageSize
	}
	path, err := s.artifactPath(uri)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	artifact, err := readArtifactPage(uri, path, page, pageSize, tail)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, fmt.Errorf("artifact not found"))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, artifact)
}

func (s *server) artifactPath(uri string) (string, error) {
	if !strings.HasPrefix(uri, "artifact://") {
		return "", fmt.Errorf("artifact uri must start with artifact://")
	}
	rel := strings.TrimPrefix(uri, "artifact://")
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "\\") {
		return "", fmt.Errorf("artifact uri must be a relative store artifact path")
	}
	if !strings.HasPrefix(rel, "artifacts/") {
		return "", fmt.Errorf("artifact uri must point under artifacts/")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || !strings.HasPrefix(clean, "artifacts/") {
		return "", fmt.Errorf("artifact uri must not escape artifacts/")
	}
	root, err := filepath.Abs(s.store.Root())
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(clean)))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("artifact uri must stay under store root")
	}
	return path, nil
}

func readArtifactPage(uri string, path string, page int, pageSize int, tail bool) (taskpkg.ArtifactPage, error) {
	file, err := os.Open(path)
	if err != nil {
		return taskpkg.ArtifactPage{}, err
	}
	defer file.Close()
	if tail {
		return readArtifactTail(uri, file, pageSize)
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	lines := make([]string, 0, pageSize)
	total := 0
	for scanner.Scan() {
		if total >= start && total < end {
			lines = append(lines, scanner.Text())
		}
		total++
	}
	if err := scanner.Err(); err != nil {
		return taskpkg.ArtifactPage{}, err
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return taskpkg.ArtifactPage{
		URI:        uri,
		Page:       page,
		PageSize:   pageSize,
		TotalLines: total,
		TotalPages: totalPages,
		HasPrev:    page > 1 && totalPages > 0,
		HasNext:    page < totalPages,
		Lines:      lines,
	}, nil
}

func readArtifactTail(uri string, file *os.File, pageSize int) (taskpkg.ArtifactPage, error) {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	lines := make([]string, 0, pageSize)
	total := 0
	for scanner.Scan() {
		line := scanner.Text()
		if len(lines) < pageSize {
			lines = append(lines, line)
		} else {
			copy(lines, lines[1:])
			lines[len(lines)-1] = line
		}
		total++
	}
	if err := scanner.Err(); err != nil {
		return taskpkg.ArtifactPage{}, err
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	page := totalPages
	if page == 0 {
		page = 1
	}
	return taskpkg.ArtifactPage{
		URI:        uri,
		Page:       page,
		PageSize:   pageSize,
		Tail:       true,
		TotalLines: total,
		TotalPages: totalPages,
		HasPrev:    page > 1 && totalPages > 0,
		HasNext:    false,
		Lines:      lines,
	}, nil
}

func optionalBool(value string, label string) (bool, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return false, nil
	}
	switch value {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", label)
	}
}
