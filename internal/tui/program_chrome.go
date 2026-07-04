package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	chromeTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("230"))
	chromePillStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)
	chromeRuleStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("239"))
	chromeRailStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	chromeHotStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("222")).Bold(true)
	chromeInputBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

func (m *model) headerView() string {
	width := m.viewportWidth()
	left := chromeTitleStyle.Render(brandInline()) + mutedStyle.Render(" workbench")
	right := chromePillStyle.Render(m.statusLabel())
	top := joinEdge(left, right, width)
	return strings.Join([]string{
		top,
		chromeRuleStyle.Render(strings.Repeat("─", width)),
	}, "\n")
}

func (m *model) workbenchView() string {
	width := m.viewportWidth()
	lines := []string{
		workbenchLine("› ", chromeHotStyle.Render("ready for work"), width),
		workbenchFieldLine("workspace", valueOr(m.cfg.Workspace, "-"), width),
		workbenchFieldLine("model", valueOr(m.cfg.Model, "scripted"), width),
		workbenchPairLine("core", valueOr(m.cfg.Core, "-"), "safety", valueOr(m.cfg.Safety, "-"), width),
		workbenchLine("› ", mutedStyle.Render("actions"), width),
		workbenchRailLine(chromeHotStyle.Render("/help")+" commands  "+chromeHotStyle.Render("/diff")+" review  "+chromeHotStyle.Render("/apply")+" write", width),
		workbenchRailLine(mutedStyle.Render("patch-first workspace, no active task"), width),
		workbenchLine("  ", mutedStyle.Render("ready for request"), width),
	}
	return strings.Join(lines, "\n")
}

func workbenchFieldLine(key string, value string, width int) string {
	return workbenchRailLine(metaItem(key, value), width)
}

func workbenchPairLine(leftKey string, leftValue string, rightKey string, rightValue string, width int) string {
	prefix := chromeRailStyle.Render("  ")
	contentWidth := width - lipgloss.Width(prefix)
	if contentWidth < 1 {
		return truncateCells(prefix, width)
	}
	content := joinEdge(metaItem(leftKey, leftValue), metaItem(rightKey, rightValue), contentWidth)
	return prefix + content
}

func workbenchLine(prefix string, content string, width int) string {
	prefix = chromeRailStyle.Render(prefix)
	contentWidth := width - lipgloss.Width(prefix)
	if contentWidth < 1 {
		return truncateCells(prefix, width)
	}
	return prefix + truncateCells(content, contentWidth)
}

func workbenchRailLine(content string, width int) string {
	return workbenchLine("  ", content, width)
}

func (m *model) statusLine() string {
	return m.statusLineForWidth(m.viewportWidth())
}

func (m *model) statusLineForWidth(width int) string {
	if width < 1 {
		return ""
	}
	pending := ""
	if len(m.pending) > 0 {
		pending = "pending " + strconv.Itoa(len(m.pending))
	}
	left := strings.Join(nonEmpty([]string{
		statusDot(m.lastStatus),
		mutedStyle.Render(m.lastStatus),
		metaItem("events", strconv.Itoa(m.eventCount)),
		pending,
	}), mutedStyle.Render("  "))
	rightText := valueOr(m.nextAction, "type a request")
	available := width - lipgloss.Width(left) - 2
	if available > 0 {
		rightText = truncateCells(rightText, available)
	}
	right := chromeHotStyle.Render(rightText)
	return joinEdge(left, right, width)
}

func (m *model) statusLabel() string {
	if m.running {
		return "working"
	}
	return valueOr(m.lastStatus, "ready")
}

func metaItem(key string, value string) string {
	return labelStyle.Render(key) + " " + metadataStyle.Render(value)
}

func statusDot(status string) string {
	switch status {
	case "error", "command error":
		return errStyle.Render("●")
	case "needs approval", "diff ready", "cancelled":
		return warnStyle.Render("●")
	default:
		return okStyle.Render("●")
	}
}

func joinEdge(left string, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return truncateCells(left+" "+right, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

func truncateCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}

func compactMiddle(value string, maxCells int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxCells <= 0 || lipgloss.Width(value) <= maxCells {
		return value
	}
	runes := []rune(value)
	if len(runes) <= 3 {
		return truncateCells(value, maxCells)
	}
	head := maxCells / 2
	tail := maxCells - head - 1
	if head < 1 || tail < 1 || len(runes) <= head+tail {
		return truncateCells(value, maxCells)
	}
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}

func valueOr(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func nonEmpty(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}
