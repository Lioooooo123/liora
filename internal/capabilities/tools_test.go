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
	for _, want := range []string{"read <path>", "document <path>", "run <shell command>", "mcp <server> <tool> <json arguments>"} {
		if !strings.Contains(planner, want) {
			t.Fatalf("expected planner list to contain %q, got:\n%s", want, planner)
		}
	}
	human := HumanToolList()
	for _, want := range []string{"read_only", "write", "shell", "external", "PDF/DOCX", "精确文本替换"} {
		if !strings.Contains(human, want) {
			t.Fatalf("expected human list to contain %q, got:\n%s", want, human)
		}
	}
}

func TestToolSchemasAreClosedObjects(t *testing.T) {
	schemas := ToolSchemas()
	if len(schemas) != len(builtinTools) {
		t.Fatalf("expected every builtin tool to expose a schema, got %d of %d", len(schemas), len(builtinTools))
	}
	byName := map[string]ToolSpec{}
	for _, spec := range schemas {
		schema := spec.InputSchema
		if schema["type"] != "object" {
			t.Fatalf("tool %q schema type should be object, got %#v", spec.Name, schema["type"])
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("tool %q schema must set additionalProperties=false", spec.Name)
		}
		byName[spec.Name] = spec
	}
	read, ok := byName["read"]
	if !ok {
		t.Fatal("expected read tool schema")
	}
	required, ok := read.InputSchema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "path" {
		t.Fatalf("expected read to require path, got %#v", read.InputSchema["required"])
	}
	properties, ok := read.InputSchema["properties"].(map[string]any)
	if !ok || properties["path"] == nil || properties["start_line"] == nil {
		t.Fatalf("expected read properties to include path and start_line, got %#v", read.InputSchema["properties"])
	}
}
