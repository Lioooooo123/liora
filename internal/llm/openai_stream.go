package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func (c *Client) GenerateStream(ctx context.Context, messages []Message, onDelta DeltaHandler) (string, error) {
	if strings.TrimSpace(c.config.APIKey) == "" {
		return "", fmt.Errorf("LLM API key is required")
	}
	if strings.TrimSpace(c.config.Model) == "" {
		return "", fmt.Errorf("LLM model is required")
	}
	switch NormalizeProvider(c.config.Provider) {
	case ProviderOpenAIChat, ProviderDeepSeek:
		return c.generateOpenAIChatStream(ctx, messages, onDelta)
	default:
		text, err := c.Generate(ctx, messages)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) != "" && onDelta != nil {
			if err := onDelta(text); err != nil {
				return "", err
			}
		}
		return text, nil
	}
}

func (c *Client) generateOpenAIChatStream(ctx context.Context, messages []Message, onDelta DeltaHandler) (string, error) {
	body := map[string]any{
		"model":       c.config.Model,
		"messages":    messages,
		"temperature": c.config.Temperature,
		"stream":      true,
	}
	completion, err := c.streamOpenAIChat(ctx, body, onDelta)
	if err != nil {
		return "", err
	}
	return completion.Content, nil
}

func (c *Client) generateOpenAIChatToolsStream(ctx context.Context, messages []Message, tools []ToolSchema, onDelta DeltaHandler) (Completion, error) {
	body := map[string]any{
		"model":       c.config.Model,
		"messages":    openAIChatMessages(messages),
		"temperature": c.config.Temperature,
		"stream":      true,
	}
	if len(tools) > 0 {
		body["tools"] = openAIChatTools(tools)
	}
	return c.streamOpenAIChat(ctx, body, onDelta)
}

func (c *Client) streamOpenAIChat(ctx context.Context, body map[string]any, onDelta DeltaHandler) (Completion, error) {
	accumulator := newOpenAIStreamAccumulator(onDelta)
	err := c.postJSONStream(ctx, c.config.BaseURL+"/chat/completions", body, bearerHeaders(c.config.APIKey), accumulator.consume)
	if err != nil {
		return Completion{}, err
	}
	return accumulator.completion()
}

type openAIStreamAccumulator struct {
	onDelta     DeltaHandler
	content     strings.Builder
	finish      string
	sawChoice   bool
	responseLen int
	calls       map[int]*ToolCall
	callOrder   []int
}

func newOpenAIStreamAccumulator(onDelta DeltaHandler) *openAIStreamAccumulator {
	return &openAIStreamAccumulator{onDelta: onDelta, calls: map[int]*ToolCall{}}
}

func (a *openAIStreamAccumulator) consume(reader io.Reader, contentType string) (int, error) {
	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return a.consumeJSON(reader)
	}
	return parseSSE(reader, func(data string) error {
		a.responseLen += len(data)
		var chunk openAIChatStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode LLM stream chunk: %w", err)
		}
		if chunk.Error.Message != "" {
			return fmt.Errorf("LLM API stream error: %s", chunk.Error.Message)
		}
		for _, choice := range chunk.Choices {
			a.sawChoice = true
			if choice.FinishReason != "" {
				a.finish = choice.FinishReason
			}
			if choice.Delta.Content != "" {
				a.content.WriteString(choice.Delta.Content)
				if a.onDelta != nil {
					if err := a.onDelta(choice.Delta.Content); err != nil {
						return err
					}
				}
			}
			for _, partial := range choice.Delta.ToolCalls {
				call := a.toolCall(partial.Index)
				if partial.ID != "" {
					call.ID = partial.ID
				}
				if partial.Function.Name != "" {
					call.Name += partial.Function.Name
				}
				if partial.Function.Arguments != "" {
					call.Arguments += partial.Function.Arguments
				}
			}
		}
		return nil
	})
}

func (a *openAIStreamAccumulator) consumeJSON(reader io.Reader) (int, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return 0, err
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
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return len(data), err
	}
	if decoded.Error.Message != "" {
		return len(data), fmt.Errorf("LLM API stream error: %s", decoded.Error.Message)
	}
	for _, choice := range decoded.Choices {
		a.sawChoice = true
		a.finish = choice.FinishReason
		if choice.Message.Content != "" {
			a.content.WriteString(choice.Message.Content)
			if a.onDelta != nil {
				if err := a.onDelta(choice.Message.Content); err != nil {
					return len(data), err
				}
			}
		}
		for i, partial := range choice.Message.ToolCalls {
			call := a.toolCall(i)
			call.ID = partial.ID
			call.Name = partial.Function.Name
			call.Arguments = partial.Function.Arguments
		}
	}
	return len(data), nil
}

func (a *openAIStreamAccumulator) toolCall(index int) *ToolCall {
	call, ok := a.calls[index]
	if ok {
		return call
	}
	call = &ToolCall{}
	a.calls[index] = call
	a.callOrder = append(a.callOrder, index)
	return call
}

func (a *openAIStreamAccumulator) completion() (Completion, error) {
	if !a.sawChoice {
		return Completion{}, fmt.Errorf("LLM API returned no choices")
	}
	completion := Completion{Content: a.content.String(), FinishReason: a.finish}
	for _, index := range a.callOrder {
		call := a.calls[index]
		if call == nil {
			continue
		}
		completion.ToolCalls = append(completion.ToolCalls, *call)
	}
	return completion, nil
}

type openAIChatStreamChunk struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func parseSSE(reader io.Reader, onData func(string) error) (int, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024), 1024*1024*10)
	var dataLines []string
	responseBytes := 0
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		if data == "[DONE]" {
			return nil
		}
		return onData(data)
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		responseBytes += len(line)
		if line == "" {
			if err := flush(); err != nil {
				return responseBytes, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return responseBytes, err
	}
	if err := flush(); err != nil {
		return responseBytes, err
	}
	return responseBytes, nil
}
