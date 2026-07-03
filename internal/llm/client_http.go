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

const (
	llmHTTPMaxAttempts = 3
	llmHTTPRetryBase   = 25 * time.Millisecond
)

type llmStatusError struct {
	status int
	body   string
}

func (e *llmStatusError) Error() string {
	return fmt.Sprintf("LLM API returned %d: %s", e.status, e.body)
}

func (c *Client) postJSON(ctx context.Context, endpoint string, body any, headers map[string]string) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	var lastErr error
	started := time.Now()
	attempts := 0
	lastStatus := 0
	for attempt := 1; attempt <= llmHTTPMaxAttempts; attempt++ {
		attempts = attempt
		responseData, status, err := c.postJSONOnce(ctx, endpoint, data, headers)
		lastStatus = status
		if err == nil && status >= 200 && status < 300 {
			c.recordMetric(status, attempts, started, len(responseData), false)
			return responseData, nil
		}
		if err == nil {
			err = &llmStatusError{status: status, body: strings.TrimSpace(string(responseData))}
		}
		lastErr = err
		if !shouldRetryLLMRequest(ctx, status, err, attempt) {
			c.recordMetric(status, attempts, started, 0, true)
			return nil, err
		}
		if err := waitBeforeLLMRetry(ctx, attempt); err != nil {
			c.recordMetric(status, attempts, started, 0, true)
			return nil, err
		}
	}
	c.recordMetric(lastStatus, attempts, started, 0, true)
	return nil, lastErr
}

func (c *Client) postJSONStream(ctx context.Context, endpoint string, body any, headers map[string]string, consume func(io.Reader, string) (int, error)) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	var lastErr error
	started := time.Now()
	attempts := 0
	lastStatus := 0
	for attempt := 1; attempt <= llmHTTPMaxAttempts; attempt++ {
		attempts = attempt
		status, responseBytes, err := c.postJSONStreamOnce(ctx, endpoint, data, headers, consume)
		lastStatus = status
		if err == nil && status >= 200 && status < 300 {
			c.recordMetric(status, attempts, started, responseBytes, false)
			return nil
		}
		lastErr = err
		if !shouldRetryLLMRequest(ctx, status, err, attempt) {
			c.recordMetric(status, attempts, started, responseBytes, true)
			return err
		}
		if err := waitBeforeLLMRetry(ctx, attempt); err != nil {
			c.recordMetric(status, attempts, started, responseBytes, true)
			return err
		}
	}
	c.recordMetric(lastStatus, attempts, started, 0, true)
	return lastErr
}

func (c *Client) postJSONOnce(ctx context.Context, endpoint string, data []byte, headers map[string]string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	responseData, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, resp.StatusCode, readErr
	}
	if closeErr != nil {
		return nil, resp.StatusCode, closeErr
	}
	return responseData, resp.StatusCode, nil
}

func (c *Client) postJSONStreamOnce(ctx context.Context, endpoint string, data []byte, headers map[string]string, consume func(io.Reader, string) (int, error)) (int, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseData, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return resp.StatusCode, 0, readErr
		}
		return resp.StatusCode, len(responseData), &llmStatusError{status: resp.StatusCode, body: strings.TrimSpace(string(responseData))}
	}
	responseBytes, err := consume(resp.Body, resp.Header.Get("Content-Type"))
	return resp.StatusCode, responseBytes, err
}

func shouldRetryLLMRequest(ctx context.Context, status int, err error, attempt int) bool {
	if attempt >= llmHTTPMaxAttempts || ctx.Err() != nil {
		return false
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		return true
	}
	return err != nil && status == 0
}

func waitBeforeLLMRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(attempt) * llmHTTPRetryBase)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) recordMetric(status int, attempts int, started time.Time, responseBytes int, hasError bool) {
	if c.config.Metrics == nil {
		return
	}
	latency := time.Since(started)
	c.config.Metrics.RecordLLMMetric(Metric{
		Provider:      NormalizeProvider(c.config.Provider),
		Model:         c.config.Model,
		BaseURL:       c.config.BaseURL,
		Status:        status,
		Attempts:      attempts,
		RetryCount:    max(0, attempts-1),
		Latency:       latency,
		LatencyMS:     latency.Milliseconds(),
		TokenEstimate: estimateTokens(responseBytes),
		StopReason:    metricStopReason(status, hasError),
		Error:         hasError,
	})
}

func metricStopReason(status int, hasError bool) string {
	if !hasError {
		return "http_success"
	}
	if status == http.StatusTooManyRequests {
		return "rate_limited"
	}
	if status >= 500 {
		return "server_error"
	}
	if status >= 400 {
		return "client_error"
	}
	if status == 0 {
		return "transport_error"
	}
	return "http_error"
}

func estimateTokens(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
}
