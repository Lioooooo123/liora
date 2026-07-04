package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/trace"
	"github.com/charmbracelet/lipgloss"
)

type Config struct {
	Workspace   string
	Model       string
	Core        string
	Safety      string
	Width       int
	Commands    CommandHandler
	Completions CompletionProvider
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

func (c CommandChain) Completions(ctx context.Context, line string) ([]Completion, error) {
	var completions []Completion
	for _, handler := range c {
		provider, ok := handler.(CompletionProvider)
		if !ok || provider == nil {
			continue
		}
		items, err := provider.Completions(ctx, line)
		if err != nil {
			return nil, err
		}
		completions = append(completions, items...)
	}
	return completions, nil
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
	return RenderWelcomeWithWidth(config, config.Width)
}

func RenderWelcomeWithWidth(config Config, width int) string {
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
		commandStyle.Render("/help")+" commands  "+commandStyle.Render("/exit")+" quit",
		commandStyle.Render("/workbench")+" workspace",
		commandStyle.Render("/memory")+" memory  "+commandStyle.Render("/schedule")+" schedules",
		commandStyle.Render("/sessions")+" sessions",
		commandStyle.Render("/context")+" context  "+commandStyle.Render("/status")+" status",
		commandStyle.Render("/resume-latest")+" resume",
		commandStyle.Render("/new-session")+" new session",
		mutedStyle.Render("Patch-first: review, then /apply."),
	)
	var out strings.Builder
	renderSectionWithWidth(&out, "Liora", strings.Join(lines, "\n"), width)
	return strings.TrimLeft(out.String(), "\n") + "\n"
}

func (a *App) Run(ctx context.Context, input io.Reader, output io.Writer) error {
	fmt.Fprint(output, RenderWelcomeWithWidth(a.config, a.renderWidth()))
	if streamer, ok := a.submitter.(StreamingSubmitter); ok {
		return a.runStreaming(ctx, input, output, streamer)
	}
	return a.runBlocking(ctx, input, output)
}

func (a *App) renderWidth() int {
	return normalizeRenderWidth(a.config.Width)
}

func (a *App) renderSection(output io.Writer, title string, body string) {
	renderSectionWithWidth(output, title, body, a.renderWidth())
}
