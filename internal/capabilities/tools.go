package capabilities

type ToolKind string

const (
	ToolReadOnly ToolKind = "read_only"
	ToolWrite    ToolKind = "write"
	ToolShell    ToolKind = "shell"
	ToolExternal ToolKind = "external"
)

type ToolSpec struct {
	Name        string          `json:"name"`
	Usage       string          `json:"usage"`
	Description string          `json:"description"`
	Kind        ToolKind        `json:"kind"`
	Access      *ToolAccessSpec `json:"access,omitempty"`
	InputSchema map[string]any  `json:"input_schema,omitempty"`
}

type MCPToolSpec struct {
	Server      string         `json:"server"`
	Name        string         `json:"name"`
	Usage       string         `json:"usage"`
	Description string         `json:"description"`
	Kind        ToolKind       `json:"kind"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Permissions []string       `json:"permissions,omitempty"`
}

type MCPServerStatus struct {
	Name        string   `json:"name"`
	Enabled     bool     `json:"enabled"`
	Source      string   `json:"source,omitempty"`
	Version     string   `json:"version,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
	ToolCount   int      `json:"tool_count"`
	Auth        string   `json:"auth"`
	LastError   string   `json:"last_error,omitempty"`
}

var builtinTools = []ToolSpec{
	{Name: "list", Usage: "list <path>", Description: "列出目录内容，默认隐藏点文件。", Kind: ToolReadOnly,
		InputSchema: objectSchema(props{
			"path": stringProp("要列出的目录相对路径，默认为当前目录。"),
		}, nil)},
	{Name: "tree", Usage: "tree <path> <max depth>", Description: "按深度查看目录树。", Kind: ToolReadOnly,
		InputSchema: objectSchema(props{
			"path":      stringProp("根目录相对路径，默认为当前目录。"),
			"max_depth": integerProp("遍历的最大深度，默认 2。"),
		}, nil)},
	{Name: "glob", Usage: "glob <pattern> <path>", Description: "用 glob 查找文件路径。", Kind: ToolReadOnly,
		InputSchema: objectSchema(props{
			"pattern": stringProp("glob 模式，例如 **/*.go。"),
			"path":    stringProp("搜索根目录相对路径，默认为当前目录。"),
		}, []string{"pattern"})},
	{Name: "stat", Usage: "stat <path>", Description: "查看文件大小、权限和是否目录。", Kind: ToolReadOnly,
		InputSchema: objectSchema(props{
			"path": stringProp("目标文件或目录的相对路径。"),
		}, []string{"path"})},
	{Name: "read", Usage: "read <path> [start line] [line count]", Description: "按行读取文本文件，自动截断过长内容。", Kind: ToolReadOnly,
		InputSchema: objectSchema(props{
			"path":       stringProp("文本文件相对路径。"),
			"start_line": integerProp("起始行号，从 1 开始，默认 1。"),
			"line_count": integerProp("读取行数，默认 1000。"),
		}, []string{"path"})},
	{Name: "document", Usage: "document <path> [start line] [line count]", Description: "读取 PDF/DOCX 文档文本，自动截断过长内容。", Kind: ToolReadOnly,
		InputSchema: objectSchema(props{
			"path":       stringProp("PDF 或 DOCX 文档相对路径。"),
			"start_line": integerProp("起始行号，从 1 开始，默认 1。"),
			"line_count": integerProp("读取行数，默认 1000。"),
		}, []string{"path"})},
	{Name: "skill", Usage: "skill <name> [start line] [line count]", Description: "按需读取已安装 Liora skill 的 SKILL.md 内容。", Kind: ToolReadOnly,
		InputSchema: objectSchema(props{
			"name":       stringProp("Skill 名称，对应 skills/<name>/SKILL.md。"),
			"start_line": integerProp("起始行号，从 1 开始，默认 1。"),
			"line_count": integerProp("读取行数，默认 1000。"),
		}, []string{"name"})},
	{Name: "search", Usage: "search <query>", Description: "优先使用 ripgrep 在 workspace 内搜索文本。", Kind: ToolReadOnly,
		InputSchema: objectSchema(props{
			"query": stringProp("要搜索的文本或正则。"),
		}, []string{"query"})},
	{Name: "write", Usage: "write <path> <content>", Description: "写入或覆盖文件内容。", Kind: ToolWrite,
		InputSchema: objectSchema(props{
			"path":    stringProp("目标文件相对路径。"),
			"content": stringProp("要写入的完整文件内容。"),
		}, []string{"path", "content"})},
	{Name: "append", Usage: "append <path> <content>", Description: "追加文件内容。", Kind: ToolWrite,
		InputSchema: objectSchema(props{
			"path":    stringProp("目标文件相对路径。"),
			"content": stringProp("要追加的内容。"),
		}, []string{"path", "content"})},
	{Name: "edit", Usage: "edit <path> <old text> <new text> [all]", Description: "做精确文本替换，默认要求 old text 唯一。", Kind: ToolWrite,
		InputSchema: objectSchema(props{
			"path":     stringProp("目标文件相对路径。"),
			"old_text": stringProp("要替换的原文本，默认必须在文件中唯一。"),
			"new_text": stringProp("替换后的新文本。"),
			"all":      booleanProp("是否替换所有匹配，默认 false。"),
		}, []string{"path", "old_text", "new_text"})},
	{Name: "replace", Usage: "replace <path> <old> <new>", Description: "替换文件中所有匹配文本。", Kind: ToolWrite,
		InputSchema: objectSchema(props{
			"path":     stringProp("目标文件相对路径。"),
			"old_text": stringProp("要替换的原文本。"),
			"new_text": stringProp("替换后的新文本。"),
		}, []string{"path", "old_text", "new_text"})},
	{Name: "mkdir", Usage: "mkdir <path>", Description: "创建 workspace 内目录。", Kind: ToolWrite,
		InputSchema: objectSchema(props{
			"path": stringProp("要创建的目录相对路径。"),
		}, []string{"path"})},
	{Name: "delete", Usage: "delete <path>", Description: "删除 workspace 内文件或目录。", Kind: ToolWrite,
		InputSchema: objectSchema(props{
			"path": stringProp("要删除的文件或目录相对路径。"),
		}, []string{"path"})},
	{Name: "run", Usage: "run <shell command>", Description: "在 workspace/sandbox 中执行 shell 命令；仅用于编译、测试或内建文件工具无法覆盖的检查。", Kind: ToolShell,
		InputSchema: objectSchema(props{
			"command": stringProp("要执行的 shell 命令。"),
		}, []string{"command"})},
	{Name: "diff", Usage: "diff", Description: "输出当前 workspace 变更。", Kind: ToolReadOnly,
		InputSchema: objectSchema(props{}, nil)},
	{Name: "todo_read", Usage: "todo_read", Description: "读取当前 daemon session 的持久 todo/plan 状态。", Kind: ToolReadOnly,
		InputSchema: objectSchema(props{}, nil)},
	{Name: "todo_write", Usage: "todo_write <json todos>", Description: "创建或更新当前 daemon session 的持久 todo/plan 状态。", Kind: ToolWrite,
		InputSchema: objectSchema(props{
			"todos": arrayProp("Todo 数组。每项包含 content，status 可为 pending/in_progress/done/cancelled，priority 可为 low/normal/high/critical。", objectSchema(props{
				"id":             stringProp("可选 todo id；省略时创建新 todo。"),
				"content":        stringProp("Todo 内容。"),
				"status":         stringProp("Todo 状态：pending、in_progress、done 或 cancelled。"),
				"priority":       stringProp("Todo 优先级：low、normal、high 或 critical。"),
				"source_task_id": stringProp("可选来源 task id；必须匹配当前 task。"),
			}, []string{"content"})),
		}, []string{"todos"})},
	{Name: "Task", Usage: "Task <prompt>", Description: "启动一个安全的子任务，把可独立探索或执行的工作交给子 agent。", Kind: ToolExternal,
		InputSchema: objectSchema(props{
			"prompt":        stringProp("子任务要完成的明确目标。"),
			"subagent_name": stringProp("可选子 agent 名称，用于展示和审计。"),
			"role":          stringProp("可选职责标签，例如 explorer、tester。"),
			"scope": objectSchema(props{
				"paths":            arrayProp("子任务允许访问的路径范围。", stringProp("路径。")),
				"network_hosts":    arrayProp("子任务允许访问的网络主机。", stringProp("主机名。")),
				"mcp_servers":      arrayProp("子任务允许使用的 MCP server。", stringProp("MCP server 名称。")),
				"mcp_tools":        arrayProp("子任务允许使用的 MCP tool。", stringProp("MCP tool 名称。")),
				"approval_actions": arrayProp("子任务允许请求的审批动作。", stringProp("审批动作。")),
			}, nil),
		}, []string{"prompt"})},
	{Name: "TaskOutput", Usage: "TaskOutput <task_id> [wait_ms] [limit]", Description: "读取当前父任务创建的子任务输出和状态。", Kind: ToolReadOnly,
		InputSchema: objectSchema(props{
			"task_id": stringProp("要读取的子任务 id。"),
			"wait_ms": integerProp("可选等待毫秒数，用于短暂等待子任务产生新输出或结束。"),
			"limit":   integerProp("输出最大字符数，默认 4000。"),
		}, []string{"task_id"})},
	{Name: "TaskStop", Usage: "TaskStop <task_id> [reason]", Description: "取消当前父任务创建的子任务，不改变父任务状态。", Kind: ToolExternal,
		InputSchema: objectSchema(props{
			"task_id": stringProp("要取消的子任务 id。"),
			"reason":  stringProp("可选取消原因。"),
		}, []string{"task_id"})},
	{Name: "mcp", Usage: "mcp <server> <tool> <json arguments>", Description: "仅当用户明确需要已配置 MCP server 时调用外部工具。", Kind: ToolExternal,
		InputSchema: objectSchema(props{
			"server":    stringProp("已配置的 MCP server 名称。"),
			"tool":      stringProp("要调用的 MCP 工具名称。"),
			"arguments": objectProp("传给 MCP 工具的参数对象。"),
		}, []string{"server", "tool"})},
}

type props map[string]map[string]any

func objectSchema(properties props, required []string) map[string]any {
	if properties == nil {
		properties = props{}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any(asAnyMap(properties)),
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func asAnyMap(properties props) map[string]any {
	out := make(map[string]any, len(properties))
	for name, prop := range properties {
		out[name] = prop
	}
	return out
}

func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func integerProp(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func booleanProp(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func objectProp(description string) map[string]any {
	return map[string]any{"type": "object", "description": description}
}

func arrayProp(description string, items map[string]any) map[string]any {
	return map[string]any{"type": "array", "description": description, "items": items}
}
