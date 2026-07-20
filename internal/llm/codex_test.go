package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodexProviderUsesOAuthCredentialAndResponsesStream(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		for name, want := range map[string]string{
			"Authorization":      "Bearer oauth-access",
			"chatgpt-account-id": "account-123",
			"originator":         "liora",
			"OpenAI-Beta":        "responses=experimental",
		} {
			if got := r.Header.Get(name); got != want {
				t.Fatalf("header %s=%q want=%q", name, got, want)
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ANSWER: \"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"connected\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()
	resolvedProvider := ""
	client, err := NewClient(Config{
		Provider: ProviderOpenAICodex,
		BaseURL:  server.URL,
		Model:    "gpt-5.4",
		CredentialResolver: func(_ context.Context, provider string) (ProviderCredential, error) {
			resolvedProvider = provider
			return ProviderCredential{AccessToken: "oauth-access", AccountID: "account-123"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	text, err := client.Generate(t.Context(), []Message{
		{Role: "system", Content: "Return a planner answer."},
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "ANSWER: connected" || resolvedProvider != ProviderOpenAICodex {
		t.Fatalf("text=%q resolved_provider=%q", text, resolvedProvider)
	}
	if gotBody["model"] != "gpt-5.4" || gotBody["stream"] != true || gotBody["store"] != false || gotBody["instructions"] != "Return a planner answer." {
		t.Fatalf("unexpected Codex body %#v", gotBody)
	}
	input, ok := gotBody["input"].([]any)
	if !ok {
		t.Fatalf("Codex input must be a list, got %T (%#v)", gotBody["input"], gotBody["input"])
	}
	if len(input) != 1 {
		t.Fatalf("unexpected Codex input %#v", input)
	}
	message, ok := input[0].(map[string]any)
	if !ok || message["role"] != "user" || message["content"] != "hello" {
		t.Fatalf("unexpected Codex input message %#v", input[0])
	}
}

func TestCodexStreamParsesSSEWithoutContentType(t *testing.T) {
	stream := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_1"}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"connected"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"status":"completed"}}`,
		"",
	}, "\n")
	accumulator := &codexStreamAccumulator{}
	if _, err := accumulator.consume(strings.NewReader(stream), ""); err != nil {
		t.Fatal(err)
	}
	if got := accumulator.content.String(); got != "connected" {
		t.Fatalf("unexpected Codex stream content %q", got)
	}
}
