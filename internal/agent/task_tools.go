package agent

import (
	"context"
	"fmt"
	"strings"
)

func (a *Agent) executeTask(ctx context.Context, args map[string]any) (string, error) {
	if a.tasks == nil {
		return "", fmt.Errorf("no task executor configured")
	}
	request := TaskRequest{
		Prompt:       strings.TrimSpace(argString(args, "prompt")),
		SubagentName: strings.TrimSpace(argString(args, "subagent_name")),
		Role:         strings.TrimSpace(argString(args, "role")),
		Scope:        parseTaskToolScope(args),
	}
	if request.Prompt == "" {
		return "", fmt.Errorf("Task expects a prompt")
	}
	result, err := a.tasks.StartTask(ctx, request)
	if err != nil {
		return "", err
	}
	return renderTaskResult(result), nil
}

func (a *Agent) executeTaskOutput(ctx context.Context, args map[string]any) (string, error) {
	if a.tasks == nil {
		return "", fmt.Errorf("no task executor configured")
	}
	waitMilliseconds, err := argNonNegativeInt(args, "wait_ms", 0)
	if err != nil {
		return "", fmt.Errorf("TaskOutput %w", err)
	}
	limit, err := argNonNegativeInt(args, "limit", 0)
	if err != nil {
		return "", fmt.Errorf("TaskOutput %w", err)
	}
	request := TaskOutputRequest{
		TaskID:           strings.TrimSpace(argString(args, "task_id")),
		WaitMilliseconds: waitMilliseconds,
		Limit:            limit,
	}
	if request.TaskID == "" {
		return "", fmt.Errorf("TaskOutput expects a task_id")
	}
	result, err := a.tasks.ReadTaskOutput(ctx, request)
	if err != nil {
		return "", err
	}
	return renderTaskOutputResult(result), nil
}

func (a *Agent) executeTaskStop(ctx context.Context, args map[string]any) (string, error) {
	if a.tasks == nil {
		return "", fmt.Errorf("no task executor configured")
	}
	request := TaskStopRequest{
		TaskID: strings.TrimSpace(argString(args, "task_id")),
		Reason: strings.TrimSpace(argString(args, "reason")),
	}
	if request.TaskID == "" {
		return "", fmt.Errorf("TaskStop expects a task_id")
	}
	result, err := a.tasks.StopTask(ctx, request)
	if err != nil {
		return "", err
	}
	return renderTaskStopResult(result), nil
}

func parseTaskToolScope(args map[string]any) TaskToolScope {
	scope, ok := args["scope"].(map[string]any)
	if !ok || scope == nil {
		return TaskToolScope{}
	}
	return TaskToolScope{
		Paths:           argStringList(scope, "paths"),
		NetworkHosts:    argStringList(scope, "network_hosts"),
		MCPServers:      argStringList(scope, "mcp_servers"),
		MCPTools:        argStringList(scope, "mcp_tools"),
		ApprovalActions: argStringList(scope, "approval_actions"),
	}
}

func argStringList(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text != "" {
			values = append(values, text)
		}
	}
	return values
}

func renderTaskResult(result TaskResult) string {
	return strings.Join([]string{
		"task_id: " + result.TaskID,
		"status: " + result.Status,
	}, "\n")
}

func renderTaskOutputResult(result TaskOutputResult) string {
	lines := []string{
		"task_id: " + result.TaskID,
		"status: " + result.Status,
	}
	if strings.TrimSpace(result.Output) != "" {
		lines = append(lines, "output:\n"+result.Output)
	}
	return strings.Join(lines, "\n")
}

func renderTaskStopResult(result TaskStopResult) string {
	return strings.Join([]string{
		"task_id: " + result.TaskID,
		"status: " + result.Status,
	}, "\n")
}
