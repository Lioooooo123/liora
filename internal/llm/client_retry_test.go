package llm

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestClientRetriesTransientHTTPFailures(t *testing.T) {
	// Given
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "temporary upstream failure", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"recovered"}}]}`))
	}))
	defer server.Close()

	metrics := &recordingMetrics{}
	client := NewOpenAICompatibleClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "test-model",
		Metrics: metrics,
	})

	// When
	text, err := client.Generate(t.Context(), []Message{{Role: "user", Content: "hello"}})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if text != "recovered" {
		t.Fatalf("unexpected generated text %q", text)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	metric, ok := clientMetric(client.config.Metrics, "openai-chat", "test-model")
	if ok && (metric.RetryCount != 2 || metric.StopReason != "http_success" || metric.LatencyMS < 0 || metric.TokenEstimate == 0) {
		t.Fatalf("unexpected retry success metric %#v", metric)
	}
}

func TestClientDoesNotRetryAuthenticationFailure(t *testing.T) {
	// Given
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer server.Close()

	metrics := &recordingMetrics{}
	client := NewOpenAICompatibleClient(Config{
		BaseURL: server.URL,
		APIKey:  "bad-key",
		Model:   "test-model",
		Metrics: metrics,
	})

	// When
	_, err := client.Generate(t.Context(), []Message{{Role: "user", Content: "hello"}})

	// Then
	if err == nil {
		t.Fatal("expected API error")
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
	metric, ok := clientMetric(client.config.Metrics, "openai-chat", "test-model")
	if ok && (metric.RetryCount != 0 || metric.StopReason != "client_error") {
		t.Fatalf("unexpected auth failure metric %#v", metric)
	}
}

func TestClientMetricsAreBucketedByProviderModelAndDoNotBlockSiblingModel(t *testing.T) {
	metrics := &recordingMetrics{}
	limitedAttempts := 0
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limitedAttempts++
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer limited.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"healthy ok"}}]}`))
	}))
	defer healthy.Close()

	limitedClient := NewOpenAICompatibleClient(Config{BaseURL: limited.URL, APIKey: "limited-key", Model: "limited-model", Metrics: metrics})
	healthyClient := NewOpenAICompatibleClient(Config{BaseURL: healthy.URL, APIKey: "healthy-key", Model: "healthy-model", Metrics: metrics})
	if _, err := limitedClient.Generate(t.Context(), []Message{{Role: "user", Content: "hello"}}); err == nil {
		t.Fatal("expected limited model to fail")
	}
	text, err := healthyClient.Generate(t.Context(), []Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if text != "healthy ok" {
		t.Fatalf("unexpected healthy response %q", text)
	}
	if limitedAttempts != llmHTTPMaxAttempts {
		t.Fatalf("expected limited model to retry %d times, got %d", llmHTTPMaxAttempts, limitedAttempts)
	}
	limitedMetric, ok := metrics.last("openai-chat", "limited-model")
	if !ok || !limitedMetric.Error || limitedMetric.Status != http.StatusTooManyRequests || limitedMetric.Attempts != llmHTTPMaxAttempts || limitedMetric.RetryCount != llmHTTPMaxAttempts-1 || limitedMetric.StopReason != "rate_limited" || limitedMetric.LatencyMS < 0 {
		t.Fatalf("unexpected limited metric ok=%v metric=%#v", ok, limitedMetric)
	}
	healthyMetric, ok := metrics.last("openai-chat", "healthy-model")
	if !ok || healthyMetric.Error || healthyMetric.Status != http.StatusOK || healthyMetric.Attempts != 1 || healthyMetric.RetryCount != 0 || healthyMetric.StopReason != "http_success" || healthyMetric.TokenEstimate == 0 || healthyMetric.LatencyMS < 0 {
		t.Fatalf("unexpected healthy metric ok=%v metric=%#v", ok, healthyMetric)
	}
}

func clientMetric(metrics MetricsRecorder, provider string, model string) (Metric, bool) {
	recorder, ok := metrics.(*recordingMetrics)
	if !ok || recorder == nil {
		return Metric{}, false
	}
	return recorder.last(provider, model)
}

type recordingMetrics struct {
	mu      sync.Mutex
	metrics []Metric
}

func (r *recordingMetrics) RecordLLMMetric(metric Metric) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metrics = append(r.metrics, metric)
}

func (r *recordingMetrics) last(provider string, model string) (Metric, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.metrics) - 1; i >= 0; i-- {
		metric := r.metrics[i]
		if metric.Provider == provider && metric.Model == model {
			return metric, true
		}
	}
	return Metric{}, false
}
