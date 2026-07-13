package manager

import (
	"maps"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
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
	m, pool := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy.example"},
		"cli": {Port: 6768, Upstream: "primary", UpstreamPool: "research-ha", ProxyURL: "http://proxy.example"},
	})
	defer m.Close()
	m.updateConfig(func(cfg *config.Config) { cfg.UpstreamPools["research-ha"] = pool })
	future := time.Now().Add(time.Hour)
	for _, poolName := range []string{"coding-ha", "research-ha"} {
		for _, upstreamName := range pool.Upstreams {
			m.probeSchedules[poolProbeScheduleKey{Pool: poolName, Upstream: upstreamName}] = poolProbeSchedule{NextProbeAt: future, Reason: ProbeScheduleStable}
		}
	}
	m.probeAllUpstreams(t.Context())

	response := patchPoolRouting(t, m, "/api/workers/app", `{"upstream_pool":""}`)
	got := map[probeExecutionKey][]string{}
	for key, spec := range m.desiredProbes {
		got[key] = spec.Pools
	}
	want := map[probeExecutionKey][]string{
		{Upstream: "primary", ProxyURL: "http://proxy.example"}: {"research-ha"},
		{Upstream: "backup", ProxyURL: "http://proxy.example"}:  {"research-ha"},
	}
	if response.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("worker detach lost shared probe identity: status %d body %s got %#v want %#v", response.Code, strings.TrimSpace(response.Body.String()), got, want)
	}
}
