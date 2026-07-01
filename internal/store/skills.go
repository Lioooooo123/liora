package store

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxSkillHeaderBytes = 64 * 1024
	maxSkillReadLines   = 1000
	maxSkillReadBytes   = 100 * 1024
)

func (s *Store) ScanSkills(workspaceRoot string) ([]Skill, error) {
	byName := map[string]Skill{}
	global, err := scanSkillDir(filepath.Join(s.root, "skills"), "global")
	if err != nil {
		return nil, err
	}
	mergeSkills(byName, global)
	if strings.TrimSpace(workspaceRoot) != "" {
		local, err := scanSkillDir(filepath.Join(workspaceRoot, ".liora", "skills"), "workspace")
		if err != nil {
			return nil, err
		}
		mergeSkills(byName, local)
	}
	skills := make([]Skill, 0, len(byName))
	for _, skill := range byName {
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills, nil
}

func mergeSkills(byName map[string]Skill, skills []Skill) {
	for _, skill := range skills {
		if existing, ok := byName[skill.Name]; ok && existing.Source == "workspace" {
			continue
		}
		byName[skill.Name] = skill
	}
}

func (s *Store) ReadSkill(workspaceRoot string, name string, startLine int, lineCount int) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("skill name is required")
	}
	skill, ok, err := s.findSkill(workspaceRoot, name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("skill %q not found", name)
	}
	return readNumberedSkillRange(skill.Path, startLine, lineCount)
}

func (s *Store) findSkill(workspaceRoot string, name string) (Skill, bool, error) {
	skills, err := s.ScanSkills(workspaceRoot)
	if err != nil {
		return Skill{}, false, err
	}
	var globalMatch *Skill
	for i := range skills {
		if skills[i].Name != name {
			continue
		}
		if skills[i].Source == "workspace" {
			return skills[i], true, nil
		}
		copy := skills[i]
		globalMatch = &copy
	}
	if globalMatch != nil {
		return *globalMatch, true, nil
	}
	return Skill{}, false, nil
}

func scanSkillDir(root string, source string) ([]Skill, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "SKILL.md")
		if _, err := checkedSkillPath(root, path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		skill, err := parseSkillFile(entry.Name(), root, path, source)
		if err != nil {
			return nil, err
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

func parseSkillFile(name string, root string, path string, source string) (Skill, error) {
	header, err := readSkillHeader(root, path)
	if err != nil {
		return Skill{}, err
	}
	skill := parseSkill(name, path, header, source)
	skill.Body = ""
	return skill, nil
}

func readSkillHeader(root string, path string) (string, error) {
	file, err := openCheckedSkillFile(root, path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	buffer := make([]byte, maxSkillHeaderBytes)
	n, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return string(buffer[:n]), nil
}

func parseSkill(name string, path string, body string, source string) Skill {
	skill := Skill{Name: name, Title: name, Path: path, Body: body, Source: source}
	lines := strings.Split(body, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for _, line := range lines[1:] {
			line = strings.TrimSpace(line)
			if line == "---" {
				break
			}
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			switch strings.TrimSpace(strings.ToLower(key)) {
			case "name", "title":
				skill.Title = strings.TrimSpace(value)
			case "description":
				skill.Description = strings.TrimSpace(value)
			}
		}
		return skill
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			skill.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			break
		}
	}
	return skill
}

func readNumberedSkillRange(path string, startLine int, lineCount int) (string, error) {
	root := filepath.Dir(filepath.Dir(path))
	file, err := openCheckedSkillFile(root, path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if startLine < 1 {
		startLine = 1
	}
	if lineCount <= 0 || lineCount > maxSkillReadLines {
		lineCount = maxSkillReadLines
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxSkillReadBytes)
	var builder strings.Builder
	currentLine := 0
	writtenLines := 0
	for scanner.Scan() {
		currentLine++
		if currentLine < startLine {
			continue
		}
		if writtenLines >= lineCount {
			break
		}
		next := fmt.Sprintf("%d\t%s\n", currentLine, scanner.Text())
		if builder.Len()+len(next) > maxSkillReadBytes {
			builder.WriteString("[...truncated]\n")
			break
		}
		builder.WriteString(next)
		writtenLines++
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func openCheckedSkillFile(root string, path string) (*os.File, error) {
	checked, err := checkedSkillPath(root, path)
	if err != nil {
		return nil, err
	}
	return os.Open(checked)
}

func checkedSkillPath(root string, path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("skill file symlink is not allowed: %s", path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("skill file is not a regular file: %s", path)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil {
		return "", err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", fmt.Errorf("skill file escapes skill root: %s", path)
	}
	return resolvedPath, nil
}
