package manager

import (
	"context"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
)

func TestManagerPooledWorkerCreateActivatesConfigSchedules(t *testing.T) {
	now := time.Date(2026, time.July, 13, 13, 14, 15, 0, time.UTC)
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{})
	m.cancelProbes()
	defer m.Close()
	m.clock = func() time.Time { return now }
	for _, upstreamName := range []string{"primary", "backup"} {
		m.readiness[poolCircuitKey("coding-ha", upstreamName)] = readinessObservation{Result: readinessTestFailure(), CheckedAt: now, ExpiresAt: now.Add(time.Hour)}
		m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: upstreamName}] = poolProbeSchedule{
			NextProbeAt: now.Add(time.Hour), ConsecutiveFailures: 2, Reason: ProbeScheduleRecovery,
		}
	}
	m.exhaustedPools["coding-ha"] = "primary"
	stale := readinessTestProbeSpec("coding-ha", "primary", "http://proxy.example", 4, "model-a")
	m.desiredProbes[stale.Key] = stale
	m.manualProbes[stale.Key] = stale
	m.pendingProbes[stale.Key] = stale
	response := requestManager(t, m, http.MethodPost, "/api/workers", `{"name":"app","port":6767,"upstream":"primary","upstream_pool":"coding-ha","proxy_url":"http://proxy.example"}`)
	got := struct {
		Code       int
		Readiness  map[string]readinessObservation
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Manual     map[probeExecutionKey]probeSpec
		Pending    map[probeExecutionKey]probeSpec
		Exhaustion map[string]string
	}{response.Code, maps.Clone(m.readiness), maps.Clone(m.probeSchedules), maps.Clone(m.manualProbes), maps.Clone(m.pendingProbes), maps.Clone(m.exhaustedPools)}
	want := struct {
		Code       int
		Readiness  map[string]readinessObservation
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Manual     map[probeExecutionKey]probeSpec
		Pending    map[probeExecutionKey]probeSpec
		Exhaustion map[string]string
	}{http.StatusCreated, map[string]readinessObservation{}, map[poolProbeScheduleKey]poolProbeSchedule{
		{Pool: "coding-ha", Upstream: "primary"}: {NextProbeAt: now, Reason: ProbeScheduleConfig},
		{Pool: "coding-ha", Upstream: "backup"}:  {NextProbeAt: now, Reason: ProbeScheduleConfig},
	}, map[probeExecutionKey]probeSpec{}, map[probeExecutionKey]probeSpec{}, map[string]string{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first pooled worker did not activate fresh config authority:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPooledWorkerPatchOwnsAttachmentTransitions(t *testing.T) {
	now := time.Date(2026, time.July, 13, 14, 15, 16, 0, time.UTC)
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary"},
	})
	m.cancelProbes()
	defer m.Close()
	m.clock = func() time.Time { return now }
	for _, upstreamName := range []string{"primary", "backup"} {
		m.readiness[poolCircuitKey("coding-ha", upstreamName)] = readinessObservation{Result: readinessTestFailure(), CheckedAt: now, ExpiresAt: now.Add(time.Hour)}
		m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: upstreamName}] = poolProbeSchedule{
			NextProbeAt: now.Add(time.Hour), ConsecutiveFailures: 2, Reason: ProbeScheduleRecovery,
		}
	}
	m.exhaustedPools["coding-ha"] = "primary"

	attached := patchPoolRouting(t, m, "/api/workers/app", `{"upstream_pool":"coding-ha"}`)
	gotAttached := struct {
		Code       int
		Pool       string
		Readiness  map[string]readinessObservation
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Exhaustion map[string]string
	}{attached.Code, m.store.Config().Workers["app"].UpstreamPool, maps.Clone(m.readiness), maps.Clone(m.probeSchedules), maps.Clone(m.exhaustedPools)}
	wantAttached := struct {
		Code       int
		Pool       string
		Readiness  map[string]readinessObservation
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Exhaustion map[string]string
	}{http.StatusOK, "coding-ha", map[string]readinessObservation{}, map[poolProbeScheduleKey]poolProbeSchedule{
		{Pool: "coding-ha", Upstream: "primary"}: {NextProbeAt: now, Reason: ProbeScheduleConfig},
		{Pool: "coding-ha", Upstream: "backup"}:  {NextProbeAt: now, Reason: ProbeScheduleConfig},
	}, map[string]string{}}
	if !reflect.DeepEqual(gotAttached, wantAttached) {
		t.Fatalf("worker attach did not acquire pool authority:\n got %#v\nwant %#v", gotAttached, wantAttached)
	}

	for _, upstreamName := range []string{"primary", "backup"} {
		m.readiness[poolCircuitKey("coding-ha", upstreamName)] = readinessObservation{Result: readinessTestSuccess(1), CheckedAt: now, ExpiresAt: now.Add(time.Hour)}
	}
	m.exhaustedPools["coding-ha"] = "primary"
	key := probeExecutionKey{Upstream: "primary"}
	spec := readinessTestProbeSpec("coding-ha", "primary", "", 3, "model-a")
	m.desiredProbes[key] = spec
	m.manualProbes[key] = spec
	m.pendingProbes[key] = spec
	detached := patchPoolRouting(t, m, "/api/workers/app", `{"upstream_pool":""}`)
	gotDetached := struct {
		Code       int
		Pool       string
		Readiness  map[string]readinessObservation
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Desired    map[probeExecutionKey]probeSpec
		Manual     map[probeExecutionKey]probeSpec
		Pending    map[probeExecutionKey]probeSpec
		Exhaustion map[string]string
	}{detached.Code, m.store.Config().Workers["app"].UpstreamPool, maps.Clone(m.readiness), maps.Clone(m.probeSchedules), maps.Clone(m.desiredProbes), maps.Clone(m.manualProbes), maps.Clone(m.pendingProbes), maps.Clone(m.exhaustedPools)}
	wantDetached := struct {
		Code       int
		Pool       string
		Readiness  map[string]readinessObservation
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Desired    map[probeExecutionKey]probeSpec
		Manual     map[probeExecutionKey]probeSpec
		Pending    map[probeExecutionKey]probeSpec
		Exhaustion map[string]string
	}{http.StatusOK, "", map[string]readinessObservation{}, map[poolProbeScheduleKey]poolProbeSchedule{}, map[probeExecutionKey]probeSpec{}, map[probeExecutionKey]probeSpec{}, map[probeExecutionKey]probeSpec{}, map[string]string{}}
	if !reflect.DeepEqual(gotDetached, wantDetached) {
		t.Fatalf("last worker detach retained pool authority:\n got %#v\nwant %#v", gotDetached, wantDetached)
	}
}

