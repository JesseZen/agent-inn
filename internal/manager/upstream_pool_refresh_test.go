package manager

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
)

type poolRefreshExecution struct {
	Key      probeExecutionKey
	Upstream string
	ProxyURL string
	Pools    []string
	Reason   ProbeScheduleReason
}

func TestManagerPoolRefreshActiveUsesUniqueExecutions(t *testing.T) {
	now := time.Date(2026, time.July, 13, 7, 8, 9, 0, time.UTC)
	m := newPoolActionTestManager(t, config.UpstreamPoolModeActive, []string{"primary", "backup"}, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy.example"},
	})
	defer m.Close()
	m.clock = func() time.Time { return now }
	calls := make(chan probeSpec, 2)
	releasePrimary := make(chan struct{})
	releaseBackup := make(chan struct{})
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		calls <- spec
		if spec.Upstream == "backup" {
			<-releaseBackup
			return readinessTestSuccess(2)
		}
		<-releasePrimary
		return readinessTestSuccess(1)
	}

	response := requestManager(t, m, http.MethodPost, "/api/upstream-pools/coding-ha/probe", "")
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected refresh status %d: %s", response.Code, response.Body.String())
	}
	executions := poolRefreshExecutions([]probeSpec{<-calls, <-calls})
	close(releasePrimary)
	eventually(t, time.Second, func() bool { return len(poolRoutingEvents(m, EventUpstreamProbed)) == 1 })
	close(releaseBackup)
	m.probeWait.Wait()
	got := struct {
		Status     int
		Executions []poolRefreshExecution
		Readiness  []PoolReadiness
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Events     []map[string]any
	}{response.Code, executions, []PoolReadiness{
		m.poolReadiness("coding-ha", "primary"), m.poolReadiness("coding-ha", "backup"),
	}, maps.Clone(m.probeSchedules), poolRoutingEvents(m, EventUpstreamProbed)}
	checkedAt := now
	want := struct {
		Status     int
		Executions []poolRefreshExecution
		Readiness  []PoolReadiness
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Events     []map[string]any
	}{http.StatusOK, []poolRefreshExecution{
		{probeExecutionKey{Upstream: "backup", ProxyURL: "http://proxy.example"}, "backup", "http://proxy.example", []string{"coding-ha"}, ProbeScheduleManual},
		{probeExecutionKey{Upstream: "primary", ProxyURL: "http://proxy.example"}, "primary", "http://proxy.example", []string{"coding-ha"}, ProbeScheduleManual},
	}, []PoolReadiness{
		{Upstream: "primary", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol, Authoritative: true, Readiness: ReadinessStateReady, Eligible: true, CheckedAt: &checkedAt, OK: true, StatusCode: http.StatusOK, LatencyMS: 1},
		{Upstream: "backup", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol, Authoritative: true, Readiness: ReadinessStateReady, Eligible: true, CheckedAt: &checkedAt, OK: true, StatusCode: http.StatusOK, LatencyMS: 2},
	}, map[poolProbeScheduleKey]poolProbeSchedule{
		{Pool: "coding-ha", Upstream: "primary"}: {NextProbeAt: now.Add(15 * time.Minute), Reason: ProbeScheduleStable},
		{Pool: "coding-ha", Upstream: "backup"}:  {NextProbeAt: now.Add(15 * time.Minute), Reason: ProbeScheduleStable},
	}, []map[string]any{
		{"upstream": "primary", "pool": "coding-ha", "mode": upstream.ProbeModeProtocol, "authoritative": true, "readiness": ReadinessStateReady, "eligible": true, "checked_at": checkedAt.Format(time.RFC3339), "ok": true, "status_code": http.StatusOK, "latency_ms": int64(1), "probe_state": PoolProbeStateAlert, "next_probe_at": now.Format(time.RFC3339), "reason": ProbeScheduleManual},
		{"upstream": "backup", "pool": "coding-ha", "mode": upstream.ProbeModeProtocol, "authoritative": true, "readiness": ReadinessStateReady, "eligible": true, "checked_at": checkedAt.Format(time.RFC3339), "ok": true, "status_code": http.StatusOK, "latency_ms": int64(2), "probe_state": PoolProbeStateStable, "next_probe_at": now.Add(15 * time.Minute).Format(time.RFC3339), "reason": ProbeScheduleManual},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected active refresh result:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPoolRefreshQueuesBehindInFlightProbe(t *testing.T) {
	now := time.Date(2026, time.July, 13, 9, 10, 11, 0, time.UTC)
	m := newPoolActionTestManager(t, config.UpstreamPoolModeActive, []string{"primary"}, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	m.clock = func() time.Time { return now }
	calls := make(chan probeSpec, 2)
	release := make(chan struct{})
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		calls <- spec
		if spec.Reason == ProbeScheduleStartup {
			<-release
			return readinessTestSuccess(1)
		}
		return readinessTestSuccess(2)
	}

	m.probeAllUpstreams(t.Context())
	first := <-calls
	response := requestManager(t, m, http.MethodPost, "/api/upstream-pools/coding-ha/probe", "")
	if response.Code != http.StatusOK {
		close(release)
		t.Fatalf("unexpected refresh status %d: %s", response.Code, response.Body.String())
	}
	select {
	case duplicate := <-calls:
		t.Fatalf("manual refresh duplicated in-flight execution: %#v", duplicate)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	second := <-calls
	m.probeWait.Wait()
	checkedAt := now
	got := struct {
		Status     int
		Reasons    []ProbeScheduleReason
		Generation []int
		Readiness  PoolReadiness
	}{response.Code, []ProbeScheduleReason{first.Reason, second.Reason}, []int{first.Generation, second.Generation}, m.poolReadiness("coding-ha", "primary")}
	want := struct {
		Status     int
		Reasons    []ProbeScheduleReason
		Generation []int
		Readiness  PoolReadiness
	}{http.StatusOK, []ProbeScheduleReason{ProbeScheduleStartup, ProbeScheduleManual}, []int{first.Generation, first.Generation}, PoolReadiness{
		Upstream: "primary", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol, Authoritative: true,
		Readiness: ReadinessStateReady, Eligible: true, CheckedAt: &checkedAt,
		OK: true, StatusCode: http.StatusOK, LatencyMS: 2,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manual refresh did not replace in-flight probe:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPoolRefreshDisabledRecordsReadinessOnly(t *testing.T) {
	now := time.Date(2026, time.July, 13, 8, 9, 10, 0, time.UTC)
	m := newPoolActionTestManager(t, config.UpstreamPoolModeDisabled, []string{"primary", "backup"}, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy.example"},
	})
	defer m.Close()
	m.clock = func() time.Time { return now }
	pool := m.config.UpstreamPools["coding-ha"]
	for failure := 0; failure < pool.CircuitBreaker.FailureThreshold; failure++ {
		m.circuits.RecordFailure(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker)
	}
	m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: "primary"}] = poolProbeSchedule{NextProbeAt: now.Add(time.Hour), ConsecutiveFailures: 2, Reason: ProbeScheduleRecovery}
	m.exhaustedPools["coding-ha"] = "primary"
	wantCircuits := []CircuitStatus{
		m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker),
		m.circuits.Status(poolCircuitKey("coding-ha", "backup"), pool.CircuitBreaker),
	}
	wantSchedules := maps.Clone(m.probeSchedules)
	wantExhaustion := maps.Clone(m.exhaustedPools)
	m.mu.RLock()
	wantWorkers := map[string]config.WorkerConfig{"app": cloneWorkerConfig(m.config.Workers["app"])}
	m.mu.RUnlock()
	calls := make(chan probeSpec, 2)
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		calls <- spec
		return readinessTestSuccess(4)
	}

	response := requestManager(t, m, http.MethodPost, "/api/upstream-pools/coding-ha/probe", "")
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected refresh status %d: %s", response.Code, response.Body.String())
	}
	executions := make([]probeSpec, 0, 2)
	for len(executions) < 2 {
		select {
		case spec := <-calls:
			executions = append(executions, spec)
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("disabled refresh started %d probe executions, want 2", len(executions))
		}
	}
	m.probeWait.Wait()
	gotExecutions := poolRefreshExecutions(executions)
	m.mu.RLock()
	gotWorkers := map[string]config.WorkerConfig{"app": cloneWorkerConfig(m.config.Workers["app"])}
	m.mu.RUnlock()
	m.failoverMu.Lock()
	expiresAt := []time.Time{
		m.readiness[poolCircuitKey("coding-ha", "primary")].ExpiresAt,
		m.readiness[poolCircuitKey("coding-ha", "backup")].ExpiresAt,
	}
	m.failoverMu.Unlock()
	got := struct {
		Status     int
		Executions []poolRefreshExecution
		Readiness  []PoolReadiness
		ExpiresAt  []time.Time
		Circuits   []CircuitStatus
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Exhaustion map[string]string
		Workers    map[string]config.WorkerConfig
	}{response.Code, gotExecutions, []PoolReadiness{
		m.poolReadiness("coding-ha", "primary"), m.poolReadiness("coding-ha", "backup"),
	}, expiresAt, []CircuitStatus{
		m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker),
		m.circuits.Status(poolCircuitKey("coding-ha", "backup"), pool.CircuitBreaker),
	}, maps.Clone(m.probeSchedules), maps.Clone(m.exhaustedPools), gotWorkers}
	checkedAt := now
	wantExpiresAt := now.Add(16 * time.Minute)
	want := struct {
		Status     int
		Executions []poolRefreshExecution
		Readiness  []PoolReadiness
		ExpiresAt  []time.Time
		Circuits   []CircuitStatus
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Exhaustion map[string]string
		Workers    map[string]config.WorkerConfig
	}{http.StatusOK, []poolRefreshExecution{
		{probeExecutionKey{Upstream: "backup", ProxyURL: "http://proxy.example"}, "backup", "http://proxy.example", nil, ProbeScheduleManual},
		{probeExecutionKey{Upstream: "primary", ProxyURL: "http://proxy.example"}, "primary", "http://proxy.example", nil, ProbeScheduleManual},
	}, []PoolReadiness{
		{Upstream: "primary", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol, Authoritative: true, Readiness: ReadinessStateReady, CheckedAt: &checkedAt, OK: true, StatusCode: http.StatusOK, LatencyMS: 4},
		{Upstream: "backup", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol, Authoritative: true, Readiness: ReadinessStateReady, Eligible: true, CheckedAt: &checkedAt, OK: true, StatusCode: http.StatusOK, LatencyMS: 4},
	}, []time.Time{wantExpiresAt, wantExpiresAt}, wantCircuits, wantSchedules, wantExhaustion, wantWorkers}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("disabled refresh changed managed state:\n got %#v\nwant %#v", got, want)
	}

	eventsBeforeExpiry := len(poolRoutingEvents(m, EventUpstreamProbed))
	m.failoverMu.Lock()
	primaryObservation := m.readiness[poolCircuitKey("coding-ha", "primary")]
	backupObservation := m.readiness[poolCircuitKey("coding-ha", "backup")]
	m.readinessTimers[poolCircuitKey("coding-ha", "primary")].Stop()
	m.readinessTimers[poolCircuitKey("coding-ha", "backup")].Stop()
	m.failoverMu.Unlock()
	now = wantExpiresAt
	m.expirePoolReadiness("coding-ha", "primary", primaryObservation.Generation, primaryObservation.CheckedAt)
	m.expirePoolReadiness("coding-ha", "backup", backupObservation.Generation, backupObservation.CheckedAt)
	expiryEvents := poolRoutingEvents(m, EventUpstreamProbed)[eventsBeforeExpiry:]
	gotExpired := struct {
		Readiness []PoolReadiness
		Events    []map[string]any
	}{[]PoolReadiness{
		m.poolReadiness("coding-ha", "primary"), m.poolReadiness("coding-ha", "backup"),
	}, expiryEvents}
	wantExpired := struct {
		Readiness []PoolReadiness
		Events    []map[string]any
	}{[]PoolReadiness{
		{Upstream: "primary", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol, Authoritative: true, Readiness: ReadinessStateUnknown, CheckedAt: &checkedAt, OK: true, StatusCode: http.StatusOK, LatencyMS: 4, Stale: true},
		{Upstream: "backup", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol, Authoritative: true, Readiness: ReadinessStateUnknown, CheckedAt: &checkedAt, OK: true, StatusCode: http.StatusOK, LatencyMS: 4, Stale: true},
	}, []map[string]any{
		{"upstream": "primary", "pool": "coding-ha", "mode": upstream.ProbeModeProtocol, "authoritative": true, "readiness": ReadinessStateUnknown, "eligible": false, "checked_at": checkedAt.Format(time.RFC3339), "ok": true, "status_code": http.StatusOK, "latency_ms": int64(4), "stale": true, "probe_state": PoolProbeStatePaused, "reason": ProbeScheduleManual},
		{"upstream": "backup", "pool": "coding-ha", "mode": upstream.ProbeModeProtocol, "authoritative": true, "readiness": ReadinessStateUnknown, "eligible": false, "checked_at": checkedAt.Format(time.RFC3339), "ok": true, "status_code": http.StatusOK, "latency_ms": int64(4), "stale": true, "probe_state": PoolProbeStatePaused, "reason": ProbeScheduleManual},
	}}
	if !reflect.DeepEqual(gotExpired, wantExpired) {
		t.Fatalf("unexpected disabled refresh expiry:\n got %#v\nwant %#v", gotExpired, wantExpired)
	}
}

