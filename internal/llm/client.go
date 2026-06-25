package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

type Generator interface {
	Generate(ctx context.Context, messages []Message) (string, error)
}

type OpenAICompatibleClient struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

type chatCompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

func NewOpenAICompatibleClient(config Config) *OpenAICompatibleClient {
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAICompatibleClient{
		baseURL: baseURL,
		apiKey:  config.APIKey,
		model:   config.Model,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *OpenAICompatibleClient) Generate(ctx context.Context, messages []Message) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("LLM API key is required")
	}
	if c.model == "" {
		return "", fmt.Errorf("LLM model is required")
	}
	body, err := json.Marshal(chatCompletionRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: 0,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("LLM API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var decoded chatCompletionResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", err
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("LLM API returned no choices")
	}
	return decoded.Choices[0].Message.Content, nil
}