func TestManagerPooledWorkerConfigDeleteClearsIdleAuthority(t *testing.T) {
	now := time.Date(2026, time.July, 13, 15, 16, 17, 0, time.UTC)
	m, _ := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	m.cancelProbes()
	defer m.Close()
	for _, upstreamName := range []string{"primary", "backup"} {
		m.readiness[poolCircuitKey("coding-ha", upstreamName)] = readinessObservation{Result: readinessTestSuccess(1), CheckedAt: now, ExpiresAt: now.Add(time.Hour)}
		m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: upstreamName}] = poolProbeSchedule{NextProbeAt: now.Add(time.Hour), Reason: ProbeScheduleStable}
	}
	m.exhaustedPools["coding-ha"] = "primary"
	key := probeExecutionKey{Upstream: "primary"}
	spec := readinessTestProbeSpec("coding-ha", "primary", "", 3, "model-a")
	m.desiredProbes[key] = spec
	m.manualProbes[key] = spec
	m.pendingProbes[key] = spec

	response := requestManager(t, m, http.MethodDelete, "/api/workers/app/config", "")
	got := struct {
		Code       int
		Workers    map[string]config.WorkerConfig
		Readiness  map[string]readinessObservation
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Desired    map[probeExecutionKey]probeSpec
		Manual     map[probeExecutionKey]probeSpec
		Pending    map[probeExecutionKey]probeSpec
		Exhaustion map[string]string
	}{response.Code, m.store.Config().Workers, maps.Clone(m.readiness), maps.Clone(m.probeSchedules), maps.Clone(m.desiredProbes), maps.Clone(m.manualProbes), maps.Clone(m.pendingProbes), maps.Clone(m.exhaustedPools)}
	want := struct {
		Code       int
		Workers    map[string]config.WorkerConfig
		Readiness  map[string]readinessObservation
		Schedules  map[poolProbeScheduleKey]poolProbeSchedule
		Desired    map[probeExecutionKey]probeSpec
		Manual     map[probeExecutionKey]probeSpec
		Pending    map[probeExecutionKey]probeSpec
		Exhaustion map[string]string
	}{http.StatusOK, map[string]config.WorkerConfig{}, map[string]readinessObservation{}, map[poolProbeScheduleKey]poolProbeSchedule{}, map[probeExecutionKey]probeSpec{}, map[probeExecutionKey]probeSpec{}, map[probeExecutionKey]probeSpec{}, map[string]string{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("worker config delete retained idle pool authority:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerWorkerDetachReconcilesSharedProbeIdentity(t *testing.T) {
	now := time.Date(2026, time.July, 14, 2, 3, 4, 0, time.UTC)
	m, pool := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy.example"},
		"cli": {Port: 6768, Upstream: "primary", UpstreamPool: "research-ha", ProxyURL: "http://proxy.example"},
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

	response := patchPoolRouting(t, m, "/api/workers/app", `{"upstream_pool":""}`)
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
		t.Fatalf("worker detach destroyed shared survivor authority: body %s\n got %#v\nwant %#v", strings.TrimSpace(response.Body.String()), got, want)
	}
}
