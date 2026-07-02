package llm

import "strings"

func ProviderCapability(provider string, model string) ModelCapability {
	normalizedProvider := NormalizeProvider(provider)
	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	capability := ModelCapability{
		Streaming:       true,
		JSONSchema:      true,
		MaxOutputTokens: 4096,
	}
	switch normalizedProvider {
	case ProviderOpenAIChat:
		capability.NativeToolUse = true
		capability.Vision = modelHasAny(normalizedModel, "gpt-4o", "gpt-5", "vision")
		capability.LongContext = modelHasAny(normalizedModel, "gpt-4o", "gpt-5", "128k", "o1", "o3", "o4")
	case ProviderOpenAIResponses:
		capability.Vision = true
		capability.LongContext = true
	case ProviderDeepSeek:
		capability.NativeToolUse = true
		capability.LongContext = modelHasAny(normalizedModel, "r1", "v3", "128k")
	case ProviderAnthropic:
		capability.NativeToolUse = true
		capability.Vision = modelHasAny(normalizedModel, "claude-3", "claude-4", "sonnet", "opus", "haiku")
		capability.LongContext = true
	case ProviderGemini:
		capability.Vision = true
		capability.LongContext = true
	default:
		return ModelCapability{}
	}
	return capability
}

func modelHasAny(model string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(model, needle) {
			return true
		}
	}
	return false
}
