package upstream

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const (
	probeTimeout             = 5 * time.Second
	probeUserAgent           = "ainn-probe/1.0"
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
		if errors.Is(err, context.DeadlineExceeded) {
			return ProbeResult{Error: "timeout", LatencyMS: latency.Milliseconds()}
		}
		return ProbeResult{Error: "connection_error", LatencyMS: latency.Milliseconds()}
	}
	defer resp.Body.Close()

	result := ProbeResult{StatusCode: resp.StatusCode, LatencyMS: latency.Milliseconds()}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if latency >= degradedLatencyThreshold {
			result.Degraded = true
			result.Error = "slow"
		} else {
			result.OK = true
		}
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		result.Error = "auth_error"
	case resp.StatusCode == 429:
		result.Degraded = true
		result.Error = "rate_limited"
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		result.Degraded = true
		result.Error = "client_error"
	case resp.StatusCode >= 500:
		result.Error = "upstream_error"
	default:
		result.Error = "unexpected_status"
	}
	return result
}
