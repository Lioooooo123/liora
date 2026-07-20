package llm

import "strings"

func ProviderCapability(provider string, model string) ModelCapability {
	definition, ok := lookupProvider(provider)
	if !ok || definition.Capability == nil {
		return ModelCapability{}
	}
	return definition.Capability(model)
}

func defaultModelCapability() ModelCapability {
	return ModelCapability{
		Streaming:       true,
		JSONSchema:      true,
		MaxOutputTokens: 4096,
	}
}

func modelHasAny(model string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(model, needle) {
			return true
		}
	}
	return false
}
