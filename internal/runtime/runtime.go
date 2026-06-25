package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Lioooooo123/liora/internal/agent"
	"github.com/Lioooooo123/liora/internal/capabilities"
	"github.com/Lioooooo123/liora/internal/llm"
	mcppkg "github.com/Lioooooo123/liora/internal/mcp"
	"github.com/Lioooooo123/liora/internal/permission"
	"github.com/Lioooooo123/liora/internal/sandbox"
	"github.com/Lioooooo123/liora/internal/store"
	"github.com/Lioooooo123/liora/internal/tools"
	"github.com/Lioooooo123/liora/internal/trace"
	"github.com/Lioooooo123/liora/internal/tui"
)

type Runtime struct {
	workspace *tools.Workspace
	planner   *llm.Planner
	store     *store.Store
	sandbox   sandbox.Executor
	checker   permission.Checker
}

type SubmitOptions struct {
	Recorder trace.Recorder
	OnPlan   func(steps string)
	OnReplan func(attempt int, reason string)
}

func New(workspacePath string, planner *llm.Planner, stores ...*store.Store) (*Runtime, error) {
	workspace, err := tools.NewWorkspace(workspacePath)
	if err != nil {
		return nil, err
	}
	return FromWorkspace(workspace, planner, stores...), nil
}

func FromWorkspace(workspace *tools.Workspace, planner *llm.Planner, stores ...*store.Store) *Runtime {
	persistentStore := store.New("")
	if len(stores) > 0 && stores[0] != nil {
		persistentStore = stores[0]
	}
	return &Runtime{workspace: workspace, planner: planner, store: persistentStore}
}

func (r *Runtime) SetSandbox(executor sandbox.Executor) {
	r.sandbox = executor
}

func (r *Runtime) SetPermissionChecker(checker permission.Checker) {
	r.checker = checker
}

func (r *Runtime) Submit(ctx context.Context, input string) (tui.TurnResult, error) {
	return r.SubmitWithOptions(ctx, input, SubmitOptions{Recorder: trace.NewMemoryRecorder()})
}

func (r *Runtime) SubmitWithRecorder(ctx context.Context, input string, recorder trace.Recorder) (tui.TurnResult, error) {
	return r.SubmitWithOptions(ctx, input, SubmitOptions{Recorder: recorder})
}

func (r *Runtime) SubmitWithOptions(ctx context.Context, input string, options SubmitOptions) (tui.TurnResult, error) {
	turn, err := r.planner.PlanTurn(ctx, llm.PlanRequest{
		WorkspaceSummary: r.workspaceSummary(),
		UserPrompt:       input,
	})
	if err != nil {
		return tui.TurnResult{}, err
	}
	if strings.TrimSpace(turn.Answer) != "" {
		return tui.TurnResult{Answer: turn.Answer}, nil
	}
	if options.OnPlan != nil {
		options.OnPlan(turn.Steps)
	}
	recorder := options.Recorder
	if recorder == nil {
		recorder = trace.NewMemoryRecorder()
	}
	result, err := r.runPlannedSteps(ctx, turn.Steps, recorder)
	plannedSteps := turn.Steps
	if err != nil && r.shouldReplan(ctx, err) {
		if options.OnReplan != nil {
			options.OnReplan(1, err.Error())
		}
		replan, replanErr := r.planner.ReplanTurn(ctx, llm.ReplanRequest{
			WorkspaceSummary: r.workspaceSummary(),
			UserPrompt:       input,
			PreviousSteps:    turn.Steps,
			Failure:          replanFailureContext(result, err),
		})
		if replanErr != nil {
			return tui.TurnResult{
				PlannedSteps: plannedSteps,
				AgentResult:  result,
				Events:       recordedEvents(recorder),
			}, err
		}
		if strings.TrimSpace(replan.Answer) != "" {
			return tui.TurnResult{Answer: replan.Answer, PlannedSteps: plannedSteps, AgentResult: result, Events: recordedEvents(recorder)}, err
		}
		if options.OnPlan != nil {
			options.OnPlan(replan.Steps)
		}
		plannedSteps = strings.TrimSpace(plannedSteps + "\n\n# Replan 1\n" + replan.Steps)
		result, err = r.runPlannedSteps(ctx, replan.Steps, recorder)
	}
	return tui.TurnResult{
		PlannedSteps: plannedSteps,
		AgentResult:  result,
		Events:       recordedEvents(recorder),
	}, err
}

