package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
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
