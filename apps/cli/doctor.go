package main

import (
	"fmt"
	"strings"

	"github.com/Lioooooo123/liora/internal/llm"
)

func printDoctor(config llm.Config) error {
	resolved, err := llm.ResolveConfig(config)
	if err != nil {
		return err
	}
	keyStatus := "missing"
	if strings.TrimSpace(resolved.APIKey) != "" {
		keyStatus = "configured"
	}
	toolsStatus := "unsupported"
	if doctorSupportsTools(resolved.Provider) {
		toolsStatus = "supported"
	}
	fmt.Println("Liora doctor")
	fmt.Println("provider: " + resolved.Provider)
	fmt.Println("display: " + llm.ProviderDisplayName(resolved.Provider))
	fmt.Println("model: " + resolved.Model)
	fmt.Println("base_url: " + resolved.BaseURL)
	fmt.Println("api_key: " + keyStatus)
	fmt.Println("tools: " + toolsStatus)
	return nil
}

func doctorSupportsTools(provider string) bool {
	switch llm.NormalizeProvider(provider) {
	case llm.ProviderOpenAIChat, llm.ProviderDeepSeek, llm.ProviderAnthropic:
		return true
	default:
		return false
	}
}
