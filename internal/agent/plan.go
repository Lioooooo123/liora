package agent

import (
	"strings"
	"unicode"
)

type Step struct {
	Raw  string
	Tool string
	Args []string
}

func parseSteps(prompt string) []Step {
	var steps []Step
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		step, ok := parseStepLine(line)
		if !ok {
			continue
		}
		steps = append(steps, step)
	}
	return steps
}

func parseStepLine(line string) (Step, bool) {
	tool, rest := firstField(line)
	if tool == "" {
		return Step{}, false
	}
	step := Step{Raw: line, Tool: strings.ToLower(tool)}
	switch step.Tool {
	case "run":
		if strings.TrimSpace(rest) != "" {
			step.Args = []string{strings.TrimSpace(rest)}
		}
	case "mcp":
		server, remaining := firstField(rest)
		toolName, argsJSON := firstField(remaining)
		if server != "" {
			step.Args = append(step.Args, server)
		}
		if toolName != "" {
			step.Args = append(step.Args, toolName)
		}
		if strings.TrimSpace(argsJSON) != "" {
			step.Args = append(step.Args, strings.TrimSpace(argsJSON))
		}
	default:
		step.Args = splitStepFields(rest)
	}
	return step, true
}

func firstField(value string) (string, string) {
	value = strings.TrimLeftFunc(value, unicode.IsSpace)
	if value == "" {
		return "", ""
	}
	for i, r := range value {
		if unicode.IsSpace(r) {
			return value[:i], value[i:]
		}
	}
	return value, ""
}

func splitStepFields(line string) []string {
	var fields []string
	var builder strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		fields = append(fields, builder.String())
		builder.Reset()
	}
	for _, r := range line {
		switch {
		case escaped:
			builder.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				builder.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			builder.WriteRune(r)
		}
	}
	if escaped {
		builder.WriteRune('\\')
	}
	flush()
	return fields
}
