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
	for _, want := range []string{"read <path>", "document <path>", "skill <name>", "run <shell command>", "todo_write <json todos>", "todo_read", "mcp <server> <tool> <json arguments>"} {
		if !strings.Contains(planner, want) {
			t.Fatalf("expected planner list to contain %q, got:\n%s", want, planner)
		}
	}
	human := HumanToolList()
	for _, want := range []string{"read_only", "write", "shell", "external", "PDF/DOCX", "精确文本替换", "编译、测试", "明确需要已配置 MCP server", "access=read:path(path)", "access=write:path(path)", "access=exclusive:workspace"} {
		if !strings.Contains(human, want) {
			t.Fatalf("expected human list to contain %q, got:\n%s", want, human)
		}
	}
	if strings.Contains(planner, "access=") {
		t.Fatalf("planner list should stay prompt-compact and omit access descriptors, got:\n%s", planner)
	}
}

func TestCapabilityTodoToolsExposeNativeSchemas(t *testing.T) {
	byName := map[string]ToolSpec{}
	for _, spec := range ToolSchemas() {
		byName[spec.Name] = spec
	}
	read := byName["todo_read"]
	if read.Name == "" || read.Kind != ToolReadOnly {
		t.Fatalf("expected read-only todo_read schema, got %#v", read)
	}
	write := byName["todo_write"]
	if write.Name == "" || write.Kind != ToolWrite {
		t.Fatalf("expected write todo_write schema, got %#v", write)
	}
	required, ok := write.InputSchema["required"].([]string)
	if !ok || strings.Join(required, ",") != "todos" {
		t.Fatalf("expected todo_write to require todos, got %#v", write.InputSchema["required"])
	}
	properties, ok := write.InputSchema["properties"].(map[string]any)
	if !ok || properties["todos"] == nil {
		t.Fatalf("expected todo_write todos property, got %#v", write.InputSchema["properties"])
	}
}

func TestCapabilityTaskControlToolsExposeNativeSchemas(t *testing.T) {
	if !HasBuiltinTool("task") || !HasBuiltinTool("TASKOUTPUT") || !HasBuiltinTool("TaskStop") {
		t.Fatal("expected task-control tools to be found case-insensitively")
	}
	byName := map[string]ToolSpec{}
	for _, spec := range ToolSchemas() {
		byName[spec.Name] = spec
	}
	task := byName["Task"]
	if task.Name == "" || task.Kind != ToolExternal {
		t.Fatalf("expected external Task schema, got %#v", task)
	}
	output := byName["TaskOutput"]
	if output.Name == "" || output.Kind != ToolReadOnly {
		t.Fatalf("expected read-only TaskOutput schema, got %#v", output)
	}
	stop := byName["TaskStop"]
	if stop.Name == "" || stop.Kind != ToolExternal {
		t.Fatalf("expected external TaskStop schema, got %#v", stop)
	}
	required, ok := task.InputSchema["required"].([]string)
	if !ok || strings.Join(required, ",") != "prompt" {
		t.Fatalf("expected Task to require prompt, got %#v", task.InputSchema["required"])
	}
	required, ok = output.InputSchema["required"].([]string)
	if !ok || strings.Join(required, ",") != "task_id" {
		t.Fatalf("expected TaskOutput to require task_id, got %#v", output.InputSchema["required"])
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

func TestCapabilitySkillToolIsReadOnlyWithSchema(t *testing.T) {
	var skill ToolSpec
	for _, spec := range ToolSchemas() {
		if spec.Name == "skill" {
			skill = spec
			break
		}
	}
	if skill.Name == "" {
		t.Fatal("expected skill tool schema")
	}
	if skill.Kind != ToolReadOnly {
		t.Fatalf("expected skill to be read-only, got %s", skill.Kind)
	}
	required, ok := skill.InputSchema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "name" {
		t.Fatalf("expected skill to require name, got %#v", skill.InputSchema["required"])
	}
	properties, ok := skill.InputSchema["properties"].(map[string]any)
	if !ok || properties["name"] == nil || properties["start_line"] == nil || properties["line_count"] == nil {
		t.Fatalf("expected skill schema properties, got %#v", skill.InputSchema["properties"])
	}
}

func TestCapabilityMCPIsSingleExternalTool(t *testing.T) {
	var mcpTools []ToolSpec
	for _, spec := range ToolSchemas() {
		if spec.Name == "mcp" {
			mcpTools = append(mcpTools, spec)
		}
	}
	if len(mcpTools) != 1 {
		t.Fatalf("expected one builtin mcp tool, got %#v", mcpTools)
	}
	mcp := mcpTools[0]
	if mcp.Kind != ToolExternal {
		t.Fatalf("expected mcp to be external, got %s", mcp.Kind)
	}
	required, ok := mcp.InputSchema["required"].([]string)
	if !ok || strings.Join(required, ",") != "server,tool" {
		t.Fatalf("expected mcp to require server and tool, got %#v", mcp.InputSchema["required"])
	}
	properties, ok := mcp.InputSchema["properties"].(map[string]any)
	if !ok || properties["server"] == nil || properties["tool"] == nil || properties["arguments"] == nil {
		t.Fatalf("expected mcp schema properties, got %#v", mcp.InputSchema["properties"])
	}
}

func TestBuiltinToolsExposeAccessDescriptors(t *testing.T) {
	byName := map[string]ToolSpec{}
	for _, spec := range ToolSchemas() {
		byName[spec.Name] = spec
	}

	assertAccess(t, byName["read"], ToolAccessRead, ToolAccessPath, "path", "")
	assertAccess(t, byName["list"], ToolAccessRead, ToolAccessPath, "path", ".")
	assertAccess(t, byName["search"], ToolAccessRead, ToolAccessWorkspace, "", "")
	assertAccess(t, byName["write"], ToolAccessWrite, ToolAccessPath, "path", "")
	assertAccess(t, byName["todo_write"], ToolAccessWrite, ToolAccessTodo, "", "")
	assertAccess(t, byName["Task"], ToolAccessExclusive, ToolAccessWorkspace, "", "")
	assertAccess(t, byName["TaskOutput"], ToolAccessRead, ToolAccessTask, "task_id", "")

	access, ok := ToolAccessFor("READ")
	if !ok {
		t.Fatal("expected access descriptor lookup to be case-insensitive")
	}
	if access.Mode != ToolAccessRead || access.Resource != ToolAccessPath || access.Argument != "path" {
		t.Fatalf("unexpected read access descriptor %#v", access)
	}
}

func assertAccess(t *testing.T, spec ToolSpec, mode ToolAccessMode, resource ToolAccessResource, argument string, fallback string) {
	t.Helper()
	if spec.Name == "" {
		t.Fatal("missing tool spec")
	}
	if spec.Access == nil {
		t.Fatalf("expected %s to expose access descriptor", spec.Name)
	}
	if spec.Access.Mode != mode || spec.Access.Resource != resource || spec.Access.Argument != argument || spec.Access.Default != fallback {
		t.Fatalf("unexpected access for %s: %#v", spec.Name, spec.Access)
	}
}
