package llm

import (
	"context"
	"fmt"
	"strings"
)

type codexAdapter struct {
	client *Client
}

func openAICodexProviderDefinition() providerDefinition {
	return providerDefinition{
		ID:             ProviderOpenAICodex,
		Aliases:        []string{"codex", "chatgpt-codex"},
		DisplayName:    "OpenAI Codex",
		DefaultBaseURL: "https://chatgpt.com/backend-api",
		DefaultModel:   DefaultOpenAICodexModel,
		AuthMode:       ProviderAuthOAuth,
		Capability: func(string) ModelCapability {
			capability := defaultModelCapability()
			capability.NativeToolUse = true
			capability.Vision = true
			capability.LongContext = true
			return capability
		},
		NewAdapter: func(client *Client) providerAdapter { return &codexAdapter{client: client} },
	}
}

func (a *codexAdapter) Complete(ctx context.Context, request providerRequest) (Completion, error) {
	credential, err := a.resolveCredential(ctx)
	if err != nil {
		return Completion{}, err
	}
	tools := request.Tools
	if !request.ToolMode {
		tools = nil
	}
	return a.client.generateCodexResponses(ctx, request.Messages, tools, credential, request.OnDelta)
}

func (a *codexAdapter) resolveCredential(ctx context.Context) (ProviderCredential, error) {
	if a.client.config.CredentialResolver == nil {
		return ProviderCredential{}, fmt.Errorf("OpenAI Codex authentication is required; run `liora auth login codex`")
	}
	credential, err := a.client.config.CredentialResolver(ctx, a.client.config.Provider)
	if err != nil {
		return ProviderCredential{}, err
	}
	if strings.TrimSpace(credential.AccessToken) == "" || strings.TrimSpace(credential.AccountID) == "" {
		return ProviderCredential{}, fmt.Errorf("OpenAI Codex authentication is incomplete; run `liora auth login codex`")
	}
	return credential, nil
}
