package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func (l *ToolLoop) executeToolCall(ctx context.Context, name string, args map[string]any) (string, error) {
	workspace := l.agent.workspace
	switch name {
	case "list":
		entries, err := workspace.List(argStringOr(args, "path", "."))
		if err != nil {
			return "", err
		}
		return strings.Join(entries, "\n"), nil
	case "tree":
		entries, err := workspace.Tree(argStringOr(args, "path", "."), argInt(args, "max_depth", 0))
		if err != nil {
			return "", err
		}
		return strings.Join(entries, "\n"), nil
	case "glob":
		pattern := argString(args, "pattern")
		if pattern == "" {
			return "", fmt.Errorf("glob expects a pattern")
		}
		matches, err := workspace.Glob(pattern, argStringOr(args, "path", "."), true)
		if err != nil {
			return "", err
		}
		return strings.Join(matches, "\n"), nil
	case "stat":
		path := argString(args, "path")
		if path == "" {
			return "", fmt.Errorf("stat expects a path")
		}
		info, err := workspace.Stat(path)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s size=%d mode=%s dir=%t", info.Path, info.Size, info.Mode, info.IsDir), nil
	case "read":
		path := argString(args, "path")
		if path == "" {
			return "", fmt.Errorf("read expects a path")
		}
		return workspace.ReadFileRange(path, argInt(args, "start_line", 1), argInt(args, "line_count", 1000))
	case "document":
		path := argString(args, "path")
		if path == "" {
			return "", fmt.Errorf("document expects a path")
		}
		return workspace.ReadDocumentRange(path, argInt(args, "start_line", 1), argInt(args, "line_count", 1000))
	case "skill":
		if l.agent.skills == nil {
			return "", fmt.Errorf("no skill reader configured")
		}
		name := argString(args, "name")
		if name == "" {
			return "", fmt.Errorf("skill expects a name")
		}
		return l.agent.skills.ReadSkill(workspace.Root(), name, argInt(args, "start_line", 1), argInt(args, "line_count", 1000))
	case "search":
		query := argString(args, "query")
		if query == "" {
			return "", fmt.Errorf("search expects a query")
		}
		matches, err := workspace.Search(query)
		if err != nil {
			return "", err
		}
		var builder strings.Builder
		for _, match := range matches {
			builder.WriteString(match.Path)
			builder.WriteString(":")
			builder.WriteString(strconv.Itoa(match.Line))
			builder.WriteString(": ")
			builder.WriteString(match.Content)
			builder.WriteString("\n")
		}
		return builder.String(), nil
	case "write":
		path := argString(args, "path")
		if path == "" {
			return "", fmt.Errorf("write expects a path")
		}
		if err := workspace.WriteFile(path, argString(args, "content")); err != nil {
			return "", err
		}
		return "written " + path, nil
	case "append":
		path := argString(args, "path")
		if path == "" {
			return "", fmt.Errorf("append expects a path")
		}
		if err := workspace.AppendFile(path, argString(args, "content")); err != nil {
			return "", err
		}
		return "appended " + path, nil
	case "edit":
		path := argString(args, "path")
		oldText := argString(args, "old_text")
		newText := argString(args, "new_text")
		if path == "" || oldText == "" {
			return "", fmt.Errorf("edit expects path, old_text and new_text")
		}
		if err := workspace.Edit(path, oldText, newText, argBool(args, "all")); err != nil {
			return "", err
		}
		return "edited " + path, nil
	case "replace":
		path := argString(args, "path")
		oldText := argString(args, "old_text")
		newText := argString(args, "new_text")
		if path == "" || oldText == "" {
			return "", fmt.Errorf("replace expects path, old_text and new_text")
		}
		if err := workspace.Replace(path, oldText, newText); err != nil {
			return "", err
		}
		return "replaced " + path, nil
	case "mkdir":
		path := argString(args, "path")
		if path == "" {
			return "", fmt.Errorf("mkdir expects a path")
		}
		if err := workspace.Mkdir(path); err != nil {
			return "", err
		}
		return "created directory " + path, nil
	case "delete":
		path := argString(args, "path")
		if path == "" {
			return "", fmt.Errorf("delete expects a path")
		}
		if err := workspace.Delete(path); err != nil {
			return "", err
		}
		return "deleted " + path, nil
	case "run":
		command := argString(args, "command")
		if command == "" {
			return "", fmt.Errorf("run expects a command")
		}
		result, err := l.agent.runShell(ctx, command)
		output := result.Stdout + result.Stderr
		if err != nil {
			return output, err
		}
		return output, nil
	case "diff":
		return workspace.GitDiff()
	case "todo_write":
		return l.agent.executeTodoWrite(ctx, args)
	case "todo_read":
		return l.agent.executeTodoRead(ctx)
	case "mcp":
		if l.agent.mcp == nil {
			return "", fmt.Errorf("no MCP servers configured")
		}
		server := argString(args, "server")
		tool := argString(args, "tool")
		if server == "" || tool == "" {
			return "", fmt.Errorf("mcp expects a server and tool name")
		}
		callArgs := map[string]any{}
		if nested, ok := args["arguments"].(map[string]any); ok {
			callArgs = nested
		}
		return l.agent.mcp.Call(ctx, server, tool, callArgs)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}
