package tui

import (
	"bytes"
	"io"
	"strings"
)

type transcriptBlock func(io.Writer, int)

func (m *model) appendBlock(render func(io.Writer)) bool {
	return m.appendWidthBlock(func(w io.Writer, _ int) { render(w) })
}

func (m *model) appendWidthBlock(render transcriptBlock) bool {
	var buf bytes.Buffer
	render(&buf, m.vp.Width())
	if buf.Len() == 0 {
		return false
	}
	if len(m.blocks) == 0 && m.body.Len() > 0 {
		existing := m.body.String()
		m.blocks = append(m.blocks, func(w io.Writer, _ int) {
			io.WriteString(w, existing)
		})
	}
	m.blocks = append(m.blocks, render)
	m.refresh()
	return true
}

func (m *model) appendStreamUpdate(update StreamUpdate) bool {
	section := FormatDaemonEventUpdate(update)
	if !section.Visible {
		return false
	}
	if section.Title == "Error" {
		m.turnErrorVisible = true
	}
	if update.Type == daemonEventAssistantDelta {
		return m.appendAssistantDelta(section.Body)
	}
	if update.Type == daemonEventSummary && m.assistantDeltaBlock >= 0 {
		return m.finalizeAssistantDelta(section.Body)
	}
	m.clearAssistantDeltaStream()
	return m.appendSection(section.Title, section.Body)
}

func (m *model) appendAssistantDelta(delta string) bool {
	if delta == "" {
		return false
	}
	if m.assistantDeltaBlock < 0 || m.assistantDeltaBlock >= len(m.blocks) {
		m.assistantDeltaText.Reset()
		m.assistantDeltaText.WriteString(delta)
		m.assistantDeltaBlock = len(m.blocks)
		return m.appendWidthBlock(m.assistantDeltaRenderBlock())
	}
	m.assistantDeltaText.WriteString(delta)
	m.blocks[m.assistantDeltaBlock] = m.assistantDeltaRenderBlock()
	m.refresh()
	return true
}

func (m *model) assistantDeltaRenderBlock() transcriptBlock {
	text := m.assistantDeltaText.String()
	return func(w io.Writer, width int) {
		renderSectionWithWidth(w, "Assistant", text, width)
	}
}

func (m *model) finalizeAssistantDelta(summary string) bool {
	if strings.TrimSpace(summary) == "" {
		m.clearAssistantDeltaStream()
		return false
	}
	if m.assistantDeltaBlock < 0 || m.assistantDeltaBlock >= len(m.blocks) {
		m.clearAssistantDeltaStream()
		return m.appendSection("Assistant", summary)
	}
	m.assistantDeltaText.Reset()
	m.assistantDeltaText.WriteString(summary)
	m.blocks[m.assistantDeltaBlock] = m.assistantDeltaRenderBlock()
	m.refresh()
	m.clearAssistantDeltaStream()
	return true
}

func (m *model) clearAssistantDeltaStream() {
	m.assistantDeltaText.Reset()
	m.assistantDeltaBlock = -1
}

func (m *model) appendSection(title string, body string) bool {
	return m.appendWidthBlock(func(w io.Writer, width int) {
		renderSectionWithWidth(w, title, body, width)
	})
}

func (m *model) appendUserMessage(line string) {
	m.appendSection("You", line)
}

func joinViewSections(sections ...string) string {
	visible := make([]string, 0, len(sections))
	for _, section := range sections {
		if strings.TrimSpace(section) != "" {
			visible = append(visible, section)
		}
	}
	return strings.Join(visible, "\n")
}
