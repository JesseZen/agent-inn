package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
)

func newTestCompiled(t *testing.T, baseURL, apiKey string) Compiled {
	t.Helper()
	runtime, err := ResolveRuntime("test", config.UpstreamProfile{
		BaseURL: baseURL,
		APIKey:  apiKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(runtime)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestProbeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	compiled := newTestCompiled(t, server.URL, "sk-test")
	got := probeWithClient(context.Background(), compiled, &http.Client{Timeout: 2 * time.Second})

	got.LatencyMS = 0
	want := ProbeResult{OK: true, StatusCode: http.StatusOK}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestProbeUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	compiled := newTestCompiled(t, server.URL, "")
	got := probeWithClient(context.Background(), compiled, &http.Client{Timeout: 2 * time.Second})

	got.LatencyMS = 0
	want := ProbeResult{OK: false, StatusCode: http.StatusUnauthorized, Error: "auth_error"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestProbeUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	compiled := newTestCompiled(t, server.URL, "")
	got := probeWithClient(context.Background(), compiled, &http.Client{Timeout: 2 * time.Second})

	got.LatencyMS = 0
	want := ProbeResult{OK: false, StatusCode: http.StatusBadGateway, Error: "upstream_error"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestProbeTimeout(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(10 * time.Second):
		case <-done:
		}
	}))
	defer server.Close()
	defer close(done)

	compiled := newTestCompiled(t, server.URL, "")
	got := probeWithClient(context.Background(), compiled, &http.Client{Timeout: 50 * time.Millisecond})

	got.LatencyMS = 0
	want := ProbeResult{OK: false, Error: "timeout"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestProbeDegradedSlowLatency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	compiled := newTestCompiled(t, server.URL, "sk-test")
	got := probeWithClient(context.Background(), compiled, &http.Client{Timeout: 3 * time.Second})

	got.LatencyMS = 0
	want := ProbeResult{OK: false, Degraded: true, StatusCode: http.StatusOK, Error: "slow"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestProbeDegradedRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	compiled := newTestCompiled(t, server.URL, "")
	got := probeWithClient(context.Background(), compiled, &http.Client{Timeout: 2 * time.Second})

	got.LatencyMS = 0
	want := ProbeResult{OK: false, Degraded: true, StatusCode: http.StatusTooManyRequests, Error: "rate_limited"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestProbeDegradedClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	compiled := newTestCompiled(t, server.URL, "")
	got := probeWithClient(context.Background(), compiled, &http.Client{Timeout: 2 * time.Second})

	got.LatencyMS = 0
	want := ProbeResult{OK: false, Degraded: true, StatusCode: http.StatusNotFound, Error: "client_error"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestProbeProtocolSendsMinimalStreamingRequest(t *testing.T) {
	tests := []struct {
		name       string
		format     appruntime.APIFormat
		wantPath   string
		wantHeader http.Header
		wantBody   map[string]any
	}{
		{
			name:     "responses",
			format:   appruntime.APIFormatResponses,
			wantPath: "/responses",
			wantHeader: http.Header{
				"Authorization": []string{"Bearer sk-test"},
			},
			wantBody: map[string]any{
				"model":             "probe-model",
				"input":             "ping",
				"max_output_tokens": float64(1),
				"stream":            true,
			},
		},
		{
			name:     "chat completions",
			format:   appruntime.APIFormatChatCompletions,
			wantPath: "/chat/completions",
			wantHeader: http.Header{
				"Authorization": []string{"Bearer sk-test"},
			},
			wantBody: map[string]any{
				"model":      "probe-model",
				"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
				"max_tokens": float64(1),
				"stream":     true,
			},
		},
		{
			name:     "anthropic",
			format:   appruntime.APIFormatAnthropic,
			wantPath: "/messages",
			wantHeader: http.Header{
				"Authorization":     []string{"Bearer sk-test"},
				"Anthropic-Version": []string{"2023-06-01"},
			},
			wantBody: map[string]any{
				"model":      "probe-model",
				"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
				"max_tokens": float64(1),
				"stream":     true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotPath string
			var gotHeader http.Header
			var gotBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotHeader = http.Header{}
				for header := range test.wantHeader {
					gotHeader[header] = r.Header.Values(header)
				}
				if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("data: {}\n\n"))
			}))
			defer server.Close()

			compiled := newTestCompiled(t, server.URL, "sk-test")
			gotResult := probeProtocolWithClient(context.Background(), compiled, test.format, "probe-model", &http.Client{Timeout: 2 * time.Second})
			gotResult.LatencyMS = 0
			got := struct {
				Path   string
				Header http.Header
				Body   map[string]any
				Result ProbeResult
			}{Path: gotPath, Header: gotHeader, Body: gotBody, Result: gotResult}
			want := struct {
				Path   string
				Header http.Header
				Body   map[string]any
				Result ProbeResult
			}{
				Path:   test.wantPath,
				Header: test.wantHeader,
				Body:   test.wantBody,
				Result: ProbeResult{OK: true, StatusCode: http.StatusOK},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected protocol probe:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}
