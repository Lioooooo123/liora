package tui

import (
	"strings"
	"testing"

	"github.com/Lioooooo123/liora/internal/agent"
)

func TestRenderStreamUpdateShowsAssistantReply_whenDiffIsReady(t *testing.T) {
	// Given
	var out strings.Builder

	// When
	RenderStreamUpdate(&out, streamUpdate("task.diff", eventPayload{Diff: "--- a/hello.py\n+++ b/hello.py\n+print('Hello, World!')"}))

	// Then
	rendered := terminalPlainText(out.String())
	for _, want := range []string{"Assistant", "已准备好变更", "1 个文件", "hello.py", "+1 -0", "还没有写入真实工作区", "/apply", "/diff"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered output to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, avoid := range []string{"\nDiff", "+++ b/hello.py", "--- a/hello.py"} {
		if strings.Contains(rendered, avoid) {
			t.Fatalf("streamed diff should stay compact and avoid raw diff %q, got:\n%s", avoid, rendered)
		}
	}
	if strings.Contains(rendered, "stop a running task") {
		t.Fatalf("completed diff guidance should not mention stopping a running task:\n%s", rendered)
	}
}

func TestRenderTurnShowsAssistantReply_whenDiffIsReady(t *testing.T) {
	// Given
	var out strings.Builder

	// When
	RenderTurn(&out, TurnView{TurnResult: TurnResult{AgentResult: agent.Result{
		Diff: "--- a/hello.py\n+++ b/hello.py\n+print('Hello, World!')",
	}}})

	// Then
	rendered := terminalPlainText(out.String())
	for _, want := range []string{"Assistant", "已准备好变更", "/apply"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestPatchReadyReplySummarizesMultipleFiles_whenUnifiedDiffIsReady(t *testing.T) {
	// Given
	diff := strings.Join([]string{
		"--- a/hello.py",
		"+++ b/hello.py",
		"+print('Hello, World!')",
		"--- a/README.md",
		"+++ b/README.md",
		"-old",
		"+new",
	}, "\n")

	// When
	reply := PatchReadyReply(diff)
	next := PatchReadyNextAction()

	// Then
	for _, want := range []string{"已准备好变更", "2 个文件", "hello.py", "README.md", "+2 -1", "还没有写入真实工作区"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("expected reply to contain %q, got:\n%s", want, reply)
		}
	}
	for _, want := range []string{"/apply", "/diff", "继续输入新任务或 /exit"} {
		if !strings.Contains(next, want) {
			t.Fatalf("expected next action to contain %q, got:\n%s", want, next)
		}
	}
}

func TestPatchReviewPreviewShowsStructuredDiff_withoutRawHeaders(t *testing.T) {
	// Given
	diff := strings.Join([]string{
		"--- a/hello.py",
		"+++ b/hello.py",
		"-print('old')",
		"+print('new')",
	}, "\n")

	// When
	preview := PatchReviewPreview(diff, 20)

	// Then
	for _, want := range []string{"变更预览:", "hello.py", "- print('old')", "+ print('new')"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("expected preview to contain %q, got:\n%s", want, preview)
		}
	}
	for _, avoid := range []string{"--- a/hello.py", "+++ b/hello.py"} {
		if strings.Contains(preview, avoid) {
			t.Fatalf("preview should hide raw diff header %q, got:\n%s", avoid, preview)
		}
	}
}
