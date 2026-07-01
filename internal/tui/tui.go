package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/trace"
	"github.com/charmbracelet/lipgloss"
)

type Config struct {
	Workspace string
	Model     string
	Core      string
	Safety    string
	Commands  CommandHandler
}

type TurnResult struct {
	Answer       string
	PlannedSteps string
	AgentResult  agent.Result
	Events       []trace.Event
}

type TurnView struct {
	Input    string
	ShowUser bool
	TurnResult
}

type Submitter interface {
	Submit(ctx context.Context, input string) (TurnResult, error)
}

type StreamingSubmitter interface {
	SubmitStream(ctx context.Context, input string, onEvent func(StreamUpdate)) (TurnResult, error)
}

type StreamUpdate struct {
	Type        string
	PayloadJSON string
}

type CommandHandler interface {
	HandleCommand(ctx context.Context, line string) (string, bool, error)
}

type CommandHandlerFunc func(ctx context.Context, line string) (string, bool, error)

func (f CommandHandlerFunc) HandleCommand(ctx context.Context, line string) (string, bool, error) {
	return f(ctx, line)
}

type CommandChain []CommandHandler

func (c CommandChain) HandleCommand(ctx context.Context, line string) (string, bool, error) {
	for _, handler := range c {
		if handler == nil {
			continue
		}
		result, handled, err := handler.HandleCommand(ctx, line)
		if handled || err != nil {
			return result, handled, err
		}
	}
	return "", false, nil
}

type SubmitterFunc func(ctx context.Context, input string) (TurnResult, error)

func (f SubmitterFunc) Submit(ctx context.Context, input string) (TurnResult, error) {
	return f(ctx, input)
}

type App struct {
	config    Config
	submitter Submitter
}

var (
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	labelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	promptStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("149")).Bold(true)
	commandStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("183"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	railStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	metadataStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
)

func New(config Config, submitter Submitter) *App {
	return &App{config: config, submitter: submitter}
}

func RenderWelcome(config Config) string {
	model := config.Model
	if model == "" {
		model = "scripted"
	}
	lines := append(brandWelcomeLines(),
		"",
		keyValue("workspace", config.Workspace),
		keyValue("model", model),
	)
	if strings.TrimSpace(config.Core) != "" {
		lines = append(lines, keyValue("core", config.Core))
	}
	if strings.TrimSpace(config.Safety) != "" {
		lines = append(lines, keyValue("safety", config.Safety))
	}
	lines = append(lines,
		"",
		commandStyle.Render("/help")+" commands  "+commandStyle.Render("/workbench")+" workspace  "+commandStyle.Render("/memory")+" memory  "+commandStyle.Render("/exit")+" quit",
		mutedStyle.Render("Patch-first: review changes, then /apply."),
	)
	return renderPanel("Liora", lines) + "\n"
}

func (a *App) Run(ctx context.Context, input io.Reader, output io.Writer) error {
	fmt.Fprint(output, RenderWelcome(a.config))
	if streamer, ok := a.submitter.(StreamingSubmitter); ok {
		return a.runStreaming(ctx, input, output, streamer)
	}
	return a.runBlocking(ctx, input, output)
}

func (a *App) runBlocking(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	for {
		fmt.Fprint(output, "\n"+promptStyle.Render("liora")+" > ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch line {
		case "/exit", "/quit":
			fmt.Fprintln(output, "Bye")
			return nil
		case "/help":
			renderSection(output, "Help", helpText())
			continue
		}
		if strings.HasPrefix(line, "/") && a.config.Commands != nil {
			result, handled, err := a.config.Commands.HandleCommand(ctx, line)
			if err != nil {
				fmt.Fprintf(output, "Error: %v\n", err)
				continue
			}
			if handled {
				renderSection(output, "System", result)
				continue
			}
			renderSection(output, "System", "Unknown command. Use /help to view available commands.")
			continue
		}
		if strings.HasPrefix(line, "/") {
			renderSection(output, "System", "Unknown command. Use /help to view available commands.")
			continue
		}
		if err := a.runTurn(ctx, line, output); err != nil {
			fmt.Fprintf(output, "Error: %v\n", err)
		}
	}
	return scanner.Err()
}

