package llm

import "strings"

type deepSeekAdapter struct {
	openAIChatAdapter
}

func deepSeekProviderDefinition() providerDefinition {
	return providerDefinition{
		ID:             ProviderDeepSeek,
		Aliases:        []string{"deepseek"},
		DisplayName:    "DeepSeek",
		DefaultBaseURL: "https://api.deepseek.com",
		AuthMode:       ProviderAuthAPIKey,
		Capability: func(model string) ModelCapability {
			capability := defaultModelCapability()
			capability.NativeToolUse = true
			capability.LongContext = modelHasAny(strings.ToLower(strings.TrimSpace(model)), "r1", "v3", "128k")
			return capability
		},
		NewAdapter: func(client *Client) providerAdapter {
			return &deepSeekAdapter{openAIChatAdapter: openAIChatAdapter{client: client}}
		},
	}
}
