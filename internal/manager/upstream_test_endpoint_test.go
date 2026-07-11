package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
)

func TestManagerAPIUpstreamTestProbesReachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	m := New(Config{
		Config: config.Config{
			Upstreams: map[string]config.UpstreamProfile{
				"groq": {BaseURL: server.URL, APIKey: "sk-test"},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/upstreams/groq/test", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}

	var got upstreamProbeResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	got.LatencyMS = 0
	want := upstreamProbeResponse{Upstream: "groq", OK: true, StatusCode: http.StatusOK}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	events := m.events.Replay(0)
	found := false
	for _, e := range events {
		if e.Type == EventUpstreamProbed {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected upstream.probed event, got events: %+v", events)
	}
}

func TestManagerAPIUpstreamTestUnknownReturns404(t *testing.T) {
	m := New(Config{Config: config.Config{}})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/upstreams/unknown/test", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "upstream not found") {
		t.Fatalf("expected not found error, got: %s", res.Body.String())
	}
}

func TestManagerAPIUpstreamTestAllProbesAllUpstreams(t *testing.T) {
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer serverA.Close()
	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer serverB.Close()

	m := New(Config{
		Config: config.Config{
			Upstreams: map[string]config.UpstreamProfile{
				"alpha": {BaseURL: serverA.URL},
				"beta":  {BaseURL: serverB.URL},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/upstreams/test", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Results []upstreamProbeResponse `json:"results"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	results := body.Results
	for i := range results {
		results[i].LatencyMS = 0
	}
	want := []upstreamProbeResponse{
		{Upstream: "alpha", OK: true, StatusCode: http.StatusOK},
		{Upstream: "beta", OK: false, StatusCode: http.StatusUnauthorized, Error: "auth_error"},
	}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("got %+v, want %+v", results, want)
	}
}

func TestManagerProtocolProbeRestoresPreferredPoolUpstream(t *testing.T) {
	var gotMethod string
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer server.Close()

	now := time.Date(2026, time.July, 11, 0, 0, 0, 0, time.UTC)
	client := &recordingWorkerClient{}
	pool := config.UpstreamPool{
		Upstreams: []string{"primary", "backup"},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold:         1,
			RecoverySuccessThreshold: 1,
			RecoveryWaitSeconds:      30,
		},
	}
	m := New(Config{
		Config: config.Config{
			Settings: config.Settings{StateDir: t.TempDir()},
			Plugins:  testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "backup", UpstreamPool: "coding-ha"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"primary": {
					BaseURL:       server.URL,
					ProtocolProbe: config.ProtocolProbeConfig{Model: "probe-model"},
				},
				"backup": {BaseURL: "https://backup.example/v1"},
			},
			UpstreamPools: map[string]config.UpstreamPool{"coding-ha": pool},
		},
		WorkerClient: client,
	})
	defer m.Close()
	m.clock = func() time.Time { return now }
	m.statuses["app"] = WorkerStateRunning
	m.circuits.RecordFailure(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker)
	now = now.Add(30 * time.Second)

	result := m.probeUpstreamByName(t.Context(), "primary")
	got := struct {
		Method     string
		Path       string
		ProbeOK    bool
		Configured string
		Applied    string
	}{
		Method:     gotMethod,
		Path:       gotPath,
		ProbeOK:    result.OK,
		Configured: workerUpstreamID(m.config.Workers["app"]),
		Applied:    string(client.appliedRuntimes[6767].Upstream.ID),
	}
	want := struct {
		Method     string
		Path       string
		ProbeOK    bool
		Configured string
		Applied    string
	}{
		Method:     http.MethodPost,
		Path:       "/responses",
		ProbeOK:    true,
		Configured: "primary",
		Applied:    "primary",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected protocol recovery:\n got %#v\nwant %#v", got, want)
	}
}