type inputLine struct {
	line string
	ok   bool
	err  error
}

type turnOutcome struct {
	err error
}

func (a *App) runStreaming(ctx context.Context, input io.Reader, output io.Writer, streamer StreamingSubmitter) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := scanInput(ctx, input)
	prompt := func() {
		fmt.Fprint(output, "\n"+promptStyle.Render("liora")+" > ")
	}
	prompt()
	var pending []string
	var running bool
	var turnDone <-chan turnOutcome
	var streamEvents <-chan StreamUpdate
	var inputClosed bool
	var scanErr error
	for {
		if !running && len(pending) > 0 {
			line := pending[0]
			pending = pending[1:]
			exit := a.handleStreamingLine(ctx, line, output, streamer, &running, &turnDone, &streamEvents)
			if exit {
				cancel()
				return nil
			}
			continue
		}
		if inputClosed && !running {
			return scanErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-turnDone:
			running = false
			turnDone = nil
			if streamEvents != nil {
				for event := range streamEvents {
					RenderStreamUpdate(output, event)
				}
			}
			streamEvents = nil
			if result.err != nil {
				fmt.Fprintf(output, "Error: %v\n", result.err)
			}
			if len(pending) == 0 && !inputClosed {
				prompt()
			}
		case event, ok := <-streamEvents:
			if ok {
				RenderStreamUpdate(output, event)
			}
		case scanned := <-lines:
			if !scanned.ok {
				inputClosed = true
				scanErr = scanned.err
				continue
			}
			line := strings.TrimSpace(scanned.line)
			if line == "" {
				if !running {
					prompt()
				}
				continue
			}
			if running && !isRunningCommand(line) {
				pending = append(pending, line)
				continue
			}
			exit := a.handleStreamingLine(ctx, line, output, streamer, &running, &turnDone, &streamEvents)
			if exit {
				cancel()
				return nil
			}
		}
	}
}

func scanInput(ctx context.Context, input io.Reader) <-chan inputLine {
	lines := make(chan inputLine)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			case lines <- inputLine{line: scanner.Text(), ok: true}:
			}
		}
		select {
		case <-ctx.Done():
		case lines <- inputLine{ok: false, err: scanner.Err()}:
		}
	}()
	return lines
}

func (a *App) handleStreamingLine(ctx context.Context, line string, output io.Writer, streamer StreamingSubmitter, running *bool, turnDone *<-chan turnOutcome, streamEvents *<-chan StreamUpdate) bool {
	switch line {
	case "/exit", "/quit":
		fmt.Fprintln(output, "Bye")
		return true
	case "/help":
		renderSection(output, "Help", helpText())
		return false
	}
	if strings.HasPrefix(line, "/") && a.config.Commands != nil {
		result, handled, err := a.config.Commands.HandleCommand(ctx, line)
		if err != nil {
			fmt.Fprintf(output, "Error: %v\n", err)
			return false
		}
		if handled {
			renderSection(output, "System", result)
			return false
		}
		renderSection(output, "System", "Unknown command. Use /help to view available commands.")
		return false
	}
	if strings.HasPrefix(line, "/") {
		renderSection(output, "System", "Unknown command. Use /help to view available commands.")
		return false
	}
	if *running {
		renderSection(output, "System", "Task is still running. Use /cancel, /approve, /deny, or wait for it to finish.")
		return false
	}
	renderSection(output, "You", line)
	a.startStreamingTurn(ctx, line, output, streamer, running, turnDone, streamEvents)
	return false
}

