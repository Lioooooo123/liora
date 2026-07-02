package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	ProviderOpenAIChat      = "openai-chat"
	ProviderOpenAIResponses = "openai-responses"
	ProviderDeepSeek        = "deepseek"
	ProviderAnthropic       = "anthropic"
	ProviderGemini          = "gemini"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls carries an assistant turn's structured tool requests. The wire
	// format is assembled per provider in GenerateWithTools, so these fields are
	// not marshaled by the plain text Generate path.
	ToolCalls  []ToolCall `json:"-"`
	ToolCallID string     `json:"-"`
	ToolError  bool       `json:"-"`
}

type Config struct {
	Provider    string
	BaseURL     string
	APIKey      string
	Model       string
	Profile     string
	Capability  ModelCapability
	Temperature float64
	MaxTokens   int
	Timeout     time.Duration
	RetryPolicy string
	TokenBudget int
	ToolUse     bool
	TraceLabels map[string]string
	Metrics     MetricsRecorder
	HTTPClient  *http.Client
}

type MetricsRecorder interface {
	RecordLLMMetric(Metric)
}

type Metric struct {
	Provider      string
	Model         string
	BaseURL       string
	Status        int
	Attempts      int
	RetryCount    int
	Latency       time.Duration
	LatencyMS     int64
	TokenEstimate int
	StopReason    string
	Error         bool
}

type ModelCapability struct {
	NativeToolUse   bool
	Streaming       bool
	Vision          bool
	LongContext     bool
	JSONSchema      bool
	MaxOutputTokens int
}

type Generator interface {
	Generate(ctx context.Context, messages []Message) (string, error)
}

type Client struct {
	config     Config
	httpClient *http.Client
}

func NewClient(config Config) (*Client, error) {
	resolved, err := ResolveConfig(config)
	if err != nil {
		return nil, err
	}
	return newClient(resolved), nil
}

// ResolveConfig normalizes provider settings and applies local defaults without calling remote APIs.
func ResolveConfig(config Config) (Config, error) {
	config.Provider = NormalizeProvider(config.Provider)
	if config.Provider == "" {
		config.Provider = ProviderOpenAIChat
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 4096
	}
	if config.Timeout == 0 {
		config.Timeout = 60 * time.Second
	}
	if strings.TrimSpace(config.RetryPolicy) == "" {
		config.RetryPolicy = "standard"
	}
	if config.TokenBudget == 0 {
		config.TokenBudget = config.MaxTokens
	}
	config.BaseURL = defaultBaseURL(config.Provider, config.BaseURL)
	if config.BaseURL == "" {
		return Config{}, fmt.Errorf("unsupported LLM provider %q", config.Provider)
	}
	config.Profile = strings.TrimSpace(config.Profile)
	config.Capability = ProviderCapability(config.Provider, config.Model)
	config.ToolUse = config.Capability.NativeToolUse
	config.TraceLabels = normalizeTraceLabels(config.TraceLabels)
	return config, nil
}

func newClient(config Config) *Client {
	httpClient := config.HTTPClient
	if httpClient == nil {
		timeout := config.Timeout
		if timeout == 0 {
			timeout = 60 * time.Second
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{config: config, httpClient: httpClient}
}

func NewOpenAICompatibleClient(config Config) *Client {
	if config.Provider == "" {
		config.Provider = ProviderOpenAIChat
	}
	config.Provider = NormalizeProvider(config.Provider)
	config.BaseURL = defaultBaseURL(config.Provider, config.BaseURL)
	if config.MaxTokens == 0 {
		config.MaxTokens = 4096
	}
	config.Capability = ProviderCapability(config.Provider, config.Model)
	config.ToolUse = config.Capability.NativeToolUse
	return newClient(config)
}

func NormalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "openai", "openai-compatible", "chat-completions", "chat":
		return ProviderOpenAIChat
	case "responses", "openai-responses":
		return ProviderOpenAIResponses
	case "deepseek":
		return ProviderDeepSeek
	case "anthropic", "claude":
		return ProviderAnthropic
	case "gemini", "google", "google-gemini":
		return ProviderGemini
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func ProviderDisplayName(provider string) string {
	switch NormalizeProvider(provider) {
	case ProviderOpenAIChat:
		return "OpenAI Chat"
	case ProviderOpenAIResponses:
		return "OpenAI Responses"
	case ProviderDeepSeek:
		return "DeepSeek"
	case ProviderAnthropic:
		return "Anthropic"
	case ProviderGemini:
		return "Gemini"
	default:
		return provider
	}
}

func defaultBaseURL(provider string, baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL != "" {
		return baseURL
	}
	switch NormalizeProvider(provider) {
	case ProviderOpenAIChat, ProviderOpenAIResponses:
		return "https://api.openai.com/v1"
	case ProviderDeepSeek:
		return "https://api.deepseek.com"
	case ProviderAnthropic:
		return "https://api.anthropic.com/v1"
	case ProviderGemini:
		return "https://generativelanguage.googleapis.com"
	default:
		return ""
	}
}

func bearerHeaders(apiKey string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + apiKey}
}
