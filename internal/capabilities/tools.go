package capabilities

import (
	"sort"
	"strings"
)

type ToolKind string

const (
	ToolReadOnly ToolKind = "read_only"
	ToolWrite    ToolKind = "write"
	ToolShell    ToolKind = "shell"
	ToolExternal ToolKind = "external"
)

type ToolSpec struct {
	Name        string         `json:"name"`
	Usage       string         `json:"usage"`
	Description string         `json:"description"`
	Kind        ToolKind       `json:"kind"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

type MCPToolSpec struct {
	Server      string         `json:"server"`
	Name        string         `json:"name"`
	Usage       string         `json:"usage"`
	Description string         `json:"description"`
	Kind        ToolKind       `json:"kind"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
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
	{Name: "run", Usage: "run <shell command>", Description: "在 workspace/sandbox 中执行 shell 命令。", Kind: ToolShell,
		InputSchema: objectSchema(props{
			"command": stringProp("要执行的 shell 命令。"),
		}, []string{"command"})},
	{Name: "diff", Usage: "diff", Description: "输出当前 workspace 变更。", Kind: ToolReadOnly,
		InputSchema: objectSchema(props{}, nil)},
	{Name: "mcp", Usage: "mcp <server> <tool> <json arguments>", Description: "调用已配置 MCP server 暴露的工具。", Kind: ToolExternal,
		InputSchema: objectSchema(props{
			"server":    stringProp("已配置的 MCP server 名称。"),
			"tool":      stringProp("要调用的 MCP 工具名称。"),
			"arguments": objectProp("传给 MCP 工具的参数对象。"),
		}, []string{"server", "tool"})},
}

func BuiltinTools() []ToolSpec {
	tools := append([]ToolSpec(nil), builtinTools...)
	sort.SliceStable(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})
	return tools
}

func HasBuiltinTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, tool := range builtinTools {
		if tool.Name == name {
			return true
		}
	}
	return false
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

// ToolSchemas returns builtin tools that carry a JSON Schema, sorted by name,
// for constructing native structured tool-call requests.
func ToolSchemas() []ToolSpec {
	var specs []ToolSpec
	for _, tool := range builtinTools {
		if tool.InputSchema == nil {
			continue
		}
		specs = append(specs, tool)
	}
	sort.SliceStable(specs, func(i, j int) bool {
		return specs[i].Name < specs[j].Name
	})
	return specs
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