func (a *App) startStreamingTurn(ctx context.Context, input string, output io.Writer, streamer StreamingSubmitter, running *bool, turnDone *<-chan turnOutcome, streamEvents *<-chan StreamUpdate) {
	done := make(chan turnOutcome, 1)
	updates := make(chan StreamUpdate, 32)
	*running = true
	*turnDone = done
	*streamEvents = updates
	go func() {
		_, err := streamer.SubmitStream(ctx, input, func(update StreamUpdate) {
			updates <- update
		})
		close(updates)
		done <- turnOutcome{err: err}
	}()
}

func isRunningCommand(line string) bool {
	return strings.TrimSpace(line) == "/cancel"
}

func (a *App) runTurn(ctx context.Context, input string, output io.Writer) error {
	if streamer, ok := a.submitter.(StreamingSubmitter); ok {
		_, err := streamer.SubmitStream(ctx, input, func(update StreamUpdate) {
			RenderStreamUpdate(output, update)
		})
		return err
	}
	result, err := a.submitter.Submit(ctx, input)
	RenderTurn(output, TurnView{
		Input:      input,
		ShowUser:   true,
		TurnResult: result,
	})
	return err
}

func RenderStreamUpdate(output io.Writer, update StreamUpdate) {
	payload := decodeEventPayload(update.PayloadJSON)
	switch update.Type {
	case "task.planning", "sandbox.run", "sandbox.workspace", "task.plan_ready", "task.replanning", "tool.call":
		return
	case "tool.result":
		if payload.Status != "" && payload.Status != string(trace.StatusOK) {
			renderSection(output, "Error", formatToolEvent(payload))
		}
	case "task.summary":
		if strings.TrimSpace(payload.Message) != "" {
			renderSection(output, "Assistant", payload.Message)
		}
	case "task.diff":
		if strings.TrimSpace(payload.Diff) != "" {
			renderSection(output, "Assistant", PatchReadyReply(payload.Diff))
			renderSection(output, "Diff", strings.TrimRight(payload.Diff, "\n"))
			renderSection(output, "Next", PatchReadyNextAction())
		}
	case "permission.requested":
		body := strings.TrimSpace(payload.Tool + " " + payload.Input)
		if payload.Risk != "" {
			body += "\nRisk: " + payload.Risk
		}
		if payload.Reason != "" {
			body += "\nReason: " + payload.Reason
		}
		body += "\nCommands: /approve to continue, /deny to cancel."
		renderSection(output, "Approval", body)
	case "permission.approved":
		renderSection(output, "Approval", "approved")
	case "permission.denied":
		renderSection(output, "Approval", "denied")
	case "task.completed", "task.cancelled":
		if update.Type == "task.cancelled" {
			status := valueOr(payload.Status, "cancelled")
			if payload.Message != "" {
				status += ": " + payload.Message
			}
			renderSection(output, "System", status)
		}
	case "task.error":
		renderSection(output, "Error", strings.TrimSpace(payload.Message+"\n"+payload.Output))
	}
}

func RenderTurn(output io.Writer, view TurnView) {
	result := view.TurnResult
	if view.ShowUser {
		renderSection(output, "You", view.Input)
	}
	if strings.TrimSpace(result.Answer) != "" {
		renderSection(output, "Assistant", result.Answer)
	}
	if strings.TrimSpace(result.AgentResult.Summary) != "" && strings.TrimSpace(result.Answer) == "" {
		renderSection(output, "Assistant", result.AgentResult.Summary)
	}
	if strings.TrimSpace(result.AgentResult.Diff) != "" {
		renderSection(output, "Assistant", PatchReadyReply(result.AgentResult.Diff))
		renderSection(output, "Diff", strings.TrimRight(result.AgentResult.Diff, "\n"))
		renderSection(output, "Next", PatchReadyNextAction())
	}
}

