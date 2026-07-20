package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func (c *Client) Generate(ctx context.Context, messages []Message) (string, error) {
	if strings.TrimSpace(c.config.Model) == "" {
		return "", fmt.Errorf("LLM model is required")
	}
	provider := NormalizeProvider(c.config.Provider)
	if provider != ProviderOpenAICodex && strings.TrimSpace(c.config.APIKey) == "" {
		return "", fmt.Errorf("LLM API key is required")
	}
	switch provider {
	case ProviderOpenAICodex:
		credential, err := c.resolveCredential(ctx)
		if err != nil {
			return "", err
		}
		return c.generateCodexResponses(ctx, messages, credential, nil)
	case ProviderOpenAIChat, ProviderDeepSeek:
		return c.generateOpenAIChat(ctx, messages)
	case ProviderOpenAIResponses:
		return c.generateOpenAIResponses(ctx, messages)
	case ProviderAnthropic:
		return c.generateAnthropic(ctx, messages)
	case ProviderGemini:
		return c.generateGemini(ctx, messages)
	default:
		return "", fmt.Errorf("unsupported LLM provider %q", c.config.Provider)
	}
}

func (c *Client) resolveCredential(ctx context.Context) (ProviderCredential, error) {
	if c.config.CredentialResolver == nil {
		return ProviderCredential{}, fmt.Errorf("OpenAI Codex authentication is required; run `liora auth login codex`")
	}
	credential, err := c.config.CredentialResolver(ctx, NormalizeProvider(c.config.Provider))
	if err != nil {
		return ProviderCredential{}, err
	}
	if strings.TrimSpace(credential.AccessToken) == "" || strings.TrimSpace(credential.AccountID) == "" {
		return ProviderCredential{}, fmt.Errorf("OpenAI Codex authentication is incomplete; run `liora auth login codex`")
	}
	return credential, nil
}

func (c *Client) generateOpenAIChat(ctx context.Context, messages []Message) (string, error) {
	body := map[string]any{
		"model":       c.config.Model,
		"messages":    messages,
		"temperature": c.config.Temperature,
	}
	data, err := c.postJSON(ctx, c.config.BaseURL+"/chat/completions", body, bearerHeaders(c.config.APIKey))
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

func (c *Client) generateOpenAIResponses(ctx context.Context, messages []Message) (string, error) {
	instructions, inputMessages := splitSystemMessages(messages)
	body := map[string]any{
		"model":       c.config.Model,
		"input":       responsesInput(inputMessages),
		"temperature": c.config.Temperature,
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	data, err := c.postJSON(ctx, c.config.BaseURL+"/responses", body, bearerHeaders(c.config.APIKey))
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

func (c *Client) generateAnthropic(ctx context.Context, messages []Message) (string, error) {
	system, rest := splitSystemMessages(messages)
	body := map[string]any{
		"model":       c.config.Model,
		"messages":    anthropicMessages(rest),
		"max_tokens":  c.config.MaxTokens,
		"temperature": c.config.Temperature,
	}
	if system != "" {
		body["system"] = system
	}
	headers := map[string]string{
		"x-api-key":         c.config.APIKey,
		"anthropic-version": "2023-06-01",
	}
	data, err := c.postJSON(ctx, c.config.BaseURL+"/messages", body, headers)
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

func (c *Client) generateGemini(ctx context.Context, messages []Message) (string, error) {
	system, rest := splitSystemMessages(messages)
	body := map[string]any{
		"contents": geminiContents(rest),
		"generationConfig": map[string]any{
			"temperature":     c.config.Temperature,
			"maxOutputTokens": c.config.MaxTokens,
		},
	}
	if system != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]string{{"text": system}},
		}
	}
	endpoint := strings.TrimRight(c.config.BaseURL, "/") + "/v1beta/models/" + url.PathEscape(c.config.Model) + ":generateContent"
	endpoint += "?key=" + url.QueryEscape(c.config.APIKey)
	data, err := c.postJSON(ctx, endpoint, body, nil)
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
