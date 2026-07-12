package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func (c *Client) generateCodexResponses(ctx context.Context, messages []Message, credential ProviderCredential, onDelta DeltaHandler) (string, error) {
	instructions, inputMessages := splitSystemMessages(messages)
	body := map[string]any{
		"model":  c.config.Model,
		"input":  responsesInput(inputMessages),
		"store":  false,
		"stream": true,
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	headers := map[string]string{
		"Authorization":      "Bearer " + credential.AccessToken,
		"chatgpt-account-id": credential.AccountID,
		"originator":         "liora",
		"OpenAI-Beta":        "responses=experimental",
		"User-Agent":         "liora",
	}
	accumulator := &codexStreamAccumulator{onDelta: onDelta}
	if err := c.postJSONStream(ctx, codexResponsesURL(c.config.BaseURL), body, headers, accumulator.consume); err != nil {
		return "", err
	}
	if accumulator.content.Len() == 0 {
		return "", fmt.Errorf("Codex API returned no text")
	}
	return accumulator.content.String(), nil
}

func codexResponsesURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(baseURL, "/codex/responses") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/codex") {
		return baseURL + "/responses"
	}
	return baseURL + "/codex/responses"
}

type codexStreamAccumulator struct {
	onDelta DeltaHandler
	content strings.Builder
}

func (a *codexStreamAccumulator) consume(reader io.Reader, contentType string) (int, error) {
	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		data, err := io.ReadAll(reader)
		if err != nil {
			return 0, err
		}
		return len(data), a.consumeJSON(data)
	}
	return parseSSE(reader, func(data string) error {
		return a.consumeJSON([]byte(data))
	})
}

func (a *codexStreamAccumulator) consumeJSON(data []byte) error {
	var event struct {
		Type    string `json:"type"`
		Delta   string `json:"delta"`
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
		OutputText string `json:"output_text"`
		Response   struct {
			Status string `json:"status"`
			Error  struct {
				Message string `json:"message"`
			} `json:"error"`
			Output []struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("decode Codex response event: %w", err)
	}
	if event.Type == "error" || event.Type == "response.failed" {
		message := firstNonEmptyString(event.Error.Message, event.Response.Error.Message, event.Message, "Codex request failed")
		return fmt.Errorf("Codex API error: %s", message)
	}
	if event.Type == "response.output_text.delta" && event.Delta != "" {
		return a.appendText(event.Delta)
	}
	if event.OutputText != "" && a.content.Len() == 0 {
		return a.appendText(event.OutputText)
	}
	if event.Type == "response.completed" && a.content.Len() == 0 {
		for _, output := range event.Response.Output {
			for _, content := range output.Content {
				if content.Text != "" {
					if err := a.appendText(content.Text); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (a *codexStreamAccumulator) appendText(text string) error {
	a.content.WriteString(text)
	if a.onDelta != nil {
		return a.onDelta(text)
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
