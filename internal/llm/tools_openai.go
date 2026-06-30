package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (c *Client) generateOpenAIChatTools(ctx context.Context, messages []Message, tools []ToolSchema) (Completion, error) {
	body := map[string]any{
		"model":       c.config.Model,
		"messages":    openAIChatMessages(messages),
		"temperature": c.config.Temperature,
	}
	if len(tools) > 0 {
		body["tools"] = openAIChatTools(tools)
	}
	data, err := c.postJSON(ctx, c.config.BaseURL+"/chat/completions", body, bearerHeaders(c.config.APIKey))
	if err != nil {
		return Completion{}, err
	}
	var decoded struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Completion{}, err
	}
	if len(decoded.Choices) == 0 {
		return Completion{}, fmt.Errorf("LLM API returned no choices")
	}
	choice := decoded.Choices[0]
	completion := Completion{Content: choice.Message.Content, FinishReason: choice.FinishReason}
	for _, call := range choice.Message.ToolCalls {
		completion.ToolCalls = append(completion.ToolCalls, ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	return completion, nil
}

func openAIChatTools(tools []ToolSchema) []map[string]any {
	converted := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		parameters := tool.Parameters
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		converted = append(converted, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  parameters,
			},
		})
	}
	return converted
}

func openAIChatMessages(messages []Message) []map[string]any {
	converted := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		entry := map[string]any{"role": message.Role}
		switch message.Role {
		case "tool":
			entry["tool_call_id"] = message.ToolCallID
			entry["content"] = message.Content
		case "assistant":
			if message.Content != "" {
				entry["content"] = message.Content
			} else if len(message.ToolCalls) == 0 {
				entry["content"] = ""
			}
			if len(message.ToolCalls) > 0 {
				entry["tool_calls"] = openAIToolCalls(message.ToolCalls)
			}
		default:
			entry["content"] = message.Content
		}
		converted = append(converted, entry)
	}
	return converted
}

func openAIToolCalls(calls []ToolCall) []map[string]any {
	converted := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		arguments := call.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		converted = append(converted, map[string]any{
			"id":   call.ID,
			"type": "function",
			"function": map[string]any{
				"name":      call.Name,
				"arguments": arguments,
			},
		})
	}
	return converted
}
