package upstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	appruntime "github.com/jesse/agent-inn/internal/runtime"
)

const (
	protocolProbeTestModel       = "probe-model"
	protocolProbeLargeEventBytes = 72 * 1024
)

func TestProbeProtocolValidatesSuccessfulTerminalEvent(t *testing.T) {
	tests := []struct {
		name    string
		format  appruntime.APIFormat
		path    string
		headers http.Header
		request map[string]any
		stream  string
	}{
		{
			name:    "responses",
			format:  appruntime.APIFormatResponses,
			path:    "/responses",
			headers: http.Header{"Authorization": []string{"Bearer sk-test"}},
			request: map[string]any{
				"model":             protocolProbeTestModel,
				"input":             "hi",
				"max_output_tokens": float64(16),
				"stream":            true,
			},
			stream: "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n",
		},
		{
			name:    "chat completions",
			format:  appruntime.APIFormatChatCompletions,
			path:    "/chat/completions",
			headers: http.Header{"Authorization": []string{"Bearer sk-test"}},
			request: map[string]any{
				"model":      protocolProbeTestModel,
				"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
				"max_tokens": float64(16),
				"stream":     true,
			},
			stream: "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n",
		},
		{
			name:   "anthropic",
			format: appruntime.APIFormatAnthropic,
			path:   "/messages",
			headers: http.Header{
				"Authorization":     []string{"Bearer sk-test"},
				"Anthropic-Version": []string{"2023-06-01"},
			},
			request: map[string]any{
				"model":      protocolProbeTestModel,
				"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
				"max_tokens": float64(16),
				"stream":     true,
			},
			stream: "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var capture struct {
				Method  string
				Path    string
				Headers http.Header
				Request map[string]any
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				capture.Method = request.Method
				capture.Path = request.URL.Path
				capture.Headers = make(http.Header, len(test.headers))
				for name := range test.headers {
					capture.Headers[name] = request.Header.Values(name)
				}
				if err := json.NewDecoder(request.Body).Decode(&capture.Request); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.stream)
			}))
			defer server.Close()

			compiled, err := Compile(appruntime.UpstreamRuntime{
				BaseURL:   server.URL,
				APIKey:    "sk-test",
				APIFormat: test.format,
			})
			if err != nil {
				t.Fatal(err)
			}
			result := ProbeProtocolWithClient(t.Context(), compiled, protocolProbeTestModel, server.Client())
			result.LatencyMS = 0
			got := struct {
				Capture struct {
					Method  string
					Path    string
					Headers http.Header
					Request map[string]any
				}
				Result ProbeResult
			}{Capture: capture, Result: result}
			want := struct {
				Capture struct {
					Method  string
					Path    string
					Headers http.Header
					Request map[string]any
				}
				Result ProbeResult
			}{
				Capture: struct {
					Method  string
					Path    string
					Headers http.Header
					Request map[string]any
				}{Method: http.MethodPost, Path: test.path, Headers: test.headers, Request: test.request},
				Result: ProbeResult{
					OK:            true,
					StatusCode:    http.StatusOK,
					Mode:          ProbeModeProtocol,
					Authoritative: true,
				},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected protocol probe:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

func TestProbeProtocolAcceptsLargeValidStream(t *testing.T) {
	stream := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"instructions\":\"" +
		strings.Repeat("x", protocolProbeLargeEventBytes) +
		"\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	defer server.Close()

	compiled, err := Compile(appruntime.UpstreamRuntime{BaseURL: server.URL, APIFormat: appruntime.APIFormatResponses})
	if err != nil {
		t.Fatal(err)
	}
	got := ProbeProtocolWithClient(t.Context(), compiled, protocolProbeTestModel, server.Client())
	got.LatencyMS = 0
	want := ProbeResult{OK: true, StatusCode: http.StatusOK, Mode: ProbeModeProtocol, Authoritative: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected large stream result:\n got %#v\nwant %#v", got, want)
	}
}

func TestProbeProtocolRejectsInvalidStreams(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		stream        string
		contentLength int
		timeout       bool
		want          ProbeResult
	}{
		{
			name:   "non-2xx",
			status: http.StatusUnauthorized,
			want: ProbeResult{
				StatusCode:    http.StatusUnauthorized,
				Error:         "auth_error",
				Mode:          ProbeModeProtocol,
				Authoritative: true,
			},
		},
		{
			name:   "response failed",
			stream: "event: response.failed\ndata: {\"type\":\"response.failed\"}\n\n",
			want:   ProbeResult{StatusCode: http.StatusOK, Error: "protocol_error", Mode: ProbeModeProtocol, Authoritative: true},
		},
		{
			name:   "SSE error",
			stream: "event: error\ndata: {\"type\":\"error\"}\n\n",
			want:   ProbeResult{StatusCode: http.StatusOK, Error: "protocol_error", Mode: ProbeModeProtocol, Authoritative: true},
		},
		{
			name:   "malformed JSON",
			stream: "data: {not-json}\n\n",
			want:   ProbeResult{StatusCode: http.StatusOK, Error: "protocol_error", Mode: ProbeModeProtocol, Authoritative: true},
		},
		{
			name:          "premature EOF",
			stream:        "data: {\"type\":\"response.completed\"}\n\n",
			contentLength: 100,
			want:          ProbeResult{StatusCode: http.StatusOK, Error: "protocol_error", Mode: ProbeModeProtocol, Authoritative: true},
		},
		{
			name:   "missing terminal event",
			stream: "data: {\"type\":\"response.output_text.delta\"}\n\n",
			want:   ProbeResult{StatusCode: http.StatusOK, Error: "protocol_error", Mode: ProbeModeProtocol, Authoritative: true},
		},
		{
			name:    "timeout",
			timeout: true,
			want:    ProbeResult{Error: "timeout", Mode: ProbeModeProtocol, Authoritative: true},
		},
		{
			name: "oversized response",
			stream: "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"instructions\":\"" +
				strings.Repeat("x", protocolProbeMaximumBytes) +
				"\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n",
			want: ProbeResult{StatusCode: http.StatusOK, Error: "protocol_error", Mode: ProbeModeProtocol, Authoritative: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if test.timeout {
					select {
					case <-request.Context().Done():
					case <-time.After(200 * time.Millisecond):
					}
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if test.contentLength > 0 {
					w.Header().Set("Content-Length", "100")
				}
				status := test.status
				if status == 0 {
					status = http.StatusOK
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, test.stream)
			}))
			defer server.Close()

			compiled, err := Compile(appruntime.UpstreamRuntime{BaseURL: server.URL, APIFormat: appruntime.APIFormatResponses})
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if test.timeout {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
			}
			got := ProbeProtocolWithClient(ctx, compiled, protocolProbeTestModel, server.Client())
			got.LatencyMS = 0
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("unexpected invalid stream result:\n got %#v\nwant %#v", got, test.want)
			}
		})
	}
}

