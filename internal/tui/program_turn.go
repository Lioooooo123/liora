package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *model) submitLine(line string) tea.Cmd {
	if line == "" {
		return nil
	}
	if m.running && !isControlCommandDuringRun(line) {
		m.pending = append(m.pending, line)
		m.appendSection("Queue", "Task is running. Queued for the next turn: "+line)
		return nil
	}
	switch line {
	case "/exit", "/quit":
		m.quitting = true
		return tea.Quit
	case "/help":
		m.appendSection("Help", helpText())
		return nil
	}
	if strings.HasPrefix(line, "/") {
		if m.commands == nil {
			m.appendSection("System", "Unknown command. Use /help to view available commands.")
			return nil
		}
		return m.runCommand(line)
	}
	if m.running {
		m.appendSection("System", "Task is still running. Use /cancel, /approve, /deny, or wait for it to finish.")
		return nil
	}
	m.appendUserMessage(line)
	m.running = true
	m.turnVisible = false
	m.clearAssistantDeltaStream()
	m.lastStatus = "working"
	m.nextAction = "/cancel to stop"
	return tea.Batch(m.startTurn(line), m.spin.Tick)
}

func (m *model) runCommand(line string) tea.Cmd {
	return func() tea.Msg {
		result, handled, err := m.commands.HandleCommand(m.ctx, line)
		return commandResultMsg{line: line, result: result, handled: handled, err: err}
	}
}

func (m *model) renderCommandResult(msg commandResultMsg) {
	switch {
	case msg.err != nil:
		m.lastStatus = "command error"
		m.appendSection("Error", msg.err.Error())
	case msg.handled:
		m.lastStatus = "command complete"
		m.appendSection(commandResultTitle(msg.line), msg.result)
	default:
		m.appendSection("System", "Unknown command. Use /help to view available commands.")
	}
}

func (m *model) startTurn(input string) tea.Cmd {
	ch := make(chan StreamUpdate, 32)
	done := make(chan error, 1)
	m.streamCh = ch
	m.doneCh = done
	go func() {
		_, err := m.submitter.SubmitStream(m.ctx, input, func(update StreamUpdate) {
			ch <- update
		})
		close(ch)
		done <- err
	}()
	return waitForUpdate(ch, done)
}

func waitForUpdate(ch chan StreamUpdate, done chan error) tea.Cmd {
	return func() tea.Msg {
		update, ok := <-ch
		if !ok {
			return turnDoneMsg{err: <-done}
		}
		return streamUpdateMsg{update: update}
	}
}

func commandResultTitle(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) > 0 && fields[0] == "/apply" {
		return "Assistant"
	}
	return "System"
}
