package llm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type codexContinuation struct {
	Items []json.RawMessage       `json:"items,omitempty"`
	Calls map[string]codexCallRef `json:"calls,omitempty"`
}

type codexCallRef struct {
	CallID string `json:"call_id"`
	ItemID string `json:"item_id,omitempty"`
}

func (c *Client) generateCodexResponses(ctx context.Context, messages []Message, tools []ToolSchema, credential ProviderCredential, onDelta DeltaHandler) (Completion, error) {
	instructions, inputMessages := splitSystemMessages(messages)
	input, err := codexResponsesInput(inputMessages)
	if err != nil {
		return Completion{}, err
	}
	body := map[string]any{
		"model":   c.config.Model,
		"input":   input,
		"store":   false,
		"stream":  true,
		"include": []string{"reasoning.encrypted_content"},
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	if len(tools) > 0 {
		body["tools"] = codexTools(tools)
		body["tool_choice"] = "auto"
		body["parallel_tool_calls"] = true
	}
	headers := map[string]string{
		"Authorization":      "Bearer " + credential.AccessToken,
		"chatgpt-account-id": credential.AccountID,
		"originator":         "liora",
		"OpenAI-Beta":        "responses=experimental",
		"User-Agent":         "liora",
	}
	accumulator := newCodexStreamAccumulator(onDelta)
	if err := c.postJSONStream(ctx, codexResponsesURL(c.config.BaseURL), body, headers, accumulator.consume); err != nil {
		return Completion{}, err
	}
	completion, err := accumulator.completion(messages)
	if err != nil {
		return Completion{}, err
	}
	if completion.Content == "" && len(completion.ToolCalls) == 0 {
		return Completion{}, fmt.Errorf("Codex API returned no text or tool calls")
	}
	return completion, nil
}

func codexTools(tools []ToolSchema) []map[string]any {
	converted := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		parameters := tool.Parameters
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		converted = append(converted, map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  parameters,
		})
	}
	return converted
}

func codexResponsesInput(messages []Message) ([]map[string]any, error) {
	input := make([]map[string]any, 0, len(messages))
	providerCallIDs := map[string]string{}
	for _, message := range messages {
		switch message.Role {
		case "assistant":
			state, err := decodeCodexContinuation(message.ProviderState)
			if err != nil {
				return nil, err
			}
			for _, raw := range state.Items {
				var item map[string]any
				if err := json.Unmarshal(raw, &item); err != nil {
					return nil, fmt.Errorf("decode Codex continuation item: %w", err)
				}
				input = append(input, item)
			}
			if message.Content != "" {
				input = append(input, map[string]any{
					"role":    "assistant",
					"content": []map[string]any{{"type": "output_text", "text": message.Content}},
				})
			}
			for _, call := range message.ToolCalls {
				ref := state.Calls[call.ID]
				providerCallID := ref.CallID
				if providerCallID == "" {
					providerCallID = call.ID
				}
				providerCallIDs[call.ID] = providerCallID
				item := map[string]any{
					"type": "function_call", "call_id": providerCallID, "name": call.Name,
					"arguments": defaultJSONArguments(call.Arguments),
				}
				if ref.ItemID != "" {
					item["id"] = ref.ItemID
				}
				input = append(input, item)
			}
		case "tool":
			callID := message.ToolCallID
			if providerCallID := providerCallIDs[callID]; providerCallID != "" {
				callID = providerCallID
			}
			input = append(input, map[string]any{
				"type": "function_call_output", "call_id": callID, "output": message.Content,
			})
		default:
			role := message.Role
			if role == "" {
				role = "user"
			}
			input = append(input, map[string]any{"role": role, "content": message.Content})
		}
	}
	return input, nil
}

