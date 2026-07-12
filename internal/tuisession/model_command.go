package tuisession

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Lioooooo123/liora/internal/llm"
	"github.com/Lioooooo123/liora/internal/store"
)

func (s *DaemonSubmitter) showCurrentModel(ctx context.Context) (string, bool, error) {
	thread, ok, err := s.currentThread(ctx)
	if err != nil {
		return "", true, err
	}
	if !ok {
		return "No current thread. Use /thread <thread_id> first.", true, nil
	}
	catalog, err := llm.LoadProviderProfileCatalogFromEnv()
	if err != nil {
		return "", true, err
	}
	lines := []string{
		"Thread model " + thread.ID + ":",
		"Title: " + thread.Title,
	}
	if thread.ModelConfig == nil {
		lines = append(lines, "Model: default")
		if catalog.Len() > 0 {
			lines = append(lines, formatModelProfileSummary(catalog), "Next: use /model profiles, /model set <profile>, or /model set <provider> <model> [profile].")
			return strings.Join(lines, "\n"), true, nil
		}
		lines = append(lines, "Next: use /model set <provider> <model> [profile] to set this thread.")
		return strings.Join(lines, "\n"), true, nil
	}
	lines = append(lines, formatThreadModelDetails(thread.ModelConfig)...)
	if catalog.Len() > 0 {
		lines = append(lines, formatModelProfileSummary(catalog), "Next: use /model profiles, /model set <profile>, or /model set <provider> <model> [profile].")
		return strings.Join(lines, "\n"), true, nil
	}
	lines = append(lines, "Next: use /model set <profile> to change profile, or /model set <provider> <model> [profile].")
	return strings.Join(lines, "\n"), true, nil
}

// SelectModel ensures a fresh TUI has a thread before persisting a provider/model choice.
func (s *DaemonSubmitter) SelectModel(ctx context.Context, provider string, model string) (string, error) {
	if s.currentSessionID() == "" {
		thread, err := s.client.CreateConversationThread(ctx, store.CreateConversationThreadRequest{
			Workspace: s.workspace,
			Title:     "New session",
		})
		if err != nil {
			return "", err
		}
		s.rememberSession(thread.ID)
	}
	output, _, err := s.setCurrentModel(ctx, strings.TrimSpace(provider)+" "+strings.TrimSpace(model))
	return output, err
}

func (s *DaemonSubmitter) handleModel(ctx context.Context, args string) (string, bool, error) {
	command, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	switch strings.TrimSpace(command) {
	case "profiles":
		return s.showModelProfiles()
	case "set":
		return s.setCurrentModel(ctx, rest)
	default:
		return modelUsage(), true, nil
	}
}

func (s *DaemonSubmitter) showModelProfiles() (string, bool, error) {
	catalog, err := llm.LoadProviderProfileCatalogFromEnv()
	if err != nil {
		return "", true, err
	}
	if catalog.Len() == 0 {
		return "No model profiles configured. Set " + llm.ProviderProfilesEnvVar + " to a JSON object first.", true, nil
	}
	lines := []string{"Model profiles:"}
	for _, name := range catalog.Names() {
		profile, ok := catalog.Lookup(name)
		if !ok {
			continue
		}
		lines = append(lines, formatProviderProfile(profile))
	}
	lines = append(lines, "Next: use /model set <profile> to select a catalog profile.")
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) setCurrentModel(ctx context.Context, args string) (string, bool, error) {
	thread, ok, err := s.currentThread(ctx)
	if err != nil {
		return "", true, err
	}
	if !ok {
		return "No current thread. Use /thread <thread_id> first.", true, nil
	}
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 || len(fields) > 3 {
		return modelSetUsage(), true, nil
	}
	request, ok, err := s.modelConfigRequest(thread, fields)
	if err != nil {
		return "", true, err
	}
	if !ok {
		return "No thread model is configured. Use /model set <provider> <model> [profile] first.", true, nil
	}
	updated, err := s.client.UpdateThreadModelConfig(ctx, thread.ID, request)
	if err != nil {
		return "", true, err
	}
	lines := []string{"Updated thread model " + thread.ID + ":"}
	lines = append(lines, formatThreadModelDetails(&updated)...)
	return strings.Join(lines, "\n"), true, nil
}

func (s *DaemonSubmitter) modelConfigRequest(thread store.ConversationThread, fields []string) (store.UpdateThreadModelConfigRequest, bool, error) {
	if len(fields) == 1 {
		catalog, err := llm.LoadProviderProfileCatalogFromEnv()
		if err != nil {
			return store.UpdateThreadModelConfigRequest{}, false, err
		}
		if profile, ok := catalog.Lookup(fields[0]); ok {
			return store.UpdateThreadModelConfigRequest{
				Provider: profile.Provider,
				Model:    profile.Model,
				BaseURL:  profile.BaseURL,
				Profile:  profile.Profile,
			}, true, nil
		}
		if thread.ModelConfig == nil || strings.TrimSpace(thread.ModelConfig.Provider) == "" || strings.TrimSpace(thread.ModelConfig.Model) == "" {
			return store.UpdateThreadModelConfigRequest{}, false, nil
		}
		return store.UpdateThreadModelConfigRequest{
			Provider:              thread.ModelConfig.Provider,
			Model:                 thread.ModelConfig.Model,
			BaseURL:               thread.ModelConfig.BaseURL,
			InheritedFromThreadID: thread.ModelConfig.InheritedFromThreadID,
			Profile:               fields[0],
		}, true, nil
	}
	request := store.UpdateThreadModelConfigRequest{
		Provider: fields[0],
		Model:    fields[1],
	}
	if len(fields) == 3 {
		request.Profile = fields[2]
	}
	return request, true, nil
}

func formatModelProfileSummary(catalog llm.ProviderProfileCatalog) string {
	return "Available profiles: " + strings.Join(catalog.Names(), ", ")
}

func formatProviderProfile(profile llm.ProviderProfile) string {
	parts := []string{fmt.Sprintf("- %s: %s/%s", profile.Name, profile.Provider, profile.Model)}
	if strings.TrimSpace(profile.Profile) != "" {
		parts = append(parts, "profile="+profile.Profile)
	}
	if strings.TrimSpace(profile.BaseURL) != "" {
		parts = append(parts, "base_url="+redactModelBaseURL(profile.BaseURL))
	}
	if strings.TrimSpace(profile.APIKey) != "" {
		parts = append(parts, "api_key=***")
	}
	return strings.Join(parts, " ")
}

func modelUsage() string {
	return "Usage: /model | /model profiles | /model set <profile> | /model set <provider> <model> [profile]"
}

func modelSetUsage() string {
	return "Usage: /model set <profile> | /model set <provider> <model> [profile]"
}

func redactModelBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "configured"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
