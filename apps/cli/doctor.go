package main

import (
	"fmt"
	"strings"

	"github.com/Lioooooo123/liora/internal/llm"
)

func printDoctor(config llm.Config) error {
	report, err := doctorReport(config, doctorReportContext{})
	if err != nil {
		return err
	}
	fmt.Println(report)
	return nil
}

type doctorReportContext struct {
	Workspace string
	Core      string
	Safety    string
}

func doctorReport(config llm.Config, reportContext doctorReportContext) (string, error) {
	resolved, err := llm.ResolveConfig(config)
	if err != nil {
		return "", err
	}
	keyStatus := "missing"
	if strings.TrimSpace(resolved.APIKey) != "" {
		keyStatus = "configured"
	}
	toolsStatus := "unsupported"
	if doctorSupportsTools(resolved.Provider) {
		toolsStatus = "supported"
	}
	lines := []string{"Liora doctor"}
	if strings.TrimSpace(reportContext.Workspace) != "" {
		lines = append(lines, "workspace: "+reportContext.Workspace)
	}
	if strings.TrimSpace(reportContext.Core) != "" {
		lines = append(lines, "core: "+reportContext.Core)
	}
	if strings.TrimSpace(reportContext.Safety) != "" {
		lines = append(lines, "safety: "+reportContext.Safety)
	}
	lines = append(lines,
		"provider: "+resolved.Provider,
		"display: "+llm.ProviderDisplayName(resolved.Provider),
		"model: "+resolved.Model,
		"base_url: "+resolved.BaseURL,
		"api_key: "+keyStatus,
		"tools: "+toolsStatus,
	)
	return strings.Join(lines, "\n"), nil
}

func doctorSupportsTools(provider string) bool {
	switch llm.NormalizeProvider(provider) {
	case llm.ProviderOpenAIChat, llm.ProviderDeepSeek, llm.ProviderAnthropic:
		return true
	default:
		return false
	}
}
