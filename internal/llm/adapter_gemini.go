package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type geminiAdapter struct {
	client *Client
}

func geminiProviderDefinition() providerDefinition {
	return providerDefinition{
		ID:             ProviderGemini,
		Aliases:        []string{"google", "google-gemini"},
		DisplayName:    "Gemini",
		DefaultBaseURL: "https://generativelanguage.googleapis.com",
		AuthMode:       ProviderAuthAPIKey,
		Capability: func(string) ModelCapability {
			capability := defaultModelCapability()
			capability.Vision = true
			capability.LongContext = true
			return capability
		},
		NewAdapter: func(client *Client) providerAdapter { return &geminiAdapter{client: client} },
	}
}

func (a *geminiAdapter) Complete(ctx context.Context, request providerRequest) (Completion, error) {
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

func (a *geminiAdapter) generate(ctx context.Context, messages []Message) (string, error) {
	system, rest := splitSystemMessages(messages)
	body := map[string]any{
		"contents": geminiContents(rest),
		"generationConfig": map[string]any{
			"temperature":     a.client.config.Temperature,
			"maxOutputTokens": a.client.config.MaxTokens,
		},
	}
	if system != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]string{{"text": system}},
		}
	}
	endpoint := strings.TrimRight(a.client.config.BaseURL, "/") + "/v1beta/models/" + url.PathEscape(a.client.config.Model) + ":generateContent"
	endpoint += "?key=" + url.QueryEscape(a.client.config.APIKey)
	data, err := a.client.postJSON(ctx, endpoint, body, nil)
	if err != nil {
		return "", err
	}
	var decoded struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", err
	}
	if len(decoded.Candidates) == 0 {
		return "", fmt.Errorf("LLM API returned no candidates")
	}
	var parts []string
	for _, part := range decoded.Candidates[0].Content.Parts {
		if part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("LLM API returned no text")
	}
	return strings.Join(parts, "\n"), nil
}

func geminiContents(messages []Message) []map[string]any {
	var converted []map[string]any
	for _, message := range messages {
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		converted = append(converted, map[string]any{
			"role":  role,
			"parts": []map[string]string{{"text": message.Content}},
		})
	}
	return converted
}
