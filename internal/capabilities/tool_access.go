package capabilities

type ToolAccessMode string

const (
	ToolAccessRead      ToolAccessMode = "read"
	ToolAccessWrite     ToolAccessMode = "write"
	ToolAccessExclusive ToolAccessMode = "exclusive"
)

type ToolAccessResource string

const (
	ToolAccessPath      ToolAccessResource = "path"
	ToolAccessWorkspace ToolAccessResource = "workspace"
	ToolAccessTodo      ToolAccessResource = "todo"
	ToolAccessSkill     ToolAccessResource = "skill"
	ToolAccessTask      ToolAccessResource = "task"
)

type ToolAccessSpec struct {
	Mode     ToolAccessMode     `json:"mode"`
	Resource ToolAccessResource `json:"resource"`
	Argument string             `json:"argument,omitempty"`
	Default  string             `json:"default,omitempty"`
}

func accessSpecForTool(tool ToolSpec) ToolAccessSpec {
	switch tool.Name {
	case "list", "tree", "glob":
		return pathAccess(ToolAccessRead, "path", ".")
	case "stat", "read", "document":
		return pathAccess(ToolAccessRead, "path", "")
	case "search", "diff":
		return fixedAccess(ToolAccessRead, ToolAccessWorkspace)
	case "skill":
		return argAccess(ToolAccessRead, ToolAccessSkill, "name")
	case "todo_read":
		return fixedAccess(ToolAccessRead, ToolAccessTodo)
	case "write", "append", "edit", "replace", "mkdir", "delete":
		return pathAccess(ToolAccessWrite, "path", "")
	case "todo_write":
		return fixedAccess(ToolAccessWrite, ToolAccessTodo)
	case "TaskOutput":
		return argAccess(ToolAccessRead, ToolAccessTask, "task_id")
	}
	switch tool.Kind {
	case ToolReadOnly:
		return fixedAccess(ToolAccessRead, ToolAccessWorkspace)
	case ToolWrite:
		return fixedAccess(ToolAccessWrite, ToolAccessWorkspace)
	default:
		return fixedAccess(ToolAccessExclusive, ToolAccessWorkspace)
	}
}

func pathAccess(mode ToolAccessMode, argument string, fallback string) ToolAccessSpec {
	return ToolAccessSpec{Mode: mode, Resource: ToolAccessPath, Argument: argument, Default: fallback}
}

func argAccess(mode ToolAccessMode, resource ToolAccessResource, argument string) ToolAccessSpec {
	return ToolAccessSpec{Mode: mode, Resource: resource, Argument: argument}
}

func fixedAccess(mode ToolAccessMode, resource ToolAccessResource) ToolAccessSpec {
	return ToolAccessSpec{Mode: mode, Resource: resource}
}
