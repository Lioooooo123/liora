package llm

import "testing"

func TestParseProviderProfileCatalogParsesJSONObjectWhenConfigured(t *testing.T) {
	// Given
	raw := `{
		"cheap": {
			"provider": "deepseek",
			"model": "deepseek-chat",
			"base_url": "https://proxy.example.test/v1/",
			"api_key": "cheap-secret",
			"profile": "cheap"
		},
		"strong": {
			"provider": "anthropic",
			"model": "claude-sonnet-4"
		}
	}`

	// When
	catalog, err := ParseProviderProfileCatalog(raw)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if names := catalog.Names(); len(names) != 2 || names[0] != "cheap" || names[1] != "strong" {
		t.Fatalf("unexpected profile names %#v", names)
	}
	cheap, ok := catalog.Lookup("cheap")
	if !ok {
		t.Fatal("expected cheap profile")
	}
	if cheap.Provider != ProviderDeepSeek || cheap.Model != "deepseek-chat" || cheap.BaseURL != "https://proxy.example.test/v1" || cheap.APIKey != "cheap-secret" || cheap.Profile != "cheap" {
		t.Fatalf("unexpected cheap profile %#v", cheap)
	}
	strong, ok := catalog.Lookup("strong")
	if !ok {
		t.Fatal("expected strong profile")
	}
	if strong.Provider != ProviderAnthropic || strong.Model != "claude-sonnet-4" || strong.Profile != "strong" {
		t.Fatalf("unexpected strong profile %#v", strong)
	}
}

func TestParseProviderProfileCatalogRejectsIncompleteProfile(t *testing.T) {
	// Given
	raw := `{"cheap":{"provider":"deepseek"}}`

	// When
	_, err := ParseProviderProfileCatalog(raw)

	// Then
	if err == nil {
		t.Fatal("expected incomplete profile to fail")
	}
}
