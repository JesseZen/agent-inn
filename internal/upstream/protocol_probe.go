package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	appruntime "github.com/jesse/agent-inn/internal/runtime"
)

const (
	protocolProbePrompt           = "hi"
	protocolProbeMaximumTokens    = 16
	protocolProbeFirstByteTimeout = 15 * time.Second
	protocolProbeTotalTimeout     = 30 * time.Second
	protocolProbeMaximumBytes     = 128 * 1024
	anthropicVersion              = "2023-06-01"
)

func ProbeProtocol(ctx context.Context, compiled Compiled, model string) ProbeResult {
	return ProbeProtocolWithClient(ctx, compiled, model, &http.Client{})
}

func ProbeProtocolWithClient(ctx context.Context, compiled Compiled, model string, client *http.Client) ProbeResult {
	return probeProtocolWithClient(ctx, compiled, compiled.APIFormat, model, client)
}

func probeProtocolWithClient(ctx context.Context, compiled Compiled, format appruntime.APIFormat, model string, client *http.Client) ProbeResult {
	path := "/responses"
	payload := map[string]any{
		"model":             model,
		"input":             protocolProbePrompt,
		"max_output_tokens": protocolProbeMaximumTokens,
		"stream":            true,
	}
	switch format {
	case "", appruntime.APIFormatResponses:
	case appruntime.APIFormatChatCompletions:
		path = "/chat/completions"
		payload = map[string]any{
			"model":      model,
			"messages":   []map[string]any{{"role": "user", "content": protocolProbePrompt}},
			"max_tokens": protocolProbeMaximumTokens,
			"stream":     true,
		}
	case appruntime.APIFormatAnthropic:
		path = "/messages"
		payload = map[string]any{
			"model":      model,
			"messages":   []map[string]any{{"role": "user", "content": protocolProbePrompt}},
			"max_tokens": protocolProbeMaximumTokens,
			"stream":     true,
		}
	default:
		return ProbeResult{Error: "unsupported_protocol"}.withMode(ProbeModeProtocol)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ProbeResult{Error: "protocol_error"}.withMode(ProbeModeProtocol)
	}
	endpoint := compiled.BaseURL.String()
	if !strings.HasSuffix(strings.TrimRight(compiled.BaseURL.Path, "/"), path) {
		endpoint, err = compiled.Join(path, "")
		if err != nil {
			return ProbeResult{Error: "connection_error"}.withMode(ProbeModeProtocol)
		}
	} else {
		baseURL := *compiled.BaseURL
		baseURL.RawQuery = ""
		baseURL.ForceQuery = false
		endpoint = baseURL.String()
	}

	totalCtx, cancelTotal := context.WithTimeout(ctx, protocolProbeTotalTimeout)
	defer cancelTotal()
	requestCtx, cancelRequest := context.WithCancelCause(totalCtx)
	defer cancelRequest(nil)
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ProbeResult{Error: "connection_error"}.withMode(ProbeModeProtocol)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", probeUserAgent)
	if compiled.AuthorizationHeader != "" {
		request.Header.Set("Authorization", compiled.AuthorizationHeader)
	}
	if format == appruntime.APIFormatAnthropic {
		request.Header.Set("Anthropic-Version", anthropicVersion)
	}

	firstByteTimer := time.AfterFunc(protocolProbeFirstByteTimeout, func() {
		cancelRequest(context.DeadlineExceeded)
	})
	start := time.Now()
	response, err := client.Do(request)
	if err != nil {
		firstByteTimer.Stop()
		return failedProbeResult(err, time.Since(start)).withMode(ProbeModeProtocol).withAuthority()
	}
	defer response.Body.Close()
	latency := time.Since(start)
	result := classifyProbeStatus(response.StatusCode, latency).withMode(ProbeModeProtocol).withAuthority()
	if !result.OK {
		firstByteTimer.Stop()
		return result
	}

	response.Body = &protocolProbeFirstByteReadCloser{source: response.Body, timer: firstByteTimer}
	data, err := io.ReadAll(io.LimitReader(response.Body, protocolProbeMaximumBytes+1))
	latency = time.Since(start)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(context.Cause(requestCtx), context.DeadlineExceeded) {
			return ProbeResult{StatusCode: response.StatusCode, LatencyMS: latency.Milliseconds(), Error: "timeout"}.withMode(ProbeModeProtocol).withAuthority()
		}
		return ProbeResult{StatusCode: response.StatusCode, LatencyMS: latency.Milliseconds(), Error: "protocol_error"}.withMode(ProbeModeProtocol).withAuthority()
	}
	if len(data) > protocolProbeMaximumBytes || !validProtocolProbeStream(format, data) {
		return ProbeResult{StatusCode: response.StatusCode, LatencyMS: latency.Milliseconds(), Error: "protocol_error"}.withMode(ProbeModeProtocol).withAuthority()
	}
	return classifyProbeStatus(response.StatusCode, latency).withMode(ProbeModeProtocol).withAuthority()
}

type protocolProbeFirstByteReadCloser struct {
	source io.ReadCloser
	timer  *time.Timer
}

func (body *protocolProbeFirstByteReadCloser) Read(buffer []byte) (int, error) {
	n, err := body.source.Read(buffer)
	if n > 0 || err != nil {
		body.timer.Stop()
	}
	return n, err
}

func (body *protocolProbeFirstByteReadCloser) Close() error {
	body.timer.Stop()
	return body.source.Close()
}

func validProtocolProbeStream(format appruntime.APIFormat, data []byte) bool {
	normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
	validEvent := false
	terminalEvent := false
	for _, block := range strings.Split(normalized, "\n\n") {
		var eventName string
		var dataLines []string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if len(dataLines) == 0 {
			continue
		}
		eventData := strings.Join(dataLines, "\n")
		if eventData == "[DONE]" {
			if format == appruntime.APIFormatChatCompletions && validEvent {
				terminalEvent = true
			}
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(eventData), &payload); err != nil {
			return false
		}
		eventType, _ := payload["type"].(string)
		if eventName == "error" || eventType == "error" || eventName == "response.failed" || eventType == "response.failed" {
			return false
		}
		switch format {
		case "", appruntime.APIFormatResponses:
			if eventName == "response.completed" || eventType == "response.completed" {
				terminalEvent = true
			}
		case appruntime.APIFormatChatCompletions:
			choices, ok := payload["choices"].([]any)
			if !ok || len(choices) == 0 {
				continue
			}
			validChoice := false
			for _, choiceValue := range choices {
				choice, ok := choiceValue.(map[string]any)
				if !ok {
					continue
				}
				if _, ok := choice["delta"].(map[string]any); ok {
					validChoice = true
				}
				if finishReason, ok := choice["finish_reason"].(string); ok && finishReason != "" {
					validChoice = true
					terminalEvent = true
				}
			}
			validEvent = validEvent || validChoice
		case appruntime.APIFormatAnthropic:
			if eventName == "message_stop" || eventType == "message_stop" {
				terminalEvent = validEvent
			} else {
				effectiveType := eventType
				if effectiveType == "" {
					effectiveType = eventName
				}
				switch effectiveType {
				case "message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta":
					validEvent = true
				}
			}
		}
	}
	return terminalEvent
}
