package tui

import (
	"regexp"
	"strings"
	"testing"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func terminalPlainText(value string) string {
	return ansiPattern.ReplaceAllString(value, "")
}

func TestRenderTurnRendersAssistantMarkdown_whenAnswerContainsMarkdown(t *testing.T) {
	var out strings.Builder
	RenderTurn(&out, TurnView{
		Input:    "总结一下",
		ShowUser: true,
		TurnResult: TurnResult{
			Answer: "# Release notes\n\n- **Fixed** SSE rendering\n\n```go\nfmt.Println(\"ok\")\n```",
		},
	})

	rendered := terminalPlainText(out.String())
	for _, want := range []string{"Assistant", "Release notes", "Fixed", "fmt.Println"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected markdown-rendered assistant output to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"# Release notes", "**Fixed**", "```go", "```"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("assistant markdown should not expose raw marker %q, got:\n%s", avoid, rendered)
		}
	}
}

func TestRenderTurnKeepsUserMarkdownLiteral_whenPromptContainsMarkdown(t *testing.T) {
	var out strings.Builder
	RenderTurn(&out, TurnView{
		Input:      "# literal\n\n**do not style me**",
		ShowUser:   true,
		TurnResult: TurnResult{Answer: "ok"},
	})

	rendered := out.String()
	for _, want := range []string{"# literal", "**do not style me**"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected user markdown to remain literal with %q, got:\n%s", want, rendered)
		}
	}
}
