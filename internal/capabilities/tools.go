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
	Name        string   `json:"name"`
	Usage       string   `json:"usage"`
	Description string   `json:"description"`
	Kind        ToolKind `json:"kind"`
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
	{Name: "list", Usage: "list <path>", Description: "列出目录内容，默认隐藏点文件。", Kind: ToolReadOnly},
	{Name: "tree", Usage: "tree <path> <max depth>", Description: "按深度查看目录树。", Kind: ToolReadOnly},
	{Name: "glob", Usage: "glob <pattern> <path>", Description: "用 glob 查找文件路径。", Kind: ToolReadOnly},
	{Name: "stat", Usage: "stat <path>", Description: "查看文件大小、权限和是否目录。", Kind: ToolReadOnly},
	{Name: "read", Usage: "read <path> [start line] [line count]", Description: "按行读取文本文件，自动截断过长内容。", Kind: ToolReadOnly},
	{Name: "document", Usage: "document <path> [start line] [line count]", Description: "读取 PDF/DOCX 文档文本，自动截断过长内容。", Kind: ToolReadOnly},
	{Name: "search", Usage: "search <query>", Description: "优先使用 ripgrep 在 workspace 内搜索文本。", Kind: ToolReadOnly},
	{Name: "write", Usage: "write <path> <content>", Description: "写入或覆盖文件内容。", Kind: ToolWrite},
	{Name: "append", Usage: "append <path> <content>", Description: "追加文件内容。", Kind: ToolWrite},
	{Name: "edit", Usage: "edit <path> <old text> <new text> [all]", Description: "做精确文本替换，默认要求 old text 唯一。", Kind: ToolWrite},
	{Name: "replace", Usage: "replace <path> <old> <new>", Description: "替换文件中所有匹配文本。", Kind: ToolWrite},
	{Name: "mkdir", Usage: "mkdir <path>", Description: "创建 workspace 内目录。", Kind: ToolWrite},
	{Name: "delete", Usage: "delete <path>", Description: "删除 workspace 内文件或目录。", Kind: ToolWrite},
	{Name: "run", Usage: "run <shell command>", Description: "在 workspace/sandbox 中执行 shell 命令。", Kind: ToolShell},
	{Name: "diff", Usage: "diff", Description: "输出当前 workspace 变更。", Kind: ToolReadOnly},
	{Name: "mcp", Usage: "mcp <server> <tool> <json arguments>", Description: "调用已配置 MCP server 暴露的工具。", Kind: ToolExternal},
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
