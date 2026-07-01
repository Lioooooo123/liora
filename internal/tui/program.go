package tui

import (
	"bytes"
	"context"
	"io"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

// RunProgram starts the full-screen Bubble Tea TUI. It is used when stdin/stdout
// are attached to a real terminal; non-TTY callers use the line-based App.Run.
func RunProgram(ctx context.Context, cfg Config, submitter StreamingSubmitter) error {
	m := newModel(ctx, cfg, submitter)
	program := tea.NewProgram(m)
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
	input textarea.Model
	spin  spinner.Model
	body  strings.Builder

	running     bool
	ready       bool
	quitting    bool
	pending     []string
	eventCount  int
	lastStatus  string
	nextAction  string
	turnVisible bool

	streamCh chan StreamUpdate
	doneCh   chan error
}

func newModel(ctx context.Context, cfg Config, submitter StreamingSubmitter) *model {
	input := textarea.New()
	input.Placeholder = "Type a request, or /help for commands"
	input.Prompt = promptStyle.Render("liora") + " > "
	input.ShowLineNumbers = false
	input.EndOfBufferCharacter = ' '
	input.MaxHeight = 1
	input.SetHeight(1)
	input.SetVirtualCursor(false)
	styles := input.Styles()
	styles.Cursor.Shape = tea.CursorBar
	input.SetStyles(styles)
	input.Focus()

	spin := spinner.New(spinner.WithSpinner(spinner.MiniDot))

	return &model{
		ctx:        ctx,
		cfg:        cfg,
		submitter:  submitter,
		commands:   cfg.Commands,
		vp:         viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
		input:      input,
		spin:       spin,
		lastStatus: "ready",
		nextAction: "type a request",
	}
}

func (m *model) Init() tea.Cmd {
	return textarea.Blink
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "enter":
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
		if m.appendBlock(func(w io.Writer) { RenderStreamUpdate(w, msg.update) }) {
			m.turnVisible = true
		}
		cmds = append(cmds, waitForUpdate(m.streamCh, m.doneCh))
	case turnDoneMsg:
		m.running = false
		m.streamCh = nil
		m.doneCh = nil
		if msg.err != nil {
			m.lastStatus = "error"
			m.appendBlock(func(w io.Writer) { renderSection(w, "Error", msg.err.Error()) })
		} else if !m.turnVisible {
			m.appendBlock(func(w io.Writer) { renderSection(w, "Assistant", "Completed.") })
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

const inputBottomGapLines = 2

func (m *model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	header := m.headerView()
	viewport := m.vp.View()
	panel := m.inputPanelView()
	content := joinViewSections(header, viewport, panel)
	view := tea.NewView(content)
	view.AltScreen = true
	if cursor := m.input.Cursor(); cursor != nil {
		cursor.Position.X += 2
		cursor.Position.Y += lipgloss.Height(viewport) + 1
		if strings.TrimSpace(header) != "" {
			cursor.Position.Y += lipgloss.Height(header)
		}
		view.Cursor = cursor
	}
	return view
}

func (m *model) footer() string {
	return m.inputPanelView()
}

func (m *model) inputPanelView() string {
	status := m.statusLine()
	if m.running {
		status = m.spin.View() + " " + status
	}
	lines := []string{m.inputBoxView(), status}
	for range inputBottomGapLines {
		lines = append(lines, " ")
	}
	return strings.Join(lines, "\n")
}

func (m *model) inputBoxView() string {
	width := m.vp.Width()
	if width <= 0 {
		width = 80
	}
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

func (m *model) resize(width, height int) {
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
	m.appendUserMessage(line)
	m.running = true
	m.turnVisible = false
	m.lastStatus = "working"
	m.nextAction = "/cancel to stop"
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

func (m *model) appendBlock(render func(io.Writer)) bool {
	var buf bytes.Buffer
	render(&buf)
	if buf.Len() == 0 {
		return false
	}
	m.body.WriteString(buf.String())
	m.refresh()
	return true
}

func (m *model) appendUserMessage(line string) {
	m.appendBlock(func(w io.Writer) { renderSection(w, "You", line) })
}

func (m *model) refresh() {
	if m.isTranscriptEmpty() {
		m.vp.SetContent(m.welcomeCardView())
		m.vp.GotoTop()
		return
	}
	m.vp.SetContent(strings.TrimLeft(m.body.String(), "\n"))
	m.vp.GotoBottom()
}

func (m *model) isTranscriptEmpty() bool {
	return strings.TrimSpace(m.body.String()) == ""
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
