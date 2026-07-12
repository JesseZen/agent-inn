package manager

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
)

func TestRunProtocolProbeInvalidProxyIsNonAuthoritative(t *testing.T) {
	got := runProtocolProbe(t.Context(), probeSpec{ProxyURL: "://invalid"})
	want := upstream.ProbeResult{Error: "connection_error", Mode: upstream.ProbeModeProtocol}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid proxy acquired authority:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerRejectsStaleProbeGeneration(t *testing.T) {
	m := newSchedulerTestManager(t)
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})
	calls := make(chan probeSpec, 2)
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		calls <- spec
		if spec.Model == "model-a" {
			<-releaseA
		} else {
			<-releaseB
		}
		return upstream.ProbeResult{
			OK:            true,
			StatusCode:    http.StatusOK,
			Mode:          upstream.ProbeModeProtocol,
			Authoritative: true,
		}
	}
	pool := m.config.UpstreamPools["coding-ha"]
	m.circuits.RecordFailure(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker)
	wantCircuit := m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker)

	m.probeAllUpstreams(t.Context())
	first := <-calls
	m.mu.Lock()
	profile := m.config.Upstreams["primary"]
	profile.ProtocolProbe.Model = "model-b"
	m.config.Upstreams["primary"] = profile
	m.mu.Unlock()
	m.probeAllUpstreams(t.Context())
	select {
	case unexpected := <-calls:
		t.Fatalf("replacement ran before stale probe exited: %#v", unexpected)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseA)
	second := <-calls
	got := struct {
		First     probeSpec
		Second    probeSpec
		Circuit   CircuitStatus
		Readiness PoolReadiness
		Schedules []poolProbeSchedule
	}{
		First:     first,
		Second:    second,
		Circuit:   m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker),
		Readiness: m.poolReadiness("coding-ha", "primary"),
		Schedules: []poolProbeSchedule{},
	}
	want := struct {
		First     probeSpec
		Second    probeSpec
		Circuit   CircuitStatus
		Readiness PoolReadiness
		Schedules []poolProbeSchedule
	}{
		First:   first,
		Second:  second,
		Circuit: wantCircuit,
		Readiness: PoolReadiness{
			Upstream:      "primary",
			Pool:          "coding-ha",
			Mode:          upstream.ProbeModeProtocol,
			Authoritative: true,
			Readiness:     ReadinessStateUnknown,
		},
		Schedules: []poolProbeSchedule{},
	}
	if first.Model != "model-a" || second.Model != "model-b" || second.Generation <= first.Generation || !reflect.DeepEqual(got, want) {
		t.Fatalf("stale probe changed Manager state:\n got %#v\nwant %#v", got, want)
	}
	close(releaseB)
	eventually(t, time.Second, func() bool {
		return m.poolReadiness("coding-ha", "primary").Readiness == ReadinessStateReady
	})
	m.Close()
}

