package task

import (
	"context"
	"fmt"
	"strings"
)

func (r *Runner) enforceTodoCompletionGate(ctx context.Context, task Task, result runtimeResult) error {
	if strings.TrimSpace(task.SessionID) == "" {
		return nil
	}
	todos, err := r.repo.TodosBySession(ctx, task.SessionID)
	if err != nil {
		return err
	}
	blockers := todoCompletionBlockers(todos)
	if len(blockers) == 0 {
		return nil
	}
	explanation := strings.Join([]string{result.answer, result.summary}, "\n")
	if todoCompletionExplained(explanation, blockers) {
		return nil
	}
	return fmt.Errorf("todo completion gate blocked: unresolved todos require explanation before completion: %s", formatTodoCompletionBlockers(blockers))
}

func todoCompletionBlockers(todos []Todo) []Todo {
	blockers := make([]Todo, 0, len(todos))
	for _, todo := range todos {
		status := strings.ToLower(strings.TrimSpace(todo.Status))
		priority := strings.ToLower(strings.TrimSpace(todo.Priority))
		switch {
		case status == TodoStatusInProgress:
			blockers = append(blockers, todo)
		case status == TodoStatusPending && (priority == TodoPriorityHigh || priority == TodoPriorityCritical):
			blockers = append(blockers, todo)
		}
	}
	return blockers
}

func todoCompletionExplained(explanation string, blockers []Todo) bool {
	explanation = strings.ToLower(strings.TrimSpace(explanation))
	if explanation == "" {
		return false
	}
	for _, blocker := range blockers {
		id := strings.ToLower(strings.TrimSpace(blocker.ID))
		content := strings.ToLower(strings.TrimSpace(blocker.Content))
		if id != "" && strings.Contains(explanation, id) {
			continue
		}
		if content != "" && strings.Contains(explanation, content) {
			continue
		}
		return false
	}
	return true
}

func formatTodoCompletionBlockers(blockers []Todo) string {
	parts := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		parts = append(parts, fmt.Sprintf("%s status=%s priority=%s content=%q", blocker.ID, blocker.Status, blocker.Priority, blocker.Content))
	}
	return strings.Join(parts, "; ")
}
