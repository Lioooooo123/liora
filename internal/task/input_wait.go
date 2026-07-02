package task

import (
	"context"
	"strings"
)

func (r *Runner) taskPrompt(ctx context.Context, task Task) (string, error) {
	prompt := task.UserInput
	completed, err := r.repo.CompletedToolResults(ctx, task.ID)
	if err != nil {
		return "", err
	}
	if summary := completedToolSummary(completed); summary != "" {
		prompt = strings.TrimSpace(prompt + "\n\n" + summary)
	}
	answer, ok, err := r.repo.LatestUserInput(ctx, task.ID)
	if err != nil {
		return "", err
	}
	if !ok {
		return prompt, nil
	}
	return strings.TrimSpace(prompt + "\n\nUser input after the previous pause:\n" + answer), nil
}

func (r *Runner) waitForUserInput(ctx context.Context, taskID string, question string) error {
	question = strings.TrimSpace(question)
	if question == "" {
		question = "Input required before continuing."
	}
	if updateErr := r.repo.UpdateStatus(ctx, taskID, StatusWaitingUser); updateErr != nil {
		return updateErr
	}
	task, _ := r.repo.Get(ctx, taskID)
	_ = r.repo.AppendEvent(ctx, taskID, EventUserInputRequest, r.eventPayloadWithModel(task, EventPayload{
		Message:   question,
		Status:    string(StatusWaitingUser),
		ExpiresAt: ExpiresAtAfter(DefaultWaitExpiry),
	}))
	return nil
}

func containsRunStep(steps string) bool {
	for _, line := range strings.Split(steps, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "run ") {
			return true
		}
	}
	return false
}
