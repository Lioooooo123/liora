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
		lines = append(lines, "- "+HumanToolLine(tool))
	}
	return strings.Join(lines, "\n")
}

func HumanToolLine(tool ToolSpec) string {
	line := tool.Usage + " [" + string(tool.Kind) + "]"
	if tool.Access != nil {
		line += " " + HumanToolAccess(*tool.Access)
	}
	if strings.TrimSpace(tool.Description) != "" {
		line += " - " + tool.Description
	}
	return line
}

func HumanToolAccess(access ToolAccessSpec) string {
	parts := []string{"access=" + string(access.Mode) + ":" + humanToolAccessResource(access)}
	if strings.TrimSpace(access.Default) != "" {
		parts = append(parts, "default="+access.Default)
	}
	parts = append(parts, "concurrency="+humanToolConcurrency(access.Mode))
	return strings.Join(parts, " ")
}

func humanToolAccessResource(access ToolAccessSpec) string {
	resource := string(access.Resource)
	if strings.TrimSpace(access.Argument) != "" {
		return resource + "(" + access.Argument + ")"
	}
	return resource
}

func humanToolConcurrency(mode ToolAccessMode) string {
	switch mode {
	case ToolAccessRead:
		return "shared-read"
	case ToolAccessWrite:
		return "serialized-on-overlap"
	default:
		return "exclusive"
	}
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
