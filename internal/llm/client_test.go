package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompatibleClientGeneratesText(t *testing.T) {
	var gotAuth string
	var gotRequest chatCompletionRequest
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
	if gotRequest.Model != "test-model" {
		t.Fatalf("unexpected model %q", gotRequest.Model)
	}
	if len(gotRequest.Messages) != 2 || gotRequest.Messages[1].Content != "fix tests" {
		t.Fatalf("unexpected messages %#v", gotRequest.Messages)
	}
	if text != "read app.txt\nrun go test ./..." {
		t.Fatalf("unexpected generated text %q", text)
	}
}

func TestOpenAICompatibleClientReportsAPIError(t *testing.T) {
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
