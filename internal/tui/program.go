package tui

import (
	"context"
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
	line    string
	result  string
	handled bool
	err     error
}

type model struct {
	ctx       context.Context
	cfg       Config
	submitter StreamingSubmitter
	commands  CommandHandler

	vp     viewport.Model
	input  textarea.Model
	spin   spinner.Model
	body   strings.Builder
	blocks []transcriptBlock
	width  int
	height int

	running             bool
	ready               bool
	quitting            bool
	pending             []string
	completions         []Completion
	eventCount          int
	lastStatus          string
	nextAction          string
	turnVisible         bool
	assistantDeltaText  strings.Builder
	assistantDeltaBlock int

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
		ctx:                 ctx,
		cfg:                 cfg,
		submitter:           submitter,
		commands:            cfg.Commands,
		vp:                  viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
		input:               input,
		spin:                spin,
		lastStatus:          "ready",
		nextAction:          "type a request",
		assistantDeltaBlock: -1,
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
			m.clearCompletions()
			cmd := m.submitLine(line)
			if m.quitting {
				return m, tea.Quit
			}
			return m, cmd
		case "tab":
			if m.applyCompletion() {
				return m, nil
			}
		case "esc":
			m.clearCompletions()
			return m, nil
		}
	case streamUpdateMsg:
		m.noteStreamUpdate(msg.update)
		if m.appendStreamUpdate(msg.update) {
			m.turnVisible = true
		}
		cmds = append(cmds, waitForUpdate(m.streamCh, m.doneCh))
	case turnDoneMsg:
		m.running = false
		m.streamCh = nil
		m.doneCh = nil
		if msg.err != nil {
			m.lastStatus = "error"
			m.appendSection("Error", msg.err.Error())
		} else if !m.turnVisible {
			m.appendSection("Assistant", "Completed.")
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
	if _, ok := msg.(tea.KeyPressMsg); ok {
		m.refreshCompletions()
		if m.ready {
			m.resize(m.width, m.height)
		}
	}
	m.vp, cmd = m.vp.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

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
	view.MouseMode = tea.MouseModeCellMotion
	if cursor := m.input.Cursor(); cursor != nil {
		cursor.Position.X += 2
		cursor.Position.Y += lipgloss.Height(viewport) + 1
		if palette := m.completionPaletteView(m.viewportWidth()); palette != "" {
			cursor.Position.Y += lipgloss.Height(palette)
		}
		if strings.TrimSpace(header) != "" {
			cursor.Position.Y += lipgloss.Height(header)
		}
		view.Cursor = cursor
	}
	return view
}
