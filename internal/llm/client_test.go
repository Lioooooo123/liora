package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIChatClientGeneratesText(t *testing.T) {
	var gotAuth string
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [
				{"message": {"role": "assistant", "content": "read app.txt\nrun go test ./..."}}
			]
		}`))
	}))
	defer server.Close()

	client := NewOpenAICompatibleClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "test-model",
	})

	text, err := client.Generate(t.Context(), []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "fix tests"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("unexpected authorization header %q", gotAuth)
	}
	if gotRequest["model"] != "test-model" {
		t.Fatalf("unexpected model %#v", gotRequest["model"])
	}
	messages, ok := gotRequest["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("unexpected messages %#v", gotRequest["messages"])
	}
	if text != "read app.txt\nrun go test ./..." {
		t.Fatalf("unexpected generated text %q", text)
	}
}

func TestOpenAIResponsesClientGeneratesText(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected auth %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output_text":"ANSWER: hi"}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{
		Provider: ProviderOpenAIResponses,
		BaseURL:  server.URL,
		APIKey:   "test-key",
		Model:    "gpt-test",
	})
	if err != nil {
		t.Fatal(err)
	}

	text, err := client.Generate(t.Context(), []Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotRequest["instructions"] != "be brief" || gotRequest["input"] != "hello" {
		t.Fatalf("unexpected responses request %#v", gotRequest)
	}
	if text != "ANSWER: hi" {
		t.Fatalf("unexpected text %q", text)
	}
}

func TestAnthropicClientGeneratesText(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("unexpected api key header %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Fatal("missing anthropic-version header")
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ANSWER: claude"}]}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{
		Provider: ProviderAnthropic,
		BaseURL:  server.URL,
		APIKey:   "test-key",
		Model:    "claude-test",
	})
	if err != nil {
		t.Fatal(err)
	}

	text, err := client.Generate(t.Context(), []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotRequest["system"] != "system prompt" || gotRequest["model"] != "claude-test" {
		t.Fatalf("unexpected anthropic request %#v", gotRequest)
	}
	if text != "ANSWER: claude" {
		t.Fatalf("unexpected text %q", text)
	}
}

func TestGeminiClientGeneratesText(t *testing.T) {
	var gotRequest map[string]any
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-test:generateContent" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		gotQuery = r.URL.Query().Get("key")
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [
				{"content": {"parts": [{"text": "ANSWER: gemini"}]}}
			]
		}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{
		Provider: ProviderGemini,
		BaseURL:  server.URL,
		APIKey:   "test-key",
		Model:    "gemini-test",
	})
	if err != nil {
		t.Fatal(err)
	}

	text, err := client.Generate(t.Context(), []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "test-key" {
		t.Fatalf("unexpected key query %q", gotQuery)
	}
	if _, ok := gotRequest["systemInstruction"]; !ok {
		t.Fatalf("expected systemInstruction in request %#v", gotRequest)
	}
	if text != "ANSWER: gemini" {
		t.Fatalf("unexpected text %q", text)
	}
}

func TestClientReportsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewOpenAICompatibleClient(Config{
		BaseURL: server.URL,
		APIKey:  "bad-key",
		Model:   "test-model",
	})

	_, err := client.Generate(t.Context(), []Message{{Role: "user", Content: "hello"}})
	if err == nil {
		t.Fatal("expected API error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected status code in error, got %v", err)
	}
}

func TestGenerateValidatesModelAndAPIKey(t *testing.T) {
	client, err := NewClient(Config{Provider: ProviderAnthropic, APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(t.Context(), []Message{{Role: "user", Content: "hello"}})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("expected model error, got %v", err)
	}
	client, err = NewClient(Config{Provider: ProviderAnthropic, Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(t.Context(), []Message{{Role: "user", Content: "hello"}})
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected API key error, got %v", err)
	}
}
