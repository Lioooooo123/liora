package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

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
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("149")).Bold(true)
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	boxStyle    = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1).
			MarginTop(1)
)

func New(config Config, submitter Submitter) *App {
	return &App{config: config, submitter: submitter}
}

func RenderWelcome(config Config) string {
	model := config.Model
	if model == "" {
		model = "scripted"
	}
	header := accentStyle.Render("Liora")
	status := strings.Join([]string{
		labelStyle.Render("Workspace") + " " + config.Workspace,
		labelStyle.Render("Model") + " " + model,
	}, "\n")
	var runtime []string
	if strings.TrimSpace(config.Core) != "" {
		runtime = append(runtime, labelStyle.Render("Core")+" "+config.Core)
	}
	if strings.TrimSpace(config.Safety) != "" {
		runtime = append(runtime, labelStyle.Render("Safety")+" "+config.Safety)
	}
	if len(runtime) > 0 {
		status += "\n" + strings.Join(runtime, "\n")
	}
	commands := mutedStyle.Render("Commands  /help  /tools  /tasks  /sessions  /timeline  /last  /tail  /diff  /resume  /resume-session  /approve  /deny  /apply  /cancel  /exit")
	return header + "\n" + status + "\n\n" + commands + "\n"
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
		fmt.Fprint(output, "\n"+accentStyle.Render("agent")+" > ")
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
			fmt.Fprintln(output, "Type a coding request in natural language. Commands: /tools, /tasks, /sessions, /timeline, /last, /tail [lines|task_id lines], /diff [task_id], /resume <task_id>, /resume-session <session_id>, /approve, /deny, /apply, /cancel, /goal, /memory, /skills, /skill, /mcp, /exit.")
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
	var outputMu sync.Mutex
	write := func(fn func()) {
		outputMu.Lock()
		defer outputMu.Unlock()
		fn()
	}
	prompt := func() {
		write(func() {
			fmt.Fprint(output, "\n"+accentStyle.Render("agent")+" > ")
		})
	}
	prompt()
	var pending []string
	var running bool
	var turnDone <-chan turnOutcome
	var inputClosed bool
	var scanErr error
	for {
		if !running && len(pending) > 0 {
			line := pending[0]
			pending = pending[1:]
			exit := a.handleStreamingLine(ctx, line, output, streamer, write, &running, &turnDone)
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
			if result.err != nil {
				write(func() {
					fmt.Fprintf(output, "Error: %v\n", result.err)
				})
			}
			if len(pending) == 0 && !inputClosed {
				prompt()
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
			exit := a.handleStreamingLine(ctx, line, output, streamer, write, &running, &turnDone)
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

func (a *App) handleStreamingLine(ctx context.Context, line string, output io.Writer, streamer StreamingSubmitter, write func(func()), running *bool, turnDone *<-chan turnOutcome) bool {
	switch line {
	case "/exit", "/quit":
		write(func() {
			fmt.Fprintln(output, "Bye")
		})
		return true
	case "/help":
		write(func() {
			fmt.Fprintln(output, "Type a coding request in natural language. Commands: /tools, /tasks, /sessions, /timeline, /last, /tail [lines|task_id lines], /diff [task_id], /resume <task_id>, /resume-session <session_id>, /approve, /deny, /apply, /cancel, /goal, /memory, /skills, /skill, /mcp, /exit.")
		})
		return false
	}
	if strings.HasPrefix(line, "/") && a.config.Commands != nil {
		result, handled, err := a.config.Commands.HandleCommand(ctx, line)
		write(func() {
			if err != nil {
				fmt.Fprintf(output, "Error: %v\n", err)
				return
			}
			if handled {
				renderSection(output, "System", result)
			}
		})
		if handled || err != nil {
			return false
		}
	}
	if *running {
		write(func() {
			renderSection(output, "System", "Task is still running. Use /cancel, /approve, /deny, or wait for it to finish.")
		})
		return false
	}
	a.startStreamingTurn(ctx, line, output, streamer, write, running, turnDone)
	return false
}

func (a *App) startStreamingTurn(ctx context.Context, input string, output io.Writer, streamer StreamingSubmitter, write func(func()), running *bool, turnDone *<-chan turnOutcome) {
	write(func() {
		renderLogLine(output, "task", "started")
	})
	done := make(chan turnOutcome, 1)
	*running = true
	*turnDone = done
	go func() {
		_, err := streamer.SubmitStream(ctx, input, func(update StreamUpdate) {
			write(func() {
				RenderStreamUpdate(output, update)
			})
		})
		done <- turnOutcome{err: err}
	}()
}

func isRunningCommand(line string) bool {
	return strings.TrimSpace(line) == "/cancel"
}

func (a *App) runTurn(ctx context.Context, input string, output io.Writer) error {
	renderLogLine(output, "task", "started")
	if streamer, ok := a.submitter.(StreamingSubmitter); ok {
		_, err := streamer.SubmitStream(ctx, input, func(update StreamUpdate) {
			RenderStreamUpdate(output, update)
		})
		return err
	}
	result, err := a.submitter.Submit(ctx, input)
	RenderTurn(output, TurnView{
		Input:      input,
		ShowUser:   false,
		TurnResult: result,
	})
	return err
}

func RenderStreamUpdate(output io.Writer, update StreamUpdate) {
	payload := decodeEventPayload(update.PayloadJSON)
	switch update.Type {
	case "task.planning", "sandbox.run", "sandbox.workspace":
		if strings.TrimSpace(payload.Message) != "" {
			renderLogLine(output, "status", payload.Message)
		}
	case "task.plan_ready":
		if strings.TrimSpace(payload.Steps) != "" {
			renderSection(output, "Plan", formatPlan(payload.Steps))
		}
	case "task.replanning":
		message := payload.Message
		if strings.TrimSpace(message) == "" {
			message = "Replanning after failure"
		}
		if strings.TrimSpace(payload.Reason) != "" {
			message += ": " + firstNonEmptyLine(payload.Reason)
		}
		renderLogLine(output, "status", message)
	case "tool.call":
		line := strings.TrimSpace(payload.Tool + " " + payload.Input)
		if line != "" {
			renderLogLine(output, "tool", line)
		}
	case "tool.result":
		renderSection(output, "Tools", formatToolEvent(payload))
	case "task.summary":
		if strings.TrimSpace(payload.Message) != "" {
			renderSection(output, "Summary", payload.Message)
		}
	case "task.diff":
		if strings.TrimSpace(payload.Diff) != "" {
			renderSection(output, "Diff", strings.TrimRight(payload.Diff, "\n"))
			renderSection(output, "Next", "Review the diff before applying changes.\nCommands: /apply to confirm, /cancel to stop a running task.")
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
		status := update.Type
		if payload.Status != "" {
			status = payload.Status
		}
		if payload.Message != "" {
			status += ": " + payload.Message
		}
		renderLogLine(output, "status", status)
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
	if strings.TrimSpace(result.PlannedSteps) != "" {
		renderSection(output, "Plan", formatPlan(result.PlannedSteps))
	}
	if len(result.Events) > 0 {
		var blocks []string
		for _, event := range result.Events {
			status := okStyle.Render("ok")
			if event.Status != trace.StatusOK {
				status = errStyle.Render("error")
			}
			var lines []string
			lines = append(lines, "["+status+"] "+event.Tool+" "+event.Input)
			out := strings.TrimSpace(event.Output)
			if out != "" {
				for _, line := range formatToolOutput(out, 12) {
					lines = append(lines, "  "+line)
				}
			}
			blocks = append(blocks, strings.Join(lines, "\n"))
		}
		renderSection(output, "Tools", strings.Join(blocks, "\n\n"))
	}
	if strings.TrimSpace(result.AgentResult.Summary) != "" {
		renderSection(output, "Summary", result.AgentResult.Summary)
	}
	if strings.TrimSpace(result.AgentResult.Diff) != "" {
		renderSection(output, "Diff", strings.TrimRight(result.AgentResult.Diff, "\n"))
		renderSection(output, "Next", "Review the diff before applying changes.\nDaemon API: POST /apply to confirm, POST /cancel to stop a running task.")
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
	status := okStyle.Render("ok")
	if payload.Status != "" && payload.Status != string(trace.StatusOK) {
		status = errStyle.Render("error")
	}
	var lines []string
	lines = append(lines, "["+status+"] "+strings.TrimSpace(payload.Tool+" "+payload.Input))
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
	content := labelStyle.Render(title) + "\n" + body
	fmt.Fprintln(output, boxStyle.Render(content))
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
