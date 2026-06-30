package agent

import "testing"

func TestParseStepLineSupportsSpacePathAndOptionalNumbers(t *testing.T) {
	steps := parseSteps(`
read course notes.txt 2 10
document assignment.docx 1 2
stat assignment\ question.pdf
stat "course notes.txt"
`)
	if len(steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(steps))
	}

	readStep := steps[0]
	if readStep.Tool != "read" || len(readStep.Args) != 3 ||
		readStep.Args[0] != "course notes.txt" || readStep.Args[1] != "2" || readStep.Args[2] != "10" {
		t.Fatalf("unexpected read step: %#v", readStep)
	}

	docStep := steps[1]
	if docStep.Tool != "document" || len(docStep.Args) != 3 ||
		docStep.Args[0] != "assignment.docx" || docStep.Args[1] != "1" || docStep.Args[2] != "2" {
		t.Fatalf("unexpected document step: %#v", docStep)
	}

	if got := steps[2].Args[0]; got != "assignment question.pdf" {
		t.Fatalf("escaped-path stat parse unexpected: %#v", got)
	}
	if got := steps[3].Args[0]; got != "course notes.txt" {
		t.Fatalf("quoted-path stat parse unexpected: %#v", got)
	}
}

func TestParseStepLineSupportsTreePathWithDepth(t *testing.T) {
	steps := parseSteps(`tree src with space 3`)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if got := steps[0].Tool; got != "tree" {
		t.Fatalf("unexpected tool: %s", got)
	}
	if args := steps[0].Args; len(args) != 2 || args[0] != "src with space" || args[1] != "3" {
		t.Fatalf("unexpected tree args: %#v", args)
	}
}
