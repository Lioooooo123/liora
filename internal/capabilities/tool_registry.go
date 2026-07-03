package capabilities

import (
	"sort"
	"strings"
)

func BuiltinTools() []ToolSpec {
	tools := make([]ToolSpec, 0, len(builtinTools))
	for _, tool := range builtinTools {
		tools = append(tools, withAccessSpec(tool))
	}
	sort.SliceStable(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})
	return tools
}

func HasBuiltinTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, tool := range builtinTools {
		if strings.EqualFold(tool.Name, name) {
			return true
		}
	}
	return false
}

func ToolAccessFor(name string) (ToolAccessSpec, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, tool := range builtinTools {
		if strings.EqualFold(tool.Name, name) {
			return accessSpecForTool(tool), true
		}
	}
	return ToolAccessSpec{}, false
}

func PlannerToolList() string {
	var lines []string
	for _, tool := range builtinTools {
		lines = append(lines, "- "+tool.Usage)
	}
	return strings.Join(lines, "\n")
}

func HumanToolList() string {
	var lines []string
	for _, tool := range BuiltinTools() {
		lines = append(lines, "- "+tool.Usage+" ["+string(tool.Kind)+"] - "+tool.Description)
	}
	return strings.Join(lines, "\n")
}

func ToolSchemas() []ToolSpec {
	var specs []ToolSpec
	for _, tool := range builtinTools {
		if tool.InputSchema == nil {
			continue
		}
		specs = append(specs, withAccessSpec(tool))
	}
	sort.SliceStable(specs, func(i, j int) bool {
		return specs[i].Name < specs[j].Name
	})
	return specs
}

func withAccessSpec(tool ToolSpec) ToolSpec {
	access := accessSpecForTool(tool)
	tool.Access = &access
	return tool
}
