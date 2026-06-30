package tui

import (
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// RunProgram starts the full-screen Bubble Tea TUI. It is used when stdin/stdout
// are attached to a real terminal; non-TTY callers use the line-based App.Run.
func RunProgram(ctx context.Context, cfg Config, submitter StreamingSubmitter) error {
	m := newModel(ctx, cfg, submitter)
	program := tea.NewProgram(m, tea.WithAltScreen())
	go func() {
		<-ctx.Done()
		program.Quit()
	}()
	_, err := program.Run()
	return err
}

type streamUpdateMsg struct{ update StreamUpdate }

type turnDoneMsg struct{ err error }

type commandResultMsg struct {
	result  string
	handled bool
	err     error
}

type model struct {
	ctx       context.Context
	cfg       Config
	submitter StreamingSubmitter
	commands  CommandHandler

	vp    viewport.Model
	input textinput.Model
	spin  spinner.Model
	body  strings.Builder

	running    bool
	ready      bool
	quitting   bool
	pending    []string
	eventCount int
	lastStatus string
	nextAction string

	streamCh chan StreamUpdate
	doneCh   chan error
}

func newModel(ctx context.Context, cfg Config, submitter StreamingSubmitter) *model {
	input := textinput.New()
	input.Placeholder = "Type a request, or /help for commands"
	input.Prompt = promptStyle.Render("liora") + " > "
	input.Focus()

	spin := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(mutedStyle))

	return &model{
		ctx:        ctx,
		cfg:        cfg,
		submitter:  submitter,
		commands:   cfg.Commands,
		vp:         viewport.New(0, 0),
		input:      input,
		spin:       spin,
		lastStatus: "ready",
		nextAction: "type a request",
	}
}

func (m *model) Init() tea.Cmd {
	return textinput.Blink
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyEnter:
			line := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			cmd := m.submitLine(line)
			if m.quitting {
				return m, tea.Quit
			}
			return m, cmd
		}
	case streamUpdateMsg:
		m.noteStreamUpdate(msg.update)
		m.appendBlock(func(w io.Writer) { RenderStreamUpdate(w, msg.update) })
		cmds = append(cmds, waitForUpdate(m.streamCh, m.doneCh))
	case turnDoneMsg:
		m.running = false
		m.streamCh = nil
		m.doneCh = nil
		if msg.err != nil {
			m.lastStatus = "error"
			m.appendBlock(func(w io.Writer) { renderSection(w, "Error", msg.err.Error()) })
		}
		if cmd := m.submitPending(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case commandResultMsg:
		m.renderCommandResult(msg)
	case spinner.TickMsg:
		if m.running {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	m.vp, cmd = m.vp.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m *model) View() string {
	if m.quitting {
		return ""
	}
	return m.headerView() + "\n" + m.vp.View() + "\n" + m.footer()
}

func (m *model) footer() string {
	status := m.statusLine()
	input := m.input.View()
	if m.running {
		status = m.spin.View() + " " + status
	}
	return status + "\n" + input
}

func (m *model) resize(width, height int) {
	m.vp.Width = width
	viewportHeight := height - lipgloss.Height(m.headerView()) - lipgloss.Height(m.footer())
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	m.vp.Height = viewportHeight
	inputWidth := width - lipgloss.Width(m.input.Prompt) - 1
	if inputWidth > 0 {
		m.input.Width = inputWidth
	}
	m.ready = true
	m.refresh()
}

func (m *model) submitLine(line string) tea.Cmd {
	if line == "" {
		return nil
	}
	if m.running && !isControlCommandDuringRun(line) {
		m.pending = append(m.pending, line)
		m.appendBlock(func(w io.Writer) {
			renderSection(w, "Queue", "Task is running. Queued for the next turn: "+line)
		})
		return nil
	}
	switch line {
	case "/exit", "/quit":
		m.quitting = true
		return tea.Quit
	case "/help":
		m.appendBlock(func(w io.Writer) { renderSection(w, "Help", helpText()) })
		return nil
	}
	if strings.HasPrefix(line, "/") {
		if m.commands == nil {
			m.appendBlock(func(w io.Writer) {
				renderSection(w, "System", "Unknown command. Use /help to view available commands.")
			})
			return nil
		}
		return m.runCommand(line)
	}
	if m.running {
		m.appendBlock(func(w io.Writer) {
			renderSection(w, "System", "Task is still running. Use /cancel, /approve, /deny, or wait for it to finish.")
		})
		return nil
	}
	m.running = true
	m.lastStatus = "working"
	m.nextAction = "/cancel stops the run"
	m.appendBlock(func(w io.Writer) { renderLogLine(w, "task", "started") })
	return tea.Batch(m.startTurn(line), m.spin.Tick)
}

func (m *model) runCommand(line string) tea.Cmd {
	return func() tea.Msg {
		result, handled, err := m.commands.HandleCommand(m.ctx, line)
		return commandResultMsg{result: result, handled: handled, err: err}
	}
}

func (m *model) renderCommandResult(msg commandResultMsg) {
	switch {
	case msg.err != nil:
		m.lastStatus = "command error"
		m.appendBlock(func(w io.Writer) { renderSection(w, "Error", msg.err.Error()) })
	case msg.handled:
		m.lastStatus = "command complete"
		m.appendBlock(func(w io.Writer) { renderSection(w, "System", msg.result) })
	default:
		m.appendBlock(func(w io.Writer) {
			renderSection(w, "System", "Unknown command. Use /help to view available commands.")
		})
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

func (m *model) appendBlock(render func(io.Writer)) {
	var buf bytes.Buffer
	render(&buf)
	if buf.Len() == 0 {
		return
	}
	m.body.WriteString(buf.String())
	m.refresh()
}

func (m *model) refresh() {
	m.vp.SetContent(strings.TrimLeft(m.body.String(), "\n"))
	m.vp.GotoBottom()
}
