package llm

import (
	"sort"
	"strings"
	"sync"
)

func ProviderIDs() []string {
	definitions := registeredProviderDefinitions()
	ids := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		ids = append(ids, definition.ID)
	}
	sort.Strings(ids)
	return ids
}

func ProviderAuthentication(provider string) ProviderAuthMode {
	definition, ok := lookupProvider(provider)
	if !ok {
		return ""
	}
	return definition.AuthMode
}

var (
	providerRegistryOnce sync.Once
	providerByID         map[string]providerDefinition
	providerAliasToID    map[string]string
)

func builtInProviderDefinitions() []providerDefinition {
	return []providerDefinition{
		openAIChatProviderDefinition(),
		openAIResponsesProviderDefinition(),
		openAICodexProviderDefinition(),
		deepSeekProviderDefinition(),
		anthropicProviderDefinition(),
		geminiProviderDefinition(),
	}
}

func initializeProviderRegistry() {
	providerByID = make(map[string]providerDefinition)
	providerAliasToID = make(map[string]string)
	for _, definition := range builtInProviderDefinitions() {
		id := strings.ToLower(strings.TrimSpace(definition.ID))
		if id == "" || definition.NewAdapter == nil {
			continue
		}
		definition.ID = id
		providerByID[id] = definition
		providerAliasToID[id] = id
		for _, alias := range definition.Aliases {
			alias = strings.ToLower(strings.TrimSpace(alias))
			if alias != "" {
				providerAliasToID[alias] = id
			}
		}
	}
}

func lookupProvider(provider string) (providerDefinition, bool) {
	providerRegistryOnce.Do(initializeProviderRegistry)
	normalized := strings.ToLower(strings.TrimSpace(provider))
	if normalized == "" {
		normalized = ProviderOpenAIChat
	}
	id, ok := providerAliasToID[normalized]
	if !ok {
		return providerDefinition{}, false
	}
	definition, ok := providerByID[id]
	return definition, ok
}

func registeredProviderDefinitions() []providerDefinition {
	providerRegistryOnce.Do(initializeProviderRegistry)
	definitions := make([]providerDefinition, 0, len(providerByID))
	for _, definition := range providerByID {
		definitions = append(definitions, definition)
	}
	return definitions
}
