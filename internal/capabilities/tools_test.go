package capabilities

import (
	"strings"
	"testing"
)

func TestBuiltinToolsExposePlannerAndHumanViews(t *testing.T) {
	if !HasBuiltinTool("read") || !HasBuiltinTool("RUN") {
		t.Fatal("expected registry to find builtin tools case-insensitively")
	}
	if HasBuiltinTool("teleport") {
		t.Fatal("unexpected unsupported tool")
	}
	planner := PlannerToolList()
	for _, want := range []string{"read <path>", "run <shell command>", "mcp <server> <tool> <json arguments>"} {
		if !strings.Contains(planner, want) {
			t.Fatalf("expected planner list to contain %q, got:\n%s", want, planner)
		}
	}
	human := HumanToolList()
	for _, want := range []string{"read_only", "write", "shell", "external", "精确文本替换"} {
		if !strings.Contains(human, want) {
			t.Fatalf("expected human list to contain %q, got:\n%s", want, human)
		}
	}
}
