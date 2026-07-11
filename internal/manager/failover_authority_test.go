package manager

import (
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
	m.handleWorkerMetricEvent("app", authorityMetric(1, http.StatusBadGateway, &worker.UpstreamFailure{
		Kind: worker.UpstreamFailureTransport, BeforeFirstByte: true,
	}))
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
