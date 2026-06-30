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
	return strings.Join(lines, "\n")
}

func PatchReadyNextAction() string {
	return strings.Join([]string{
		"请先 review 上面的 diff。",
		"/apply  应用全部变更到真实工作区",
		"/diff   重新查看最近一次 diff",
		"继续输入新任务或 /exit 会保留为未应用状态",
	}, "\n")
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