func (r *Runtime) runPlannedSteps(ctx context.Context, steps string, recorder trace.Recorder) (agent.Result, error) {
	runner := agent.New(r.workspace, recorder)
	if r.sandbox != nil {
		runner.SetShellExecutor(r.sandbox)
	}
	if r.checker != nil {
		runner.SetPermissionChecker(r.checker)
	}
	if manager, err := r.mcpManager(); err == nil {
		runner.SetMCP(manager)
	}
	return runner.Run(ctx, steps)
}

func (r *Runtime) shouldReplan(ctx context.Context, err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return false
	}
	var permissionErr *permission.RequiredError
	return !errors.As(err, &permissionErr)
}

func replanFailureContext(result agent.Result, err error) string {
	var builder strings.Builder
	if err != nil {
		builder.WriteString(err.Error())
	}
	if strings.TrimSpace(result.Summary) != "" {
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(result.Summary)
	}
	if strings.TrimSpace(result.Diff) != "" {
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("Current diff:\n")
		builder.WriteString(result.Diff)
	}
	return builder.String()
}

type eventRecorder interface {
	Events() []trace.Event
}

func recordedEvents(recorder trace.Recorder) []trace.Event {
	if recorder == nil {
		return nil
	}
	if events, ok := recorder.(eventRecorder); ok {
		return events.Events()
	}
	return nil
}

func (r *Runtime) HandleCommand(ctx context.Context, line string) (string, bool, error) {
	line = strings.TrimSpace(line)
	switch {
	case line == "/goal" || strings.HasPrefix(line, "/goal "):
		return r.handleGoal(strings.TrimSpace(strings.TrimPrefix(line, "/goal")))
	case line == "/memory" || strings.HasPrefix(line, "/memory "):
		return r.handleMemory(strings.TrimSpace(strings.TrimPrefix(line, "/memory")))
	case line == "/skill" || strings.HasPrefix(line, "/skill "):
		return r.handleSkill(strings.TrimSpace(strings.TrimPrefix(line, "/skill")))
	case line == "/skills":
		return r.handleSkills()
	case line == "/tools":
		return capabilities.HumanToolList(), true, nil
	case line == "/mcp":
		return r.handleMCP(ctx)
	default:
		return "", false, nil
	}
}

func (r *Runtime) handleGoal(args string) (string, bool, error) {
	command, rest, _ := strings.Cut(args, " ")
	switch strings.TrimSpace(command) {
	case "", "show":
		goal, ok, err := r.store.Goal()
		if err != nil {
			return "", true, err
		}
		if !ok {
			return "No goal set.", true, nil
		}
		return "Current goal: " + goal, true, nil
	case "set":
		if err := r.store.SetGoal(rest); err != nil {
			return "", true, err
		}
		return "Current goal: " + strings.TrimSpace(rest), true, nil
	case "clear":
		if err := r.store.ClearGoal(); err != nil {
			return "", true, err
		}
		return "Goal cleared.", true, nil
	default:
		return "Usage: /goal show | /goal set <text> | /goal clear", true, nil
	}
}

func (r *Runtime) handleMemory(args string) (string, bool, error) {
	command, rest, _ := strings.Cut(args, " ")
	switch strings.TrimSpace(command) {
	case "", "list":
		memories, err := r.store.ListMemories(10)
		if err != nil {
			return "", true, err
		}
		return formatMemories(memories), true, nil
	case "add":
		if err := r.store.AddMemory(rest); err != nil {
			return "", true, err
		}
		return "Memory saved.", true, nil
	case "search":
		memories, err := r.store.SearchMemories(rest, 10)
		if err != nil {
			return "", true, err
		}
		return formatMemories(memories), true, nil
	default:
		return "Usage: /memory list | /memory add <text> | /memory search <query>", true, nil
	}
}

