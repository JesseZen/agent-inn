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

type ProbeMode string

const (
	ProbeModeReachability ProbeMode = "reachability"
	ProbeModeProtocol     ProbeMode = "protocol"
)

// ProbeResult 表示对单个 upstream 的一次探测结果。
type ProbeResult struct {
	OK            bool      `json:"ok"`
	Degraded      bool      `json:"degraded,omitempty"`
	StatusCode    int       `json:"status_code"`
	LatencyMS     int64     `json:"latency_ms"`
	Error         string    `json:"error,omitempty"`
	Mode          ProbeMode `json:"mode"`
	Authoritative bool      `json:"authoritative"`
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
		return failedProbeResult(err, latency).withMode(ProbeModeReachability)
	}
	defer resp.Body.Close()
	return classifyProbeStatus(resp.StatusCode, latency).withMode(ProbeModeReachability)
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

func (result ProbeResult) withMode(mode ProbeMode) ProbeResult {
	result.Mode = mode
	return result
}

func (result ProbeResult) withAuthority() ProbeResult {
	result.Authoritative = true
	return result
}
