package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRegistryResolvesPerRequestProviderConfig(t *testing.T) {
	registry, err := NewRegistry(Config{
		Provider:    ProviderOpenAIChat,
		BaseURL:     "https://default.example/v1",
		APIKey:      "default-key",
		Model:       "default-model",
		Timeout:     7 * time.Second,
		RetryPolicy: "standard",
		TraceLabels: map[string]string{"workspace": "/repo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := registry.Resolve(Config{
		Provider: ProviderAnthropic,
		Model:    "claude-thread",
		Profile:  "strong",
		TraceLabels: map[string]string{
			"thread_id": "thread-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Provider != ProviderAnthropic || config.BaseURL != "https://api.anthropic.com/v1" || config.Model != "claude-thread" {
		t.Fatalf("unexpected resolved provider config %#v", config)
	}
	if config.APIKey != "default-key" || config.Timeout != 7*time.Second || config.Profile != "strong" {
		t.Fatalf("expected inherited secret/timeout and request profile, got %#v", config)
	}
	if config.TokenBudget != config.MaxTokens || config.RetryPolicy != "standard" || !config.ToolUse {
		t.Fatalf("missing resolved operational fields %#v", config)
	}
	if config.TraceLabels["workspace"] != "/repo" || config.TraceLabels["thread_id"] != "thread-1" {
		t.Fatalf("unexpected trace labels %#v", config.TraceLabels)
	}
}

func TestRegistryUsesProfileCatalogSecretWhenRequestMatchesProfile(t *testing.T) {
	// Given
	t.Setenv(ProviderProfilesEnvVar, `{
		"cheap": {
			"provider": "deepseek",
			"model": "deepseek-chat",
			"base_url": "https://proxy.example.test/v1",
			"api_key": "cheap-secret",
			"profile": "cheap"
		}
	}`)
	registry, err := NewRegistry(Config{
		Provider: ProviderOpenAIChat,
		APIKey:   "default-key",
		Model:    "default-model",
	})
	if err != nil {
		t.Fatal(err)
	}

	// When
	config, err := registry.Resolve(Config{
		Provider: ProviderDeepSeek,
		Model:    "deepseek-chat",
		Profile:  "cheap",
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if config.APIKey != "cheap-secret" || config.BaseURL != "https://proxy.example.test/v1" || config.Provider != ProviderDeepSeek || config.Model != "deepseek-chat" || config.Profile != "cheap" {
		t.Fatalf("expected catalog profile to supply secret and endpoint, got %#v", config)
	}
}

func TestRegistryUsesProfileCatalogSecretWhenRequestMatchesProfileLabel(t *testing.T) {
	// Given
	t.Setenv(ProviderProfilesEnvVar, `{
		"cheap": {
			"provider": "deepseek",
			"model": "deepseek-chat",
			"api_key": "cheap-secret",
			"profile": "budget"
		}
	}`)
	registry, err := NewRegistry(Config{
		Provider: ProviderOpenAIChat,
		APIKey:   "default-key",
		Model:    "default-model",
	})
	if err != nil {
		t.Fatal(err)
	}

	// When
	config, err := registry.Resolve(Config{
		Provider: ProviderDeepSeek,
		Model:    "deepseek-chat",
		Profile:  "budget",
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if config.APIKey != "cheap-secret" || config.Profile != "budget" {
		t.Fatalf("expected profile label to resolve catalog secret, got %#v", config)
	}
}

func TestRegistryRejectsUnsupportedProvider(t *testing.T) {
	registry, err := NewRegistry(Config{Provider: ProviderOpenAIChat, APIKey: "key", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(Config{Provider: "unknown-provider", Model: "model"}); err == nil {
		t.Fatal("expected unsupported provider to fail")
	}
}

func TestRegistryResolvesProviderModelCapabilities(t *testing.T) {
	registry, err := NewRegistry(Config{Provider: ProviderOpenAIChat, APIKey: "key", Model: "gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	openAI, err := registry.Capability(Config{Provider: ProviderOpenAIChat, Model: "gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	if !openAI.NativeToolUse || !openAI.Streaming || !openAI.JSONSchema || !openAI.LongContext || openAI.MaxOutputTokens == 0 {
		t.Fatalf("unexpected OpenAI capability %#v", openAI)
	}
	responses, err := registry.Capability(Config{Provider: ProviderOpenAIResponses, Model: "gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	if responses.NativeToolUse || !responses.Streaming || !responses.Vision || !responses.LongContext || !responses.JSONSchema {
		t.Fatalf("responses adapter should be queryable but not routed to native tool-use yet: %#v", responses)
	}
	codex, err := registry.Capability(Config{Provider: ProviderOpenAICodex, Model: "gpt-5.4"})
	if err != nil {
		t.Fatal(err)
	}
	if !codex.NativeToolUse || !codex.Streaming || !codex.Vision || !codex.LongContext {
		t.Fatalf("unexpected Codex adapter capability %#v", codex)
	}
	if _, err := registry.Capability(Config{Provider: "unknown-provider", Model: "mystery"}); err == nil {
		t.Fatal("expected unknown provider capability lookup to fail closed")
	}
}

func TestRegistryConcurrentPlannersKeepRequestConfigIsolated(t *testing.T) {
	first := newAssertingOpenAIChatServer(t, "first-model", "first-key")
	defer first.Close()
	second := newAssertingOpenAIChatServer(t, "second-model", "second-key")
	defer second.Close()
	registry, err := NewRegistry(Config{
		Provider: ProviderOpenAIChat,
		BaseURL:  "http://default.invalid",
		APIKey:   "default-key",
		Model:    "default-model",
	})
	if err != nil {
		t.Fatal(err)
	}

	requests := []Config{
		{Provider: ProviderOpenAIChat, BaseURL: first.URL, APIKey: "first-key", Model: "first-model"},
		{Provider: ProviderOpenAIChat, BaseURL: second.URL, APIKey: "second-key", Model: "second-model"},
	}
	errs := make(chan error, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		request := requests[i%len(requests)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			planner, resolved, err := registry.Planner(request)
			if err != nil {
				errs <- err
				return
			}
			if resolved.Model != request.Model || resolved.APIKey != request.APIKey || resolved.BaseURL != request.BaseURL {
				errs <- fmt.Errorf("resolved config bleed: got %#v want model=%s key=%s base=%s", resolved, request.Model, request.APIKey, request.BaseURL)
				return
			}
			turn, err := planner.PlanTurn(context.Background(), PlanRequest{UserPrompt: "answer directly"})
			if err != nil {
				errs <- err
				return
			}
			if turn.Answer == "" {
				errs <- fmt.Errorf("expected direct answer from %s", request.Model)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func newAssertingOpenAIChatServer(t *testing.T, expectedModel string, expectedKey string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+expectedKey {
			http.Error(w, "unexpected authorization "+got, http.StatusUnauthorized)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Model != expectedModel {
			http.Error(w, "unexpected model "+body.Model, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ANSWER: isolated"}}]}`))
	}))
}