func TestProbeProtocolPreRequestFailureIsNonAuthoritative(t *testing.T) {
	compiled, err := Compile(appruntime.UpstreamRuntime{BaseURL: "https://example.com/v1", APIFormat: appruntime.APIFormat("invalid")})
	if err != nil {
		t.Fatal(err)
	}
	got := ProbeProtocolWithClient(t.Context(), compiled, protocolProbeTestModel, http.DefaultClient)
	want := ProbeResult{Error: "unsupported_protocol", Mode: ProbeModeProtocol}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pre-request failure acquired authority:\n got %#v\nwant %#v", got, want)
	}
}

func TestProbeProtocolRejectsStructurallyInvalidStreams(t *testing.T) {
	tests := []struct {
		name   string
		format appruntime.APIFormat
		stream string
	}{
		{name: "null chat choice", format: appruntime.APIFormatChatCompletions, stream: "data: {\"choices\":[null]}\n\ndata: [DONE]\n\n"},
		{name: "anthropic ping", format: appruntime.APIFormatAnthropic, stream: "event: ping\ndata: {\"type\":\"ping\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.stream)
			}))
			defer server.Close()
			compiled, err := Compile(appruntime.UpstreamRuntime{BaseURL: server.URL, APIFormat: test.format})
			if err != nil {
				t.Fatal(err)
			}
			got := ProbeProtocolWithClient(t.Context(), compiled, protocolProbeTestModel, server.Client())
			got.LatencyMS = 0
			want := ProbeResult{StatusCode: http.StatusOK, Error: "protocol_error", Mode: ProbeModeProtocol, Authoritative: true}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("invalid stream was accepted:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

func TestProbeProtocolPreservesEndpointBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		format   appruntime.APIFormat
		basePath string
		wantPath string
		stream   string
	}{
		{
			name:     "responses API root",
			format:   appruntime.APIFormatResponses,
			basePath: "/v1",
			wantPath: "/v1/responses",
			stream:   "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n",
		},
		{
			name:     "anthropic endpoint",
			format:   appruntime.APIFormatAnthropic,
			basePath: "/v1/messages",
			wantPath: "/v1/messages",
			stream:   "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		},
		{
			name:     "anthropic endpoint trailing slash",
			format:   appruntime.APIFormatAnthropic,
			basePath: "/v1/messages/",
			wantPath: "/v1/messages/",
			stream:   "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		},
		{
			name:     "chat completions endpoint",
			format:   appruntime.APIFormatChatCompletions,
			basePath: "/v1/chat/completions",
			wantPath: "/v1/chat/completions",
			stream:   "data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n",
		},
		{
			name:     "proxied anthropic endpoint",
			format:   appruntime.APIFormatAnthropic,
			basePath: "/proxy/anthropic/messages",
			wantPath: "/proxy/anthropic/messages",
			stream:   "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotURL string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				gotURL = request.URL.String()
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.stream)
			}))
			defer server.Close()

			compiled, err := Compile(appruntime.UpstreamRuntime{BaseURL: server.URL + test.basePath, APIFormat: test.format})
			if err != nil {
				t.Fatal(err)
			}
			result := ProbeProtocolWithClient(t.Context(), compiled, protocolProbeTestModel, server.Client())
			result.LatencyMS = 0
			got := struct {
				URL    string
				Result ProbeResult
			}{URL: gotURL, Result: result}
			want := struct {
				URL    string
				Result ProbeResult
			}{
				URL: test.wantPath,
				Result: ProbeResult{
					OK:            true,
					StatusCode:    http.StatusOK,
					Mode:          ProbeModeProtocol,
					Authoritative: true,
				},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected endpoint probe:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}
