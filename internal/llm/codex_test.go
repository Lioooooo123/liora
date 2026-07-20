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

func TestCodexStreamCollectsMultipleFallbackMessagesWithoutDuplicates(t *testing.T) {
	accumulator := newCodexStreamAccumulator(nil)
	if err := accumulator.consumeJSON([]byte(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","content":[{"type":"output_text","text":"one"}]}}`)); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.consumeJSON([]byte(`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","id":"msg_1","content":[{"type":"output_text","text":"one"}]},{"type":"message","id":"msg_2","content":[{"type":"output_text","text":"two"}]}]}}`)); err != nil {
		t.Fatal(err)
	}
	if got := accumulator.content.String(); got != "onetwo" {
		t.Fatalf("unexpected fallback content %q", got)
	}
}

func TestCodexAdapterRunsNativeToolContinuation(t *testing.T) {
	requests := 0
	var secondBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			if body["tool_choice"] != "auto" || body["parallel_tool_calls"] != true {
				t.Fatalf("missing Codex tool controls in %#v", body)
			}
			tools, ok := body["tools"].([]any)
			if !ok || len(tools) != 1 {
				t.Fatalf("missing Codex tools in %#v", body)
			}
			include, ok := body["include"].([]any)
			if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
				t.Fatalf("missing Codex reasoning continuation request in %#v", body)
			}
			events := []string{
				`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_123","summary":[],"encrypted_content":"opaque-reasoning"}}`,
				`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_123","call_id":"call_123","name":"list","arguments":""}}`,
				`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"path\":\".\"}"}`,
				`{"type":"response.function_call_arguments.done","output_index":1,"arguments":"{\"path\":\".\"}"}`,
				`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_123","call_id":"call_123","name":"list","arguments":"{\"path\":\".\"}"}}`,
				`{"type":"response.completed","response":{"status":"completed"}}`,
			}
			for _, event := range events {
				_, _ = w.Write([]byte("data: " + event + "\n\n"))
			}
			return
		}

		secondBody = body
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		Provider: ProviderOpenAICodex,
		BaseURL:  server.URL,
		Model:    "gpt-5.4",
		CredentialResolver: func(context.Context, string) (ProviderCredential, error) {
			return ProviderCredential{AccessToken: "oauth-access", AccountID: "account-123"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := []ToolSchema{{Name: "list", Description: "list files", Parameters: map[string]any{"type": "object"}}}
	first, err := client.GenerateWithTools(t.Context(), []Message{{Role: "user", Content: "list files"}}, tools)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 || !strings.HasPrefix(first.ToolCalls[0].ID, "codex-call-") || first.ToolCalls[0].Name != "list" {
		t.Fatalf("unexpected Codex tool completion %#v", first)
	}
	if first.ProviderState == nil || first.ProviderState.Provider != ProviderOpenAICodex || !strings.Contains(string(first.ProviderState.Data), "opaque-reasoning") {
		t.Fatalf("missing opaque Codex continuation state %#v", first.ProviderState)
	}

	call := first.ToolCalls[0]
	second, err := client.GenerateWithTools(t.Context(), []Message{
		{Role: "user", Content: "list files"},
		{Role: "assistant", ToolCalls: first.ToolCalls, ProviderState: first.ProviderState},
		{Role: "tool", ToolCallID: call.ID, Content: "README.md"},
	}, tools)
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "done" || len(second.ToolCalls) != 0 {
		t.Fatalf("unexpected second Codex completion %#v", second)
	}
	input, ok := secondBody["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("unexpected second Codex input %#v", secondBody["input"])
	}
	reasoning := input[1].(map[string]any)
	functionCall := input[2].(map[string]any)
	functionOutput := input[3].(map[string]any)
	if reasoning["type"] != "reasoning" || reasoning["encrypted_content"] != "opaque-reasoning" {
		t.Fatalf("unexpected reasoning continuation %#v", reasoning)
	}
	if functionCall["type"] != "function_call" || functionCall["id"] != "fc_123" || functionCall["call_id"] != "call_123" {
		t.Fatalf("unexpected function call continuation %#v", functionCall)
	}
	if functionOutput["type"] != "function_call_output" || functionOutput["call_id"] != "call_123" || functionOutput["output"] != "README.md" {
		t.Fatalf("unexpected function output continuation %#v", functionOutput)
	}
}
