package tui

import "strings"

func (m *model) refresh() {
	wasAtBottom := m.vp.AtBottom()
	offset := m.vp.YOffset()
	m.rebuildBody()
	if m.isTranscriptEmpty() {
		m.vp.SetContent(m.workbenchView())
		m.vp.GotoTop()
		return
	}
	m.vp.SetContent(strings.TrimLeft(m.body.String(), "\n"))
	if wasAtBottom {
		m.vp.GotoBottom()
		return
	}
	m.vp.SetYOffset(offset)
}

func (m *model) rebuildBody() {
	if len(m.blocks) == 0 {
		return
	}
	m.body.Reset()
	for _, block := range m.blocks {
		block(&m.body, m.vp.Width())
	}
}

func (m *model) isTranscriptEmpty() bool {
	return strings.TrimSpace(m.body.String()) == ""
}
