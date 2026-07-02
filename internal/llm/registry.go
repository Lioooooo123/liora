package llm

import (
	"fmt"
	"strings"
)

type Registry struct {
	defaults Config
}

func NewRegistry(defaults Config) (*Registry, error) {
	resolved, err := ResolveConfig(defaults)
	if err != nil {
		return nil, err
	}
	return &Registry{defaults: resolved}, nil
}

func (r *Registry) DefaultConfig() (Config, bool) {
	if r == nil {
		return Config{}, false
	}
	return r.defaults, true
}

func (r *Registry) Resolve(request Config) (Config, error) {
	if r == nil {
		return ResolveConfig(request)
	}
	config := r.defaults
	requestProvider := NormalizeProvider(request.Provider)
	defaultProvider := NormalizeProvider(config.Provider)
	if requestProvider != "" && requestProvider != defaultProvider && strings.TrimSpace(request.BaseURL) == "" {
		config.BaseURL = ""
	}
	if requestProvider != "" {
		config.Provider = requestProvider
	}
	if strings.TrimSpace(request.BaseURL) != "" {
		config.BaseURL = strings.TrimSpace(request.BaseURL)
	}
	if strings.TrimSpace(request.APIKey) != "" {
		config.APIKey = strings.TrimSpace(request.APIKey)
	}
	if strings.TrimSpace(request.Model) != "" {
		config.Model = strings.TrimSpace(request.Model)
	}
	if strings.TrimSpace(request.Profile) != "" {
		config.Profile = strings.TrimSpace(request.Profile)
	}
	if request.Temperature != 0 {
		config.Temperature = request.Temperature
	}
	if request.MaxTokens != 0 {
		config.MaxTokens = request.MaxTokens
	}
	if request.Timeout != 0 {
		config.Timeout = request.Timeout
	}
	if strings.TrimSpace(request.RetryPolicy) != "" {
		config.RetryPolicy = strings.TrimSpace(request.RetryPolicy)
	}
	if request.TokenBudget != 0 {
		config.TokenBudget = request.TokenBudget
	}
	if request.Metrics != nil {
		config.Metrics = request.Metrics
	}
	config.TraceLabels = mergeTraceLabels(config.TraceLabels, request.TraceLabels)
	return ResolveConfig(config)
}

func (r *Registry) Planner(request Config) (*Planner, Config, error) {
	config, err := r.Resolve(request)
	if err != nil {
		return nil, Config{}, err
	}
	client, err := NewClient(config)
	if err != nil {
		return nil, Config{}, err
	}
	return NewPlanner(client), config, nil
}

func (r *Registry) Capability(request Config) (ModelCapability, error) {
	config, err := r.Resolve(request)
	if err != nil {
		return ModelCapability{}, err
	}
	return config.Capability, nil
}

func normalizeTraceLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(labels))
	for key, value := range labels {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		normalized[key] = value
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func mergeTraceLabels(base map[string]string, override map[string]string) map[string]string {
	merged := normalizeTraceLabels(base)
	if len(override) == 0 {
		return merged
	}
	if merged == nil {
		merged = map[string]string{}
	}
	for key, value := range normalizeTraceLabels(override) {
		merged[key] = value
	}
	return merged
}

func (c Config) Label() string {
	provider := NormalizeProvider(c.Provider)
	if provider == "" {
		provider = ProviderOpenAIChat
	}
	if strings.TrimSpace(c.Model) == "" {
		return ProviderDisplayName(provider)
	}
	return fmt.Sprintf("%s / %s", ProviderDisplayName(provider), strings.TrimSpace(c.Model))
}