func (r *Runtime) handleSkills() (string, bool, error) {
	skills, err := r.store.ScanSkills(r.workspace.Root())
	if err != nil {
		return "", true, err
	}
	if len(skills) == 0 {
		return "No skills found. Add SKILL.md under ~/.config/liora/skills/<name>/ or .liora/skills/<name>/.", true, nil
	}
	var lines []string
	for _, skill := range skills {
		text := fmt.Sprintf("- %s: %s", skill.Name, skill.Title)
		if skill.Description != "" {
			text += " - " + skill.Description
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n"), true, nil
}

func (r *Runtime) handleSkill(name string) (string, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Usage: /skill <name>", true, nil
	}
	skills, err := r.store.ScanSkills(r.workspace.Root())
	if err != nil {
		return "", true, err
	}
	for _, skill := range skills {
		if skill.Name == name {
			var builder strings.Builder
			builder.WriteString(skill.Title)
			if skill.Description != "" {
				builder.WriteString("\n")
				builder.WriteString(skill.Description)
			}
			builder.WriteString("\n\n")
			builder.WriteString(strings.TrimSpace(skill.Body))
			return builder.String(), true, nil
		}
	}
	return fmt.Sprintf("Skill %q not found.", name), true, nil
}

func (r *Runtime) handleMCP(ctx context.Context) (string, bool, error) {
	config, err := r.store.LoadMCPConfig()
	if err != nil {
		return "", true, err
	}
	if len(config.Servers) == 0 {
		return "No MCP servers configured. Add mcp.json under LIORA_HOME or ~/.config/liora.", true, nil
	}
	manager := mcppkg.NewManager(convertMCPConfig(config))
	tools, err := manager.ListTools(ctx)
	if err != nil {
		return "", true, err
	}
	if len(tools) == 0 {
		return "No MCP tools exposed by configured servers.", true, nil
	}
	var lines []string
	for _, tool := range tools {
		text := fmt.Sprintf("- %s/%s", tool.Server, tool.Name)
		if tool.Description != "" {
			text += ": " + tool.Description
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n"), true, nil
}

func (r *Runtime) mcpManager() (*mcppkg.Manager, error) {
	config, err := r.store.LoadMCPConfig()
	if err != nil {
		return nil, err
	}
	if len(config.Servers) == 0 {
		return nil, nil
	}
	return mcppkg.NewManager(convertMCPConfig(config)), nil
}

func (r *Runtime) workspaceSummary() string {
	var builder strings.Builder
	builder.WriteString("workspace root: ")
	builder.WriteString(r.workspace.Root())
	if goal, ok, err := r.store.Goal(); err == nil && ok {
		builder.WriteString("\nCurrent goal: ")
		builder.WriteString(goal)
	}
	if memories, err := r.store.ListMemories(5); err == nil && len(memories) > 0 {
		builder.WriteString("\nMemories:")
		for _, memory := range memories {
			builder.WriteString("\n- ")
			builder.WriteString(memory.Text)
		}
	}
	if skills, err := r.store.ScanSkills(r.workspace.Root()); err == nil && len(skills) > 0 {
		builder.WriteString("\nAvailable skills:")
		for _, skill := range skills {
			builder.WriteString("\n- ")
			builder.WriteString(skill.Name)
			builder.WriteString(": ")
			builder.WriteString(skill.Title)
			if skill.Description != "" {
				builder.WriteString(" - ")
				builder.WriteString(skill.Description)
			}
			if body := summarizeSkillBody(skill.Body, 600); body != "" {
				builder.WriteString("\n  ")
				builder.WriteString(body)
			}
		}
	}
	if config, err := r.store.LoadMCPConfig(); err == nil && len(config.Servers) > 0 {
		var names []string
		for name := range config.Servers {
			names = append(names, name)
		}
		sort.Strings(names)
		builder.WriteString("\nConfigured MCP servers: ")
		builder.WriteString(strings.Join(names, ", "))
	}
	return builder.String()
}

func summarizeSkillBody(body string, max int) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	body = strings.Join(strings.Fields(body), " ")
	if len(body) > max {
		return body[:max] + "..."
	}
	return body
}

func formatMemories(memories []store.Memory) string {
	if len(memories) == 0 {
		return "No memories found."
	}
	var lines []string
	for _, memory := range memories {
		lines = append(lines, "- "+memory.Text)
	}
	return strings.Join(lines, "\n")
}

func convertMCPConfig(config store.MCPConfig) mcppkg.Config {
	servers := make(map[string]mcppkg.ServerConfig, len(config.Servers))
	for name, server := range config.Servers {
		servers[name] = mcppkg.ServerConfig{
			Command: server.Command,
			Args:    server.Args,
			Env:     server.Env,
		}
	}
	return mcppkg.Config{Servers: servers}
}