func TestManagerRunsPendingReplacementProbe(t *testing.T) {
	m := newSchedulerTestManager(t)
	defer m.Close()
	releaseA := make(chan struct{})
	calls := make(chan probeSpec, 2)
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		calls <- spec
		if spec.Model == "model-a" {
			<-releaseA
		}
		return upstream.ProbeResult{
			OK:            true,
			StatusCode:    http.StatusOK,
			Mode:          upstream.ProbeModeProtocol,
			Authoritative: true,
		}
	}

	m.probeAllUpstreams(t.Context())
	first := <-calls
	m.mu.Lock()
	profile := m.config.Upstreams["primary"]
	profile.ProtocolProbe.Model = "model-b"
	m.config.Upstreams["primary"] = profile
	m.mu.Unlock()
	m.probeAllUpstreams(t.Context())
	close(releaseA)
	second := <-calls
	eventually(t, time.Second, func() bool {
		return m.poolReadiness("coding-ha", "primary").Readiness == ReadinessStateReady
	})
	got := struct {
		Models      []string
		Generations []int
		Readiness   PoolReadiness
	}{
		Models:      []string{first.Model, second.Model},
		Generations: []int{first.Generation, second.Generation},
		Readiness:   m.poolReadiness("coding-ha", "primary"),
	}
	want := struct {
		Models      []string
		Generations []int
		Readiness   PoolReadiness
	}{
		Models:      []string{"model-a", "model-b"},
		Generations: []int{first.Generation, first.Generation + 1},
		Readiness:   got.Readiness,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected replacement probe execution:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerAdaptiveProbeCadence(t *testing.T) {
	now := time.Date(2026, time.July, 13, 1, 2, 3, 0, time.UTC)
	m := newSchedulerTestManager(t)
	defer m.Close()
	m.clock = func() time.Time { return now }
	executions := []probeSpec{}
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		executions = append(executions, spec)
		return readinessTestSuccess(1)
	}
	key := poolProbeScheduleKey{Pool: "coding-ha", Upstream: "primary"}
	executionKey := probeExecutionKey{Upstream: "primary"}

	m.probeAllUpstreams(t.Context())
	m.probeWait.Wait()
	wantStartup := m.desiredProbes[executionKey]
	wantStartup.Due = true
	wantStartup.Reason = ProbeScheduleStartup
	wantSchedule := poolProbeSchedule{
		NextProbeAt: now.Add(time.Duration(config.DefaultPoolProbeStableIntervalSeconds) * time.Second),
		Reason:      ProbeScheduleStable,
	}
	got := struct {
		Executions []probeSpec
		Schedules  []poolProbeSchedule
	}{append([]probeSpec(nil), executions...), []poolProbeSchedule{m.probeSchedules[key]}}
	want := struct {
		Executions []probeSpec
		Schedules  []poolProbeSchedule
	}{[]probeSpec{wantStartup}, []poolProbeSchedule{wantSchedule}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected startup cadence:\n got %#v\nwant %#v", got, want)
	}

	now = now.Add(defaultUpstreamProbeInterval)
	m.probeAllUpstreams(t.Context())
	m.probeWait.Wait()
	got = struct {
		Executions []probeSpec
		Schedules  []poolProbeSchedule
	}{append([]probeSpec(nil), executions...), []poolProbeSchedule{m.probeSchedules[key]}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stable probe ran before its deadline:\n got %#v\nwant %#v", got, want)
	}

	now = wantSchedule.NextProbeAt
	m.probeAllUpstreams(t.Context())
	m.probeWait.Wait()
	wantStable := wantStartup
	wantStable.Reason = ProbeScheduleStable
	wantSchedule.NextProbeAt = now.Add(time.Duration(config.DefaultPoolProbeStableIntervalSeconds) * time.Second)
	want.Executions = append(want.Executions, wantStable)
	want.Schedules = []poolProbeSchedule{wantSchedule}
	got = struct {
		Executions []probeSpec
		Schedules  []poolProbeSchedule
	}{append([]probeSpec(nil), executions...), []poolProbeSchedule{m.probeSchedules[key]}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stable probe did not run at its deadline:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerProbeFailureBackoffCapsAtStableInterval(t *testing.T) {
	now := time.Date(2026, time.July, 13, 1, 2, 3, 0, time.UTC)
	m := newSchedulerTestManager(t)
	defer m.Close()
	m.clock = func() time.Time { return now }
	var executions []probeSpec
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		executions = append(executions, spec)
		return upstream.ProbeResult{Error: "protocol_error", Mode: upstream.ProbeModeProtocol, Authoritative: true}
	}
	key := poolProbeScheduleKey{Pool: "coding-ha", Upstream: "primary"}
	executionKey := probeExecutionKey{Upstream: "primary"}
	wantExecutions := make([]probeSpec, 0, 6)

	for failure, wantDelay := range []time.Duration{
		time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		15 * time.Minute,
		15 * time.Minute,
	} {
		m.probeAllUpstreams(t.Context())
		m.probeWait.Wait()
		wantSpec := m.desiredProbes[executionKey]
		wantSpec.Due = true
		wantSpec.Reason = ProbeScheduleRecovery
		if failure == 0 {
			wantSpec.Reason = ProbeScheduleStartup
		}
		wantExecutions = append(wantExecutions, wantSpec)
		wantSchedule := poolProbeSchedule{
			NextProbeAt:         now.Add(wantDelay),
			ConsecutiveFailures: failure + 1,
			Reason:              ProbeScheduleRecovery,
		}
		got := struct {
			Executions []probeSpec
			Schedules  []poolProbeSchedule
		}{append([]probeSpec(nil), executions...), []poolProbeSchedule{m.probeSchedules[key]}}
		want := struct {
			Executions []probeSpec
			Schedules  []poolProbeSchedule
		}{append([]probeSpec(nil), wantExecutions...), []poolProbeSchedule{wantSchedule}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected failure cadence after failure %d:\n got %#v\nwant %#v", failure+1, got, want)
		}
		now = wantSchedule.NextProbeAt
	}
}

func TestManagerIdleAndDisabledPoolsDoNotProbe(t *testing.T) {
	m := newSchedulerTestManager(t)
	defer m.Close()
	m.mu.Lock()
	disabled := m.config.UpstreamPools["coding-ha"]
	disabled.Mode = config.UpstreamPoolModeDisabled
	m.config.UpstreamPools["coding-ha"] = disabled
	m.config.Upstreams["backup"] = config.UpstreamProfile{
		BaseURL: "https://backup.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-b"},
	}
	m.config.UpstreamPools["idle"] = config.UpstreamPool{Upstreams: []string{"backup"}}
	m.mu.Unlock()
	var executions []probeSpec
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		executions = append(executions, spec)
		return readinessTestSuccess(1)
	}

	m.probeAllUpstreams(t.Context())
	m.probeWait.Wait()
	got := struct {
		Executions []probeSpec
		Schedules  []poolProbeSchedule
	}{append([]probeSpec(nil), executions...), []poolProbeSchedule{}}
	want := struct {
		Executions []probeSpec
		Schedules  []poolProbeSchedule
	}{nil, []poolProbeSchedule{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("idle or disabled pool probed:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerSharedProbeUsesEarliestPoolDeadline(t *testing.T) {
	now := time.Date(2026, time.July, 13, 1, 2, 3, 0, time.UTC)
	m := newSchedulerTestManager(t)
	defer m.Close()
	m.clock = func() time.Time { return now }
	m.mu.Lock()
	m.config.Workers["cli"] = config.WorkerConfig{Port: 6768, Upstream: "primary", UpstreamPool: "research-ha"}
	m.config.UpstreamPools["research-ha"] = m.config.UpstreamPools["coding-ha"]
	m.mu.Unlock()
	stableDeadline := now.Add(time.Duration(config.DefaultPoolProbeStableIntervalSeconds) * time.Second)
	m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: "primary"}] = poolProbeSchedule{
		NextProbeAt: stableDeadline,
		Reason:      ProbeScheduleStable,
	}
	m.probeSchedules[poolProbeScheduleKey{Pool: "research-ha", Upstream: "primary"}] = poolProbeSchedule{
		NextProbeAt:         now,
		ConsecutiveFailures: 2,
		Reason:              ProbeScheduleRecovery,
	}
	var executions []probeSpec
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		executions = append(executions, spec)
		return readinessTestSuccess(1)
	}

	m.probeAllUpstreams(t.Context())
	m.probeWait.Wait()
	wantSpec := m.desiredProbes[probeExecutionKey{Upstream: "primary"}]
	wantSpec.Due = true
	wantSpec.Reason = ProbeScheduleRecovery
	wantSchedules := []poolProbeSchedule{
		{NextProbeAt: stableDeadline, Reason: ProbeScheduleStable},
		{NextProbeAt: stableDeadline, Reason: ProbeScheduleStable},
	}
	got := struct {
		Executions []probeSpec
		Schedules  []poolProbeSchedule
	}{append([]probeSpec(nil), executions...), []poolProbeSchedule{
		m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: "primary"}],
		m.probeSchedules[poolProbeScheduleKey{Pool: "research-ha", Upstream: "primary"}],
	}}
	want := struct {
		Executions []probeSpec
		Schedules  []poolProbeSchedule
	}{[]probeSpec{wantSpec}, wantSchedules}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shared probe did not use the earliest deadline:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerOpenCircuitWaitsForRecoveryWindow(t *testing.T) {
	now := time.Date(2026, time.July, 13, 1, 2, 3, 0, time.UTC)
	m := newSchedulerTestManager(t)
	defer m.Close()
	m.clock = func() time.Time { return now }
	m.mu.Lock()
	pool := m.config.UpstreamPools["coding-ha"]
	pool.CircuitBreaker.RecoveryWaitSeconds = 5 * int(time.Minute/time.Second)
	m.config.UpstreamPools["coding-ha"] = pool
	m.mu.Unlock()
	key := poolCircuitKey("coding-ha", "primary")
	for failure := 0; failure < pool.CircuitBreaker.FailureThreshold; failure++ {
		m.circuits.RecordFailure(key, pool.CircuitBreaker)
	}
	opened := m.circuits.Status(key, pool.CircuitBreaker)
	var executions []probeSpec
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		executions = append(executions, spec)
		return upstream.ProbeResult{Error: "protocol_error", Mode: upstream.ProbeModeProtocol, Authoritative: true}
	}
	scheduleKey := poolProbeScheduleKey{Pool: "coding-ha", Upstream: "primary"}

	m.probeAllUpstreams(t.Context())
	m.probeWait.Wait()
	wantSpec := m.desiredProbes[probeExecutionKey{Upstream: "primary"}]
	wantSpec.Due = true
	wantSpec.Reason = ProbeScheduleStartup
	recoveryDeadline := opened.OpenedAt.Add(time.Duration(pool.CircuitBreaker.RecoveryWaitSeconds) * time.Second)
	wantSchedule := poolProbeSchedule{NextProbeAt: recoveryDeadline, ConsecutiveFailures: 1, Reason: ProbeScheduleRecovery}
	got := struct {
		Executions []probeSpec
		Schedules  []poolProbeSchedule
	}{append([]probeSpec(nil), executions...), []poolProbeSchedule{m.probeSchedules[scheduleKey]}}
	want := struct {
		Executions []probeSpec
		Schedules  []poolProbeSchedule
	}{[]probeSpec{wantSpec}, []poolProbeSchedule{wantSchedule}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("open circuit did not retain its recovery deadline:\n got %#v\nwant %#v", got, want)
	}

	now = now.Add(defaultUpstreamProbeInterval)
	m.probeAllUpstreams(t.Context())
	m.probeWait.Wait()
	got = struct {
		Executions []probeSpec
		Schedules  []poolProbeSchedule
	}{append([]probeSpec(nil), executions...), []poolProbeSchedule{m.probeSchedules[scheduleKey]}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("open circuit probed before recovery window:\n got %#v\nwant %#v", got, want)
	}

	now = recoveryDeadline
	m.probeAllUpstreams(t.Context())
	m.probeWait.Wait()
	wantRecovery := wantSpec
	wantRecovery.Reason = ProbeScheduleRecovery
	want.Executions = append(want.Executions, wantRecovery)
	wantSchedule.NextProbeAt = now.Add(time.Duration(pool.CircuitBreaker.RecoveryWaitSeconds) * time.Second)
	wantSchedule.ConsecutiveFailures = 2
	want.Schedules = []poolProbeSchedule{wantSchedule}
	got = struct {
		Executions []probeSpec
		Schedules  []poolProbeSchedule
	}{append([]probeSpec(nil), executions...), []poolProbeSchedule{m.probeSchedules[scheduleKey]}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("open circuit did not probe at recovery window:\n got %#v\nwant %#v", got, want)
	}
}

func newSchedulerTestManager(t *testing.T) *Manager {
	t.Helper()
	return New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: t.TempDir()},
		Workers: map[string]config.WorkerConfig{
			"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"primary": {BaseURL: "https://primary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-a"}},
		},
		UpstreamPools: map[string]config.UpstreamPool{
			"coding-ha": {
				Upstreams: []string{"primary"},
				CircuitBreaker: config.CircuitBreakerConfig{
					FailureThreshold:         3,
					RecoverySuccessThreshold: 2,
					RecoveryWaitSeconds:      60,
				},
			},
		},
	}})
}
