package task

import (
	"context"
	"time"

	"github.com/Lioooooo123/liora/internal/agent"
)

type ChildTaskRequest struct {
	Prompt       string
	SubagentName string
	Role         string
	Scope        TaskScope
}

type ChildTaskOutputRequest struct {
	TaskID string
	Wait   time.Duration
	Limit  int
}

type ChildTaskStopRequest struct {
	TaskID string
	Reason string
}

type TaskController interface {
	CreateChildTask(ctx context.Context, parent Task, request ChildTaskRequest) (Task, error)
	ReadChildTaskOutput(ctx context.Context, parent Task, request ChildTaskOutputRequest) (Task, string, error)
	StopChildTask(ctx context.Context, parent Task, request ChildTaskStopRequest) (Task, error)
}

type taskToolExecutor struct {
	parent     Task
	controller TaskController
}

func newTaskToolExecutor(parent Task, controller TaskController) agent.TaskExecutor {
	if controller == nil {
		return nil
	}
	return taskToolExecutor{parent: parent, controller: controller}
}

func (e taskToolExecutor) StartTask(ctx context.Context, request agent.TaskRequest) (agent.TaskResult, error) {
	child, err := e.controller.CreateChildTask(ctx, e.parent, ChildTaskRequest{
		Prompt:       request.Prompt,
		SubagentName: request.SubagentName,
		Role:         request.Role,
		Scope:        agentScopeToTaskScope(request.Scope),
	})
	if err != nil {
		return agent.TaskResult{}, err
	}
	return agent.TaskResult{TaskID: child.ID, Status: string(child.Status)}, nil
}

func (e taskToolExecutor) ReadTaskOutput(ctx context.Context, request agent.TaskOutputRequest) (agent.TaskOutputResult, error) {
	wait := time.Duration(request.WaitMilliseconds) * time.Millisecond
	child, output, err := e.controller.ReadChildTaskOutput(ctx, e.parent, ChildTaskOutputRequest{
		TaskID: request.TaskID,
		Wait:   wait,
		Limit:  request.Limit,
	})
	if err != nil {
		return agent.TaskOutputResult{}, err
	}
	return agent.TaskOutputResult{TaskID: child.ID, Status: string(child.Status), Output: output}, nil
}

func (e taskToolExecutor) StopTask(ctx context.Context, request agent.TaskStopRequest) (agent.TaskStopResult, error) {
	child, err := e.controller.StopChildTask(ctx, e.parent, ChildTaskStopRequest{
		TaskID: request.TaskID,
		Reason: request.Reason,
	})
	if err != nil {
		return agent.TaskStopResult{}, err
	}
	return agent.TaskStopResult{TaskID: child.ID, Status: string(child.Status)}, nil
}

func agentScopeToTaskScope(scope agent.TaskToolScope) TaskScope {
	return TaskScope{
		Paths:           append([]string(nil), scope.Paths...),
		NetworkHosts:    append([]string(nil), scope.NetworkHosts...),
		MCPServers:      append([]string(nil), scope.MCPServers...),
		MCPTools:        append([]string(nil), scope.MCPTools...),
		ApprovalActions: append([]string(nil), scope.ApprovalActions...),
	}
}
