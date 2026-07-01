package tui

import (
	"fmt"
	"strings"
)

type patchStats struct {
	files   []string
	added   int
	removed int
}

func PatchReadyReply(diff string) string {
	stats := summarizePatch(diff)
	if len(stats.files) == 0 {
		return "已准备好变更，但还没有写入真实工作区。"
	}
	lines := []string{
		fmt.Sprintf("已准备好变更：%d 个文件，还没有写入真实工作区。", len(stats.files)),
		fmt.Sprintf("变更统计: +%d -%d", stats.added, stats.removed),
		"文件:",
	}
	for _, file := range stats.files {
		lines = append(lines, "- "+file)
	}
	lines = append(lines, "", "下一步: /diff review，/apply 应用。")
	return strings.Join(lines, "\n")
}

func PatchReadyNextAction() string {
	return strings.Join([]string{
		"请先用 /diff review 变更。",
		"/apply  应用全部变更到真实工作区",
		"/diff   查看结构化 diff 预览",
		"继续输入新任务或 /exit 会保留为未应用状态",
	}, "\n")
}

func PatchReviewPreview(diff string, maxLines int) string {
	var lines []string
	currentFile := ""
	omitted := 0
	for _, raw := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		switch {
		case strings.HasPrefix(raw, "+++ "):
			path := cleanDiffPath(strings.TrimSpace(strings.TrimPrefix(raw, "+++ ")))
			if path != "" && path != "/dev/null" && path != currentFile {
				currentFile = path
				lines = appendPreviewLine(lines, "", maxLines, &omitted)
				lines = appendPreviewLine(lines, currentFile, maxLines, &omitted)
			}
		case strings.HasPrefix(raw, "--- "), strings.HasPrefix(raw, "@@"):
			continue
		case strings.HasPrefix(raw, "+"):
			lines = appendPreviewLine(lines, "+ "+strings.TrimPrefix(raw, "+"), maxLines, &omitted)
		case strings.HasPrefix(raw, "-"):
			lines = appendPreviewLine(lines, "- "+strings.TrimPrefix(raw, "-"), maxLines, &omitted)
		}
	}
	lines = trimLeadingBlank(lines)
	if omitted > 0 {
		lines = append(lines, fmt.Sprintf("... %d more changed lines omitted", omitted))
	}
	if len(lines) == 0 {
		return "变更预览: 无可显示的行级变更。"
	}
	return "变更预览:\n" + strings.Join(lines, "\n")
}

func appendPreviewLine(lines []string, line string, maxLines int, omitted *int) []string {
	if maxLines > 0 && len(lines) >= maxLines {
		*omitted = *omitted + 1
		return lines
	}
	if len(line) > 180 {
		line = line[:177] + "..."
	}
	return append(lines, line)
}

func trimLeadingBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	return lines
}

func summarizePatch(diff string) patchStats {
	stats := patchStats{}
	seen := map[string]bool{}
	oldPath := ""
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "--- "):
			oldPath = cleanDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "--- ")))
		case strings.HasPrefix(line, "+++ "):
			path := cleanDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
			if path == "/dev/null" {
				path = oldPath
			}
			if path != "" && path != "/dev/null" && !seen[path] {
				stats.files = append(stats.files, path)
				seen[path] = true
			}
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			stats.added++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			stats.removed++
		}
	}
	return stats
}

func cleanDiffPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return path
}
