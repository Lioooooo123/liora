package task

import (
	"context"
	"time"

	"github.com/Lioooooo123/liora/internal/agent"
)

type repositoryTodoExecutor struct {
	repo         *Repository
	sessionID    string
	sourceTaskID string
}

func (e repositoryTodoExecutor) WriteTodos(ctx context.Context, todos []agent.Todo) ([]agent.Todo, error) {
	items := make([]TodoWriteItem, 0, len(todos))
	for _, todo := range todos {
		items = append(items, TodoWriteItem{
			ID:           todo.ID,
			SourceTaskID: todo.SourceTaskID,
			Content:      todo.Content,
			Status:       todo.Status,
			Priority:     todo.Priority,
		})
	}
	written, err := e.repo.WriteTodos(ctx, TodoWriteRequest{
		SessionID:    e.sessionID,
		SourceTaskID: e.sourceTaskID,
		Todos:        items,
	})
	if err != nil {
		return nil, err
	}
	return agentTodos(written), nil
}

func (e repositoryTodoExecutor) ReadTodos(ctx context.Context) ([]agent.Todo, error) {
	todos, err := e.repo.TodosBySession(ctx, e.sessionID)
	if err != nil {
		return nil, err
	}
	return agentTodos(todos), nil
}

func agentTodos(todos []Todo) []agent.Todo {
	out := make([]agent.Todo, 0, len(todos))
	for _, todo := range todos {
		out = append(out, agent.Todo{
			ID:           todo.ID,
			SourceTaskID: todo.SourceTaskID,
			Content:      todo.Content,
			Status:       todo.Status,
			Priority:     todo.Priority,
			UpdatedAt:    todo.UpdatedAt.Format(time.RFC3339Nano),
		})
	}
	return out
}