type eventPayload struct {
	Message string `json:"message,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Input   string `json:"input,omitempty"`
	Output  string `json:"output,omitempty"`
	Status  string `json:"status,omitempty"`
	Steps   string `json:"steps,omitempty"`
	Diff    string `json:"diff,omitempty"`
	Risk    string `json:"risk,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func decodeEventPayload(payloadJSON string) eventPayload {
	var payload eventPayload
	_ = json.Unmarshal([]byte(payloadJSON), &payload)
	return payload
}

func formatPlan(steps string) string {
	var lines []string
	for _, step := range strings.Split(steps, "\n") {
		if strings.TrimSpace(step) != "" {
			lines = append(lines, "- "+step)
		}
	}
	return strings.Join(lines, "\n")
}

func formatToolEvent(payload eventPayload) string {
	status := renderStatus(payload.Status)
	var lines []string
	lines = append(lines, status+" "+metadataStyle.Render(strings.TrimSpace(payload.Tool+" "+payload.Input)))
	out := strings.TrimSpace(payload.Output)
	if out != "" {
		for _, line := range formatToolOutput(out, 12) {
			lines = append(lines, "  "+line)
		}
	}
	return strings.Join(lines, "\n")
}

func renderSection(output io.Writer, title string, body string) {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return
	}
	fmt.Fprintln(output, "\n"+renderPanel(title, strings.Split(indentBody(body), "\n")))
}

func renderLogLine(output io.Writer, label string, body string) {
	body = firstNonEmptyLine(strings.TrimSpace(body))
	if body == "" {
		return
	}
	label = strings.TrimSpace(label)
	if label != "" {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	fmt.Fprintln(output, mutedStyle.Render("  "+label+" - ")+body)
}

func keyValue(key string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "-"
	}
	return labelStyle.Render(key) + " " + metadataStyle.Render(value)
}

func helpText() string {
	groups := []string{
		commandStyle.Render("work") + "      /tools  /workbench  /spawn <request>  /watch [active|task_id...]",
		commandStyle.Render("history") + "   /tasks  /sessions  /timeline [limit]  /transcript [limit]  /history <query>  /tail",
		commandStyle.Render("changes") + "   /diff [task_id]  /apply  /cancel [task_id]",
		commandStyle.Render("approval") + "  /approvals  /approve [task_id]  /deny [task_id]",
		commandStyle.Render("context") + "   /memory  /goal  /skills  /skill <name>  /mcp",
		commandStyle.Render("system") + "    /doctor  /config  /status",
		commandStyle.Render("session") + "   /resume <task_id>  /resume-session <id>  /resume-latest  /new-session  /clear  /exit",
	}
	return "Type a natural-language request, or use a command.\n\n" + strings.Join(groups, "\n")
}

func renderStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" || status == string(trace.StatusOK) {
		return okStyle.Render("ok")
	}
	if status == "cancelled" || status == "waiting_user" {
		return warnStyle.Render(status)
	}
	return errStyle.Render(status)
}

func indentBody(body string) string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func renderPanel(title string, lines []string) string {
	var rendered []string
	rendered = append(rendered, railStyle.Render("╭─ ")+labelStyle.Render(title))
	for _, line := range lines {
		if line == "" {
			rendered = append(rendered, railStyle.Render("│"))
			continue
		}
		rendered = append(rendered, railStyle.Render("│ ")+line)
	}
	rendered = append(rendered, railStyle.Render("╰"))
	return strings.Join(rendered, "\n")
}

func firstNonEmptyLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func formatToolOutput(value string, maxLines int) []string {
	var lines []string
	rawLines := strings.Split(strings.TrimSpace(value), "\n")
	for i, line := range rawLines {
		if i >= maxLines {
			remaining := len(rawLines) - maxLines
			lines = append(lines, "... "+strconv.Itoa(remaining)+" more lines")
			break
		}
		line = strings.TrimRight(line, "\r")
		if len(line) > 140 {
			line = line[:137] + "..."
		}
		lines = append(lines, line)
	}
	return lines
}
