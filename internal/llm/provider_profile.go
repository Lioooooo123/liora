package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

const ProviderProfilesEnvVar = "LIORA_LLM_PROFILES"

type ProviderProfile struct {
	Name     string
	Provider string
	Model    string
	BaseURL  string
	APIKey   string
	Profile  string
}

type ProviderProfileCatalog struct {
	profiles map[string]ProviderProfile
}

type providerProfileJSON struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Profile  string `json:"profile"`
}

func LoadProviderProfileCatalogFromEnv() (ProviderProfileCatalog, error) {
	return ParseProviderProfileCatalog(os.Getenv(ProviderProfilesEnvVar))
}

func ParseProviderProfileCatalog(raw string) (ProviderProfileCatalog, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ProviderProfileCatalog{}, nil
	}
	var decoded map[string]providerProfileJSON
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return ProviderProfileCatalog{}, fmt.Errorf("parse %s: %w", ProviderProfilesEnvVar, err)
	}
	profiles := make(map[string]ProviderProfile, len(decoded))
	for name, entry := range decoded {
		profile, err := parseProviderProfile(name, entry)
		if err != nil {
			return ProviderProfileCatalog{}, err
		}
		profiles[profile.Name] = profile
	}
	return ProviderProfileCatalog{profiles: profiles}, nil
}

func parseProviderProfile(name string, entry providerProfileJSON) (ProviderProfile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ProviderProfile{}, fmt.Errorf("%s contains a blank profile name", ProviderProfilesEnvVar)
	}
	provider := NormalizeProvider(entry.Provider)
	model := strings.TrimSpace(entry.Model)
	if provider == "" || model == "" {
		return ProviderProfile{}, fmt.Errorf("%s profile %q requires provider and model", ProviderProfilesEnvVar, name)
	}
	if defaultBaseURL(provider, "") == "" {
		return ProviderProfile{}, fmt.Errorf("%s profile %q has unsupported provider %q", ProviderProfilesEnvVar, name, entry.Provider)
	}
	profile := strings.TrimSpace(entry.Profile)
	if profile == "" {
		profile = name
	}
	return ProviderProfile{
		Name:     name,
		Provider: provider,
		Model:    model,
		BaseURL:  strings.TrimRight(strings.TrimSpace(entry.BaseURL), "/"),
		APIKey:   strings.TrimSpace(entry.APIKey),
		Profile:  profile,
	}, nil
}

func (c ProviderProfileCatalog) Names() []string {
	if len(c.profiles) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.profiles))
	for name := range c.profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c ProviderProfileCatalog) Lookup(name string) (ProviderProfile, bool) {
	if len(c.profiles) == 0 {
		return ProviderProfile{}, false
	}
	profile, ok := c.profiles[strings.TrimSpace(name)]
	return profile, ok
}

func (c ProviderProfileCatalog) MatchProfile(name string) (ProviderProfile, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ProviderProfile{}, false
	}
	if profile, ok := c.Lookup(name); ok {
		return profile, true
	}
	for _, profileName := range c.Names() {
		profile := c.profiles[profileName]
		if profile.Profile == name {
			return profile, true
		}
	}
	return ProviderProfile{}, false
}

func (c ProviderProfileCatalog) Len() int {
	return len(c.profiles)
}
