package tui

import "strings"

func (m *model) refreshCompletions() {
	line := strings.TrimRight(m.input.Value(), "\r\n")
	if !strings.HasPrefix(line, "/") {
		m.clearCompletions()
		return
	}
	builtin, _ := builtinCompletionProvider{}.Completions(m.ctx, line)
	var dynamic []Completion
	provider := m.cfg.Completions
	if provider == nil {
		if commandProvider, ok := m.commands.(CompletionProvider); ok {
			provider = commandProvider
		}
	}
	if provider != nil {
		items, err := provider.Completions(m.ctx, line)
		if err == nil {
			dynamic = items
		}
	}
	m.completions = mergeCompletions(line, dynamic, builtin)
}

func (m *model) clearCompletions() {
	m.completions = nil
}

func (m *model) applyCompletion() bool {
	m.refreshCompletions()
	if len(m.completions) == 0 {
		return false
	}
	m.input.SetValue(m.completions[0].Value)
	m.input.CursorEnd()
	m.clearCompletions()
	return true
}

func (m *model) completionPaletteView(width int) string {
	if len(m.completions) == 0 || width < 24 {
		return ""
	}
	innerWidth := width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}
	lines := []string{
		chromeInputBorderStyle.Render("╭" + strings.Repeat("─", innerWidth) + "╮"),
		paletteLine(chromeHotStyle.Render("command layer"), width),
		paletteLine(mutedStyle.Render("Commands"), width),
	}
	commandRows := completionRows(m.completions, "command", width)
	if len(commandRows) == 0 {
		commandRows = []string{paletteLine("  /help  show commands", width)}
	}
	lines = append(lines, commandRows...)
	skillRows := completionRows(m.completions, "skill", width)
	if len(skillRows) > 0 {
		lines = append(lines, paletteLine(mutedStyle.Render("Skills"), width))
		lines = append(lines, skillRows...)
	}
	lines = append(lines,
		chromeInputBorderStyle.Render("╰"+strings.Repeat("─", innerWidth)+"╯"),
	)
	return strings.Join(lines, "\n")
}

func completionRows(items []Completion, kind string, width int) []string {
	var rows []string
	for _, item := range items {
		if completionKind(item) != kind {
			continue
		}
		label := completionLabel(item)
		if kind == "skill" {
			label = strings.TrimPrefix(label, "/skill ")
		}
		text := "  " + commandStyle.Render(label)
		if kind == "skill" && strings.TrimSpace(item.Value) != "" && strings.TrimSpace(item.Value) != label {
			text += mutedStyle.Render("  " + strings.TrimSpace(item.Value))
		}
		if description := completionDescription(item); description != "" {
			text += mutedStyle.Render("  " + description)
		}
		rows = append(rows, paletteLine(text, width))
	}
	return rows
}

func completionKind(item Completion) string {
	if strings.TrimSpace(item.Kind) != "" {
		return strings.TrimSpace(item.Kind)
	}
	if strings.HasPrefix(strings.TrimSpace(item.Value), "/skill ") {
		return "skill"
	}
	return "command"
}

func paletteLine(content string, innerWidth int) string {
	return truncateCells("  "+content, innerWidth)
}

func (m *model) completionHint(width int) string {
	if len(m.completions) == 0 || width < 12 {
		return ""
	}
	parts := make([]string, 0, len(m.completions))
	for _, item := range m.completions {
		label := commandStyle.Render(completionLabel(item))
		if description := completionDescription(item); description != "" {
			label += mutedStyle.Render(" " + description)
		}
		parts = append(parts, label)
	}
	return truncateCells(strings.Join(parts, mutedStyle.Render("  ")), width)
}
