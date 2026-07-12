package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
)

func TestManagerReachabilityProbeIsNonAuthoritative(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer server.Close()
	m := New(Config{Config: config.Config{Upstreams: map[string]config.UpstreamProfile{"direct": {BaseURL: server.URL}}}})
	defer m.Close()
	response := httptest.NewRecorder()
	m.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://manager.local/api/upstreams/direct/test", nil))
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	body["latency_ms"] = float64(0)
	events := poolRoutingEvents(m, EventUpstreamProbed)
	events[0]["latency_ms"] = int64(0)
	got := struct {
		Code   int
		Body   map[string]any
		Events []map[string]any
	}{response.Code, body, events}
	wantObservation := map[string]any{"upstream": "direct", "mode": "reachability", "authoritative": false, "readiness": "unknown", "ok": false, "degraded": true, "status_code": float64(http.StatusNotFound), "latency_ms": float64(0), "error": "client_error"}
	wantEvent := map[string]any{"upstream": "direct", "mode": upstream.ProbeModeReachability, "authoritative": false, "readiness": ReadinessStateUnknown, "ok": false, "degraded": true, "status_code": http.StatusNotFound, "latency_ms": int64(0), "error": "client_error"}
	want := struct {
		Code   int
		Body   map[string]any
		Events []map[string]any
	}{http.StatusOK, wantObservation, []map[string]any{wantEvent}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected reachability diagnostic:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerManualProbeHasNoCircuitAuthority(t *testing.T) {
	m, pool, release := newBlockedManualProbeManager(t)
	defer m.Close()
	defer close(release)
	pool.CircuitBreaker.RecoveryWaitSeconds = 0
	m.mu.Lock()
	m.config.UpstreamPools["coding-ha"] = pool
	m.mu.Unlock()
	key := poolCircuitKey("coding-ha", "primary")
	m.circuits.RecordFailure(key, pool.CircuitBreaker)
	wantCircuit := m.circuits.Status(key, pool.CircuitBreaker)
	response := httptest.NewRecorder()
	m.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://manager.local/api/upstreams/primary/test", nil))
	got := struct {
		Code      int
		Circuit   CircuitStatus
		Readiness PoolReadiness
	}{response.Code, m.circuits.Status(key, pool.CircuitBreaker), m.poolReadiness("coding-ha", "primary")}
	want := struct {
		Code      int
		Circuit   CircuitStatus
		Readiness PoolReadiness
	}{http.StatusOK, wantCircuit, readinessTestExpected("coding-ha", ReadinessStateUnknown)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manual probe acquired authority or blocked:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerManualProtocolProbeIsDiagnostic(t *testing.T) {
	m, _, release := newBlockedManualProbeManager(t)
	defer m.Close()
	defer close(release)
	response := httptest.NewRecorder()
	m.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://manager.local/api/upstreams/test", nil))
	var body struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	body.Results[0]["latency_ms"] = float64(0)
	events := poolRoutingEvents(m, EventUpstreamProbed)
	events[0]["latency_ms"] = int64(0)
	got := struct {
		Code    int
		Results []map[string]any
		Events  []map[string]any
	}{response.Code, body.Results, events}
	wantResult := map[string]any{"upstream": "primary", "mode": "protocol", "authoritative": false, "readiness": "unknown", "ok": true, "status_code": float64(http.StatusOK), "latency_ms": float64(0)}
	wantEvent := map[string]any{"upstream": "primary", "mode": upstream.ProbeModeProtocol, "authoritative": false, "readiness": ReadinessStateUnknown, "ok": true, "status_code": http.StatusOK, "latency_ms": int64(0)}
	want := struct {
		Code    int
		Results []map[string]any
		Events  []map[string]any
	}{http.StatusOK, []map[string]any{wantResult}, []map[string]any{wantEvent}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected manual protocol diagnostic:\n got %#v\nwant %#v", got, want)
	}
}

func newBlockedManualProbeManager(t *testing.T) (*Manager, config.UpstreamPool, chan struct{}) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"))
	}))
	t.Cleanup(server.Close)
	pool := config.UpstreamPool{Upstreams: []string{"primary"}, CircuitBreaker: config.CircuitBreakerConfig{FailureThreshold: 1, RecoverySuccessThreshold: 1, RecoveryWaitSeconds: 1}}
	m := New(Config{Config: config.Config{Settings: config.Settings{StateDir: t.TempDir()}, Workers: map[string]config.WorkerConfig{"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"}}, Upstreams: map[string]config.UpstreamProfile{"primary": {BaseURL: server.URL, ProtocolProbe: config.ProtocolProbeConfig{Model: "probe-model"}}}, UpstreamPools: map[string]config.UpstreamPool{"coding-ha": pool}}})
	now := time.Date(2026, time.July, 11, 5, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	started := make(chan struct{})
	release := make(chan struct{})
	m.probeRunner = func(_ context.Context, _ probeSpec) upstream.ProbeResult {
		close(started)
		<-release
		return readinessTestSuccess(1)
	}
	m.probeAllUpstreams(m.probeContext)
	<-started
	now = now.Add(time.Second)
	return m, pool, release
}

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
	want := upstreamProbeResponse{Upstream: "groq", OK: true, StatusCode: http.StatusOK, Mode: upstream.ProbeModeReachability, Readiness: ReadinessStateUnknown}
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
		{Upstream: "alpha", OK: true, StatusCode: http.StatusOK, Mode: upstream.ProbeModeReachability, Readiness: ReadinessStateUnknown},
		{Upstream: "beta", OK: false, StatusCode: http.StatusUnauthorized, Error: "auth_error", Mode: upstream.ProbeModeReachability, Readiness: ReadinessStateUnknown},
	}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("got %+v, want %+v", results, want)
	}
}

func TestManagerProtocolProbeReportsSuccess(t *testing.T) {
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
		Configured: "backup",
		Applied:    "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected protocol recovery:\n got %#v\nwant %#v", got, want)
	}
}
