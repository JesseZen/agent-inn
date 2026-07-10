package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	appruntime "github.com/jesse/agent-inn/internal/runtime"
)

const (
	probeTimeout             = 5 * time.Second
	probeUserAgent           = "ainn-probe/1.0"
	probePrompt              = "ping"
	anthropicVersion         = "2023-06-01"
	degradedLatencyThreshold = 1000 * time.Millisecond
)

// ProbeResult 表示对单个 upstream 的一次探测结果。
type ProbeResult struct {
	OK         bool   `json:"ok"`
	Degraded   bool   `json:"degraded,omitempty"`
	StatusCode int    `json:"status_code"`
	LatencyMS  int64  `json:"latency_ms"`
	Error      string `json:"error,omitempty"`
}

// Probe 对 compiled 指向的 upstream 发起一次 GET 探测，使用默认超时。
func Probe(ctx context.Context, compiled Compiled) ProbeResult {
	return probeWithClient(ctx, compiled, &http.Client{Timeout: probeTimeout})
}

func ProbeProtocol(ctx context.Context, compiled Compiled, model string) ProbeResult {
	return probeProtocolWithClient(ctx, compiled, compiled.APIFormat, model, &http.Client{Timeout: probeTimeout})
}

func probeProtocolWithClient(ctx context.Context, compiled Compiled, format appruntime.APIFormat, model string, client *http.Client) ProbeResult {
	path := "/responses"
	payload := map[string]any{
		"model":             model,
		"input":             probePrompt,
		"max_output_tokens": 1,
		"stream":            true,
	}
	switch format {
	case "", appruntime.APIFormatResponses:
	case appruntime.APIFormatChatCompletions:
		path = "/chat/completions"
		payload = map[string]any{
			"model":      model,
			"messages":   []map[string]any{{"role": "user", "content": probePrompt}},
			"max_tokens": 1,
			"stream":     true,
		}
	case appruntime.APIFormatAnthropic:
		path = "/messages"
		payload = map[string]any{
			"model":      model,
			"messages":   []map[string]any{{"role": "user", "content": probePrompt}},
			"max_tokens": 1,
			"stream":     true,
		}
	default:
		return ProbeResult{Error: "unsupported_protocol"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ProbeResult{Error: "protocol_error"}
	}
	url, err := compiled.Join(path, "")
	if err != nil {
		return ProbeResult{Error: "connection_error"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ProbeResult{Error: "connection_error"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", probeUserAgent)
	if compiled.AuthorizationHeader != "" {
		req.Header.Set("Authorization", compiled.AuthorizationHeader)
	}
	if format == appruntime.APIFormatAnthropic {
		req.Header.Set("Anthropic-Version", anthropicVersion)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return failedProbeResult(err, time.Since(start))
	}
	defer resp.Body.Close()
	latency := time.Since(start)
	result := classifyProbeStatus(resp.StatusCode, latency)
	if !result.OK {
		return result
	}
	buffer := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, buffer); err != nil {
		latency = time.Since(start)
		if errors.Is(err, context.DeadlineExceeded) {
			return ProbeResult{Error: "timeout", LatencyMS: latency.Milliseconds()}
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return ProbeResult{StatusCode: resp.StatusCode, Error: "empty_response", LatencyMS: latency.Milliseconds()}
		}
		return ProbeResult{StatusCode: resp.StatusCode, Error: "connection_error", LatencyMS: latency.Milliseconds()}
	}
	latency = time.Since(start)
	return classifyProbeStatus(resp.StatusCode, latency)
}

func probeWithClient(ctx context.Context, compiled Compiled, client *http.Client) ProbeResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, compiled.BaseURL.String(), nil)
	if err != nil {
		return ProbeResult{Error: "connection_error"}
	}
	if compiled.AuthorizationHeader != "" {
		req.Header.Set("Authorization", compiled.AuthorizationHeader)
	}
	req.Header.Set("User-Agent", probeUserAgent)

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return failedProbeResult(err, latency)
	}
	defer resp.Body.Close()
	return classifyProbeStatus(resp.StatusCode, latency)
}

func failedProbeResult(err error, latency time.Duration) ProbeResult {
	if errors.Is(err, context.DeadlineExceeded) {
		return ProbeResult{Error: "timeout", LatencyMS: latency.Milliseconds()}
	}
	return ProbeResult{Error: "connection_error", LatencyMS: latency.Milliseconds()}
}

func classifyProbeStatus(statusCode int, latency time.Duration) ProbeResult {
	result := ProbeResult{StatusCode: statusCode, LatencyMS: latency.Milliseconds()}
	switch {
	case statusCode >= 200 && statusCode < 300:
		if latency >= degradedLatencyThreshold {
			result.Degraded = true
			result.Error = "slow"
		} else {
			result.OK = true
		}
	case statusCode == 401 || statusCode == 403:
		result.Error = "auth_error"
	case statusCode == 429:
		result.Degraded = true
		result.Error = "rate_limited"
	case statusCode >= 400 && statusCode < 500:
		result.Degraded = true
		result.Error = "client_error"
	case statusCode >= 500:
		result.Error = "upstream_error"
	default:
		result.Error = "unexpected_status"
	}
	return result
}
