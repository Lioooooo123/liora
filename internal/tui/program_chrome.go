package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	chromeTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("58")).
				Padding(0, 1)
	chromePillStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)
	chromeRuleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("239"))
	chromeHotStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("222")).Bold(true)
)

func (m *model) headerView() string {
	width := m.vp.Width
	if width <= 0 {
		width = 80
	}
	left := chromeTitleStyle.Render("Liora")
	right := chromePillStyle.Render(m.statusLabel())
	top := joinEdge(left, right, width)
	meta := strings.Join([]string{
		metaItem("workspace", compactMiddle(m.cfg.Workspace, 42)),
		metaItem("model", valueOr(m.cfg.Model, "scripted")),
		metaItem("core", valueOr(m.cfg.Core, "-")),
		metaItem("safety", valueOr(m.cfg.Safety, "-")),
	}, mutedStyle.Render("  "))
	commands := strings.Join([]string{
		chromeHotStyle.Render("/help"),
		chromeHotStyle.Render("/workbench"),
		chromeHotStyle.Render("/timeline"),
		chromeHotStyle.Render("/diff"),
		chromeHotStyle.Render("/apply"),
		chromeHotStyle.Render("/cancel"),
	}, mutedStyle.Render(" "))
	return strings.Join([]string{
		top,
		truncateCells(meta, width),
		truncateCells(commands, width),
		chromeRuleStyle.Render(strings.Repeat("─", width)),
	}, "\n")
}

func (m *model) statusLine() string {
	width := m.vp.Width
	if width <= 0 {
		width = 80
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
	right := chromeHotStyle.Render(valueOr(m.nextAction, "type a request"))
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
	if width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes)+"…") > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
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
