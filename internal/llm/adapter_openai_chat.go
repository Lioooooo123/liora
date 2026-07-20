package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type openAIChatAdapter struct {
	client *Client
}

func openAIChatProviderDefinition() providerDefinition {
	return providerDefinition{
		ID:             ProviderOpenAIChat,
		Aliases:        []string{"openai", "openai-compatible", "chat-completions", "chat"},
		DisplayName:    "OpenAI Chat",
		DefaultBaseURL: "https://api.openai.com/v1",
		AuthMode:       ProviderAuthAPIKey,
		Capability: func(model string) ModelCapability {
			normalized := strings.ToLower(strings.TrimSpace(model))
			capability := defaultModelCapability()
			capability.NativeToolUse = true
			capability.Vision = modelHasAny(normalized, "gpt-4o", "gpt-5", "vision")
			capability.LongContext = modelHasAny(normalized, "gpt-4o", "gpt-5", "128k", "o1", "o3", "o4")
			return capability
		},
		NewAdapter: func(client *Client) providerAdapter { return &openAIChatAdapter{client: client} },
	}
}

func (a *openAIChatAdapter) Complete(ctx context.Context, request providerRequest) (Completion, error) {
	if strings.TrimSpace(a.client.config.APIKey) == "" {
		return Completion{}, fmt.Errorf("LLM API key is required")
	}
	if request.ToolMode {
		if request.Stream {
			return a.generateToolsStream(ctx, request.Messages, request.Tools, request.OnDelta)
		}
		return a.generateTools(ctx, request.Messages, request.Tools)
	}
	if request.Stream {
		text, err := a.generateStream(ctx, request.Messages, request.OnDelta)
		return Completion{Content: text}, err
	}
	text, err := a.generate(ctx, request.Messages)
	return Completion{Content: text}, err
}

func (a *openAIChatAdapter) generate(ctx context.Context, messages []Message) (string, error) {
	body := map[string]any{
		"model":       a.client.config.Model,
		"messages":    messages,
		"temperature": a.client.config.Temperature,
	}
	data, err := a.client.postJSON(ctx, a.client.config.BaseURL+"/chat/completions", body, bearerHeaders(a.client.config.APIKey))
	if err != nil {
		return "", err
	}
	var decoded struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", err
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("LLM API returned no choices")
	}
	return decoded.Choices[0].Message.Content, nil
}
