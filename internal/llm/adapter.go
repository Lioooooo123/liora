package llm

import "context"

// providerRequest is the single request shape crossing the provider seam.
// Adapters own how messages, tools, streaming, authentication and continuation
// are represented on the wire.
type providerRequest struct {
	Messages []Message
	Tools    []ToolSchema
	ToolMode bool
	Stream   bool
	OnDelta  DeltaHandler
}

// providerAdapter is deliberately small: every provider-specific behavior is
// hidden behind one complete model turn.
type providerAdapter interface {
	Complete(context.Context, providerRequest) (Completion, error)
}

type providerDefinition struct {
	ID             string
	Aliases        []string
	DisplayName    string
	DefaultBaseURL string
	DefaultModel   string
	AuthMode       ProviderAuthMode
	Capability     func(string) ModelCapability
	NewAdapter     func(*Client) providerAdapter
}

type ProviderAuthMode string

const (
	ProviderAuthAPIKey ProviderAuthMode = "api-key"
	ProviderAuthOAuth  ProviderAuthMode = "oauth"
)

func emitWholeCompletion(completion Completion, request providerRequest) (Completion, error) {
	if request.Stream && request.OnDelta != nil && completion.Content != "" {
		if err := request.OnDelta(completion.Content); err != nil {
			return Completion{}, err
		}
	}
	return completion, nil
}
