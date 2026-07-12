package tui

import (
	"strings"
)

type commandSpec struct {
	Value          string
	Label          string
	Description    string
	Group          string
	AllowDuringRun bool
}

var builtinCommands = []commandSpec{
	{Value: "/help", Label: "/help", Description: "show commands", Group: "system", AllowDuringRun: true},
	{Value: "/tools", Label: "/tools", Description: "list available tools", Group: "work"},
	{Value: "/workbench", Label: "/workbench", Description: "show workspace status", Group: "work", AllowDuringRun: true},
	{Value: "/spawn ", Label: "/spawn <request>", Description: "start a background turn", Group: "work"},
	{Value: "/watch", Label: "/watch [active|task_id...]", Description: "watch task activity", Group: "work", AllowDuringRun: true},
	{Value: "/tasks", Label: "/tasks", Description: "list daemon tasks", Group: "history"},
	{Value: "/sessions", Label: "/sessions", Description: "list workspace sessions", Group: "history"},
	{Value: "/threads", Label: "/threads", Description: "list conversation threads", Group: "history"},
	{Value: "/timeline", Label: "/timeline [limit]", Description: "show session timeline", Group: "history", AllowDuringRun: true},
	{Value: "/transcript", Label: "/transcript [limit]", Description: "show transcript", Group: "history"},
	{Value: "/todo", Label: "/todo", Description: "show current todos", Group: "history", AllowDuringRun: true},
	{Value: "/history ", Label: "/history <query>", Description: "search session history", Group: "history"},
	{Value: "/last", Label: "/last", Description: "replay latest task", Group: "history"},
	{Value: "/tail", Label: "/tail [task_id|limit]", Description: "show recent events", Group: "history", AllowDuringRun: true},
	{Value: "/artifact ", Label: "/artifact <uri>", Description: "read artifact pages", Group: "history"},
	{Value: "/diff", Label: "/diff [task_id]", Description: "review current patch", Group: "changes", AllowDuringRun: true},
	{Value: "/apply", Label: "/apply", Description: "apply current patch", Group: "changes"},
	{Value: "/cancel", Label: "/cancel [task_id]", Description: "cancel running task", Group: "changes", AllowDuringRun: true},
	{Value: "/approvals", Label: "/approvals", Description: "list pending approvals", Group: "approval", AllowDuringRun: true},
	{Value: "/approve", Label: "/approve [task_id]", Description: "approve pending request", Group: "approval", AllowDuringRun: true},
	{Value: "/deny", Label: "/deny [task_id]", Description: "deny pending request", Group: "approval", AllowDuringRun: true},
	{Value: "/permissions", Label: "/permissions", Description: "list permission rules", Group: "approval"},
	{Value: "/permission-rule ", Label: "/permission-rule <add|delete>", Description: "manage permission rules", Group: "approval"},
	{Value: "/memory", Label: "/memory", Description: "manage memory", Group: "context"},
	{Value: "/schedule", Label: "/schedule", Description: "manage schedules", Group: "context"},
	{Value: "/goal", Label: "/goal", Description: "show or set active goal", Group: "context"},
	{Value: "/skills", Label: "/skills", Description: "list installed skills", Group: "context"},
	{Value: "/skill ", Label: "/skill <name>", Description: "read an installed skill", Group: "context"},
	{Value: "/mcp", Label: "/mcp", Description: "list MCP tools", Group: "context"},
	{Value: "/context", Label: "/context [limit budget]", Description: "show selected context", Group: "context"},
	{Value: "/prompt-context", Label: "/prompt-context [--last]", Description: "inspect prompt context", Group: "context"},
	{Value: "/compact", Label: "/compact [auto] [limit budget]", Description: "compact session context", Group: "context"},
	{Value: "/model", Label: "/model", Description: "show or set thread model", Group: "system"},
	{Value: "/auth", Label: "/auth", Description: "show Codex authentication", Group: "system"},
	{Value: "/login", Label: "/login [codex]", Description: "sign in to Codex", Group: "system"},
	{Value: "/logout", Label: "/logout [codex]", Description: "remove Codex credentials", Group: "system"},
	{Value: "/doctor", Label: "/doctor", Description: "run local preflight", Group: "system"},
	{Value: "/config", Label: "/config", Description: "show resolved config", Group: "system"},
	{Value: "/status", Label: "/status", Description: "show daemon status", Group: "system", AllowDuringRun: true},
	{Value: "/thread ", Label: "/thread <id>", Description: "switch thread", Group: "session"},
	{Value: "/thread-new ", Label: "/thread-new <title>", Description: "create thread", Group: "session"},
	{Value: "/thread-send ", Label: "/thread-send <id> <message>", Description: "send to thread", Group: "session"},
	{Value: "/thread-inbox ", Label: "/thread-inbox [id]", Description: "read thread inbox", Group: "session"},
	{Value: "/thread-rename ", Label: "/thread-rename <id> <title>", Description: "rename thread", Group: "session"},
	{Value: "/thread-archive ", Label: "/thread-archive <id>", Description: "archive thread", Group: "session"},
	{Value: "/resume ", Label: "/resume <task_id>", Description: "replay task", Group: "session"},
	{Value: "/resume-session ", Label: "/resume-session <id>", Description: "reattach session", Group: "session"},
	{Value: "/resume-latest", Label: "/resume-latest", Description: "reattach latest session", Group: "session"},
	{Value: "/continue", Label: "/continue", Description: "alias for resume latest", Group: "session"},
	{Value: "/new-session", Label: "/new-session", Description: "start a new session", Group: "session"},
	{Value: "/clear", Label: "/clear", Description: "start a new session", Group: "session"},
	{Value: "/exit", Label: "/exit", Description: "quit", Group: "session"},
}

