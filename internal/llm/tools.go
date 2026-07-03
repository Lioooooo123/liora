package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrToolsUnsupported is returned by GenerateWithTools when the configured
// provider cannot perform native structured tool calls. Callers fall back to
// the text planner path.
var ErrToolsUnsupported = errors.New("provider does not support native tool calls")

// ToolSchema describes a tool the model may invoke. Parameters is a JSON Schema
// object (type=object, properties, required, additionalProperties=false).
type ToolSchema struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ToolCall is a single structured tool invocation requested by the model.
// Arguments is the raw JSON string emitted by the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Completion is the result of a tool-enabled model turn.
type Completion struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
}

// ToolCaller is implemented by clients that can drive a structured tool-use loop.
type ToolCaller interface {
	GenerateWithTools(ctx context.Context, messages []Message, tools []ToolSchema) (Completion, error)
}

type ToolStreamCaller interface {
	GenerateWithToolsStream(ctx context.Context, messages []Message, tools []ToolSchema, onDelta DeltaHandler) (Completion, error)
}

func (c *Client) GenerateWithTools(ctx context.Context, messages []Message, tools []ToolSchema) (Completion, error) {
	if strings.TrimSpace(c.config.APIKey) == "" {
		return Completion{}, fmt.Errorf("LLM API key is required")
	}
	if strings.TrimSpace(c.config.Model) == "" {
		return Completion{}, fmt.Errorf("LLM model is required")
	}
	switch NormalizeProvider(c.config.Provider) {
	case ProviderOpenAIChat, ProviderDeepSeek:
		return c.generateOpenAIChatTools(ctx, messages, tools)
	case ProviderAnthropic:
		return c.generateAnthropicTools(ctx, messages, tools)
	case ProviderGemini, ProviderOpenAIResponses:
		return Completion{}, ErrToolsUnsupported
	default:
		return Completion{}, fmt.Errorf("unsupported LLM provider %q", c.config.Provider)
	}
}

func (c *Client) GenerateWithToolsStream(ctx context.Context, messages []Message, tools []ToolSchema, onDelta DeltaHandler) (Completion, error) {
	if strings.TrimSpace(c.config.APIKey) == "" {
		return Completion{}, fmt.Errorf("LLM API key is required")
	}
	if strings.TrimSpace(c.config.Model) == "" {
		return Completion{}, fmt.Errorf("LLM model is required")
	}
	switch NormalizeProvider(c.config.Provider) {
	case ProviderOpenAIChat, ProviderDeepSeek:
		return c.generateOpenAIChatToolsStream(ctx, messages, tools, onDelta)
	default:
		completion, err := c.GenerateWithTools(ctx, messages, tools)
		if err != nil {
			return Completion{}, err
		}
		if strings.TrimSpace(completion.Content) != "" && onDelta != nil {
			if err := onDelta(completion.Content); err != nil {
				return Completion{}, err
			}
		}
		return completion, nil
	}
}

// SupportsTools reports whether the provider can run the structured tool-use loop.
func (c *Client) SupportsTools() bool {
	return c.config.Capability.NativeToolUse
}
