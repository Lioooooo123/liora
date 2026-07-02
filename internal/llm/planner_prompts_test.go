package llm

import (
	"strings"
	"testing"
)

func TestPlannerPromptsIncludeToolChoiceReflection(t *testing.T) {
	for name, prompt := range map[string]string{
		"plan":   plannerSystemPrompt(),
		"replan": replanSystemPrompt(),
	} {
		for _, want := range []string{
			"Tool choice reflection",
			"silently decide whether a tool is needed",
			"lowest-cost tool",
			"run only for build, test, or shell-only checks",
			"mcp only for explicit configured external tools",
			"Stop calling tools once you have enough evidence",
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("%s prompt missing %q:\n%s", name, want, prompt)
			}
		}
	}
}
