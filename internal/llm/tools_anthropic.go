package llm

import (
	"context"
	"encoding/json"
	"strings"
)

func (c *Client) generateAnthropicTools(ctx context.Context, messages []Message, tools []ToolSchema) (Completion, error) {
	system, rest := splitSystemMessages(messages)
	body := map[string]any{
		"model":       c.config.Model,
		"messages":    anthropicToolMessages(rest),
		"max_tokens":  c.config.MaxTokens,
		"temperature": c.config.Temperature,
	}
	if system != "" {
		body["system"] = system
	}
	if len(tools) > 0 {
		body["tools"] = anthropicTools(tools)
	}
	headers := map[string]string{
		"x-api-key":         c.config.APIKey,
		"anthropic-version": "2023-06-01",
	}
	data, err := c.postJSON(ctx, c.config.BaseURL+"/messages", body, headers)
	if err != nil {
		return Completion{}, err
	}
	var decoded struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Completion{}, err
	}
	completion := Completion{FinishReason: decoded.StopReason}
	var parts []string
	for _, block := range decoded.Content {
		switch block.Type {
		case "text", "":
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case "tool_use":
			arguments := "{}"
			if len(block.Input) > 0 {
				arguments = string(block.Input)
			}
			completion.ToolCalls = append(completion.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: arguments,
			})
		}
	}
	completion.Content = strings.Join(parts, "\n")
	return completion, nil
}

func anthropicTools(tools []ToolSchema) []map[string]any {
	converted := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		schema := tool.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		converted = append(converted, map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": schema,
		})
	}
	return converted
}

func anthropicToolMessages(messages []Message) []map[string]any {
	converted := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "tool":
			converted = append(converted, anthropicToolResultMessage(message))
		case "assistant":
			converted = append(converted, anthropicAssistantToolMessage(message))
		default:
			converted = append(converted, map[string]any{"role": "user", "content": message.Content})
		}
	}
	return converted
}

func anthropicToolResultMessage(message Message) map[string]any {
	block := map[string]any{
		"type":        "tool_result",
		"tool_use_id": message.ToolCallID,
		"content":     message.Content,
	}
	if message.ToolError {
		block["is_error"] = true
	}
	return map[string]any{"role": "user", "content": []map[string]any{block}}
}

func anthropicAssistantToolMessage(message Message) map[string]any {
	var blocks []map[string]any
	if strings.TrimSpace(message.Content) != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": message.Content})
	}
	for _, call := range message.ToolCalls {
		input := map[string]any{}
		if strings.TrimSpace(call.Arguments) != "" {
			_ = json.Unmarshal([]byte(call.Arguments), &input)
		}
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    call.ID,
			"name":  call.Name,
			"input": input,
		})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": ""})
	}
	return map[string]any{"role": "assistant", "content": blocks}
}
