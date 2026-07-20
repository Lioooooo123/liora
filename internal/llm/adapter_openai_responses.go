package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type openAIResponsesAdapter struct {
	client *Client
}

func openAIResponsesProviderDefinition() providerDefinition {
	return providerDefinition{
		ID:             ProviderOpenAIResponses,
		Aliases:        []string{"responses"},
		DisplayName:    "OpenAI Responses",
		DefaultBaseURL: "https://api.openai.com/v1",
		AuthMode:       ProviderAuthAPIKey,
		Capability: func(string) ModelCapability {
			capability := defaultModelCapability()
			capability.Vision = true
			capability.LongContext = true
			return capability
		},
		NewAdapter: func(client *Client) providerAdapter { return &openAIResponsesAdapter{client: client} },
	}
}

func (a *openAIResponsesAdapter) Complete(ctx context.Context, request providerRequest) (Completion, error) {
	if request.ToolMode {
		return Completion{}, ErrToolsUnsupported
	}
	if strings.TrimSpace(a.client.config.APIKey) == "" {
		return Completion{}, fmt.Errorf("LLM API key is required")
	}
	text, err := a.generate(ctx, request.Messages)
	if err != nil {
		return Completion{}, err
	}
	return emitWholeCompletion(Completion{Content: text}, request)
}

func (a *openAIResponsesAdapter) generate(ctx context.Context, messages []Message) (string, error) {
	instructions, inputMessages := splitSystemMessages(messages)
	body := map[string]any{
		"model":       a.client.config.Model,
		"input":       responsesInput(inputMessages),
		"temperature": a.client.config.Temperature,
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	data, err := a.client.postJSON(ctx, a.client.config.BaseURL+"/responses", body, bearerHeaders(a.client.config.APIKey))
	if err != nil {
		return "", err
	}
	var decoded struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", err
	}
	if decoded.OutputText != "" {
		return decoded.OutputText, nil
	}
	var parts []string
	for _, output := range decoded.Output {
		for _, content := range output.Content {
			if content.Type == "" || content.Type == "output_text" || content.Type == "text" {
				parts = append(parts, content.Text)
			}
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("LLM API returned no text")
	}
	return strings.Join(parts, "\n"), nil
}
