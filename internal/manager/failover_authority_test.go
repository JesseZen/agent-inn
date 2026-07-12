package manager

import (
	"context"
	"maps"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
	"github.com/jesse/agent-inn/internal/worker"
)

func TestManagerClosedProtocolResultsPreserveWorkerFailures(t *testing.T) {
	m, pool := newAuthorityTestManager(t, "primary", 3)
	defer m.Close()
	key := poolCircuitKey("coding-ha", "primary")
	m.circuits.RecordFailure(key, pool.CircuitBreaker)
	wantCircuit := m.circuits.Status(key, pool.CircuitBreaker)

	authorityObserve(t, m, "primary", upstream.ProbeResult{
		OK: true, StatusCode: http.StatusOK, Mode: upstream.ProbeModeProtocol, Authoritative: true,
	})
	ready := m.poolReadiness("coding-ha", "primary")
	authorityObserve(t, m, "primary", upstream.ProbeResult{
		StatusCode: http.StatusUnauthorized, Error: "auth_error",
		Mode: upstream.ProbeModeProtocol, Authoritative: true,
	})
	got := struct {
		Circuit  CircuitStatus
		Ready    ReadinessState
		NotReady ReadinessState
	}{m.circuits.Status(key, pool.CircuitBreaker), ready.Readiness, m.poolReadiness("coding-ha", "primary").Readiness}
	want := struct {
		Circuit  CircuitStatus
		Ready    ReadinessState
		NotReady ReadinessState
	}{wantCircuit, ReadinessStateReady, ReadinessStateNotReady}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("closed protocol result changed Worker failures:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerProtocolFailuresDoNotAccumulateClosedCircuit(t *testing.T) {
	m, pool := newAuthorityTestManager(t, "primary", 2)
	defer m.Close()
	key := poolCircuitKey("coding-ha", "primary")
	m.circuits.RecordFailure(key, pool.CircuitBreaker)
	want := m.circuits.Status(key, pool.CircuitBreaker)
	for _, result := range []upstream.ProbeResult{
		{Error: "protocol_error", Mode: upstream.ProbeModeProtocol, Authoritative: true},
		{OK: true, StatusCode: http.StatusOK, Mode: upstream.ProbeModeProtocol, Authoritative: true},
		{Error: "protocol_error", Mode: upstream.ProbeModeProtocol, Authoritative: true},
	} {
		authorityObserve(t, m, "primary", result)
	}
	if got := m.circuits.Status(key, pool.CircuitBreaker); !reflect.DeepEqual(got, want) {
		t.Fatalf("protocol failures accumulated on closed circuit:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerExhaustedOpenWorkerOutcomesPreserveCircuit(t *testing.T) {
	m, pool := newAuthorityTestManager(t, "primary", 1)
	defer m.Close()
	authorityObserve(t, m, "primary", readinessTestSuccess(4))
	key := poolCircuitKey("coding-ha", "primary")
	m.circuits.RecordFailure(key, pool.CircuitBreaker)
	wantCircuit := m.circuits.Status(key, pool.CircuitBreaker)
	wantReadiness := m.poolReadiness("coding-ha", "primary")
	m.handleWorkerMetricEvent("app", authorityMetric(1, http.StatusOK, nil))
	m.handleWorkerMetricEvent("app", authorityMetric(1, http.StatusBadGateway, &worker.UpstreamFailure{
		Kind: worker.UpstreamFailureTransport, BeforeFirstByte: true,
	}))
	got := struct {
		Circuit   CircuitStatus
		Readiness PoolReadiness
	}{m.circuits.Status(key, pool.CircuitBreaker), m.poolReadiness("coding-ha", "primary")}
	want := struct {
		Circuit   CircuitStatus
		Readiness PoolReadiness
	}{wantCircuit, wantReadiness}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("open Worker outcome changed probe authority:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerForcedHalfOpenWorkerOutcomesPreserveProbeAuthority(t *testing.T) {
	now := time.Date(2026, time.July, 11, 2, 0, 0, 0, time.UTC)
	m, pool := newAuthorityTestManager(t, "primary", 1)
	defer m.Close()
	m.clock = func() time.Time { return now }
	authorityObserve(t, m, "primary", upstream.ProbeResult{
		Error: "protocol_error", Mode: upstream.ProbeModeProtocol, Authoritative: true,
	})
	key := poolCircuitKey("coding-ha", "primary")
	m.circuits.RecordFailure(key, pool.CircuitBreaker)
	now = now.Add(time.Duration(pool.CircuitBreaker.RecoveryWaitSeconds) * time.Second)
	if !m.circuits.Allow(key, pool.CircuitBreaker) {
		t.Fatal("circuit did not enter half-open state")
	}
	before := m.circuits.Status(key, pool.CircuitBreaker)
	wantReadiness := m.poolReadiness("coding-ha", "primary")
	m.handleWorkerMetricEvent("app", authorityMetric(1, http.StatusOK, nil))
	afterSuccess := m.circuits.Status(key, pool.CircuitBreaker)
	now = now.Add(time.Second)
	m.handleWorkerMetricEvent("app", authorityMetric(1, http.StatusBadGateway, &worker.UpstreamFailure{
		Kind: worker.UpstreamFailureTransport, BeforeFirstByte: true,
	}))
	wantReopened := before
	wantReopened.State, wantReopened.RecoverySuccesses, wantReopened.OpenedAt = CircuitStateOpen, 0, now
	got := struct {
		AfterSuccess CircuitStatus
		AfterFailure CircuitStatus
		Readiness    PoolReadiness
	}{afterSuccess, m.circuits.Status(key, pool.CircuitBreaker), m.poolReadiness("coding-ha", "primary")}
	want := struct {
		AfterSuccess CircuitStatus
		AfterFailure CircuitStatus
		Readiness    PoolReadiness
	}{before, wantReopened, wantReadiness}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("half-open Worker outcome changed probe authority:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerRejectsStaleWorkerMetricGeneration(t *testing.T) {
	now := time.Date(2026, time.July, 11, 2, 0, 0, 0, time.Local)
	m, pool := newAuthorityTestManager(t, "primary", 1)
	defer m.Close()
	m.clock = func() time.Time { return now }
	m.mu.Lock()
	m.generations["app"] = 2
	m.mu.Unlock()
	key := poolCircuitKey("coding-ha", "primary")
	wantCircuit := m.circuits.Status(key, pool.CircuitBreaker)
	metric := authorityMetric(1, http.StatusBadGateway, &worker.UpstreamFailure{
		Kind: worker.UpstreamFailureTransport, BeforeFirstByte: true,
	})
	metric.Timestamp = now
	m.handleWorkerMetricEvent("app", metric)
	response, err := m.metricsStore.Query(MetricsQuery{Range: MetricsRangeToday}, []WorkerSummary{{Name: "app", Port: 6767}})
	if err != nil {
		t.Fatal(err)
	}
	got := struct {
		Circuit CircuitStatus
		Totals  MetricsTotals
	}{m.circuits.Status(key, pool.CircuitBreaker), response.Workers[0].Totals}
	want := struct {
		Circuit CircuitStatus
		Totals  MetricsTotals
	}{wantCircuit, MetricsTotals{Requests: 1, Errors: 1, UnknownUsageRequests: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stale metric had circuit authority or was not persisted:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerHalfOpenProtocolFailurePreservesWorkerFailures(t *testing.T) {
	now := time.Date(2026, time.July, 11, 2, 0, 0, 0, time.UTC)
	m, pool := newAuthorityTestManager(t, "primary", 2)
	defer m.Close()
	m.clock = func() time.Time { return now }
	key := poolCircuitKey("coding-ha", "primary")
	m.circuits.RecordFailure(key, pool.CircuitBreaker)
	m.circuits.RecordFailure(key, pool.CircuitBreaker)
	opened := m.circuits.Status(key, pool.CircuitBreaker)
	now = now.Add(time.Duration(pool.CircuitBreaker.RecoveryWaitSeconds) * time.Second)
	authorityObserve(t, m, "primary", upstream.ProbeResult{
		Error: "protocol_error", Mode: upstream.ProbeModeProtocol, Authoritative: true,
	})
	want := opened
	want.OpenedAt = now
	if got := m.circuits.Status(key, pool.CircuitBreaker); !reflect.DeepEqual(got, want) {
		t.Fatalf("half-open probe failure changed Worker count:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerOpenProbeFailurePreservesRecoveryWait(t *testing.T) {
	now := time.Date(2026, time.July, 11, 2, 0, 0, 0, time.UTC)
	m, pool := newAuthorityTestManager(t, "primary", 1)
	defer m.Close()
	m.clock = func() time.Time { return now }
	key := poolCircuitKey("coding-ha", "primary")
	m.circuits.RecordFailure(key, pool.CircuitBreaker)
	wantCircuit := m.circuits.Status(key, pool.CircuitBreaker)
	now = now.Add(time.Duration(pool.CircuitBreaker.RecoveryWaitSeconds-1) * time.Second)
	authorityObserve(t, m, "primary", upstream.ProbeResult{
		Error: "protocol_error", Mode: upstream.ProbeModeProtocol, Authoritative: true,
	})
	got := struct {
		Circuit   CircuitStatus
		Readiness ReadinessState
	}{m.circuits.Status(key, pool.CircuitBreaker), m.poolReadiness("coding-ha", "primary").Readiness}
	want := struct {
		Circuit   CircuitStatus
		Readiness ReadinessState
	}{wantCircuit, ReadinessStateNotReady}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pre-wait probe failure refreshed recovery wait:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerProtocolReadinessControlsFailover(t *testing.T) {
	tests := []struct {
		name        string
		readyBackup bool
		wantActive  string
	}{
		{name: "unknown backup is ineligible", wantActive: "primary"},
		{name: "ready backup is eligible", readyBackup: true, wantActive: "backup"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, _ := newAuthorityTestManager(t, "primary", 1)
			defer m.Close()
			if test.readyBackup {
				authorityObserve(t, m, "backup", readinessTestSuccess(3))
			}
			m.handleWorkerMetricEvent("app", authorityMetric(1, http.StatusBadGateway, &worker.UpstreamFailure{
				Kind: worker.UpstreamFailureTransport, BeforeFirstByte: true,
			}))
			got := struct {
				Configured string
				Applied    string
			}{workerUpstreamID(m.config.Workers["app"]), string(m.workerClient.(*recordingWorkerClient).appliedRuntimes[6767].Upstream.ID)}
			want := struct {
				Configured string
				Applied    string
			}{test.wantActive, ""}
			if test.wantActive == "backup" {
				want.Applied = "backup"
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected readiness-controlled failover:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

func TestManagerScheduledProbeDoesNotCrossPoolEgress(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	pool := config.UpstreamPool{
		Upstreams: []string{"primary"},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: 1, RecoverySuccessThreshold: 1, RecoveryWaitSeconds: 30,
		},
	}
	m := New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: t.TempDir()},
		Workers: map[string]config.WorkerConfig{
			"a": {Port: 6767, Upstream: "primary", UpstreamPool: "pool-a", ProxyURL: "http://proxy-a.example"},
			"b": {Port: 6768, Upstream: "primary", UpstreamPool: "pool-b", ProxyURL: "http://proxy-b.example"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"primary": {BaseURL: "https://primary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-a"}},
		},
		UpstreamPools: map[string]config.UpstreamPool{"pool-a": pool, "pool-b": pool},
	}})
	defer m.Close()
	m.clock = func() time.Time { return now }
	keyA, keyB := poolCircuitKey("pool-a", "primary"), poolCircuitKey("pool-b", "primary")
	m.circuits.RecordFailure(keyA, pool.CircuitBreaker)
	m.circuits.RecordFailure(keyB, pool.CircuitBreaker)
	wantB := m.circuits.Status(keyB, pool.CircuitBreaker)
	now = now.Add(30 * time.Second)
	spec := readinessTestProbeSpec("pool-a", "primary", "http://proxy-a.example", 1, "model-a")
	installReadinessTestSpec(m, spec)
	m.recordScheduledProbeResult(spec, readinessTestSuccess(2))
	got := []CircuitStatus{
		m.circuits.Status(keyA, pool.CircuitBreaker),
		m.circuits.Status(keyB, pool.CircuitBreaker),
	}
	want := []CircuitStatus{{State: CircuitStateClosed}, wantB}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("one egress probe changed another pool circuit:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerRecoveredProbeEventUsesFinalEligibility(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	m, pool := newAuthorityTestManager(t, "primary", 1)
	defer m.Close()
	m.clock = func() time.Time { return now }
	m.mu.Lock()
	pool = m.config.UpstreamPools["coding-ha"]
	pool.CircuitBreaker.RecoverySuccessThreshold = 1
	m.config.UpstreamPools["coding-ha"] = pool
	m.mu.Unlock()
	key := poolCircuitKey("coding-ha", "primary")
	m.circuits.RecordFailure(key, pool.CircuitBreaker)
	now = now.Add(30 * time.Second)
	authorityObserve(t, m, "primary", readinessTestSuccess(2))
	checkedAt := now
	events := m.events.Replay(0)
	got := struct {
		Circuit   CircuitStatus
		Event     map[string]any
		Readiness PoolReadiness
		Schedule  poolProbeSchedule
	}{
		Circuit:   m.circuits.Status(key, pool.CircuitBreaker),
		Event:     events[len(events)-1].Payload,
		Readiness: m.poolReadiness("coding-ha", "primary"),
		Schedule:  m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: "primary"}],
	}
	want := struct {
		Circuit   CircuitStatus
		Event     map[string]any
		Readiness PoolReadiness
		Schedule  poolProbeSchedule
	}{
		Circuit: CircuitStatus{State: CircuitStateClosed},
		Event: map[string]any{
			"upstream": "primary", "pool": "coding-ha", "mode": upstream.ProbeModeProtocol,
			"authoritative": true, "readiness": ReadinessStateReady, "eligible": true,
			"checked_at": now.Format(time.RFC3339), "ok": true, "status_code": http.StatusOK,
			"latency_ms": int64(2), "probe_state": PoolProbeStateAlert,
			"next_probe_at": now.Format(time.RFC3339), "reason": ProbeScheduleStartup,
		},
		Readiness: PoolReadiness{
			Upstream: "primary", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol,
			Authoritative: true, Readiness: ReadinessStateReady, Eligible: true,
			CheckedAt: &checkedAt, OK: true, StatusCode: http.StatusOK, LatencyMS: 2,
		},
		Schedule: poolProbeSchedule{
			NextProbeAt: now.Add(time.Duration(config.DefaultPoolProbeStableIntervalSeconds) * time.Second),
			Reason:      ProbeScheduleStable,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scheduled event used pre-transition eligibility:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerWorkerMetricRechecksAuthorityUnderFailoverLock(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manager)
	}{
		{
			name: "generation changed",
			mutate: func(m *Manager) {
				m.generations["app"] = 2
			},
		},
		{
			name: "worker became out of sync",
			mutate: func(m *Manager) {
				m.statuses["app"] = WorkerStateOutOfSync
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, pool := newAuthorityTestManager(t, "primary", 1)
			defer m.Close()
			key := poolCircuitKey("coding-ha", "primary")
			wantCircuit := m.circuits.Status(key, pool.CircuitBreaker)
			m.failoverMu.Lock()
			done := make(chan struct{})
			go func() {
				m.handleWorkerMetricEvent("app", authorityMetric(1, http.StatusBadGateway, &worker.UpstreamFailure{
					Kind: worker.UpstreamFailureTransport, BeforeFirstByte: true,
				}))
				close(done)
			}()
			eventually(t, time.Second, func() bool {
				m.mu.RLock()
				tracker := m.metricsTrackers["app"]
				m.mu.RUnlock()
				return tracker != nil && tracker.Snapshot().Requests == 1
			})
			m.metricsStore.writeMu.Lock()
			m.metricsStore.writeMu.Unlock()
			time.Sleep(20 * time.Millisecond)
			m.mu.Lock()
			test.mutate(m)
			m.mu.Unlock()
			m.failoverMu.Unlock()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("metric outcome did not finish")
			}
			got := struct {
				Circuit CircuitStatus
				Stored  int64
			}{Circuit: m.circuits.Status(key, pool.CircuitBreaker)}
			response, err := m.metricsStore.Query(MetricsQuery{Range: MetricsRangeToday}, []WorkerSummary{{Name: "app", Port: 6767}})
			if err != nil {
				t.Fatal(err)
			}
			got.Stored = response.Workers[0].Totals.Requests
			want := struct {
				Circuit CircuitStatus
				Stored  int64
			}{Circuit: wantCircuit, Stored: 1}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("metric authority changed after initial check:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

func TestManagerFirstWorkerFailureRefreshesFallbacks(t *testing.T) {
	now := time.Date(2026, time.July, 13, 3, 4, 5, 0, time.UTC)
	m, pool := newAuthorityTestManager(t, "primary", 3)
	defer m.Close()
	m.clock = func() time.Time { return now }
	future := now.Add(time.Hour)
	for _, upstreamName := range pool.Upstreams {
		m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: upstreamName}] = poolProbeSchedule{NextProbeAt: future, Reason: ProbeScheduleStable}
	}
	started := make(chan probeSpec, 1)
	m.probeRunner = func(_ context.Context, spec probeSpec) upstream.ProbeResult {
		started <- spec
		return upstream.ProbeResult{}
	}

	if err := m.recordWorkerUpstreamFailure("app", "primary"); err != nil {
		t.Fatal(err)
	}
	var probe probeSpec
	select {
	case probe = <-started:
	case <-time.After(time.Second):
		t.Fatal("failure-triggered probe did not start")
	}
	gotProbe := struct {
		Upstream string
		Pools    []string
		Due      bool
		Reason   ProbeScheduleReason
	}{probe.Upstream, probe.Pools, probe.Due, probe.Reason}
	wantProbe := struct {
		Upstream string
		Pools    []string
		Due      bool
		Reason   ProbeScheduleReason
	}{"backup", []string{"coding-ha"}, true, ProbeScheduleWorkerFailure}
	if !reflect.DeepEqual(gotProbe, wantProbe) {
		t.Fatalf("unexpected failure-triggered probe: got %#v want %#v", gotProbe, wantProbe)
	}
	got := struct {
		Active   string
		Circuit  CircuitStatus
		Schedule poolProbeSchedule
	}{m.poolActiveUpstream("coding-ha"), m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker), m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: "backup"}]}
	want := struct {
		Active   string
		Circuit  CircuitStatus
		Schedule poolProbeSchedule
	}{"primary", CircuitStatus{State: CircuitStateClosed, ConsecutiveFailures: 1}, poolProbeSchedule{NextProbeAt: now, Reason: ProbeScheduleWorkerFailure}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected first failure state:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerDisabledPoolIgnoresWorkerOutcomes(t *testing.T) {
	m, pool := newAuthorityTestManager(t, "primary", 2)
	m.cancelProbes()
	defer m.Close()
	m.updateConfig(func(cfg *config.Config) {
		configured := cfg.UpstreamPools["coding-ha"]
		configured.Mode = config.UpstreamPoolModeDisabled
		cfg.UpstreamPools["coding-ha"] = configured
	})
	pool = m.config.UpstreamPools["coding-ha"]
	m.circuits.RecordFailure(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker)
	m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: "backup"}] = poolProbeSchedule{NextProbeAt: time.Now(), Reason: ProbeScheduleStable}
	m.circuits.mu.Lock()
	wantCircuits := maps.Clone(m.circuits.states)
	m.circuits.mu.Unlock()
	m.mu.RLock()
	wantWorkers := cloneConfig(m.config).Workers
	m.mu.RUnlock()
	want := struct {
		Circuits  map[string]CircuitStatus
		Schedules map[poolProbeScheduleKey]poolProbeSchedule
		Workers   map[string]config.WorkerConfig
		Events    []Event
	}{wantCircuits, maps.Clone(m.probeSchedules), wantWorkers, m.events.Replay(0)}

	if err := m.recordWorkerUpstreamFailure("app", "primary"); err != nil {
		t.Fatal(err)
	}
	if err := m.recordWorkerUpstreamSuccess("app", "primary"); err != nil {
		t.Fatal(err)
	}
	m.circuits.mu.Lock()
	gotCircuits := maps.Clone(m.circuits.states)
	m.circuits.mu.Unlock()
	m.mu.RLock()
	gotWorkers := cloneConfig(m.config).Workers
	m.mu.RUnlock()
	got := struct {
		Circuits  map[string]CircuitStatus
		Schedules map[poolProbeScheduleKey]poolProbeSchedule
		Workers   map[string]config.WorkerConfig
		Events    []Event
	}{gotCircuits, maps.Clone(m.probeSchedules), gotWorkers, m.events.Replay(0)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("disabled pool accepted worker outcome:\n got %#v\nwant %#v", got, want)
	}
}

func newAuthorityTestManager(t *testing.T, active string, failureThreshold int) (*Manager, config.UpstreamPool) {
	t.Helper()
	pool := config.UpstreamPool{
		Upstreams: []string{"primary", "backup"},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: failureThreshold, RecoverySuccessThreshold: 2, RecoveryWaitSeconds: 30,
		},
	}
	client := &recordingWorkerClient{}
	m := New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: t.TempDir()}, Plugins: testPluginDefinitions(),
		Workers: map[string]config.WorkerConfig{
			"app": {Port: 6767, Upstream: active, UpstreamPool: "coding-ha"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"primary": {BaseURL: "https://primary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-a"}},
			"backup":  {BaseURL: "https://backup.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-b"}},
		},
		UpstreamPools: map[string]config.UpstreamPool{"coding-ha": pool},
	}, WorkerClient: client})
	m.mu.Lock()
	m.statuses["app"], m.generations["app"] = WorkerStateRunning, 1
	m.mu.Unlock()
	return m, pool
}

func authorityObserve(t *testing.T, m *Manager, upstreamName string, result upstream.ProbeResult) {
	t.Helper()
	spec := readinessTestProbeSpec("coding-ha", upstreamName, "", 1, "model")
	installReadinessTestSpec(m, spec)
	m.recordScheduledProbeResult(spec, result)
}

func authorityMetric(generation int, status int, failure *worker.UpstreamFailure) worker.RequestMetricEvent {
	return worker.RequestMetricEvent{
		Timestamp: time.Now(), SnapshotGeneration: generation, Upstream: "primary",
		Status: status, Failure: failure,
	}
}
