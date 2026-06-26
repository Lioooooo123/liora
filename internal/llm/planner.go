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

type ReplanRequest struct {
	WorkspaceSummary string
	UserPrompt       string
	PreviousSteps    string
	Failure          string
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

func (p *Planner) ReplanTurn(ctx context.Context, request ReplanRequest) (PlanTurn, error) {
	if strings.TrimSpace(request.UserPrompt) == "" {
		return PlanTurn{}, fmt.Errorf("user prompt is required")
	}
	if strings.TrimSpace(request.PreviousSteps) == "" {
		return PlanTurn{}, fmt.Errorf("previous steps are required")
	}
	if strings.TrimSpace(request.Failure) == "" {
		return PlanTurn{}, fmt.Errorf("failure context is required")
	}
	response, err := p.generator.Generate(ctx, []Message{
		{Role: "system", Content: replanSystemPrompt()},
		{Role: "user", Content: replanUserPrompt(request)},
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
- Use document for .pdf and .docx files; use read for plain text and source code.
- Prefer glob for finding files by pattern, search for finding text, and read with line ranges before editing.
- Prefer edit for precise replacements; use write only for new files or full-file rewrites.
- Quote paths or text arguments that contain spaces, for example: stat "Assignment Question.pdf".
- Prefer built-in file tools over shell commands when possible.
- End with diff after file edits.
- Do not output unsupported tools.
- Use mcp only when the request explicitly needs a configured MCP server.
- For greetings such as "hello" or "你好", answer directly with ANSWER:.`
}

func replanSystemPrompt() string {
	return `You are a coding-agent repair planner. A previous tool plan failed.

Output a corrected newline-separated tool plan that continues from the current workspace state.

Allowed tools:
` + capabilities.PlannerToolList() + `

Rules:
- Use relative paths only.
- Do not repeat the exact failing step unless it is intentionally fixed by earlier steps.
- Prefer observing the workspace with list, glob, search, stat, or read before editing.
- Use document for .pdf and .docx files; use read for plain text and source code.
- Prefer built-in file tools over shell commands when possible.
- Quote paths or text arguments that contain spaces, for example: document "Assignment Question.pdf".
- End with diff after file edits.
- Do not output unsupported tools.
- Do not output explanations.`
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

func replanUserPrompt(request ReplanRequest) string {
	var builder strings.Builder
	if strings.TrimSpace(request.WorkspaceSummary) != "" {
		builder.WriteString("Workspace:\n")
		builder.WriteString(request.WorkspaceSummary)
		builder.WriteString("\n\n")
	}
	builder.WriteString("Original user request:\n")
	builder.WriteString(request.UserPrompt)
	builder.WriteString("\n\nPrevious plan:\n")
	builder.WriteString(request.PreviousSteps)
	builder.WriteString("\n\nFailure:\n")
	builder.WriteString(request.Failure)
	builder.WriteString("\n\nReturn a corrected plan.")
	return builder.String()
}

func cleanPlannerResponse(response string) string {
	response = strings.TrimSpace(response)
	var steps []string
	inFence := false
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		normalized := normalizePlannerLine(line)
		if normalized == "" {
			continue
		}
		if answer, ok := parseDirectAnswer(normalized); ok {
			return "ANSWER: " + answer
		}
		tool, _ := firstPlannerField(normalized)
		if capabilities.HasBuiltinTool(tool) {
			steps = append(steps, normalized)
			continue
		}
		if inFence {
			steps = append(steps, normalized)
		}
	}
	if len(steps) == 0 {
		return normalizePlannerLine(response)
	}
	return strings.Join(steps, "\n")
}

func validateSteps(steps string) error {
	if strings.TrimSpace(steps) == "" {
		return fmt.Errorf("planner returned no steps")
	}
	for _, line := range strings.Split(steps, "\n") {
		tool, _ := firstPlannerField(normalizePlannerLine(line))
		if tool == "" {
			continue
		}
		if !capabilities.HasBuiltinTool(tool) {
			return fmt.Errorf("unsupported tool %q in planner output", tool)
		}
	}
	return nil
}

func parseDirectAnswer(response string) (string, bool) {
	response = strings.TrimSpace(response)
	if len(response) < len("ANSWER:") || !strings.EqualFold(response[:len("ANSWER:")], "ANSWER:") {
		return "", false
	}
	answer, ok := strings.CutPrefix(response, response[:len("ANSWER:")])
	if !ok {
		return "", false
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}
	return answer, true
}

func normalizePlannerLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "- ")
	line = strings.TrimPrefix(line, "* ")
	line = strings.TrimPrefix(line, "+ ")
	line = strings.TrimSpace(line)
	for i, r := range line {
		if r != '.' && r != ')' {
			continue
		}
		prefix := line[:i]
		if prefix == "" || strings.Trim(prefix, "0123456789") != "" {
			continue
		}
		return strings.TrimSpace(line[i+1:])
	}
	return line
}

func firstPlannerField(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	for i, r := range value {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return strings.ToLower(value[:i]), value[i:]
		}
	}
	return strings.ToLower(value), ""
}
