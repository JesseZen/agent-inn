package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
)

func TestManagerProtocolReadinessExpires(t *testing.T) {
	now := time.Date(2026, time.July, 11, 1, 2, 3, 0, time.UTC)
	m := newReadinessTestManager(t)
	defer m.Close()
	m.clock = func() time.Time { return now }
	spec := readinessTestProbeSpec("coding-ha", "primary", "", 1, "model-a")
	installReadinessTestSpec(m, spec)

	got := []PoolReadiness{m.poolReadiness("coding-ha", "primary")}
	m.recordScheduledProbeResult(spec, readinessTestSuccess(12))
	got = append(got, m.poolReadiness("coding-ha", "primary"))
	now = now.Add(readinessFreshness)
	got = append(got, m.poolReadiness("coding-ha", "primary"))
	checkedAt := now.Add(-readinessFreshness)
	never := readinessTestExpected("coding-ha", ReadinessStateUnknown)
	fresh := readinessTestExpected("coding-ha", ReadinessStateReady)
	fresh.Eligible, fresh.CheckedAt, fresh.OK, fresh.StatusCode, fresh.LatencyMS = true, &checkedAt, true, http.StatusOK, 12
	expired := fresh
	expired.Readiness, expired.Eligible, expired.Stale = ReadinessStateUnknown, false, true
	want := []PoolReadiness{never, fresh, expired}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected readiness lifecycle:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerExpiredReadinessPublishesUnknown(t *testing.T) {
	now := time.Date(2026, time.July, 11, 1, 2, 3, 0, time.UTC)
	m := newReadinessTestManager(t)
	defer m.Close()
	m.clock = func() time.Time { return now }
	spec := readinessTestProbeSpec("coding-ha", "primary", "", 1, "model-a")
	installReadinessTestSpec(m, spec)
	m.recordScheduledProbeResult(spec, readinessTestSuccess(12))

	checkedAt := now
	now = now.Add(readinessFreshness)
	m.expirePoolReadiness("coding-ha", "primary", spec.Generation, checkedAt)
	events := m.events.Replay(0)
	got := events[len(events)-1]
	want := Event{ID: got.ID, Type: EventUpstreamProbed, At: got.At, Payload: map[string]any{
		"upstream": "primary", "pool": "coding-ha", "mode": upstream.ProbeModeProtocol,
		"authoritative": true, "readiness": ReadinessStateUnknown, "eligible": false,
		"checked_at": checkedAt.Format(time.RFC3339), "ok": true,
		"status_code": http.StatusOK, "latency_ms": int64(12), "stale": true,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected readiness expiry event:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerProbeObservationIdentity(t *testing.T) {
	now := time.Date(2026, time.July, 11, 1, 2, 3, 0, time.UTC)
	m := newReadinessTestManager(t)
	defer m.Close()
	m.clock = func() time.Time { return now }
	poolA := readinessTestProbeSpec("pool-a", "primary", "http://proxy-a.example", 1, "model-a")
	poolB := readinessTestProbeSpec("pool-b", "primary", "http://proxy-b.example", 1, "model-a")
	installReadinessTestSpec(m, poolA)
	installReadinessTestSpec(m, poolB)
	m.recordScheduledProbeResult(poolA, readinessTestSuccess(0))
	m.recordScheduledProbeResult(poolB, upstream.ProbeResult{
		StatusCode: http.StatusUnauthorized, Error: "auth_error",
		Mode: upstream.ProbeModeProtocol, Authoritative: true,
	})

	checkedAt := now
	ready := readinessTestExpected("pool-a", ReadinessStateReady)
	ready.Eligible, ready.CheckedAt, ready.OK, ready.StatusCode = true, &checkedAt, true, http.StatusOK
	notReady := readinessTestExpected("pool-b", ReadinessStateNotReady)
	notReady.CheckedAt, notReady.StatusCode, notReady.Error = &checkedAt, http.StatusUnauthorized, "auth_error"
	got := []PoolReadiness{m.poolReadiness("pool-a", "primary"), m.poolReadiness("pool-b", "primary")}
	want := []PoolReadiness{ready, notReady}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected pool-scoped observations:\n got %#v\nwant %#v", got, want)
	}
	gotEvents := poolRoutingEvents(m, EventUpstreamProbed)
	wantEvents := []map[string]any{
		{"upstream": "primary", "pool": "pool-a", "mode": upstream.ProbeModeProtocol, "authoritative": true, "readiness": ReadinessStateReady, "eligible": true, "checked_at": checkedAt.Format(time.RFC3339), "ok": true, "status_code": http.StatusOK, "latency_ms": int64(0)},
		{"upstream": "primary", "pool": "pool-b", "mode": upstream.ProbeModeProtocol, "authoritative": true, "readiness": ReadinessStateNotReady, "eligible": false, "checked_at": checkedAt.Format(time.RFC3339), "ok": false, "status_code": http.StatusUnauthorized, "latency_ms": int64(0), "error": "auth_error"},
	}
	if !reflect.DeepEqual(gotEvents, wantEvents) {
		t.Fatalf("unexpected pool-scoped events:\n got %#v\nwant %#v", gotEvents, wantEvents)
	}
}

func TestManagerProbeObservationLifecycleJSON(t *testing.T) {
	checkedAt := time.Date(2026, time.July, 11, 1, 2, 3, 0, time.UTC)
	unknown := readinessTestExpected("coding-ha", ReadinessStateUnknown)
	ready := readinessTestExpected("coding-ha", ReadinessStateReady)
	ready.Eligible, ready.CheckedAt, ready.OK, ready.StatusCode, ready.LatencyMS = true, &checkedAt, true, http.StatusOK, 12
	stale := ready
	stale.Readiness, stale.Eligible, stale.Stale = ReadinessStateUnknown, false, true
	values := []PoolReadiness{unknown, ready, stale}
	got := make([]string, len(values))
	for index, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		got[index] = string(data)
	}
	want := []string{
		`{"upstream":"primary","pool":"coding-ha","mode":"protocol","authoritative":true,"readiness":"unknown","eligible":false,"ok":false,"status_code":0,"latency_ms":0}`,
		`{"upstream":"primary","pool":"coding-ha","mode":"protocol","authoritative":true,"readiness":"ready","eligible":true,"checked_at":"2026-07-11T01:02:03Z","ok":true,"status_code":200,"latency_ms":12}`,
		`{"upstream":"primary","pool":"coding-ha","mode":"protocol","authoritative":true,"readiness":"unknown","eligible":false,"checked_at":"2026-07-11T01:02:03Z","ok":true,"status_code":200,"latency_ms":12,"stale":true}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected readiness JSON lifecycle:\n got %#v\nwant %#v", got, want)
	}

	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	m.clock = func() time.Time { return checkedAt }
	spec := readinessTestProbeSpec("coding-ha", "primary", "", 1, "model-a")
	installReadinessTestSpec(m, spec)
	apiValues := make([]string, 0, 3)
	for index := 0; index < 3; index++ {
		if index == 1 {
			m.recordScheduledProbeResult(spec, readinessTestSuccess(12))
		}
		if index == 2 {
			m.clock = func() time.Time { return checkedAt.Add(readinessFreshness) }
		}
		response := httptest.NewRecorder()
		m.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://manager.local/api/upstreams", nil))
		var body struct {
			Upstreams map[string]struct {
				PoolReadiness []PoolReadiness `json:"pool_readiness"`
			} `json:"upstreams"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(body.Upstreams["primary"].PoolReadiness)
		if err != nil {
			t.Fatal(err)
		}
		apiValues = append(apiValues, string(data))
	}
	wantAPI := []string{"[" + want[0] + "]", "[" + want[1] + "]", "[" + want[2] + "]"}
	if !reflect.DeepEqual(apiValues, wantAPI) {
		t.Fatalf("unexpected readiness API lifecycle:\n got %#v\nwant %#v", apiValues, wantAPI)
	}
}

func TestManagerPreRequestProbeFailurePreservesReadiness(t *testing.T) {
	m := newReadinessTestManager(t)
	defer m.Close()
	spec := readinessTestProbeSpec("coding-ha", "primary", "", 1, "model-a")
	installReadinessTestSpec(m, spec)
	m.recordScheduledProbeResult(spec, readinessTestSuccess(12))
	want := m.poolReadiness("coding-ha", "primary")
	m.recordScheduledProbeResult(spec, upstream.ProbeResult{Mode: upstream.ProbeModeProtocol, Error: "connection_error"})
	if got := m.poolReadiness("coding-ha", "primary"); !reflect.DeepEqual(got, want) {
		t.Fatalf("pre-request failure replaced readiness:\n got %#v\nwant %#v", got, want)
	}
}

func newReadinessTestManager(t *testing.T) *Manager {
	t.Helper()
	return New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: t.TempDir()},
		Workers: map[string]config.WorkerConfig{
			"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"primary": {BaseURL: "https://primary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-a"}},
			"backup":  {BaseURL: "https://backup.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-b"}},
		},
		UpstreamPools: map[string]config.UpstreamPool{
			"coding-ha": {Upstreams: []string{"primary", "backup"}},
			"pool-a":    {Upstreams: []string{"primary"}}, "pool-b": {Upstreams: []string{"primary"}},
		},
	}})
}

func readinessTestProbeSpec(pool string, upstreamName string, proxyURL string, generation int, model string) probeSpec {
	return probeSpec{
		Key:      probeExecutionKey{Upstream: upstreamName, ProxyURL: proxyURL},
		Upstream: upstreamName, ProxyURL: proxyURL, Model: model, Generation: generation,
		Fingerprint: model + "@" + proxyURL, Pools: []string{pool},
	}
}

func installReadinessTestSpec(m *Manager, spec probeSpec) {
	m.failoverMu.Lock()
	m.desiredProbes[spec.Key], m.probeGenerations[spec.Key] = spec, spec.Generation
	m.failoverMu.Unlock()
}

func readinessTestSuccess(latencyMS int64) upstream.ProbeResult {
	return upstream.ProbeResult{
		OK: true, StatusCode: http.StatusOK, LatencyMS: latencyMS,
		Mode: upstream.ProbeModeProtocol, Authoritative: true,
	}
}

func readinessTestExpected(pool string, state ReadinessState) PoolReadiness {
	return PoolReadiness{
		Upstream: "primary", Pool: pool, Mode: upstream.ProbeModeProtocol,
		Authoritative: true, Readiness: state,
	}
}