func TestManagerPoolRefreshRejectsStaleIdentityResult(t *testing.T) {
	m := newPoolActionTestManager(t, config.UpstreamPoolModeDisabled, []string{"primary"}, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy.example"},
	})
	defer m.Close()
	started := make(chan probeSpec, 1)
	release := make(chan struct{})
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		started <- spec
		<-release
		return readinessTestSuccess(9)
	}

	probeResponse := requestManager(t, m, http.MethodPost, "/api/upstream-pools/coding-ha/probe", "")
	if probeResponse.Code != http.StatusOK {
		t.Fatalf("unexpected refresh status %d: %s", probeResponse.Code, probeResponse.Body.String())
	}
	var stale probeSpec
	select {
	case stale = <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("disabled refresh did not start a manual probe")
	}
	patchResponse := requestManager(t, m, http.MethodPatch, "/api/upstreams/primary", `{"protocol_probe":{"model":"model-new"}}`)
	close(release)
	m.probeWait.Wait()
	got := struct {
		Statuses   []int
		StaleModel string
		Readiness  PoolReadiness
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Events     []map[string]any
	}{[]int{probeResponse.Code, patchResponse.Code}, stale.Model, m.poolReadiness("coding-ha", "primary"), maps.Clone(m.probeSchedules), poolRoutingEvents(m, EventUpstreamProbed)}
	want := struct {
		Statuses   []int
		StaleModel string
		Readiness  PoolReadiness
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Events     []map[string]any
	}{[]int{http.StatusOK, http.StatusOK}, "model-primary", PoolReadiness{
		Upstream: "primary", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol,
		Authoritative: true, Readiness: ReadinessStateUnknown,
	}, map[poolProbeScheduleKey]poolProbeSchedule{}, nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stale manual result changed readiness:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPoolProbeRechecksDeletedPoolUnderAuthority(t *testing.T) {
	m := newPoolActionTestManager(t, config.UpstreamPoolModeActive, []string{"primary"}, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	m.cancelProbes()
	defer m.Close()
	m.mu.RLock()
	_, routeSawPool := m.config.UpstreamPools["coding-ha"]
	m.mu.RUnlock()
	if !routeSawPool {
		t.Fatal("route-level setup did not observe pool")
	}
	m.updateConfig(func(cfg *config.Config) { delete(cfg.UpstreamPools, "coding-ha") })
	wantAuthority := struct {
		Readiness map[string]readinessObservation
		Schedules map[poolProbeScheduleKey]poolProbeSchedule
		Desired   map[probeExecutionKey]probeSpec
		Manual    map[probeExecutionKey]probeSpec
		Pending   map[probeExecutionKey]probeSpec
	}{maps.Clone(m.readiness), maps.Clone(m.probeSchedules), maps.Clone(m.desiredProbes), maps.Clone(m.manualProbes), maps.Clone(m.pendingProbes)}

	response := httptest.NewRecorder()
	m.handleUpstreamPoolProbe(response, httptest.NewRequest(http.MethodPost, "http://manager.local/api/upstream-pools/coding-ha/probe", nil), "coding-ha")
	got := struct {
		Code      int
		Authority struct {
			Readiness map[string]readinessObservation
			Schedules map[poolProbeScheduleKey]poolProbeSchedule
			Desired   map[probeExecutionKey]probeSpec
			Manual    map[probeExecutionKey]probeSpec
			Pending   map[probeExecutionKey]probeSpec
		}
	}{response.Code, struct {
		Readiness map[string]readinessObservation
		Schedules map[poolProbeScheduleKey]poolProbeSchedule
		Desired   map[probeExecutionKey]probeSpec
		Manual    map[probeExecutionKey]probeSpec
		Pending   map[probeExecutionKey]probeSpec
	}{maps.Clone(m.readiness), maps.Clone(m.probeSchedules), maps.Clone(m.desiredProbes), maps.Clone(m.manualProbes), maps.Clone(m.pendingProbes)}}
	want := struct {
		Code      int
		Authority struct {
			Readiness map[string]readinessObservation
			Schedules map[poolProbeScheduleKey]poolProbeSchedule
			Desired   map[probeExecutionKey]probeSpec
			Manual    map[probeExecutionKey]probeSpec
			Pending   map[probeExecutionKey]probeSpec
		}
	}{http.StatusNotFound, wantAuthority}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted pool probe acquired authority:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPoolRefreshActiveWithoutWorkersUsesIdleAuthority(t *testing.T) {
	now := time.Date(2026, time.July, 13, 10, 11, 12, 0, time.UTC)
	m := newPoolActionTestManager(t, config.UpstreamPoolModeActive, []string{"primary"}, nil)
	defer m.Close()
	m.clock = func() time.Time { return now }
	calls := make(chan probeSpec, 1)
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		calls <- spec
		return readinessTestSuccess(6)
	}

	response := requestManager(t, m, http.MethodPost, "/api/upstream-pools/coding-ha/probe", "")
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected idle refresh status %d: %s", response.Code, response.Body.String())
	}
	m.probeWait.Wait()
	execution := <-calls
	checkedAt := now
	expiresAt := now.Add(16 * time.Minute)
	fresh := m.poolReadiness("coding-ha", "primary")
	m.failoverMu.Lock()
	observation := m.readiness[poolCircuitKey("coding-ha", "primary")]
	m.readinessTimers[poolCircuitKey("coding-ha", "primary")].Stop()
	m.failoverMu.Unlock()
	now = expiresAt
	m.expirePoolReadiness("coding-ha", "primary", observation.Generation, observation.CheckedAt)
	got := struct {
		Status     int
		Execution  poolRefreshExecution
		Fresh      PoolReadiness
		Stale      PoolReadiness
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Manual     map[probeExecutionKey]probeSpec
		ProbeState PoolProbeState
		Events     []map[string]any
	}{response.Code, poolRefreshExecutions([]probeSpec{execution})[0], fresh, m.poolReadiness("coding-ha", "primary"), maps.Clone(m.probeSchedules), maps.Clone(m.manualProbes), m.poolProbeStateLocked("coding-ha"), poolRoutingEvents(m, EventUpstreamProbed)}
	want := struct {
		Status     int
		Execution  poolRefreshExecution
		Fresh      PoolReadiness
		Stale      PoolReadiness
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Manual     map[probeExecutionKey]probeSpec
		ProbeState PoolProbeState
		Events     []map[string]any
	}{http.StatusOK, poolRefreshExecution{
		Key: probeExecutionKey{Upstream: "primary"}, Upstream: "primary", Reason: ProbeScheduleManual,
	}, PoolReadiness{
		Upstream: "primary", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol, Authoritative: true,
		Readiness: ReadinessStateReady, Eligible: true, CheckedAt: &checkedAt,
		OK: true, StatusCode: http.StatusOK, LatencyMS: 6,
	}, PoolReadiness{
		Upstream: "primary", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol, Authoritative: true,
		Readiness: ReadinessStateUnknown, CheckedAt: &checkedAt,
		OK: true, StatusCode: http.StatusOK, LatencyMS: 6, Stale: true,
	}, map[poolProbeScheduleKey]poolProbeSchedule{}, map[probeExecutionKey]probeSpec{}, PoolProbeStateIdle, []map[string]any{
		{"upstream": "primary", "pool": "coding-ha", "mode": upstream.ProbeModeProtocol, "authoritative": true, "readiness": ReadinessStateReady, "eligible": true, "checked_at": checkedAt.Format(time.RFC3339), "ok": true, "status_code": http.StatusOK, "latency_ms": int64(6), "probe_state": PoolProbeStateIdle, "reason": ProbeScheduleManual},
		{"upstream": "primary", "pool": "coding-ha", "mode": upstream.ProbeModeProtocol, "authoritative": true, "readiness": ReadinessStateUnknown, "eligible": false, "checked_at": checkedAt.Format(time.RFC3339), "ok": true, "status_code": http.StatusOK, "latency_ms": int64(6), "stale": true, "probe_state": PoolProbeStateIdle, "reason": ProbeScheduleManual},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected idle manual refresh lifecycle:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerInvalidatePoolProbeIdentityClearsEveryAuthority(t *testing.T) {
	m := newPoolActionTestManager(t, config.UpstreamPoolModeDisabled, []string{"primary"}, nil)
	defer m.Close()
	key := probeExecutionKey{Upstream: "primary", ProxyURL: "http://proxy.example"}
	scheduled := readinessTestProbeSpec("coding-ha", "primary", key.ProxyURL, 7, "model-primary")
	manual := scheduled
	manual.Pools = nil
	manual.ManualPools = []string{"coding-ha"}
	pending := scheduled
	pending.ManualPools = []string{"coding-ha"}
	m.failoverMu.Lock()
	m.desiredProbes[key] = scheduled
	m.manualProbes[key] = manual
	m.pendingProbes[key] = pending
	m.probeGenerations[key] = 7
	m.invalidatePoolProbeIdentityLocked("coding-ha")
	got := struct {
		Generation int
		Desired    map[probeExecutionKey]probeSpec
		Manual     map[probeExecutionKey]probeSpec
		Pending    map[probeExecutionKey]probeSpec
	}{m.probeGenerations[key], maps.Clone(m.desiredProbes), maps.Clone(m.manualProbes), maps.Clone(m.pendingProbes)}
	m.failoverMu.Unlock()
	want := struct {
		Generation int
		Desired    map[probeExecutionKey]probeSpec
		Manual     map[probeExecutionKey]probeSpec
		Pending    map[probeExecutionKey]probeSpec
	}{8, map[probeExecutionKey]probeSpec{}, map[probeExecutionKey]probeSpec{}, map[probeExecutionKey]probeSpec{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("identity invalidation retained probe authority:\n got %#v\nwant %#v", got, want)
	}
}

func poolRefreshExecutions(specs []probeSpec) []poolRefreshExecution {
	slices.SortFunc(specs, func(a probeSpec, b probeSpec) int {
		if a.Upstream < b.Upstream {
			return -1
		}
		if a.Upstream > b.Upstream {
			return 1
		}
		return 0
	})
	executions := make([]poolRefreshExecution, len(specs))
	for index, spec := range specs {
		executions[index] = poolRefreshExecution{spec.Key, spec.Upstream, spec.ProxyURL, spec.Pools, spec.Reason}
	}
	return executions
}
