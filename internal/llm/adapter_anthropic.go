package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type anthropicAdapter struct {
	client *Client
}

func anthropicProviderDefinition() providerDefinition {
	return providerDefinition{
		ID:             ProviderAnthropic,
		Aliases:        []string{"claude"},
		DisplayName:    "Anthropic",
		DefaultBaseURL: "https://api.anthropic.com/v1",
		AuthMode:       ProviderAuthAPIKey,
		Capability: func(model string) ModelCapability {
			capability := defaultModelCapability()
			capability.NativeToolUse = true
			capability.Vision = modelHasAny(strings.ToLower(strings.TrimSpace(model)), "claude-3", "claude-4", "sonnet", "opus", "haiku")
			capability.LongContext = true
			return capability
		},
		NewAdapter: func(client *Client) providerAdapter { return &anthropicAdapter{client: client} },
	}
}

func (a *anthropicAdapter) Complete(ctx context.Context, request providerRequest) (Completion, error) {
	if strings.TrimSpace(a.client.config.APIKey) == "" {
		return Completion{}, fmt.Errorf("LLM API key is required")
	}
	if request.ToolMode {
		completion, err := a.generateTools(ctx, request.Messages, request.Tools)
		if err != nil {
			return Completion{}, err
		}
		return emitWholeCompletion(completion, request)
	}
	text, err := a.generate(ctx, request.Messages)
	if err != nil {
		return Completion{}, err
	}
	return emitWholeCompletion(Completion{Content: text}, request)
}

func (a *anthropicAdapter) generate(ctx context.Context, messages []Message) (string, error) {
	system, rest := splitSystemMessages(messages)
	body := map[string]any{
		"model":       a.client.config.Model,
		"messages":    anthropicMessages(rest),
		"max_tokens":  a.client.config.MaxTokens,
		"temperature": a.client.config.Temperature,
	}
	if system != "" {
		body["system"] = system
	}
	headers := map[string]string{
		"x-api-key":         a.client.config.APIKey,
		"anthropic-version": "2023-06-01",
	}
	data, err := a.client.postJSON(ctx, a.client.config.BaseURL+"/messages", body, headers)
	if err != nil {
		return "", err
	}
	var decoded struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", err
	}
	var parts []string
	for _, content := range decoded.Content {
		if content.Type == "" || content.Type == "text" {
			parts = append(parts, content.Text)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("LLM API returned no text")
	}
	return strings.Join(parts, "\n"), nil
}

func anthropicMessages(messages []Message) []map[string]string {
	var converted []map[string]string
	for _, message := range messages {
		role := message.Role
		if role != "assistant" {
			role = "user"
		}
		converted = append(converted, map[string]string{"role": role, "content": message.Content})
	}
	return converted
}
