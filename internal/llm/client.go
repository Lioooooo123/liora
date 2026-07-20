package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	ProviderOpenAIChat      = "openai-chat"
	ProviderOpenAIResponses = "openai-responses"
	ProviderOpenAICodex     = "openai-codex"
	ProviderDeepSeek        = "deepseek"
	ProviderAnthropic       = "anthropic"
	ProviderGemini          = "gemini"
	DefaultOpenAICodexModel = "gpt-5.4"
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
	// ProviderState is opaque to the agent loop. The adapter that produced it
	// owns its schema and may use it to continue provider-specific reasoning.
	ProviderState *ProviderState `json:"-"`
}

type ProviderState struct {
	Provider string
	Data     json.RawMessage
}

type Config struct {
	Provider           string
	BaseURL            string
	APIKey             string
	Model              string
	Profile            string
	Capability         ModelCapability
	Temperature        float64
	MaxTokens          int
	Timeout            time.Duration
	RetryPolicy        string
	TokenBudget        int
	ToolUse            bool
	TraceLabels        map[string]string
	Metrics            MetricsRecorder
	HTTPClient         *http.Client
	CredentialResolver CredentialResolver
}

type ProviderCredential struct {
	AccessToken string
	AccountID   string
}

type CredentialResolver func(context.Context, string) (ProviderCredential, error)

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

type DeltaHandler func(delta string) error

type StreamGenerator interface {
	GenerateStream(ctx context.Context, messages []Message, onDelta DeltaHandler) (string, error)
}

type Client struct {
	config     Config
	httpClient *http.Client
	adapter    providerAdapter
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
	definition, ok := lookupProvider(config.Provider)
	if !ok {
		return Config{}, fmt.Errorf("unsupported LLM provider %q", config.Provider)
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
	config.Profile = strings.TrimSpace(config.Profile)
	if strings.TrimSpace(config.Model) == "" && definition.DefaultModel != "" {
		config.Model = definition.DefaultModel
	}
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
	client := &Client{config: config, httpClient: httpClient}
	if definition, ok := lookupProvider(config.Provider); ok {
		client.adapter = definition.NewAdapter(client)
	}
	return client
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
	normalized := strings.ToLower(strings.TrimSpace(provider))
	if definition, ok := lookupProvider(normalized); ok {
		return definition.ID
	}
	return normalized
}

func ProviderDisplayName(provider string) string {
	if definition, ok := lookupProvider(provider); ok {
		return definition.DisplayName
	}
	return provider
}

func defaultBaseURL(provider string, baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL != "" {
		return baseURL
	}
	if definition, ok := lookupProvider(provider); ok {
		return definition.DefaultBaseURL
	}
	return ""
}

func bearerHeaders(apiKey string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + apiKey}
}
