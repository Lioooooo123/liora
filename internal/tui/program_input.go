package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const inputBottomGapLines = 2

func (m *model) footer() string {
	return m.inputPanelView()
}

func (m *model) inputPanelView() string {
	width := m.viewportWidth()
	status := m.statusLineForWidth(width)
	if m.running {
		prefix := m.spin.View() + " "
		statusWidth := width - lipgloss.Width(prefix)
		if statusWidth < 1 {
			status = truncateCells(prefix, width)
		} else {
			status = prefix + m.statusLineForWidth(statusWidth)
		}
	}
	lines := make([]string, 0, 4)
	if palette := m.completionPaletteView(width); palette != "" {
		lines = append(lines, palette)
	}
	lines = append(lines, m.inputBoxView())
	lines = append(lines, status)
	for range inputBottomGapLines {
		lines = append(lines, " ")
	}
	return strings.Join(lines, "\n")
}

func (m *model) inputBoxView() string {
	width := m.viewportWidth()
	if width < 4 {
		return m.input.View()
	}
	innerWidth := width - 2
	top := chromeInputBorderStyle.Render("╭" + strings.Repeat("─", innerWidth) + "╮")
	bottom := chromeInputBorderStyle.Render("╰" + strings.Repeat("─", innerWidth) + "╯")
	content := " " + m.input.View()
	padding := innerWidth - lipgloss.Width(content)
	if padding < 0 {
		padding = 0
	}
	middle := chromeInputBorderStyle.Render("│") + content + strings.Repeat(" ", padding) + chromeInputBorderStyle.Render("│")
	return strings.Join([]string{top, middle, bottom}, "\n")
}

func (m *model) viewportWidth() int {
	width := m.vp.Width()
	if width <= 0 {
		return 80
	}
	return width
}

func (m *model) resize(width, height int) {
	m.width = width
	m.height = height
	m.vp.SetWidth(width)
	viewportHeight := height - lipgloss.Height(m.headerView()) - lipgloss.Height(m.footer())
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	m.vp.SetHeight(viewportHeight)
	inputWidth := width - 4
	if inputWidth > 0 {
		m.input.SetWidth(inputWidth)
	}
	m.ready = true
	m.refresh()
}
