package agent

import (
	"strconv"
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
		line = normalizeStepLine(strings.TrimSpace(line))
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
	line = normalizeStepLine(line)
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
	case "read", "document":
		step.Args = parsePathWithOptionalNumbers(splitStepFields(rest), 2)
	case "stat", "list", "mkdir", "delete":
		fields := splitStepFields(rest)
		if len(fields) <= 1 {
			step.Args = fields
			break
		}
		step.Args = []string{strings.Join(fields, " ")}
	case "tree":
		fields := splitStepFields(rest)
		if len(fields) == 0 {
			break
		}
		if len(fields) >= 2 {
			if depth, ok := parseOptionalInt(fields[len(fields)-1]); ok {
				step.Args = []string{strings.Join(fields[:len(fields)-1], " "), strconv.Itoa(depth)}
				break
			}
		}
		step.Args = []string{strings.Join(fields, " ")}
	default:
		step.Args = splitStepFields(rest)
	}
	return step, true
}

func parsePathWithOptionalNumbers(fields []string, maxTailNumbers int) []string {
	if len(fields) == 0 {
		return nil
	}
	if len(fields) == 1 {
		return fields
	}

	max := maxTailNumbers
	numbers := []int{}
	for i := len(fields) - 1; i >= 0 && len(numbers) < max; i-- {
		value, err := strconv.Atoi(fields[i])
		if err != nil || value <= 0 {
			break
		}
		numbers = append(numbers, value)
		fields = fields[:i]
	}

	if len(fields) == 0 {
		// Fallback: avoid returning an empty path. Keep original raw args.
		out := make([]string, 0, len(numbers))
		for i := len(numbers) - 1; i >= 0; i-- {
			out = append(out, strconv.Itoa(numbers[i]))
		}
		return out
	}

	// numbers were collected from right to left; restore original order.
	for i, j := 0, len(numbers)-1; i < j; i, j = i+1, j-1 {
		numbers[i], numbers[j] = numbers[j], numbers[i]
	}
	path := strings.Join(fields, " ")
	if len(numbers) == 0 {
		return []string{path}
	}

	if len(numbers) >= 2 {
		return []string{path, strconv.Itoa(numbers[0]), strconv.Itoa(numbers[1])}
	}
	return []string{path, strconv.Itoa(numbers[0])}
}

func parseOptionalInt(value string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	return parsed, err == nil && parsed > 0
}

func normalizeStepLine(line string) string {
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
