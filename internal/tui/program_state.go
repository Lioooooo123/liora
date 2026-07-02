package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *model) submitPending() tea.Cmd {
	if len(m.pending) == 0 {
		return nil
	}
	line := m.pending[0]
	m.pending = m.pending[1:]
	return m.submitLine(line)
}

func (m *model) noteStreamUpdate(update StreamUpdate) {
	m.eventCount++
	payload := decodeEventPayload(update.PayloadJSON)
	switch update.Type {
	case "task.planning":
		m.lastStatus = "thinking"
		m.nextAction = "/cancel to stop"
	case "sandbox.workspace", "sandbox.run":
		m.lastStatus = "preparing"
		m.nextAction = "waiting for model"
	case "task.plan_ready":
		m.lastStatus = "planned"
		m.nextAction = "running tools"
	case "tool.call":
		m.lastStatus = "running tool"
		if payload.Tool != "" {
			m.nextAction = payload.Tool
		}
	case "task.diff":
		m.lastStatus = "diff ready"
		m.nextAction = "/apply after review"
	case "permission.requested":
		m.lastStatus = "needs approval"
		m.nextAction = "/approve or /deny"
	case "task.completed":
		m.lastStatus = "completed"
		m.nextAction = "continue or /timeline"
	case "task.cancelled":
		m.lastStatus = "cancelled"
		m.nextAction = "continue"
	case "task.error":
		m.lastStatus = "error"
		m.nextAction = "/tail for details"
	}
}

func isControlCommandDuringRun(line string) bool {
	command := strings.Fields(strings.TrimSpace(line))
	if len(command) == 0 {
		return false
	}
	switch command[0] {
	case "/cancel", "/approvals", "/approve", "/deny", "/diff", "/todo", "/tail", "/timeline", "/workbench", "/watch":
		return true
	default:
		return false
	}
}