func decodeCodexContinuation(providerState *ProviderState) (codexContinuation, error) {
	if providerState == nil || len(providerState.Data) == 0 {
		return codexContinuation{}, nil
	}
	if NormalizeProvider(providerState.Provider) != ProviderOpenAICodex {
		return codexContinuation{}, nil
	}
	var state codexContinuation
	if err := json.Unmarshal(providerState.Data, &state); err != nil {
		return codexContinuation{}, fmt.Errorf("decode Codex continuation state: %w", err)
	}
	return state, nil
}

func defaultJSONArguments(arguments string) string {
	if strings.TrimSpace(arguments) == "" {
		return "{}"
	}
	return arguments
}

func codexResponsesURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(baseURL, "/codex/responses") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/codex") {
		return baseURL + "/responses"
	}
	return baseURL + "/codex/responses"
}

type codexResponseItem struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Content   []codexContent  `json:"content"`
	Raw       json.RawMessage `json:"-"`
}

type codexContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (i *codexResponseItem) UnmarshalJSON(data []byte) error {
	type wireItem codexResponseItem
	var decoded wireItem
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*i = codexResponseItem(decoded)
	i.Raw = append(i.Raw[:0], data...)
	return nil
}

type codexPendingCall struct {
	CallID    string
	ItemID    string
	Name      string
	Arguments string
}

type codexStreamAccumulator struct {
	onDelta         DeltaHandler
	content         strings.Builder
	hasStreamedText bool
	calls           map[int]*codexPendingCall
	providerItems   []json.RawMessage
	providerItemIDs map[string]bool
	messageItemIDs  map[string]bool
}

func newCodexStreamAccumulator(onDelta DeltaHandler) *codexStreamAccumulator {
	return &codexStreamAccumulator{
		onDelta: onDelta, calls: map[int]*codexPendingCall{},
		providerItemIDs: map[string]bool{}, messageItemIDs: map[string]bool{},
	}
}

func (a *codexStreamAccumulator) consume(reader io.Reader, _ string) (int, error) {
	return parseSSE(reader, func(data string) error {
		return a.consumeJSON([]byte(data))
	})
}

