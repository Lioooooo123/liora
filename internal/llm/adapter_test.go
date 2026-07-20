package llm

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

type recordingProviderAdapter struct {
	requests []providerRequest
	result   Completion
}

func (a *recordingProviderAdapter) Complete(_ context.Context, request providerRequest) (Completion, error) {
	a.requests = append(a.requests, request)
	if request.Stream && request.OnDelta != nil && a.result.Content != "" {
		if err := request.OnDelta(a.result.Content); err != nil {
			return Completion{}, err
		}
	}
	return a.result, nil
}

func TestClientEntryPointsShareProviderAdapterSeam(t *testing.T) {
	adapter := &recordingProviderAdapter{result: Completion{
		Content:      "done",
		ToolCalls:    []ToolCall{{ID: "call-1", Name: "list", Arguments: `{}`}},
		FinishReason: "tool_calls",
	}}
	client := &Client{config: Config{Provider: ProviderOpenAIChat, Model: "test-model"}, adapter: adapter}

	text, err := client.Generate(t.Context(), []Message{{Role: "user", Content: "plain"}})
	if err != nil || text != "done" {
		t.Fatalf("Generate text=%q err=%v", text, err)
	}
	var streamed string
	text, err = client.GenerateStream(t.Context(), []Message{{Role: "user", Content: "stream"}}, func(delta string) error {
		streamed += delta
		return nil
	})
	if err != nil || text != "done" || streamed != "done" {
		t.Fatalf("GenerateStream text=%q streamed=%q err=%v", text, streamed, err)
	}
	tools := []ToolSchema{{Name: "list"}}
	if _, err := client.GenerateWithTools(t.Context(), []Message{{Role: "user", Content: "tools"}}, tools); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GenerateWithToolsStream(t.Context(), []Message{{Role: "user", Content: "tool stream"}}, tools, nil); err != nil {
		t.Fatal(err)
	}

	if len(adapter.requests) != 4 {
		t.Fatalf("expected four requests through one seam, got %d", len(adapter.requests))
	}
	if adapter.requests[0].Stream || adapter.requests[0].ToolMode {
		t.Fatalf("plain request flags %#v", adapter.requests[0])
	}
	if !adapter.requests[1].Stream || adapter.requests[1].ToolMode {
		t.Fatalf("stream request flags %#v", adapter.requests[1])
	}
	if adapter.requests[2].Stream || !adapter.requests[2].ToolMode {
		t.Fatalf("tool request flags %#v", adapter.requests[2])
	}
	if !adapter.requests[3].Stream || !adapter.requests[3].ToolMode {
		t.Fatalf("tool stream request flags %#v", adapter.requests[3])
	}
}

func TestBuiltInProvidersOwnMetadataAndAdapter(t *testing.T) {
	wantIDs := []string{
		ProviderAnthropic,
		ProviderDeepSeek,
		ProviderGemini,
		ProviderOpenAIChat,
		ProviderOpenAICodex,
		ProviderOpenAIResponses,
	}
	definitions := registeredProviderDefinitions()
	gotIDs := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		gotIDs = append(gotIDs, definition.ID)
		if definition.DisplayName == "" || definition.DefaultBaseURL == "" || definition.AuthMode == "" || definition.Capability == nil || definition.NewAdapter == nil {
			t.Fatalf("incomplete provider definition %#v", definition)
		}
		config, err := ResolveConfig(Config{Provider: definition.ID, Model: "test-model", APIKey: "test-key"})
		if err != nil {
			t.Fatalf("resolve %s: %v", definition.ID, err)
		}
		client := newClient(config)
		if client.adapter == nil {
			t.Fatalf("provider %s did not construct an adapter", definition.ID)
		}
		for _, alias := range definition.Aliases {
			if got := NormalizeProvider(alias); got != definition.ID {
				t.Fatalf("alias %q normalized to %q, want %q", alias, got, definition.ID)
			}
		}
	}
	if got := ProviderAuthentication(ProviderOpenAICodex); got != ProviderAuthOAuth {
		t.Fatalf("Codex auth mode=%q", got)
	}
	if got := ProviderAuthentication(ProviderAnthropic); got != ProviderAuthAPIKey {
		t.Fatalf("Anthropic auth mode=%q", got)
	}
	sort.Strings(gotIDs)
	sort.Strings(wantIDs)
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("registered providers=%v want=%v", gotIDs, wantIDs)
	}
}
