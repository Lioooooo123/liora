package llm

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateWithToolsParsesOpenAIToolCalls(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [
				{"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[
					{"id":"call_1","type":"function","function":{"name":"list","arguments":"{\"path\":\".\"}"}}
				]}}
			]
		}`))
	}))
	defer server.Close()

	client := NewOpenAICompatibleClient(Config{BaseURL: server.URL, APIKey: "test-key", Model: "test-model"})
	completion, err := client.GenerateWithTools(t.Context(), []Message{
		{Role: "system", Content: "be a tool agent"},
		{Role: "user", Content: "看看目录"},
	}, []ToolSchema{{Name: "list", Description: "list dir", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "additionalProperties": false}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %#v", completion.ToolCalls)
	}
	call := completion.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "list" || call.Arguments != `{"path":"."}` {
		t.Fatalf("unexpected tool call %#v", call)
	}
	if completion.FinishReason != "tool_calls" {
		t.Fatalf("unexpected finish reason %q", completion.FinishReason)
	}
	tools, ok := gotRequest["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected tools in request, got %#v", gotRequest["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Fatalf("expected function tool, got %#v", tool)
	}
}

func TestGenerateWithToolsSerializesToolResultMessages(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}]}`))
	}))
	defer server.Close()

	client := NewOpenAICompatibleClient(Config{BaseURL: server.URL, APIKey: "test-key", Model: "test-model"})
	completion, err := client.GenerateWithTools(t.Context(), []Message{
		{Role: "user", Content: "看看目录"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "list", Arguments: `{"path":"."}`}}},
		{Role: "tool", ToolCallID: "call_1", Content: "README.md"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 0 {
		t.Fatalf("expected no tool calls, got %#v", completion.ToolCalls)
	}
	if completion.Content != "done" {
		t.Fatalf("unexpected content %q", completion.Content)
	}
	messages, ok := gotRequest["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("unexpected messages %#v", gotRequest["messages"])
	}
	assistant := messages[1].(map[string]any)
	calls, ok := assistant["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected assistant tool_calls, got %#v", assistant)
	}
	toolMessage := messages[2].(map[string]any)
	if toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "call_1" || toolMessage["content"] != "README.md" {
		t.Fatalf("unexpected tool message %#v", toolMessage)
	}
}

func TestGenerateWithToolsAnthropicToolUse(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stop_reason":"tool_use","content":[
			{"type":"text","text":"let me look"},
			{"type":"tool_use","id":"toolu_1","name":"list","input":{"path":"."}}
		]}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{Provider: ProviderAnthropic, BaseURL: server.URL, APIKey: "test-key", Model: "claude-test"})
	if err != nil {
		t.Fatal(err)
	}
	completion, err := client.GenerateWithTools(t.Context(), []Message{
		{Role: "system", Content: "agent"},
		{Role: "user", Content: "list"},
	}, []ToolSchema{{Name: "list", Description: "list", Parameters: map[string]any{"type": "object"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 1 || completion.ToolCalls[0].Name != "list" {
		t.Fatalf("unexpected tool calls %#v", completion.ToolCalls)
	}
	if completion.ToolCalls[0].Arguments != `{"path":"."}` {
		t.Fatalf("unexpected arguments %q", completion.ToolCalls[0].Arguments)
	}
	if _, ok := gotRequest["tools"]; !ok {
		t.Fatalf("expected tools in anthropic request %#v", gotRequest)
	}
}

func TestClientSupportsToolsFollowsResolvedCapability(t *testing.T) {
	openAI := NewOpenAICompatibleClient(Config{
		Provider: ProviderOpenAIChat,
		BaseURL:  "http://localhost",
		APIKey:   "key",
		Model:    "gpt-5",
	})
	if !openAI.SupportsTools() {
		t.Fatal("expected OpenAI chat capability to enable native tool-use")
	}
	responses, err := NewClient(Config{
		Provider: ProviderOpenAIResponses,
		BaseURL:  "http://localhost",
		APIKey:   "key",
		Model:    "gpt-5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if responses.SupportsTools() {
		t.Fatal("expected responses adapter to fall back until native tool loop is implemented")
	}
}

func TestGenerateWithToolsUnsupportedProvider(t *testing.T) {
	client, err := NewClient(Config{Provider: ProviderGemini, APIKey: "key", Model: "gemini-test"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GenerateWithTools(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil)
	if !errors.Is(err, ErrToolsUnsupported) {
		t.Fatalf("expected ErrToolsUnsupported, got %v", err)
	}
	if client.SupportsTools() {
		t.Fatal("gemini should not support tools")
	}
}
