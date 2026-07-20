package llm

import (
	"context"
	"fmt"
	"strings"
)

func (c *Client) Generate(ctx context.Context, messages []Message) (string, error) {
	if strings.TrimSpace(c.config.Model) == "" {
		return "", fmt.Errorf("LLM model is required")
	}
	if c.adapter == nil {
		return "", fmt.Errorf("unsupported LLM provider %q", c.config.Provider)
	}
	completion, err := c.adapter.Complete(ctx, providerRequest{Messages: messages})
	if err != nil {
		return "", err
	}
	return completion.Content, nil
}

func splitSystemMessages(messages []Message) (string, []Message) {
	var system []string
	var rest []Message
	for _, message := range messages {
		if message.Role == "system" {
			system = append(system, message.Content)
			continue
		}
		rest = append(rest, message)
	}
	return strings.Join(system, "\n\n"), rest
}

func responsesInput(messages []Message) any {
	if len(messages) == 1 && messages[0].Role == "user" {
		return messages[0].Content
	}
	return responsesMessageInput(messages)
}

func responsesMessageInput(messages []Message) []map[string]string {
	input := make([]map[string]string, 0, len(messages))
	for _, message := range messages {
		role := message.Role
		if role == "" {
			role = "user"
		}
		input = append(input, map[string]string{"role": role, "content": message.Content})
	}
	return input
}
