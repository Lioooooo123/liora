package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/Lioooooo123/liora/internal/capabilities"
)

type PlanRequest struct {
	WorkspaceSummary string
	UserPrompt       string
}

type Planner struct {
	generator Generator
}

type PlanTurn struct {
	Steps  string
	Answer string
}

func NewPlanner(generator Generator) *Planner {
	return &Planner{generator: generator}
}

func (p *Planner) Plan(ctx context.Context, request PlanRequest) (string, error) {
	turn, err := p.PlanTurn(ctx, request)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(turn.Steps) == "" {
		return "", fmt.Errorf("planner returned no steps")
	}
	return turn.Steps, nil
}

func (p *Planner) PlanTurn(ctx context.Context, request PlanRequest) (PlanTurn, error) {
	if strings.TrimSpace(request.UserPrompt) == "" {
		return PlanTurn{}, fmt.Errorf("user prompt is required")
	}
	response, err := p.generator.Generate(ctx, []Message{
		{Role: "system", Content: plannerSystemPrompt()},
		{Role: "user", Content: plannerUserPrompt(request)},
	})
	if err != nil {
		return PlanTurn{}, err
	}
	steps := cleanPlannerResponse(response)
	if answer, ok := parseDirectAnswer(steps); ok {
		return PlanTurn{Answer: answer}, nil
	}
	if err := validateSteps(steps); err != nil {
		return PlanTurn{}, err
	}
	return PlanTurn{Steps: steps}, nil
}

func plannerSystemPrompt() string {
	return `You are a coding-agent planner. Convert the user's request into newline-separated tool steps.

Output one of:
1. Executable steps when repository tools are needed.
2. ANSWER: <short reply> when the user is greeting, chatting, or asking something that does not need tools.

Allowed tools:
` + capabilities.PlannerToolList() + `

Rules:
- Use relative paths only.
- Prefer list <path> for folder listing or "what is in this directory" requests.
- Prefer glob for finding files by pattern, search for finding text, and read with line ranges before editing.
- Prefer edit for precise replacements; use write only for new files or full-file rewrites.
- Prefer built-in file tools over shell commands when possible.
- End with diff after file edits.
- Do not output unsupported tools.
- Use mcp only when the request explicitly needs a configured MCP server.
- For greetings such as "hello" or "你好", answer directly with ANSWER:.`
}

func plannerUserPrompt(request PlanRequest) string {
	var builder strings.Builder
	if strings.TrimSpace(request.WorkspaceSummary) != "" {
		builder.WriteString("Workspace:\n")
		builder.WriteString(request.WorkspaceSummary)
		builder.WriteString("\n\n")
	}
	builder.WriteString("User request:\n")
	builder.WriteString(request.UserPrompt)
	return builder.String()
}

func cleanPlannerResponse(response string) string {
	response = strings.TrimSpace(response)
	if strings.HasPrefix(response, "```") {
		lines := strings.Split(response, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
			if strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
				lines = lines[:len(lines)-1]
			}
			response = strings.Join(lines, "\n")
		}
	}
	var steps []string
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		steps = append(steps, line)
	}
	return strings.Join(steps, "\n")
}

func validateSteps(steps string) error {
	if strings.TrimSpace(steps) == "" {
		return fmt.Errorf("planner returned no steps")
	}
	for _, line := range strings.Split(steps, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		tool := strings.ToLower(fields[0])
		if !capabilities.HasBuiltinTool(tool) {
			return fmt.Errorf("unsupported tool %q in planner output", tool)
		}
	}
	return nil
}

func parseDirectAnswer(response string) (string, bool) {
	answer, ok := strings.CutPrefix(strings.TrimSpace(response), "ANSWER:")
	if !ok {
		return "", false
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}
	return answer, true
}
