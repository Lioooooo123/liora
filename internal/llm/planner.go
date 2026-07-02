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
	Steps    string
	Answer   string
	Question string
}

func NewPlanner(generator Generator) *Planner {
	return &Planner{generator: generator}
}

// Generator exposes the underlying generator so the runtime can detect whether
// it also supports native structured tool calls (llm.ToolCaller) and drive the
// tool-use loop instead of the text planner path.
func (p *Planner) Generator() Generator {
	return p.generator
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
	if question, ok := parseUserQuestion(steps); ok {
		return PlanTurn{Question: question}, nil
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
	if question, ok := parseUserQuestion(steps); ok {
		return PlanTurn{Question: question}, nil
	}
	if err := validateSteps(steps); err != nil {
		return PlanTurn{}, err
	}
	return PlanTurn{Steps: steps}, nil
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
		if question, ok := parseUserQuestion(normalized); ok {
			return "ASK_USER: " + question
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
	return parsePrefixedText(response, "ANSWER:")
}

func parseUserQuestion(response string) (string, bool) {
	return parsePrefixedText(response, "ASK_USER:")
}

func parsePrefixedText(response string, prefix string) (string, bool) {
	response = strings.TrimSpace(response)
	if len(response) < len(prefix) || !strings.EqualFold(response[:len(prefix)], prefix) {
		return "", false
	}
	answer, ok := strings.CutPrefix(response, response[:len(prefix)])
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