func (a *codexStreamAccumulator) consumeJSON(data []byte) error {
	var event struct {
		Type        string            `json:"type"`
		Delta       string            `json:"delta"`
		Message     string            `json:"message"`
		Arguments   string            `json:"arguments"`
		OutputIndex int               `json:"output_index"`
		Item        codexResponseItem `json:"item"`
		Error       struct {
			Message string `json:"message"`
		} `json:"error"`
		OutputText string `json:"output_text"`
		Response   struct {
			Status string `json:"status"`
			Error  struct {
				Message string `json:"message"`
			} `json:"error"`
			Output []codexResponseItem `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("decode Codex response event: %w", err)
	}
	if event.Type == "error" || event.Type == "response.failed" {
		message := firstNonEmptyString(event.Error.Message, event.Response.Error.Message, event.Message, "Codex request failed")
		return fmt.Errorf("Codex API error: %s", message)
	}
	if event.Type == "response.output_text.delta" && event.Delta != "" {
		a.hasStreamedText = true
		return a.appendText(event.Delta)
	}
	switch event.Type {
	case "response.output_item.added":
		if event.Item.Type == "function_call" {
			a.updateCall(event.OutputIndex, event.Item, false)
		}
	case "response.function_call_arguments.delta":
		if event.Delta != "" {
			a.updateCall(event.OutputIndex, codexResponseItem{Arguments: event.Delta}, true)
		}
	case "response.function_call_arguments.done":
		a.updateCall(event.OutputIndex, codexResponseItem{Arguments: event.Arguments}, false)
	case "response.output_item.done":
		switch event.Item.Type {
		case "function_call":
			a.updateCall(event.OutputIndex, event.Item, false)
		case "reasoning":
			a.addProviderItem(event.Item)
		case "message":
			if err := a.addMessageItem(event.Item); err != nil {
				return err
			}
		}
	}
	if event.OutputText != "" && a.content.Len() == 0 {
		a.hasStreamedText = true
		return a.appendText(event.OutputText)
	}
	if event.Type == "response.completed" {
		for index, output := range event.Response.Output {
			switch output.Type {
			case "function_call":
				a.updateCall(index, output, false)
			case "reasoning":
				a.addProviderItem(output)
			case "message":
				if err := a.addMessageItem(output); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (a *codexStreamAccumulator) updateCall(index int, item codexResponseItem, appendArguments bool) {
	call := a.calls[index]
	if call == nil {
		call = &codexPendingCall{}
		a.calls[index] = call
	}
	if item.ID != "" {
		call.ItemID = item.ID
	}
	if item.CallID != "" {
		call.CallID = item.CallID
	}
	if item.Name != "" {
		call.Name = item.Name
	}
	if appendArguments {
		call.Arguments += item.Arguments
	} else if item.Arguments != "" {
		call.Arguments = item.Arguments
	}
}

func (a *codexStreamAccumulator) addProviderItem(item codexResponseItem) {
	if len(item.Raw) == 0 {
		return
	}
	key := item.ID
	if key == "" {
		key = string(item.Raw)
	}
	if a.providerItemIDs[key] {
		return
	}
	a.providerItemIDs[key] = true
	a.providerItems = append(a.providerItems, append(json.RawMessage(nil), item.Raw...))
}

func (a *codexStreamAccumulator) addMessageItem(item codexResponseItem) error {
	if a.hasStreamedText {
		return nil
	}
	key := item.ID
	if key == "" {
		key = string(item.Raw)
	}
	if key != "" && a.messageItemIDs[key] {
		return nil
	}
	if key != "" {
		a.messageItemIDs[key] = true
	}
	return a.appendItemText(item)
}

func (a *codexStreamAccumulator) completion(messages []Message) (Completion, error) {
	completion := Completion{Content: a.content.String(), FinishReason: "stop"}
	continuation := codexContinuation{Items: append([]json.RawMessage(nil), a.providerItems...), Calls: map[string]codexCallRef{}}
	indexes := make([]int, 0, len(a.calls))
	for index := range a.calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	toolTurn := codexToolTurn(messages)
	for order, index := range indexes {
		call := a.calls[index]
		arguments := defaultJSONArguments(call.Arguments)
		stableID := stableCodexToolCallID(toolTurn, order, call.Name, arguments)
		completion.ToolCalls = append(completion.ToolCalls, ToolCall{ID: stableID, Name: call.Name, Arguments: arguments})
		providerCallID := call.CallID
		if providerCallID == "" {
			providerCallID = stableID
		}
		continuation.Calls[stableID] = codexCallRef{CallID: providerCallID, ItemID: call.ItemID}
	}
	if len(completion.ToolCalls) > 0 {
		completion.FinishReason = "tool_calls"
	}
	if len(continuation.Items) > 0 || len(continuation.Calls) > 0 {
		data, err := json.Marshal(continuation)
		if err != nil {
			return Completion{}, fmt.Errorf("encode Codex continuation state: %w", err)
		}
		completion.ProviderState = &ProviderState{Provider: ProviderOpenAICodex, Data: data}
	}
	return completion, nil
}

func codexToolTurn(messages []Message) int {
	turn := 0
	for _, message := range messages {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			turn++
		}
	}
	return turn
}

func stableCodexToolCallID(turn, index int, name, arguments string) string {
	fingerprint := fmt.Sprintf("%d\x00%d\x00%s\x00%s", turn, index, name, arguments)
	digest := sha256.Sum256([]byte(fingerprint))
	return fmt.Sprintf("codex-call-%x", digest[:8])
}

func (a *codexStreamAccumulator) appendItemText(item codexResponseItem) error {
	for _, content := range item.Content {
		if content.Text != "" {
			if err := a.appendText(content.Text); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *codexStreamAccumulator) appendText(text string) error {
	a.content.WriteString(text)
	if a.onDelta != nil {
		return a.onDelta(text)
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
