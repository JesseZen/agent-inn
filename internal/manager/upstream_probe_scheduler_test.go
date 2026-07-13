package manager

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
)

const (
	schedulerTestCredentialFingerprint = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	schedulerTestModelAFingerprint     = "49d290629f361cb17e64877a9d91e0d5e6efead3bf08ac5c9561b6544f83b8d3"
	schedulerTestModelBFingerprint     = "3ee7c54efe5bcf8ee9bb79db5df8d90900b449f8af2a4b0b4ec707e3bd39fb4c"
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
	m.failoverMu.Lock()
	schedules := maps.Clone(m.probeSchedules)
	m.failoverMu.Unlock()
	got := struct {
		First     probeSpec
		Second    probeSpec
		Circuit   CircuitStatus
		Readiness PoolReadiness
		Schedules map[poolProbeScheduleKey]poolProbeSchedule
	}{
		First:     first,
		Second:    second,
		Circuit:   m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker),
		Readiness: m.poolReadiness("coding-ha", "primary"),
		Schedules: schedules,
	}
	want := struct {
		First     probeSpec
		Second    probeSpec
		Circuit   CircuitStatus
		Readiness PoolReadiness
		Schedules map[poolProbeScheduleKey]poolProbeSchedule
	}{
		First:   schedulerTestExpectedProbe(t, "model-a", schedulerTestModelAFingerprint, 1, []string{"coding-ha"}, ProbeScheduleStartup),
		Second:  schedulerTestExpectedProbe(t, "model-b", schedulerTestModelBFingerprint, 2, []string{"coding-ha"}, ProbeScheduleStartup),
		Circuit: wantCircuit,
		Readiness: PoolReadiness{
			Upstream:      "primary",
			Pool:          "coding-ha",
			Mode:          upstream.ProbeModeProtocol,
			Authoritative: true,
			Readiness:     ReadinessStateUnknown,
		},
		Schedules: map[poolProbeScheduleKey]poolProbeSchedule{},
	}
	if !reflect.DeepEqual(got, want) {
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

	m.probeAllUpstreams(t.Context())
	m.probeWait.Wait()
	wantStartup := schedulerTestExpectedProbe(t, "model-a", schedulerTestModelAFingerprint, 1, []string{"coding-ha"}, ProbeScheduleStartup)
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
	startupEvents := poolRoutingEvents(m, EventUpstreamProbed)
	wantStartupEvents := []map[string]any{{
		"upstream": "primary", "pool": "coding-ha", "mode": upstream.ProbeModeProtocol,
		"authoritative": true, "readiness": ReadinessStateReady, "eligible": true,
		"checked_at": now.Format(time.RFC3339), "ok": true, "status_code": http.StatusOK,
		"latency_ms": int64(1), "probe_state": PoolProbeStateStable,
		"next_probe_at": wantSchedule.NextProbeAt.Format(time.RFC3339), "reason": ProbeScheduleStartup,
	}}
	if !reflect.DeepEqual(startupEvents, wantStartupEvents) {
		t.Fatalf("startup authority did not publish complete event:\n got %#v\nwant %#v", startupEvents, wantStartupEvents)
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
	wantExecutions := make([]probeSpec, 0, 6)
	wantEvents := make([]map[string]any, 0, 6)

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
		wantReason := ProbeScheduleRecovery
		if failure == 0 {
			wantReason = ProbeScheduleStartup
		}
		wantSpec := schedulerTestExpectedProbe(t, "model-a", schedulerTestModelAFingerprint, 1, []string{"coding-ha"}, wantReason)
		wantExecutions = append(wantExecutions, wantSpec)
		wantSchedule := poolProbeSchedule{
			NextProbeAt:         now.Add(wantDelay),
			ConsecutiveFailures: failure + 1,
			Reason:              ProbeScheduleRecovery,
		}
		wantEvents = append(wantEvents, map[string]any{
			"upstream": "primary", "pool": "coding-ha", "mode": upstream.ProbeModeProtocol,
			"authoritative": true, "readiness": ReadinessStateNotReady, "eligible": false,
			"checked_at": now.Format(time.RFC3339), "ok": false, "status_code": 0,
			"latency_ms": int64(0), "error": "protocol_error", "probe_state": PoolProbeStateAlert,
			"next_probe_at": wantSchedule.NextProbeAt.Format(time.RFC3339), "reason": wantReason,
		})
		got := struct {
			Executions []probeSpec
			Schedules  []poolProbeSchedule
			Events     []map[string]any
		}{append([]probeSpec(nil), executions...), []poolProbeSchedule{m.probeSchedules[key]}, poolRoutingEvents(m, EventUpstreamProbed)}
		want := struct {
			Executions []probeSpec
			Schedules  []poolProbeSchedule
			Events     []map[string]any
		}{append([]probeSpec(nil), wantExecutions...), []poolProbeSchedule{wantSchedule}, append([]map[string]any(nil), wantEvents...)}
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
	wantSpec := schedulerTestExpectedProbe(t, "model-a", schedulerTestModelAFingerprint, 1, []string{"coding-ha", "research-ha"}, ProbeScheduleRecovery)
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
	wantSpec := schedulerTestExpectedProbe(t, "model-a", schedulerTestModelAFingerprint, 1, []string{"coding-ha"}, ProbeScheduleStartup)
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
	t.Setenv("PRIMARY_API_KEY", "")
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

func schedulerTestExpectedProbe(t *testing.T, model string, fingerprint string, generation int, pools []string, reason ProbeScheduleReason) probeSpec {
	t.Helper()
	baseURL, err := url.Parse("https://primary.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	return probeSpec{
		Key:                   probeExecutionKey{Upstream: "primary"},
		Upstream:              "primary",
		Compiled:              upstream.Compiled{ID: "primary", BaseURL: baseURL},
		CredentialFingerprint: schedulerTestCredentialFingerprint,
		Model:                 model,
		Generation:            generation,
		Fingerprint:           fingerprint,
		Pools:                 pools,
		Due:                   true,
		Reason:                reason,
	}
}
