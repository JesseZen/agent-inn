package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
)

func TestManagerActiveProtocolFailureSwitchesWithoutOpeningCircuit(t *testing.T) {
	m, pool := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	authorityObserve(t, m, "backup", readinessTestSuccess(1))
	authorityObserve(t, m, "primary", readinessTestFailure())
	got := struct {
		Active    string
		Circuit   CircuitStatus
		Readiness ReadinessState
	}{workerUpstreamID(m.config.Workers["app"]), m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker), m.poolReadiness("coding-ha", "primary").Readiness}
	want := struct {
		Active    string
		Circuit   CircuitStatus
		Readiness ReadinessState
	}{"backup", CircuitStatus{State: CircuitStateClosed}, ReadinessStateNotReady}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected active protocol failure result:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerActiveProtocolFailurePublishesPoolExhausted(t *testing.T) {
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	authorityObserve(t, m, "primary", readinessTestFailure())
	got := poolRoutingEvents(m, EventUpstreamPoolExhausted)
	want := []map[string]any{{"pool": "coding-ha", "upstream": "primary", "reason": "no_eligible_fallback"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected exhaustion events:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerNoFreshFallbackWaitsForProbeResult(t *testing.T) {
	now := time.Date(2026, time.July, 13, 4, 5, 6, 0, time.UTC)
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		"cli": {Port: 6768, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	m.cancelProbes()
	defer m.Close()
	m.clock = func() time.Time { return now }
	m.mu.Lock()
	m.statuses["app"], m.generations["app"] = WorkerStateRunning, 1
	m.statuses["cli"], m.generations["cli"] = WorkerStateRunning, 1
	m.mu.Unlock()
	authorityObserve(t, m, "primary", readinessTestSuccess(1))
	authorityObserve(t, m, "backup", readinessTestSuccess(1))
	checkedAt := now
	now = now.Add(config.DefaultPoolProbeStableIntervalSeconds*time.Second + time.Minute + time.Second)

	if err := m.recordWorkerUpstreamFailure("app", "primary"); err != nil {
		t.Fatal(err)
	}
	gotStale := struct {
		Workers    []string
		Readiness  PoolReadiness
		Exhaustion []map[string]any
		Switches   []map[string]any
	}{[]string{workerUpstreamID(m.config.Workers["app"]), workerUpstreamID(m.config.Workers["cli"])}, m.poolReadiness("coding-ha", "backup"), poolRoutingEvents(m, EventUpstreamPoolExhausted), poolRoutingEvents(m, EventUpstreamPoolSwitched)}
	wantStale := struct {
		Workers    []string
		Readiness  PoolReadiness
		Exhaustion []map[string]any
		Switches   []map[string]any
	}{[]string{"primary", "primary"}, PoolReadiness{
		Upstream: "backup", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol,
		Authoritative: true, Readiness: ReadinessStateUnknown, CheckedAt: &checkedAt,
		OK: true, StatusCode: http.StatusOK, LatencyMS: 1, Stale: true,
	}, []map[string]any{{"pool": "coding-ha", "upstream": "primary", "reason": "no_eligible_fallback"}}, nil}
	if !reflect.DeepEqual(gotStale, wantStale) {
		t.Fatalf("stale fallback changed routing:\n got %#v\nwant %#v", gotStale, wantStale)
	}

	authorityObserve(t, m, "backup", readinessTestSuccess(2))
	gotFresh := struct {
		Workers  []string
		Switches []map[string]any
	}{[]string{workerUpstreamID(m.config.Workers["app"]), workerUpstreamID(m.config.Workers["cli"])}, poolRoutingEvents(m, EventUpstreamPoolSwitched)}
	wantFresh := struct {
		Workers  []string
		Switches []map[string]any
	}{[]string{"backup", "backup"}, []map[string]any{{"pool": "coding-ha", "previous_upstream": "primary", "upstream": "backup"}}}
	if !reflect.DeepEqual(gotFresh, wantFresh) {
		t.Fatalf("fresh fallback did not switch pool:\n got %#v\nwant %#v", gotFresh, wantFresh)
	}
}

func TestManagerPoolReadinessEligibilityFollowsCircuit(t *testing.T) {
	m, pool := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	authorityObserve(t, m, "backup", readinessTestSuccess(1))
	ready := m.poolReadiness("coding-ha", "backup")
	m.circuits.RecordFailure(poolCircuitKey("coding-ha", "backup"), pool.CircuitBreaker)
	open := m.poolReadiness("coding-ha", "backup")
	if got, want := []bool{ready.Eligible, open.Eligible}, []bool{true, false}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected eligibility: got %#v want %#v", got, want)
	}
}

func TestManagerActiveReadinessExpiryDoesNotSwitch(t *testing.T) {
	now := time.Date(2026, time.July, 11, 4, 0, 0, 0, time.UTC)
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	m.clock = func() time.Time { return now }
	authorityObserve(t, m, "backup", readinessTestSuccess(1))
	authorityObserve(t, m, "primary", readinessTestSuccess(1))
	observation := m.readiness[poolCircuitKey("coding-ha", "primary")]
	switchesBefore := len(poolRoutingEvents(m, EventUpstreamPoolSwitched))
	now = observation.ExpiresAt
	m.expirePoolReadiness("coding-ha", "primary", observation.Generation, observation.CheckedAt)
	got := struct {
		Active     string
		Readiness  ReadinessState
		Switches   int
		Exhaustion int
	}{workerUpstreamID(m.config.Workers["app"]), m.poolReadiness("coding-ha", "primary").Readiness, len(poolRoutingEvents(m, EventUpstreamPoolSwitched)) - switchesBefore, len(poolRoutingEvents(m, EventUpstreamPoolExhausted))}
	want := struct {
		Active     string
		Readiness  ReadinessState
		Switches   int
		Exhaustion int
	}{"primary", ReadinessStateUnknown, 0, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expiry changed routing:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPooledWorkerPatchRequiresEligibleTarget(t *testing.T) {
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	response := patchPoolRouting(t, m, "/api/workers/app", `{"upstream_id":"backup"}`)
	if got, want := struct {
		Code int
		Body string
	}{response.Code, response.Body.String()}, struct {
		Code int
		Body string
	}{http.StatusConflict, "{\"error\":\"target upstream is not eligible\"}\n"}; got != want {
		t.Fatalf("unexpected ineligible response: got %#v want %#v", got, want)
	}
}

func TestManagerPooledWorkerPatchForceSwitchesWholePool(t *testing.T) {
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		"cli": {Port: 6768, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	response := patchPoolRouting(t, m, "/api/workers/app", `{"upstream_id":"backup","force":true}`)
	var summary WorkerSummary
	if err := json.Unmarshal(response.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	got := struct {
		Code    int
		Summary WorkerSummary
		Workers []string
		Events  []map[string]any
	}{response.Code, summary, []string{workerUpstreamID(m.config.Workers["app"]), workerUpstreamID(m.config.Workers["cli"])}, poolRoutingEvents(m, EventUpstreamPoolSwitched)}
	want := struct {
		Code    int
		Summary WorkerSummary
		Workers []string
		Events  []map[string]any
	}{http.StatusOK, WorkerSummary{
		ID: "app", Name: "app", Port: 6767, Role: "cli", Launcher: "codex",
		UpstreamID: "backup", UpstreamPool: "coding-ha",
		Upstream: upstream.RedactedUpstream{ID: "backup", Name: "backup", BaseURL: "https://backup.example/v1"},
		Protocol: appruntime.ProtocolResponses,
		ModuleSupport: map[string]appruntime.ModuleProtocolSupport{
			"tool_filter":    {Protocols: []appruntime.ProtocolKind{appruntime.ProtocolResponses}, Capabilities: []appruntime.ProtocolCapability{appruntime.ProtocolCapabilityToolCalls}},
			"debug_sse":      {Protocols: []appruntime.ProtocolKind{appruntime.ProtocolResponses}, Capabilities: []appruntime.ProtocolCapability{appruntime.ProtocolCapabilityStreamEvents}},
			"api_translate":  {Protocols: []appruntime.ProtocolKind{appruntime.ProtocolResponses, appruntime.ProtocolChatCompletions}, Capabilities: []appruntime.ProtocolCapability{appruntime.ProtocolCapabilityInputText, appruntime.ProtocolCapabilityToolCalls, appruntime.ProtocolCapabilityStreamEvents}},
			"config_patch":   {Protocols: []appruntime.ProtocolKind{appruntime.ProtocolResponses, appruntime.ProtocolChatCompletions}},
			"model_override": {Protocols: []appruntime.ProtocolKind{appruntime.ProtocolResponses, appruntime.ProtocolChatCompletions, appruntime.ProtocolAnthropic}, Capabilities: []appruntime.ProtocolCapability{appruntime.ProtocolCapabilityInputText}},
			"request_log":    {},
		},
		Status: "configured", SnapshotGeneration: 1, LogLevel: "simple",
	}, []string{"backup", "backup"}, []map[string]any{{"pool": "coding-ha", "previous_upstream": "primary", "upstream": "backup", "forced": true}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected forced switch:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPooledWorkerFirstAttachEstablishesPool(t *testing.T) {
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "backup", ProxyURL: "http://proxy.example"},
	})
	defer m.Close()
	response := patchPoolRouting(t, m, "/api/workers/app", `{"upstream_pool":"coding-ha"}`)
	got := struct {
		Code   int
		Worker config.WorkerConfig
	}{response.Code, m.config.Workers["app"]}
	wantWorker := config.WorkerConfig{Name: "app", Role: "cli", Launcher: "codex", Port: 6767, Upstream: "backup", UpstreamID: "backup", UpstreamPool: "coding-ha", ProxyURL: "http://proxy.example", LogLevel: "simple", RequestModules: map[string]config.ModuleConfig{}, Hooks: map[string]config.ModuleConfig{}}
	want := struct {
		Code   int
		Worker config.WorkerConfig
	}{http.StatusOK, wantWorker}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected first attach:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPooledWorkerAttachRejectsProxyMismatch(t *testing.T) {
	tests := []struct {
		name      string
		worker    config.WorkerConfig
		wantError string
	}{
		{name: "non-member", worker: config.WorkerConfig{Port: 6768, Upstream: "other", ProxyURL: "http://proxy-a.example"}, wantError: "worker upstream is not a member of target pool"},
		{name: "active mismatch", worker: config.WorkerConfig{Port: 6768, Upstream: "backup", ProxyURL: "http://proxy-a.example"}, wantError: "worker upstream does not match target pool active upstream"},
		{name: "proxy mismatch", worker: config.WorkerConfig{Port: 6768, Upstream: "primary", ProxyURL: "http://proxy-b.example"}, wantError: "worker proxy_url does not match target pool proxy_url"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy-a.example"},
				"cli": test.worker,
			})
			defer m.Close()
			if test.name == "non-member" {
				m.updateConfig(func(cfg *config.Config) {
					cfg.Upstreams["other"] = config.UpstreamProfile{BaseURL: "https://other.example/v1"}
				})
			}
			response := patchPoolRouting(t, m, "/api/workers/cli", `{"upstream_pool":"coding-ha"}`)
			got := struct {
				Code int
				Body string
				Pool string
			}{response.Code, response.Body.String(), m.config.Workers["cli"].UpstreamPool}
			want := struct {
				Code int
				Body string
				Pool string
			}{http.StatusConflict, "{\"error\":\"" + test.wantError + "\"}\n", ""}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected attach response: got %#v want %#v", got, want)
			}
		})
	}
}

func TestManagerRejectsPooledWorkerProxyPatch(t *testing.T) {
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	response := patchPoolRouting(t, m, "/api/workers/app", `{"proxy_url":"http://proxy.example"}`)
	if got, want := struct {
		Code int
		Body string
	}{response.Code, response.Body.String()}, struct {
		Code int
		Body string
	}{http.StatusConflict, "{\"error\":\"pooled worker proxy_url cannot be changed\"}\n"}; got != want {
		t.Fatalf("unexpected proxy response: got %#v want %#v", got, want)
	}
}

func TestManagerPooledWorkerCreateRequiresPoolInvariant(t *testing.T) {
	tests := []struct {
		name      string
		upstream  string
		proxyURL  string
		wantError string
	}{
		{name: "non-member", upstream: "other", proxyURL: "http://proxy.example", wantError: "worker upstream is not a member of target pool"},
		{name: "active mismatch", upstream: "backup", proxyURL: "http://proxy.example", wantError: "worker upstream does not match target pool active upstream"},
		{name: "proxy mismatch", upstream: "primary", proxyURL: "http://other-proxy.example", wantError: "worker proxy_url does not match target pool proxy_url"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy.example"},
			})
			defer m.Close()
			if test.upstream == "other" {
				m.updateConfig(func(cfg *config.Config) {
					cfg.Upstreams["other"] = config.UpstreamProfile{BaseURL: "https://other.example/v1"}
				})
			}
			body := fmt.Sprintf(`{"name":"cli","port":6768,"upstream":"%s","upstream_pool":"coding-ha","proxy_url":"%s"}`, test.upstream, test.proxyURL)
			response := httptest.NewRecorder()
			m.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers", strings.NewReader(body)))
			got := struct {
				Code    int
				Body    string
				Created bool
			}{response.Code, response.Body.String(), m.config.Workers["cli"].Port != 0}
			want := struct {
				Code    int
				Body    string
				Created bool
			}{http.StatusConflict, "{\"error\":\"" + test.wantError + "\"}\n", false}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected create result:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

func TestManagerPooledWorkerCreateEstablishesEmptyPool(t *testing.T) {
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{})
	defer m.Close()
	response := httptest.NewRecorder()
	m.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers", strings.NewReader(`{"name":"app","port":6767,"upstream":"backup","upstream_pool":"coding-ha","proxy_url":"http://proxy.example"}`)))
	got := struct {
		Code     int
		Active   string
		ProxyURL string
	}{response.Code, m.poolActiveUpstream("coding-ha"), m.poolProxyURL("coding-ha")}
	want := struct {
		Code     int
		Active   string
		ProxyURL string
	}{http.StatusCreated, "backup", "http://proxy.example"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected pooled create:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPooledWorkerCreateFailurePreservesPoolIdentity(t *testing.T) {
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy.example"},
	})
	defer m.Close()
	authorityObserve(t, m, "primary", readinessTestSuccess(1))
	wantReadiness := m.poolReadiness("coding-ha", "primary")
	m.starter = &recordingStarter{err: fmt.Errorf("spawn failed")}
	response := httptest.NewRecorder()
	m.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers", strings.NewReader(`{"name":"cli","port":6768,"upstream":"primary","upstream_pool":"coding-ha","proxy_url":"http://proxy.example"}`)))
	_, created := m.config.Workers["cli"]
	got := struct {
		Code      int
		Created   bool
		Readiness PoolReadiness
	}{response.Code, created, m.poolReadiness("coding-ha", "primary")}
	want := struct {
		Code      int
		Created   bool
		Readiness PoolReadiness
	}{http.StatusInternalServerError, false, wantReadiness}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("failed create changed pool identity:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPooledWorkerDetachPreservesUpstreamAndProxy(t *testing.T) {
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy.example"},
	})
	defer m.Close()
	response := patchPoolRouting(t, m, "/api/workers/app", `{"upstream_pool":"","upstream_id":"backup","proxy_url":"http://other-proxy.example"}`)
	got := struct {
		Code     int
		Pool     string
		Upstream string
		ProxyURL string
	}{response.Code, m.config.Workers["app"].UpstreamPool, workerUpstreamID(m.config.Workers["app"]), m.config.Workers["app"].ProxyURL}
	want := struct {
		Code     int
		Pool     string
		Upstream string
		ProxyURL string
	}{http.StatusOK, "", "primary", "http://proxy.example"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detach changed preserved fields:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerHealthIdentityPatchResetsCircuit(t *testing.T) {
	m, pool := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	key := poolCircuitKey("coding-ha", "primary")
	m.circuits.RecordFailure(key, pool.CircuitBreaker)
	authorityObserve(t, m, "primary", readinessTestFailure())
	response := patchPoolRouting(t, m, "/api/upstreams/primary", `{"base_url":"https://new.example/v1"}`)
	got := struct {
		Code      int
		Circuit   CircuitStatus
		Readiness ReadinessState
		Reasons   []any
	}{response.Code, m.circuits.Status(key, pool.CircuitBreaker), m.poolReadiness("coding-ha", "primary").Readiness, poolRoutingReasons(m)}
	want := struct {
		Code      int
		Circuit   CircuitStatus
		Readiness ReadinessState
		Reasons   []any
	}{http.StatusOK, CircuitStatus{State: CircuitStateClosed}, ReadinessStateUnknown, []any{"identity_changed"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected health identity reset:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerProbeModelPatchPreservesCircuit(t *testing.T) {
	m, pool := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	key := poolCircuitKey("coding-ha", "primary")
	m.circuits.RecordFailure(key, pool.CircuitBreaker)
	wantCircuit := m.circuits.Status(key, pool.CircuitBreaker)
	authorityObserve(t, m, "primary", readinessTestFailure())
	response := patchPoolRouting(t, m, "/api/upstreams/primary", `{"protocol_probe":{"model":"model-new"}}`)
	got := struct {
		Code      int
		Circuit   CircuitStatus
		Readiness ReadinessState
	}{response.Code, m.circuits.Status(key, pool.CircuitBreaker), m.poolReadiness("coding-ha", "primary").Readiness}
	want := struct {
		Code      int
		Circuit   CircuitStatus
		Readiness ReadinessState
	}{http.StatusOK, wantCircuit, ReadinessStateUnknown}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected model identity result:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPoolEgressChangeResetsCircuits(t *testing.T) {
	m, pool := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", ProxyURL: "http://proxy.example"},
	})
	defer m.Close()
	for _, member := range pool.Upstreams {
		m.circuits.RecordFailure(poolCircuitKey("coding-ha", member), pool.CircuitBreaker)
	}
	response := patchPoolRouting(t, m, "/api/workers/app", `{"upstream_pool":"coding-ha"}`)
	got := []CircuitStatus{m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker), m.circuits.Status(poolCircuitKey("coding-ha", "backup"), pool.CircuitBreaker)}
	want := []CircuitStatus{{State: CircuitStateClosed}, {State: CircuitStateClosed}}
	if response.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("egress attach did not reset circuits: status %d got %#v want %#v", response.Code, got, want)
	}
}

func TestManagerUpstreamProbeModelPatchReprobes(t *testing.T) {
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		if spec.Upstream == "primary" && spec.Model == "model-new" {
			return readinessTestSuccess(1)
		}
		return readinessTestFailure()
	}
	response := patchPoolRouting(t, m, "/api/upstreams/primary", `{"protocol_probe":{"model":" model-new "}}`)
	eventually(t, time.Second, func() bool { return m.poolReadiness("coding-ha", "primary").Readiness == ReadinessStateReady })
	if got, want := struct {
		Code  int
		Model string
	}{response.Code, m.config.Upstreams["primary"].ProtocolProbe.Model}, struct {
		Code  int
		Model string
	}{http.StatusOK, "model-new"}; got != want {
		t.Fatalf("unexpected reprobe result: got %#v want %#v", got, want)
	}
}

func newPoolRoutingTestManager(t *testing.T, workers map[string]config.WorkerConfig) (*Manager, config.UpstreamPool) {
	t.Helper()
	pool := config.UpstreamPool{Upstreams: []string{"primary", "backup"}, CircuitBreaker: config.CircuitBreakerConfig{FailureThreshold: 1, RecoverySuccessThreshold: 1, RecoveryWaitSeconds: 30}}
	m := New(Config{Config: config.Config{Settings: config.Settings{StateDir: t.TempDir()}, Plugins: testPluginDefinitions(), Workers: workers, Upstreams: map[string]config.UpstreamProfile{
		"primary": {BaseURL: "https://primary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-a"}},
		"backup":  {BaseURL: "https://backup.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-b"}},
	}, UpstreamPools: map[string]config.UpstreamPool{"coding-ha": pool}}, WorkerClient: &recordingWorkerClient{}})
	return m, pool
}

func readinessTestFailure() upstream.ProbeResult {
	return upstream.ProbeResult{Mode: upstream.ProbeModeProtocol, Authoritative: true, Error: "protocol_error"}
}

func patchPoolRouting(t *testing.T, m *Manager, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	m.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "http://manager.local"+path, strings.NewReader(body)))
	return response
}

func poolRoutingEvents(m *Manager, eventType EventType) []map[string]any {
	var payloads []map[string]any
	for _, event := range m.events.Replay(0) {
		if event.Type == eventType {
			payloads = append(payloads, event.Payload)
		}
	}
	return payloads
}

func poolRoutingReasons(m *Manager) []any {
	var reasons []any
	for _, payload := range poolRoutingEvents(m, EventUpstreamCircuitChanged) {
		if reason, exists := payload["reason"]; exists {
			reasons = append(reasons, reason)
		}
	}
	return reasons
}
