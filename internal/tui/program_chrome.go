package tui

import (
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	chromeTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("149"))
	chromePillStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)
	chromeRuleStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("239"))
	chromeRailStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	chromeHotStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	chromeWarnStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	chromeInputBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	chromePanelStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
)

func (m *model) headerView() string {
	width := m.viewportWidth()
	left := chromeTitleStyle.Render(brandPrompt())
	if !m.running && m.lastStatus == "ready" {
		return truncateCells(left, width)
	}
	right := chromePillStyle.Render(m.statusLabel())
	return joinEdge(left, right, width)
}

func (m *model) workbenchView() string {
	width := m.viewportWidth()
	return strings.Join(nonEmpty([]string{
		m.welcomePanelView(width),
		"",
		goalModeView(width),
	}), "\n")
}

func (m *model) welcomePanelView(width int) string {
	if width < 24 {
		return truncateCells(brandPrompt()+" ready", width)
	}
	innerWidth := width - 2
	top := chromePanelStyle.Render("╭" + strings.Repeat("─", innerWidth) + "╮")
	bottom := chromePanelStyle.Render("╰" + strings.Repeat("─", innerWidth) + "╯")
	bodyWidth := innerWidth
	var body []string
	if width >= 68 {
		body = m.welcomePanelWideLines(bodyWidth)
	} else {
		body = m.welcomePanelCompactLines(bodyWidth)
	}
	lines := []string{top}
	for _, line := range body {
		lines = append(lines, panelLine(line, bodyWidth))
	}
	lines = append(lines, bottom)
	return strings.Join(lines, "\n")
}

func (m *model) welcomePanelWideLines(width int) []string {
	avatar := brandAvatarLines()
	gap := "    "
	text := []string{
		chromeHotStyle.Render("Welcome to Liora"),
		mutedStyle.Render("Local-first coding agent. Configure a model, then start a turn."),
		"",
		metaLine("Directory", valueOr(m.cfg.Workspace, "-")),
		metaLine("Session", "fresh, no previous transcript injected"),
		metaLine("Model", modelStateText(m.cfg.Model)),
		metaLine("Runtime", strings.TrimSpace(valueOr(m.cfg.Core, "-")+"  "+valueOr(m.cfg.Safety, "-"))),
		shortcutCompactLine(),
	}
	contentWidth := width - 6
	if contentWidth < 1 {
		contentWidth = width
	}
	lines := []string{""}
	for i := 0; i < len(text); i++ {
		left := strings.Repeat(" ", 4+lipgloss.Width(avatar[0])) + gap
		if i < len(avatar) {
			left = "    " + avatar[i] + gap
		}
		line := left + text[i]
		lines = append(lines, truncateCells(line, contentWidth))
	}
	return lines
}

func (m *model) welcomePanelCompactLines(width int) []string {
	lines := []string{
		" " + brandInline(),
		" " + mutedStyle.Render("fresh session, no previous transcript injected"),
		" " + metaLine("Directory", compactMiddle(valueOr(m.cfg.Workspace, "-"), max(8, width-14))),
		" " + metaLine("Model", modelStateText(m.cfg.Model)),
	}
	for _, line := range shortcutLines() {
		lines = append(lines, " "+line)
	}
	return lines
}

func panelLine(content string, width int) string {
	return chromePanelStyle.Render("│") + truncateCells(content, width) + strings.Repeat(" ", max(0, width-lipgloss.Width(content))) + chromePanelStyle.Render("│")
}

func metaLine(key string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "-"
	}
	return mutedStyle.Render(key+": ") + metadataStyle.Render(value)
}

func modelStateText(model string) string {
	if strings.TrimSpace(model) == "" || strings.TrimSpace(model) == "scripted" {
		return chromeWarnStyle.Render("not set, run /model or configure LIORA_LLM_*")
	}
	return metadataStyle.Render(model)
}

func shortcutLines() []string {
	return []string{
		mutedStyle.Render("Sessions: ") +
			commandStyle.Render("/sessions") + mutedStyle.Render("  ") +
			commandStyle.Render("/resume-latest"),
		mutedStyle.Render("New:      ") +
			commandStyle.Render("/new-session"),
		mutedStyle.Render("Context:  ") +
			commandStyle.Render("/context") + mutedStyle.Render("  ") +
			commandStyle.Render("/status") + mutedStyle.Render("  ") +
			commandStyle.Render("/help"),
	}
}

func shortcutCompactLine() string {
	return mutedStyle.Render("Shortcuts: ") +
		commandStyle.Render("/sessions") + mutedStyle.Render("  ") +
		commandStyle.Render("/resume-latest") + mutedStyle.Render("  ") +
		commandStyle.Render("/new-session") + mutedStyle.Render("  ") +
		commandStyle.Render("/context") + mutedStyle.Render("  ") +
		commandStyle.Render("/status")
}

func goalModeView(width int) string {
	title := chromeHotStyle.Render("✦ Goal Mode")
	first := title + " " + metadataStyle.Render("use "+commandStyle.Render("/goal set <outcome>")+" to keep multi-turn work focused")
	second := mutedStyle.Render("  Best for multi-step tasks with a clear, verifiable finish line")
	return strings.Join([]string{
		truncateCells(first, width),
		truncateCells(second, width),
	}, "\n")
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
	if !m.running && len(m.pending) == 0 && m.eventCount == 0 && m.lastStatus == "ready" {
		return m.idleFooterLine(width)
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

func (m *model) idleFooterLine(width int) string {
	left := compactWorkspace(m.cfg.Workspace, max(8, width/4))
	middle := mutedStyle.Render("type a request  |  /compact compresses long context")
	right := metadataStyle.Render("context: fresh")
	available := width - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if available < 8 {
		return truncateCells(left+"  "+right, width)
	}
	middle = truncateCells(middle, available)
	return joinEdge(left, middle+"  "+right, width)
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

func compactWorkspace(path string, maxCells int) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "-"
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" && strings.HasPrefix(path, home) {
		path = "~" + strings.TrimPrefix(path, home)
	}
	return mutedStyle.Render(compactMiddle(path, maxCells))
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
