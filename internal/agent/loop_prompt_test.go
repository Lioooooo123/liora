package agent

import (
	"strings"
	"testing"
)

func TestToolLoopSystemPromptIncludesToolChoiceReflection(t *testing.T) {
	prompt := loopSystemPrompt()
	for _, want := range []string{
		"Tool choice reflection",
		"silently decide whether a tool is needed",
		"lowest-cost tool",
		"run only for build, test, or shell-only checks",
		"mcp only for explicit configured external tools",
		"Stop calling tools once you have enough evidence",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("loop prompt missing %q:\n%s", want, prompt)
		}
	}
}
