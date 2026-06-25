package agent

import (
	"strings"
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
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		steps = append(steps, Step{
			Raw:  line,
			Tool: strings.ToLower(fields[0]),
			Args: fields[1:],
		})
	}
	return steps
}
