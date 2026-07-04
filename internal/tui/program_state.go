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
			m.nextAction = strings.TrimSpace(payload.Tool + " " + payload.Input)
			if m.nextAction == "" {
				m.nextAction = payload.Tool
			}
		}
	case "tool.lifecycle":
		if payload.Phase != "" {
			m.lastStatus = "tool " + payload.Phase
		} else {
			m.lastStatus = "tool"
		}
		if access := formatToolLifecycleAccess(payload); access != "" {
			m.nextAction = strings.TrimSpace(payload.Tool + " " + access)
		} else if payload.Tool != "" {
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
	return commandCanRunDuringTurn(line)
}
