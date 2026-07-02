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

	running     bool
	ready       bool
	quitting    bool
	pending     []string
	completions []Completion
	eventCount  int
	lastStatus  string
	nextAction  string
	turnVisible bool

	streamCh chan StreamUpdate
	doneCh   chan error
}

type transcriptBlock func(io.Writer, int)

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
		if m.appendWidthBlock(func(w io.Writer, width int) { RenderStreamUpdateWithWidth(w, msg.update, width) }) {
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
	m.completions = mergeCompletions(line, builtin, dynamic)
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
		paletteLine("Commands", innerWidth),
	}
	commandRows := completionRows(m.completions, "command", innerWidth)
	if len(commandRows) == 0 {
		commandRows = []string{paletteLine("  /help  show commands", innerWidth)}
	}
	lines = append(lines, commandRows...)
	skillRows := completionRows(m.completions, "skill", innerWidth)
	if len(skillRows) > 0 {
		lines = append(lines, paletteLine("Skills", innerWidth))
		lines = append(lines, skillRows...)
	}
	lines = append(lines,
		paletteLine("Tab accept  Enter run  Esc close", innerWidth),
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
	content = truncateCells(content, innerWidth)
	padding := innerWidth - lipgloss.Width(content)
	if padding < 0 {
		padding = 0
	}
	return chromeInputBorderStyle.Render("│") + content + strings.Repeat(" ", padding) + chromeInputBorderStyle.Render("│")
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

func (m *model) appendSection(title string, body string) bool {
	return m.appendWidthBlock(func(w io.Writer, width int) {
		renderSectionWithWidth(w, title, body, width)
	})
}

func (m *model) appendUserMessage(line string) {
	m.appendSection("You", line)
}

func commandResultTitle(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) > 0 && fields[0] == "/apply" {
		return "Assistant"
	}
	return "System"
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
