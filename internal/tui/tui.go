package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"coding-agent-mvp/internal/agent"
	"coding-agent-mvp/internal/trace"
	"github.com/charmbracelet/lipgloss"
)

type Config struct {
	Workspace string
	Model     string
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

type CommandHandler interface {
	HandleCommand(ctx context.Context, line string) (string, bool, error)
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
	commands := mutedStyle.Render("Commands  /help  /goal  /memory  /skills  /skill  /mcp  /exit")
	return header + "\n" + status + "\n\n" + commands + "\n"
}

func (a *App) Run(ctx context.Context, input io.Reader, output io.Writer) error {
	fmt.Fprint(output, RenderWelcome(a.config))
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
			fmt.Fprintln(output, "Type a coding request in natural language. Commands: /goal, /memory, /skills, /skill, /mcp, /exit.")
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

func (a *App) runTurn(ctx context.Context, input string, output io.Writer) error {
	fmt.Fprintln(output, mutedStyle.Render("Working..."))
	result, err := a.submitter.Submit(ctx, input)
	RenderTurn(output, TurnView{
		Input:      input,
		ShowUser:   false,
		TurnResult: result,
	})
	return err
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
		var lines []string
		for _, step := range strings.Split(result.PlannedSteps, "\n") {
			if strings.TrimSpace(step) != "" {
				lines = append(lines, "- "+step)
			}
		}
		renderSection(output, "Plan", strings.Join(lines, "\n"))
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
	}
}

func renderSection(output io.Writer, title string, body string) {
	body = strings.TrimRight(body, "\n")
	content := labelStyle.Render(title) + "\n" + body
	fmt.Fprintln(output, boxStyle.Render(content))
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