func builtinCommandCompletions(line string) []Completion {
	commands := builtinCommands
	if strings.TrimSpace(line) == "/" {
		commands = promotedBuiltinCommands()
	}
	items := make([]Completion, 0, len(commands))
	for _, command := range commands {
		items = append(items, Completion{
			Value:       command.Value,
			Label:       command.Label,
			Description: command.Description,
			Kind:        "command",
		})
	}
	return items
}

func promotedBuiltinCommands() []commandSpec {
	names := []string{"/help", "/diff", "/apply", "/tools", "/workbench", "/timeline", "/memory", "/model"}
	commands := make([]commandSpec, 0, len(names))
	for _, name := range names {
		if command, ok := findBuiltinCommand(name); ok {
			commands = append(commands, command)
		}
	}
	return commands
}

func findBuiltinCommand(name string) (commandSpec, bool) {
	for _, command := range builtinCommands {
		if commandValueName(command.Value) == name {
			return command, true
		}
	}
	return commandSpec{}, false
}

func helpText() string {
	groupOrder := []string{"work", "history", "changes", "approval", "context", "system", "session"}
	lines := make([]string, 0, len(groupOrder))
	for _, group := range groupOrder {
		labels := commandLabelsForGroup(group)
		if len(labels) == 0 {
			continue
		}
		lines = append(lines, commandStyle.Render(padCommandGroup(group))+" "+strings.Join(labels, "  "))
	}
	return "Type a natural-language request, or use a command.\n\n" + strings.Join(lines, "\n")
}

func commandLabelsForGroup(group string) []string {
	var labels []string
	for _, command := range builtinCommands {
		if command.Group == group {
			labels = append(labels, command.Label)
		}
	}
	return labels
}

func padCommandGroup(group string) string {
	return (group + "        ")[:8]
}

func commandCanRunDuringTurn(line string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return false
	}
	for _, command := range builtinCommands {
		if commandValueName(command.Value) == fields[0] {
			return command.AllowDuringRun
		}
	}
	return false
}

func commandValueName(value string) string {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
