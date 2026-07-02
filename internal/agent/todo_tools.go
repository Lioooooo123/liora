package agent

import (
	"context"
	"encoding/json"
	"fmt"
)

func (a *Agent) executeTodoWrite(ctx context.Context, args map[string]any) (string, error) {
	if a.todos == nil {
		return "", fmt.Errorf("no todo executor configured")
	}
	todos, err := parseTodoItems(args)
	if err != nil {
		return "", err
	}
	written, err := a.todos.WriteTodos(ctx, todos)
	if err != nil {
		return "", err
	}
	return renderTodos(written)
}

func (a *Agent) executeTodoRead(ctx context.Context) (string, error) {
	if a.todos == nil {
		return "", fmt.Errorf("no todo executor configured")
	}
	todos, err := a.todos.ReadTodos(ctx)
	if err != nil {
		return "", err
	}
	return renderTodos(todos)
}

func parseTodoItems(args map[string]any) ([]Todo, error) {
	raw, ok := args["todos"]
	if !ok {
		return nil, fmt.Errorf("todos are required")
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("todos must be an array")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("todos are required")
	}
	todos := make([]Todo, 0, len(items))
	for index, rawItem := range items {
		itemMap, ok := rawItem.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("todo %d must be an object", index+1)
		}
		todos = append(todos, Todo{
			ID:           stringField(itemMap, "id"),
			SourceTaskID: stringField(itemMap, "source_task_id"),
			Content:      stringField(itemMap, "content"),
			Status:       stringField(itemMap, "status"),
			Priority:     stringField(itemMap, "priority"),
		})
	}
	return todos, nil
}

func stringField(values map[string]any, name string) string {
	if value, ok := values[name].(string); ok {
		return value
	}
	return ""
}

func renderTodos(todos []Todo) (string, error) {
	bytes, err := json.MarshalIndent(struct {
		Todos []Todo `json:"todos"`
	}{Todos: todos}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
