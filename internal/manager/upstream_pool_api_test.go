package manager

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
)

func TestManagerUpstreamPoolCRUD(t *testing.T) {
	m := New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: t.TempDir()},
		Plugins:  testPluginDefinitions(),
		Workers: map[string]config.WorkerConfig{
			"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"primary":  {BaseURL: "https://primary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "probe"}},
			"backup":   {BaseURL: "https://backup.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "probe"}},
			"tertiary": {BaseURL: "https://tertiary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "probe"}},
		},
		UpstreamPools: map[string]config.UpstreamPool{
			"coding-ha": {Upstreams: []string{"primary", "backup"}},
		},
	}})
	m.cancelProbes()
	defer m.Close()
	now := time.Date(2026, time.July, 13, 1, 2, 3, 0, time.UTC)
	m.clock = func() time.Time { return now }

	response := requestManager(t, m, http.MethodGet, "/api/upstream-pools", "")
	var listed struct {
		Pools []upstreamPoolSummary `json:"pools"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	wantListed := []upstreamPoolSummary{{
		ID:        "coding-ha",
		Name:      "coding-ha",
		Mode:      config.UpstreamPoolModeActive,
		Upstreams: []string{"primary", "backup"},
		Probe: config.PoolProbeConfig{
			StableIntervalSeconds: config.DefaultPoolProbeStableIntervalSeconds,
			AlertIntervalSeconds:  config.DefaultPoolProbeAlertIntervalSeconds,
		},
		CircuitBreaker: config.CircuitBreakerConfig{FailureThreshold: 3, RecoverySuccessThreshold: 2, RecoveryWaitSeconds: 60},
		ActiveUpstream: "primary",
		Workers:        []string{"app"},
		ProbeState:     PoolProbeStateAlert,
		NextProbeAt:    &now,
		Readiness: []PoolReadiness{
			{Upstream: "primary", Pool: "coding-ha", Mode: "protocol", Authoritative: true, Readiness: ReadinessStateUnknown},
			{Upstream: "backup", Pool: "coding-ha", Mode: "protocol", Authoritative: true, Readiness: ReadinessStateUnknown},
		},
	}}
	if !reflect.DeepEqual(listed.Pools, wantListed) {
		t.Fatalf("unexpected pools:\n got %#v\nwant %#v", listed.Pools, wantListed)
	}

	response = requestManager(t, m, http.MethodPost, "/api/upstream-pools", `{"name":"research-ha","mode":"disabled","upstreams":["backup","tertiary"],"probe":{"stable_interval_seconds":600,"alert_interval_seconds":120}}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("unexpected create status %d: %s", response.Code, response.Body.String())
	}
	var created upstreamPoolSummary
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if got, want := struct {
		Mode  config.UpstreamPoolMode
		Probe config.PoolProbeConfig
	}{created.Mode, created.Probe}, struct {
		Mode  config.UpstreamPoolMode
		Probe config.PoolProbeConfig
	}{config.UpstreamPoolModeDisabled, config.PoolProbeConfig{StableIntervalSeconds: 600, AlertIntervalSeconds: 120}}; got != want {
		t.Fatalf("unexpected created lifecycle config: got %#v want %#v", got, want)
	}
	response = requestManager(t, m, http.MethodPatch, "/api/upstream-pools/research-ha", `{"mode":"active","upstreams":["tertiary","backup"],"circuit_breaker":{"failure_threshold":5,"recovery_success_threshold":4,"recovery_wait_seconds":30}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected patch status %d: %s", response.Code, response.Body.String())
	}
	gotPool := m.store.Config().UpstreamPools["research-ha"]
	wantPool := config.UpstreamPool{
		Name:      "research-ha",
		Mode:      config.UpstreamPoolModeActive,
		Upstreams: []string{"tertiary", "backup"},
		Probe:     config.PoolProbeConfig{StableIntervalSeconds: 600, AlertIntervalSeconds: 120},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: 5, RecoverySuccessThreshold: 4, RecoveryWaitSeconds: 30,
		},
	}
	if !reflect.DeepEqual(gotPool, wantPool) {
		t.Fatalf("unexpected updated pool:\n got %#v\nwant %#v", gotPool, wantPool)
	}

	response = requestManager(t, m, http.MethodDelete, "/api/upstream-pools/coding-ha", "")
	var conflict map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	wantConflict := map[string]any{"error": `worker "app" upstream pool "coding-ha" does not exist`}
	if response.Code != http.StatusConflict || !reflect.DeepEqual(conflict, wantConflict) {
		t.Fatalf("expected attached pool delete conflict, got %d: %s", response.Code, response.Body.String())
	}
	response = requestManager(t, m, http.MethodDelete, "/api/upstream-pools/research-ha", "")
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected delete status %d: %s", response.Code, response.Body.String())
	}
	if _, exists := m.store.Config().UpstreamPools["research-ha"]; exists {
		t.Fatal("expected unused pool to be deleted")
	}
}

func TestManagerUpstreamPoolUpdateRejectsInvalidMemberSet(t *testing.T) {
	m := New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: t.TempDir()},
		Plugins:  testPluginDefinitions(),
		Workers: map[string]config.WorkerConfig{
			"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"primary": {BaseURL: "https://primary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "probe"}},
			"backup":  {BaseURL: "https://backup.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "probe"}},
		},
		UpstreamPools: map[string]config.UpstreamPool{
			"coding-ha": {Upstreams: []string{"primary", "backup"}},
		},
	}})
	m.cancelProbes()
	defer m.Close()

	response := requestManager(t, m, http.MethodPatch, "/api/upstream-pools/coding-ha", `{"upstreams":["backup"]}`)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `workers must use one active upstream`) {
		t.Fatalf("expected active member conflict, got %d: %s", response.Code, response.Body.String())
	}
	want := []string{"primary", "backup"}
	if got := m.store.Config().UpstreamPools["coding-ha"].Upstreams; !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid mutation changed pool: got %#v want %#v", got, want)
	}

	response = requestManager(t, m, http.MethodDelete, "/api/upstreams/backup", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("expected pool member delete conflict, got %d: %s", response.Code, response.Body.String())
	}
	if _, exists := m.store.Config().Upstreams["backup"]; !exists {
		t.Fatal("pool member delete changed upstream config")
	}
}

func TestManagerUpstreamPoolPatchReownsSchedules(t *testing.T) {
	now := time.Date(2026, time.July, 13, 11, 12, 13, 0, time.UTC)
	tests := []struct {
		name          string
		mode          config.UpstreamPoolMode
		workers       map[string]config.WorkerConfig
		body          string
		wantMembers   []string
		wantSchedules map[poolProbeScheduleKey]poolProbeSchedule
	}{
		{
			name: "probe policy",
			workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
			},
			body:        `{"probe":{"stable_interval_seconds":600,"alert_interval_seconds":120}}`,
			wantMembers: []string{"primary", "backup"},
			wantSchedules: map[poolProbeScheduleKey]poolProbeSchedule{
				{Pool: "coding-ha", Upstream: "primary"}: {NextProbeAt: now, Reason: ProbeScheduleConfig},
				{Pool: "coding-ha", Upstream: "backup"}:  {NextProbeAt: now, Reason: ProbeScheduleConfig},
			},
		},
		{
			name: "member removal and re-add",
			workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
			},
			body:        `{"upstreams":["primary","tertiary"]}`,
			wantMembers: []string{"primary", "tertiary"},
			wantSchedules: map[poolProbeScheduleKey]poolProbeSchedule{
				{Pool: "coding-ha", Upstream: "primary"}:  {NextProbeAt: now, Reason: ProbeScheduleConfig},
				{Pool: "coding-ha", Upstream: "tertiary"}: {NextProbeAt: now, Reason: ProbeScheduleConfig},
			},
		},
		{
			name: "disabled",
			mode: config.UpstreamPoolModeDisabled,
			workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
			},
			body:          `{"probe":{"stable_interval_seconds":600,"alert_interval_seconds":120}}`,
			wantMembers:   []string{"primary", "backup"},
			wantSchedules: map[poolProbeScheduleKey]poolProbeSchedule{},
		},
		{
			name:          "unattached",
			workers:       map[string]config.WorkerConfig{},
			body:          `{"probe":{"stable_interval_seconds":600,"alert_interval_seconds":120}}`,
			wantMembers:   []string{"primary", "backup"},
			wantSchedules: map[poolProbeScheduleKey]poolProbeSchedule{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, pool := newPoolRoutingTestManager(t, test.workers)
			m.cancelProbes()
			defer m.Close()
			m.clock = func() time.Time { return now }
			m.updateConfig(func(cfg *config.Config) {
				cfg.Upstreams["tertiary"] = config.UpstreamProfile{BaseURL: "https://tertiary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-c"}}
				if test.mode != "" {
					configured := cfg.UpstreamPools["coding-ha"]
					configured.Mode = test.mode
					cfg.UpstreamPools["coding-ha"] = configured
				}
			})
			for _, upstreamName := range []string{"primary", "backup", "tertiary"} {
				m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: upstreamName}] = poolProbeSchedule{
					NextProbeAt: now.Add(time.Hour), ConsecutiveFailures: 3, Reason: ProbeScheduleRecovery,
				}
			}
			for _, upstreamName := range append([]string(nil), test.wantMembers...) {
				key := poolCircuitKey("coding-ha", upstreamName)
				m.readiness[key] = readinessObservation{Result: readinessTestFailure(), CheckedAt: now, ExpiresAt: now.Add(time.Hour)}
			}
			m.exhaustedPools["coding-ha"] = "primary"
			response := requestManager(t, m, http.MethodPatch, "/api/upstream-pools/coding-ha", test.body)
			got := struct {
				Code       int
				Members    []string
				Readiness  map[string]readinessObservation
				Schedules  map[poolProbeScheduleKey]poolProbeSchedule
				Exhaustion map[string]string
			}{response.Code, m.store.Config().UpstreamPools["coding-ha"].Upstreams, maps.Clone(m.readiness), maps.Clone(m.probeSchedules), maps.Clone(m.exhaustedPools)}
			want := struct {
				Code       int
				Members    []string
				Readiness  map[string]readinessObservation
				Schedules  map[poolProbeScheduleKey]poolProbeSchedule
				Exhaustion map[string]string
			}{http.StatusOK, test.wantMembers, map[string]readinessObservation{}, test.wantSchedules, map[string]string{}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("pool PATCH retained stale schedule authority:\n got %#v\nwant %#v", got, want)
			}
			_ = pool
		})
	}
}

func TestManagerUpstreamPoolProbePolicyEventUsesConfigAuthority(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 13, 14, 0, time.UTC)
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy.example"},
	})
	defer m.Close()
	m.clock = func() time.Time { return now }
	started := make(chan probeSpec, 2)
	releasePrimary := make(chan struct{})
	releaseBackup := make(chan struct{})
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		started <- spec
		if spec.Upstream == "primary" {
			<-releasePrimary
		} else {
			<-releaseBackup
		}
		return readinessTestSuccess(4)
	}

	response := requestManager(t, m, http.MethodPatch, "/api/upstream-pools/coding-ha", `{"probe":{"stable_interval_seconds":600,"alert_interval_seconds":120}}`)
	specs := []probeSpec{<-started, <-started}
	sort.Slice(specs, func(i int, j int) bool { return specs[i].Upstream < specs[j].Upstream })
	close(releasePrimary)
	eventually(t, time.Second, func() bool { return len(poolRoutingEvents(m, EventUpstreamProbed)) == 1 })
	close(releaseBackup)
	m.probeWait.Wait()
	events := poolRoutingEvents(m, EventUpstreamProbed)
	got := struct {
		Code    int
		Reasons []ProbeScheduleReason
		Events  []map[string]any
	}{response.Code, []ProbeScheduleReason{specs[0].Reason, specs[1].Reason}, events}
	checkedAt := now
	want := struct {
		Code    int
		Reasons []ProbeScheduleReason
		Events  []map[string]any
	}{http.StatusOK, []ProbeScheduleReason{ProbeScheduleConfig, ProbeScheduleConfig}, []map[string]any{
		{"upstream": "primary", "pool": "coding-ha", "mode": upstream.ProbeModeProtocol, "authoritative": true, "readiness": ReadinessStateReady, "eligible": true, "checked_at": checkedAt.Format(time.RFC3339), "ok": true, "status_code": http.StatusOK, "latency_ms": int64(4), "probe_state": PoolProbeStateAlert, "next_probe_at": now.Format(time.RFC3339), "reason": ProbeScheduleConfig},
		{"upstream": "backup", "pool": "coding-ha", "mode": upstream.ProbeModeProtocol, "authoritative": true, "readiness": ReadinessStateReady, "eligible": true, "checked_at": checkedAt.Format(time.RFC3339), "ok": true, "status_code": http.StatusOK, "latency_ms": int64(4), "probe_state": PoolProbeStateStable, "next_probe_at": now.Add(10 * time.Minute).Format(time.RFC3339), "reason": ProbeScheduleConfig},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config authority did not reach real probe event:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerUpstreamPoolPatchPreservesConcurrentModeTransition(t *testing.T) {
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	m.cancelProbes()
	defer m.Close()
	m.failoverMu.Lock()
	reader, writer := io.Pipe()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		m.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/upstream-pools/coding-ha", reader))
		done <- response
	}()
	if _, err := writer.Write([]byte(`{"probe":{"stable_interval_seconds":600,"alert_interval_seconds":120}}`)); err != nil {
		t.Fatal(err)
	}
	m.updateConfig(func(cfg *config.Config) {
		pool := cfg.UpstreamPools["coding-ha"]
		pool.Mode = config.UpstreamPoolModeDisabled
		cfg.UpstreamPools["coding-ha"] = pool
	})
	writer.Close()
	m.failoverMu.Unlock()
	response := <-done
	got := struct {
		Code int
		Pool config.UpstreamPool
	}{response.Code, m.store.Config().UpstreamPools["coding-ha"]}
	wantPool := got.Pool
	wantPool.Mode = config.UpstreamPoolModeDisabled
	wantPool.Probe = config.PoolProbeConfig{StableIntervalSeconds: 600, AlertIntervalSeconds: 120}
	want := struct {
		Code int
		Pool config.UpstreamPool
	}{http.StatusOK, wantPool}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("concurrent patch lost mode transition:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerUpstreamPoolDisableReconcilesSharedProbeIdentity(t *testing.T) {
	now := time.Date(2026, time.July, 14, 1, 2, 3, 0, time.UTC)
	m, pool := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		"cli": {Port: 6768, Upstream: "primary", UpstreamPool: "research-ha"},
	})
	defer m.Close()
	m.clock = func() time.Time { return now }
	m.updateConfig(func(cfg *config.Config) { cfg.UpstreamPools["research-ha"] = pool })
	future := now.Add(time.Hour)
	for _, poolName := range []string{"coding-ha", "research-ha"} {
		for _, upstreamName := range pool.Upstreams {
			m.probeSchedules[poolProbeScheduleKey{Pool: poolName, Upstream: upstreamName}] = poolProbeSchedule{NextProbeAt: future, Reason: ProbeScheduleStable}
		}
	}
	m.probeAllUpstreams(t.Context())
	wantDesired := map[probeExecutionKey]probeSpec{}
	wantManual := map[probeExecutionKey]probeSpec{}
	wantPending := map[probeExecutionKey]probeSpec{}
	wantGenerations := maps.Clone(m.probeGenerations)
	for key, desired := range m.desiredProbes {
		for _, poolName := range desired.Pools {
			m.readiness[poolCircuitKey(poolName, desired.Upstream)] = readinessObservation{
				Result: readinessTestSuccess(1), CheckedAt: now, ExpiresAt: future.Add(time.Hour),
				Generation: desired.Generation, Fingerprint: desired.Fingerprint,
			}
		}
		manual := desired
		manual.ManualPools = append([]string(nil), desired.Pools...)
		m.manualProbes[key] = manual
		m.pendingProbes[key] = manual
		desired.Pools = []string{"research-ha"}
		wantDesired[key] = desired
		manual.Pools = []string{"research-ha"}
		manual.ManualPools = []string{"research-ha"}
		wantManual[key] = manual
		wantPending[key] = manual
	}
	wantReadiness := map[string]readinessObservation{
		poolCircuitKey("research-ha", "primary"): m.readiness[poolCircuitKey("research-ha", "primary")],
		poolCircuitKey("research-ha", "backup"):  m.readiness[poolCircuitKey("research-ha", "backup")],
	}
	wantSchedules := map[poolProbeScheduleKey]poolProbeSchedule{
		{Pool: "research-ha", Upstream: "primary"}: {NextProbeAt: future, Reason: ProbeScheduleStable},
		{Pool: "research-ha", Upstream: "backup"}:  {NextProbeAt: future, Reason: ProbeScheduleStable},
	}
	executions := make(chan probeSpec, 1)
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		executions <- spec
		return readinessTestSuccess(2)
	}
	response := requestManager(t, m, http.MethodPatch, "/api/upstream-pools/coding-ha", `{"mode":"disabled"}`)
	got := struct {
		Code        int
		Readiness   map[string]readinessObservation
		Schedules   map[poolProbeScheduleKey]poolProbeSchedule
		Generations map[probeExecutionKey]int
		Desired     map[probeExecutionKey]probeSpec
		Manual      map[probeExecutionKey]probeSpec
		Pending     map[probeExecutionKey]probeSpec
		Executed    bool
	}{
		Code: response.Code, Readiness: maps.Clone(m.readiness), Schedules: maps.Clone(m.probeSchedules),
		Generations: maps.Clone(m.probeGenerations), Desired: maps.Clone(m.desiredProbes),
		Manual: maps.Clone(m.manualProbes), Pending: maps.Clone(m.pendingProbes),
	}
	select {
	case <-executions:
		got.Executed = true
	default:
	}
	want := struct {
		Code        int
		Readiness   map[string]readinessObservation
		Schedules   map[poolProbeScheduleKey]poolProbeSchedule
		Generations map[probeExecutionKey]int
		Desired     map[probeExecutionKey]probeSpec
		Manual      map[probeExecutionKey]probeSpec
		Pending     map[probeExecutionKey]probeSpec
		Executed    bool
	}{http.StatusOK, wantReadiness, wantSchedules, wantGenerations, wantDesired, wantManual, wantPending, false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("disable destroyed shared survivor authority:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerUpstreamPoolMemberRemovalPreservesSharedSurvivorAuthority(t *testing.T) {
	now := time.Date(2026, time.July, 14, 3, 4, 5, 0, time.UTC)
	m, pool := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		"cli": {Port: 6768, Upstream: "primary", UpstreamPool: "research-ha"},
	})
	defer m.Close()
	m.clock = func() time.Time { return now }
	m.updateConfig(func(cfg *config.Config) { cfg.UpstreamPools["research-ha"] = pool })
	future := now.Add(time.Hour)
	for _, poolName := range []string{"coding-ha", "research-ha"} {
		for _, upstreamName := range pool.Upstreams {
			m.probeSchedules[poolProbeScheduleKey{Pool: poolName, Upstream: upstreamName}] = poolProbeSchedule{NextProbeAt: future, Reason: ProbeScheduleStable}
		}
	}
	m.probeAllUpstreams(t.Context())
	wantDesired := map[probeExecutionKey]probeSpec{}
	wantManual := map[probeExecutionKey]probeSpec{}
	wantPending := map[probeExecutionKey]probeSpec{}
	wantGenerations := maps.Clone(m.probeGenerations)
	for key, desired := range m.desiredProbes {
		for _, poolName := range desired.Pools {
			m.readiness[poolCircuitKey(poolName, desired.Upstream)] = readinessObservation{
				Result: readinessTestSuccess(1), CheckedAt: now, ExpiresAt: future.Add(time.Hour),
				Generation: desired.Generation, Fingerprint: desired.Fingerprint,
			}
		}
		manual := desired
		manual.ManualPools = append([]string(nil), desired.Pools...)
		m.manualProbes[key] = manual
		if desired.Upstream == "backup" {
			pending := manual
			m.pendingProbes[key] = pending
			desired.Pools = []string{"research-ha"}
			manual.Pools = []string{"research-ha"}
			manual.ManualPools = []string{"research-ha"}
			wantPending[key] = manual
		} else {
			desired.Due = true
			desired.Reason = ProbeScheduleConfig
		}
		wantDesired[key] = desired
		wantManual[key] = manual
	}
	wantReadiness := map[string]readinessObservation{
		poolCircuitKey("research-ha", "primary"): m.readiness[poolCircuitKey("research-ha", "primary")],
		poolCircuitKey("research-ha", "backup"):  m.readiness[poolCircuitKey("research-ha", "backup")],
	}
	wantSchedules := map[poolProbeScheduleKey]poolProbeSchedule{
		{Pool: "coding-ha", Upstream: "primary"}:   {NextProbeAt: now, Reason: ProbeScheduleConfig},
		{Pool: "research-ha", Upstream: "primary"}: {NextProbeAt: future, Reason: ProbeScheduleStable},
		{Pool: "research-ha", Upstream: "backup"}:  {NextProbeAt: future, Reason: ProbeScheduleStable},
	}
	started := make(chan probeSpec, 2)
	release := make(chan struct{})
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		started <- spec
		<-release
		return upstream.ProbeResult{}
	}

	response := requestManager(t, m, http.MethodPatch, "/api/upstream-pools/coding-ha", `{"upstreams":["primary"]}`)
	var execution probeSpec
	select {
	case execution = <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("member config probe did not start")
	}
	got := struct {
		Code        int
		Readiness   map[string]readinessObservation
		Schedules   map[poolProbeScheduleKey]poolProbeSchedule
		Generations map[probeExecutionKey]int
		Desired     map[probeExecutionKey]probeSpec
		Manual      map[probeExecutionKey]probeSpec
		Pending     map[probeExecutionKey]probeSpec
		Execution   probeSpec
	}{
		response.Code, maps.Clone(m.readiness), maps.Clone(m.probeSchedules), maps.Clone(m.probeGenerations),
		maps.Clone(m.desiredProbes), maps.Clone(m.manualProbes), maps.Clone(m.pendingProbes), execution,
	}
	wantExecution := wantDesired[probeExecutionKey{Upstream: "primary"}]
	want := struct {
		Code        int
		Readiness   map[string]readinessObservation
		Schedules   map[poolProbeScheduleKey]poolProbeSchedule
		Generations map[probeExecutionKey]int
		Desired     map[probeExecutionKey]probeSpec
		Manual      map[probeExecutionKey]probeSpec
		Pending     map[probeExecutionKey]probeSpec
		Execution   probeSpec
	}{http.StatusOK, wantReadiness, wantSchedules, wantGenerations, wantDesired, wantManual, wantPending, wantExecution}
	if !reflect.DeepEqual(got, want) {
		close(release)
		t.Fatalf("member removal destroyed shared survivor authority:\n got %#v\nwant %#v", got, want)
	}
	select {
	case extra := <-started:
		close(release)
		t.Fatalf("member removal launched unnecessary survivor probe: %#v", extra)
	default:
	}
	close(release)
	m.probeWait.Wait()
}

func TestManagerSharedInFlightResultRejectsDepartedPoolBinding(t *testing.T) {
	now := time.Date(2026, time.July, 14, 4, 5, 6, 0, time.UTC)
	m, pool := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		"cli": {Port: 6768, Upstream: "primary", UpstreamPool: "research-ha"},
	})
	defer m.Close()
	m.clock = func() time.Time { return now }
	m.updateConfig(func(cfg *config.Config) { cfg.UpstreamPools["research-ha"] = pool })
	future := now.Add(time.Hour)
	for _, poolName := range []string{"coding-ha", "research-ha"} {
		for _, upstreamName := range pool.Upstreams {
			m.probeSchedules[poolProbeScheduleKey{Pool: poolName, Upstream: upstreamName}] = poolProbeSchedule{NextProbeAt: future, Reason: ProbeScheduleStable}
		}
	}
	m.probeAllUpstreams(t.Context())
	key := probeExecutionKey{Upstream: "primary"}
	spec := m.desiredProbes[key]
	for _, poolName := range spec.Pools {
		m.readiness[poolCircuitKey(poolName, "primary")] = readinessObservation{
			Result: readinessTestSuccess(1), CheckedAt: now, ExpiresAt: future.Add(time.Hour),
			Generation: spec.Generation, Fingerprint: spec.Fingerprint,
		}
	}
	wantSurvivorBefore := m.readiness[poolCircuitKey("research-ha", "primary")]
	started := make(chan probeSpec, 2)
	release := make(chan struct{})
	m.probeRunner = func(_ context.Context, running probeSpec) upstream.ProbeResult {
		started <- running
		<-release
		return readinessTestSuccess(9)
	}
	m.failoverMu.Lock()
	m.startProbeLocked(spec)
	m.failoverMu.Unlock()
	<-started

	response := requestManager(t, m, http.MethodPatch, "/api/upstream-pools/coding-ha", `{"mode":"disabled"}`)
	desired := spec
	desired.Pools = []string{"research-ha"}
	gotBefore := struct {
		Code       int
		Readiness  map[string]readinessObservation
		Generation int
		Desired    probeSpec
	}{response.Code, maps.Clone(m.readiness), m.probeGenerations[key], m.desiredProbes[key]}
	wantBefore := struct {
		Code       int
		Readiness  map[string]readinessObservation
		Generation int
		Desired    probeSpec
	}{http.StatusOK, map[string]readinessObservation{poolCircuitKey("research-ha", "primary"): wantSurvivorBefore}, spec.Generation, desired}
	if !reflect.DeepEqual(gotBefore, wantBefore) {
		close(release)
		t.Fatalf("departing binding changed shared in-flight authority:\n got %#v\nwant %#v", gotBefore, wantBefore)
	}
	close(release)
	m.probeWait.Wait()
	checkedAt := now
	wantSurvivorAfter := PoolReadiness{
		Upstream: "primary", Pool: "research-ha", Mode: upstream.ProbeModeProtocol,
		Authoritative: true, Readiness: ReadinessStateReady, Eligible: true,
		CheckedAt: &checkedAt, OK: true, StatusCode: http.StatusOK, LatencyMS: 9,
	}
	gotAfter := struct {
		Departed PoolReadiness
		Survivor PoolReadiness
		Events   []map[string]any
	}{m.poolReadiness("coding-ha", "primary"), m.poolReadiness("research-ha", "primary"), poolRoutingEvents(m, EventUpstreamProbed)}
	wantAfter := struct {
		Departed PoolReadiness
		Survivor PoolReadiness
		Events   []map[string]any
	}{PoolReadiness{Upstream: "primary", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol, Authoritative: true, Readiness: ReadinessStateUnknown}, wantSurvivorAfter, []map[string]any{
		{"upstream": "primary", "pool": "research-ha", "mode": upstream.ProbeModeProtocol, "authoritative": true, "readiness": ReadinessStateReady, "eligible": true, "checked_at": now.Format(time.RFC3339), "ok": true, "status_code": http.StatusOK, "latency_ms": int64(9), "probe_state": PoolProbeStateAlert, "next_probe_at": now.Add(15 * time.Minute).Format(time.RFC3339), "reason": ProbeScheduleStable},
	}}
	if !reflect.DeepEqual(gotAfter, wantAfter) {
		t.Fatalf("stale shared result reached departed binding:\n got %#v\nwant %#v", gotAfter, wantAfter)
	}
	select {
	case extra := <-started:
		t.Fatalf("shared survivor launched replacement before deadline: %#v", extra)
	default:
	}
}

func TestManagerUpstreamPoolModeTransitions(t *testing.T) {
	now := time.Date(2026, time.July, 13, 2, 3, 4, 0, time.UTC)
	m, pool := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy.example"},
	})
	m.cancelProbes()
	defer m.Close()
	m.clock = func() time.Time { return now }
	m.mu.RLock()
	wantWorkers := map[string]config.WorkerConfig{"app": cloneWorkerConfig(m.config.Workers["app"])}
	pool = m.config.UpstreamPools["coding-ha"]
	m.mu.RUnlock()
	for _, upstreamName := range pool.Upstreams {
		key := poolCircuitKey("coding-ha", upstreamName)
		m.readiness[key] = readinessObservation{Result: readinessTestSuccess(1), CheckedAt: now, ExpiresAt: now.Add(time.Minute)}
		m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: upstreamName}] = poolProbeSchedule{
			NextProbeAt: now.Add(time.Minute), Reason: ProbeScheduleStable,
		}
	}
	m.circuits.RecordFailure(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker)
	wantDisabledCircuits := []CircuitStatus{
		m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker),
		m.circuits.Status(poolCircuitKey("coding-ha", "backup"), pool.CircuitBreaker),
	}
	spec := readinessTestProbeSpec("coding-ha", "backup", "http://proxy.example", 1, "model-b")
	m.desiredProbes[spec.Key] = spec
	m.pendingProbes[spec.Key] = spec
	m.exhaustedPools["coding-ha"] = "primary"
	eventsBefore := len(m.events.Replay(0))

	response := requestManager(t, m, http.MethodPatch, "/api/upstream-pools/coding-ha", `{"mode":"disabled"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected disable status %d: %s", response.Code, response.Body.String())
	}
	var gotSummary upstreamPoolSummary
	if err := json.Unmarshal(response.Body.Bytes(), &gotSummary); err != nil {
		t.Fatal(err)
	}
	pool.Mode = config.UpstreamPoolModeDisabled
	wantSummary := upstreamPoolSummary{
		ID: "coding-ha", Name: "coding-ha", Mode: config.UpstreamPoolModeDisabled,
		Upstreams: pool.Upstreams, Probe: pool.Probe, CircuitBreaker: pool.CircuitBreaker,
		ActiveUpstream: "primary", Workers: []string{"app"}, ProbeState: PoolProbeStatePaused,
		Readiness: []PoolReadiness{
			{Upstream: "primary", Pool: "coding-ha", Mode: "protocol", Authoritative: true, Readiness: ReadinessStateUnknown},
			{Upstream: "backup", Pool: "coding-ha", Mode: "protocol", Authoritative: true, Readiness: ReadinessStateUnknown},
		},
	}
	events := m.events.Replay(0)
	var modeEvents []Event
	for _, event := range events[eventsBefore:] {
		if event.Type == EventUpstreamPoolModeChanged {
			modeEvents = append(modeEvents, event)
		}
	}
	gotEvent := Event{}
	if len(modeEvents) == 1 {
		gotEvent = modeEvents[0]
	}
	wantEvent := Event{
		ID: gotEvent.ID, Type: EventUpstreamPoolModeChanged, At: gotEvent.At,
		Payload: map[string]any{
			"pool": "coding-ha", "previous_mode": config.UpstreamPoolModeActive, "mode": config.UpstreamPoolModeDisabled,
		},
	}
	m.mu.RLock()
	gotWorkers := map[string]config.WorkerConfig{"app": cloneWorkerConfig(m.config.Workers["app"])}
	m.mu.RUnlock()
	gotDisabled := struct {
		Pool       config.UpstreamPool
		Summary    upstreamPoolSummary
		Workers    map[string]config.WorkerConfig
		Circuits   []CircuitStatus
		Readiness  map[string]readinessObservation
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Desired    map[probeExecutionKey]probeSpec
		Pending    map[probeExecutionKey]probeSpec
		Exhaustion map[string]string
		Events     []Event
	}{m.store.Config().UpstreamPools["coding-ha"], gotSummary, gotWorkers, []CircuitStatus{
		m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker),
		m.circuits.Status(poolCircuitKey("coding-ha", "backup"), pool.CircuitBreaker),
	}, m.readiness, m.probeSchedules, m.desiredProbes, m.pendingProbes, m.exhaustedPools, modeEvents}
	wantDisabled := struct {
		Pool       config.UpstreamPool
		Summary    upstreamPoolSummary
		Workers    map[string]config.WorkerConfig
		Circuits   []CircuitStatus
		Readiness  map[string]readinessObservation
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Desired    map[probeExecutionKey]probeSpec
		Pending    map[probeExecutionKey]probeSpec
		Exhaustion map[string]string
		Events     []Event
	}{pool, wantSummary, wantWorkers, wantDisabledCircuits, map[string]readinessObservation{}, map[poolProbeScheduleKey]poolProbeSchedule{}, map[probeExecutionKey]probeSpec{}, map[probeExecutionKey]probeSpec{}, map[string]string{}, []Event{wantEvent}}
	if !reflect.DeepEqual(gotDisabled, wantDisabled) {
		t.Fatalf("unexpected disabled pool state:\n got %#v\nwant %#v", gotDisabled, wantDisabled)
	}
	gotPool, gotPreviousMode, gotMode, ok := gotEvent.AsUpstreamPoolModeChanged()
	if gotAccessor, wantAccessor := struct {
		Pool     string
		Previous config.UpstreamPoolMode
		Mode     config.UpstreamPoolMode
		OK       bool
	}{gotPool, gotPreviousMode, gotMode, ok}, struct {
		Pool     string
		Previous config.UpstreamPoolMode
		Mode     config.UpstreamPoolMode
		OK       bool
	}{"coding-ha", config.UpstreamPoolModeActive, config.UpstreamPoolModeDisabled, true}; !reflect.DeepEqual(gotAccessor, wantAccessor) {
		t.Fatalf("unexpected mode event accessor: got %#v want %#v", gotAccessor, wantAccessor)
	}

	nextProbe := config.PoolProbeConfig{StableIntervalSeconds: 600, AlertIntervalSeconds: 120}
	response = requestManager(t, m, http.MethodPatch, "/api/upstream-pools/coding-ha", `{"mode":"active","probe":{"stable_interval_seconds":600,"alert_interval_seconds":120}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected enable status %d: %s", response.Code, response.Body.String())
	}
	gotSchedules := map[poolProbeScheduleKey]poolProbeSchedule{}
	for key, schedule := range m.probeSchedules {
		gotSchedules[key] = schedule
	}
	wantSchedules := map[poolProbeScheduleKey]poolProbeSchedule{
		{Pool: "coding-ha", Upstream: "primary"}: {NextProbeAt: now, Reason: ProbeScheduleConfig},
		{Pool: "coding-ha", Upstream: "backup"}:  {NextProbeAt: now, Reason: ProbeScheduleConfig},
	}
	gotEnabled := struct {
		Mode      config.UpstreamPoolMode
		Probe     config.PoolProbeConfig
		Circuits  []CircuitStatus
		Readiness map[string]readinessObservation
		Schedules map[poolProbeScheduleKey]poolProbeSchedule
	}{m.store.Config().UpstreamPools["coding-ha"].Mode, m.store.Config().UpstreamPools["coding-ha"].Probe, []CircuitStatus{
		m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker),
		m.circuits.Status(poolCircuitKey("coding-ha", "backup"), pool.CircuitBreaker),
	}, m.readiness, gotSchedules}
	wantEnabled := struct {
		Mode      config.UpstreamPoolMode
		Probe     config.PoolProbeConfig
		Circuits  []CircuitStatus
		Readiness map[string]readinessObservation
		Schedules map[poolProbeScheduleKey]poolProbeSchedule
	}{config.UpstreamPoolModeActive, nextProbe, []CircuitStatus{{State: CircuitStateClosed}, {State: CircuitStateClosed}}, map[string]readinessObservation{}, wantSchedules}
	if !reflect.DeepEqual(gotEnabled, wantEnabled) {
		t.Fatalf("unexpected enabled pool state:\n got %#v\nwant %#v", gotEnabled, wantEnabled)
	}
}

func requestManager(t *testing.T, m *Manager, method string, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	m.ServeHTTP(response, httptest.NewRequest(method, "http://manager.local"+path, strings.NewReader(body)))
	return response
}
