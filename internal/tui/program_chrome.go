package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	chromeHotStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("222")).Bold(true)
	chromeCardBorderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	chromeInputBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	chromeWelcomeLogoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("149")).Bold(true)
	chromeWelcomeTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
)

func (m *model) headerView() string {
	if m.isTranscriptEmpty() {
		return ""
	}
	width := m.vp.Width()
	if width <= 0 {
		width = 80
	}
	left := chromeTitleStyle.Render(brandInline())
	right := chromePillStyle.Render(m.statusLabel())
	top := joinEdge(left, right, width)
	return strings.Join([]string{
		top,
		chromeRuleStyle.Render(strings.Repeat("─", width)),
	}, "\n")
}

func (m *model) welcomeCardView() string {
	width := m.vp.Width()
	if width <= 0 {
		width = 80
	}
	if width < 24 {
		return truncateCells(brandInline()+"  "+valueOr(m.cfg.Workspace, "workspace"), width)
	}
	innerWidth := width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}
	top := chromeCardBorderStyle.Render("╭" + strings.Repeat("─", innerWidth) + "╮")
	bottom := chromeCardBorderStyle.Render("╰" + strings.Repeat("─", innerWidth) + "╯")
	empty := cardLine("", innerWidth)
	logo := chromeWelcomeLogoStyle.Render("▟██▙")
	title := chromeWelcomeTextStyle.Render("Welcome to Liora")
	tagline := mutedStyle.Render("Ask, inspect, patch, then apply.")
	lines := []string{
		top,
		empty,
		cardLine("  "+logo+"  "+title, innerWidth),
		cardLine("        "+tagline, innerWidth),
		empty,
		cardLine("  "+metaItem("Directory:", valueOr(m.cfg.Workspace, "-")), innerWidth),
		cardLine("  "+metaItem("Model:", valueOr(m.cfg.Model, "scripted")), innerWidth),
		cardLine("  "+metaItem("Core:", valueOr(m.cfg.Core, "-")), innerWidth),
		cardLine("  "+metaItem("Safety:", valueOr(m.cfg.Safety, "-")), innerWidth),
		empty,
		cardLine("  "+chromeHotStyle.Render("/help")+" commands  "+chromeHotStyle.Render("/diff")+" review  "+chromeHotStyle.Render("/apply")+" write", innerWidth),
		empty,
		bottom,
	}
	return strings.Join(lines, "\n")
}

func cardLine(content string, innerWidth int) string {
	content = truncateCells(content, innerWidth)
	padding := innerWidth - lipgloss.Width(content)
	if padding < 0 {
		padding = 0
	}
	return chromeCardBorderStyle.Render("│") + content + strings.Repeat(" ", padding) + chromeCardBorderStyle.Render("│")
}

func (m *model) statusLine() string {
	width := m.vp.Width()
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
