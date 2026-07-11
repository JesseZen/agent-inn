package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/worker"
)

func TestCircuitBreakerTransitionsFromClosedToOpenToHalfOpen(t *testing.T) {
	now := time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC)
	breaker := newCircuitBreaker(func() time.Time { return now })
	policy := config.CircuitBreakerConfig{
		FailureThreshold:         2,
		RecoverySuccessThreshold: 2,
		RecoveryWaitSeconds:      60,
	}

	breaker.RecordFailure("primary", policy)
	breaker.RecordFailure("primary", policy)

	wantOpen := CircuitStatus{
		State:               CircuitStateOpen,
		ConsecutiveFailures: 2,
		OpenedAt:            now,
	}
	if got := breaker.Status("primary", policy); !reflect.DeepEqual(got, wantOpen) {
		t.Fatalf("unexpected open circuit:\n got %#v\nwant %#v", got, wantOpen)
	}
	if breaker.Allow("primary", policy) {
		t.Fatal("open circuit allowed a request before recovery wait elapsed")
	}

	now = now.Add(time.Minute)
	if !breaker.Allow("primary", policy) {
		t.Fatal("circuit did not allow the half-open probe")
	}
	wantHalfOpen := CircuitStatus{
		State:               CircuitStateHalfOpen,
		ConsecutiveFailures: 2,
		OpenedAt:            time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC),
	}
	if got := breaker.Status("primary", policy); !reflect.DeepEqual(got, wantHalfOpen) {
		t.Fatalf("unexpected half-open circuit:\n got %#v\nwant %#v", got, wantHalfOpen)
	}
}

func TestCircuitBreakerReopensOnHalfOpenFailureAndClosesAfterRecoverySuccesses(t *testing.T) {
	now := time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC)
	breaker := newCircuitBreaker(func() time.Time { return now })
	policy := config.CircuitBreakerConfig{
		FailureThreshold:         1,
		RecoverySuccessThreshold: 2,
		RecoveryWaitSeconds:      30,
	}

	breaker.RecordFailure("primary", policy)
	now = now.Add(30 * time.Second)
	if !breaker.Allow("primary", policy) {
		t.Fatal("circuit did not enter half-open state")
	}
	breaker.RecordFailure("primary", policy)

	wantReopened := CircuitStatus{
		State:               CircuitStateOpen,
		ConsecutiveFailures: 2,
		OpenedAt:            now,
	}
	if got := breaker.Status("primary", policy); !reflect.DeepEqual(got, wantReopened) {
		t.Fatalf("unexpected reopened circuit:\n got %#v\nwant %#v", got, wantReopened)
	}

	now = now.Add(30 * time.Second)
	if !breaker.Allow("primary", policy) {
		t.Fatal("circuit did not re-enter half-open state")
	}
	breaker.RecordSuccess("primary", policy)
	breaker.RecordSuccess("primary", policy)

	wantClosed := CircuitStatus{State: CircuitStateClosed}
	if got := breaker.Status("primary", policy); !reflect.DeepEqual(got, wantClosed) {
		t.Fatalf("unexpected recovered circuit:\n got %#v\nwant %#v", got, wantClosed)
	}
}

func TestManagerFailoverSwitchesEveryWorkerInPool(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
				"cli": {Port: 6768, Upstream: "primary", UpstreamPool: "coding-ha"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"primary": {BaseURL: "https://primary.example/v1"},
				"backup":  {BaseURL: "https://backup.example/v1"},
			},
			UpstreamPools: map[string]config.UpstreamPool{
				"coding-ha": {
					Upstreams: []string{"primary", "backup"},
					CircuitBreaker: config.CircuitBreakerConfig{
						FailureThreshold:         1,
						RecoverySuccessThreshold: 1,
						RecoveryWaitSeconds:      60,
					},
				},
			},
		},
		WorkerClient: client,
	})
	defer m.Close()
	m.statuses["app"] = WorkerStateRunning
	m.statuses["cli"] = WorkerStateRunning
	authorityObserve(t, m, "backup", readinessTestSuccess(0))

	if err := m.recordWorkerUpstreamFailure("app", "primary"); err != nil {
		t.Fatal(err)
	}

	got := struct {
		Configured map[string]string
		Applied    map[int]string
	}{
		Configured: map[string]string{
			"app": workerUpstreamID(m.config.Workers["app"]),
			"cli": workerUpstreamID(m.config.Workers["cli"]),
		},
		Applied: map[int]string{
			6767: string(client.appliedRuntimes[6767].Upstream.ID),
			6768: string(client.appliedRuntimes[6768].Upstream.ID),
		},
	}
	want := struct {
		Configured map[string]string
		Applied    map[int]string
	}{
		Configured: map[string]string{"app": "backup", "cli": "backup"},
		Applied:    map[int]string{6767: "backup", 6768: "backup"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected pool failover:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerFailoverRollsBackPartialPoolSwitchAndCanRetry(t *testing.T) {
	client := &recordingWorkerClient{
		runtimeStates: map[int]appruntime.WorkerRuntime{
			6767: {Upstream: appruntime.UpstreamRuntime{ID: "primary"}},
			6768: {Upstream: appruntime.UpstreamRuntime{ID: "primary"}},
		},
		applyErrors: map[int][]error{6768: {errors.New("worker rejected runtime")}},
	}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
				"cli": {Port: 6768, Upstream: "primary", UpstreamPool: "coding-ha"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"primary": {BaseURL: "https://primary.example/v1"},
				"backup":  {BaseURL: "https://backup.example/v1"},
			},
			UpstreamPools: map[string]config.UpstreamPool{
				"coding-ha": {Upstreams: []string{"primary", "backup"}},
			},
		},
		WorkerClient: client,
	})
	defer m.Close()
	m.statuses["app"] = WorkerStateRunning
	m.statuses["cli"] = WorkerStateRunning

	if err := m.switchUpstreamPool("coding-ha", "primary", "backup"); err == nil {
		t.Fatal("partial switch unexpectedly succeeded")
	}
	gotRollback := struct {
		Configured map[string]string
		Applied    map[int]string
	}{
		Configured: map[string]string{
			"app": workerUpstreamID(m.config.Workers["app"]),
			"cli": workerUpstreamID(m.config.Workers["cli"]),
		},
		Applied: map[int]string{
			6767: string(client.runtimeStates[6767].Upstream.ID),
			6768: string(client.runtimeStates[6768].Upstream.ID),
		},
	}
	wantRollback := struct {
		Configured map[string]string
		Applied    map[int]string
	}{
		Configured: map[string]string{"app": "primary", "cli": "primary"},
		Applied:    map[int]string{6767: "primary", 6768: "primary"},
	}
	if !reflect.DeepEqual(gotRollback, wantRollback) {
		t.Fatalf("unexpected rollback state:\n got %#v\nwant %#v", gotRollback, wantRollback)
	}

	if err := m.switchUpstreamPool("coding-ha", "primary", "backup"); err != nil {
		t.Fatal(err)
	}
	gotRetry := struct {
		Configured map[string]string
		Applied    map[int]string
	}{
		Configured: map[string]string{
			"app": workerUpstreamID(m.config.Workers["app"]),
			"cli": workerUpstreamID(m.config.Workers["cli"]),
		},
		Applied: map[int]string{
			6767: string(client.runtimeStates[6767].Upstream.ID),
			6768: string(client.runtimeStates[6768].Upstream.ID),
		},
	}
	wantRetry := struct {
		Configured map[string]string
		Applied    map[int]string
	}{
		Configured: map[string]string{"app": "backup", "cli": "backup"},
		Applied:    map[int]string{6767: "backup", 6768: "backup"},
	}
	if !reflect.DeepEqual(gotRetry, wantRetry) {
		t.Fatalf("unexpected retry state:\n got %#v\nwant %#v", gotRetry, wantRetry)
	}
}

func TestManagerFailoverRestoresPreferredUpstreamAfterRecovery(t *testing.T) {
	now := time.Date(2026, time.July, 11, 0, 0, 0, 0, time.UTC)
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"primary": {BaseURL: "https://primary.example/v1"},
				"backup":  {BaseURL: "https://backup.example/v1"},
			},
			UpstreamPools: map[string]config.UpstreamPool{
				"coding-ha": {
					Upstreams: []string{"primary", "backup"},
					CircuitBreaker: config.CircuitBreakerConfig{
						FailureThreshold:         1,
						RecoverySuccessThreshold: 2,
						RecoveryWaitSeconds:      30,
					},
				},
			},
		},
		WorkerClient: client,
	})
	defer m.Close()
	m.clock = func() time.Time { return now }
	m.statuses["app"] = WorkerStateRunning
	authorityObserve(t, m, "backup", readinessTestSuccess(0))

	if err := m.recordWorkerUpstreamFailure("app", "primary"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	authorityObserve(t, m, "primary", readinessTestSuccess(0))
	authorityObserve(t, m, "primary", readinessTestSuccess(0))

	pool := m.config.UpstreamPools["coding-ha"]
	got := struct {
		Configured string
		Applied    string
		Circuit    CircuitStatus
	}{
		Configured: workerUpstreamID(m.config.Workers["app"]),
		Applied:    string(client.appliedRuntimes[6767].Upstream.ID),
		Circuit:    m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker),
	}
	want := struct {
		Configured string
		Applied    string
		Circuit    CircuitStatus
	}{
		Configured: "primary",
		Applied:    "primary",
		Circuit:    CircuitStatus{State: CircuitStateClosed},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected preferred recovery:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerFailoverRestoresRecoveredHigherPriorityFallback(t *testing.T) {
	now := time.Date(2026, time.July, 11, 0, 0, 0, 0, time.UTC)
	client := &recordingWorkerClient{}
	pool := config.UpstreamPool{
		Upstreams: []string{"primary", "secondary", "tertiary"},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold:         1,
			RecoverySuccessThreshold: 1,
			RecoveryWaitSeconds:      30,
		},
	}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "tertiary", UpstreamPool: "coding-ha"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"primary":   {BaseURL: "https://primary.example/v1"},
				"secondary": {BaseURL: "https://secondary.example/v1"},
				"tertiary":  {BaseURL: "https://tertiary.example/v1"},
			},
			UpstreamPools: map[string]config.UpstreamPool{"coding-ha": pool},
		},
		WorkerClient: client,
	})
	defer m.Close()
	m.clock = func() time.Time { return now }
	m.statuses["app"] = WorkerStateRunning
	m.circuits.RecordFailure(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker)
	m.circuits.RecordFailure(poolCircuitKey("coding-ha", "secondary"), pool.CircuitBreaker)
	now = now.Add(30 * time.Second)

	authorityObserve(t, m, "secondary", readinessTestSuccess(0))
	got := struct {
		Configured string
		Applied    string
	}{
		Configured: workerUpstreamID(m.config.Workers["app"]),
		Applied:    string(client.appliedRuntimes[6767].Upstream.ID),
	}
	want := struct {
		Configured string
		Applied    string
	}{Configured: "secondary", Applied: "secondary"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected fallback recovery:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerWorkerFailureEventTriggersOnlyCurrentUpstreamFailover(t *testing.T) {
	tests := []struct {
		name        string
		current     string
		wantCurrent string
		wantApplied map[int]appliedUpstream
	}{
		{
			name:        "current upstream failure",
			current:     "primary",
			wantCurrent: "backup",
			wantApplied: map[int]appliedUpstream{6767: {ID: "backup"}},
		},
		{
			name:        "stale upstream failure",
			current:     "backup",
			wantCurrent: "backup",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingWorkerClient{}
			m := New(Config{
				Config: config.Config{
					Settings: config.Settings{StateDir: t.TempDir()},
					Plugins:  testPluginDefinitions(),
					Workers: map[string]config.WorkerConfig{
						"app": {Port: 6767, Upstream: test.current, UpstreamPool: "coding-ha"},
					},
					Upstreams: map[string]config.UpstreamProfile{
						"primary": {BaseURL: "https://primary.example/v1"},
						"backup":  {BaseURL: "https://backup.example/v1"},
					},
					UpstreamPools: map[string]config.UpstreamPool{
						"coding-ha": {
							Upstreams: []string{"primary", "backup"},
							CircuitBreaker: config.CircuitBreakerConfig{
								FailureThreshold:         1,
								RecoverySuccessThreshold: 1,
								RecoveryWaitSeconds:      60,
							},
						},
					},
				},
				WorkerClient: client,
			})
			defer m.Close()
			m.statuses["app"] = WorkerStateRunning
			m.generations["app"] = 1
			if test.current == "primary" {
				authorityObserve(t, m, "backup", readinessTestSuccess(0))
			}

			m.handleWorkerMetricEvent("app", worker.RequestMetricEvent{
				Timestamp:          time.Now(),
				SnapshotGeneration: 1,
				Upstream:           "primary",
				Method:             "POST",
				Path:               "/v1/responses",
				Status:             502,
				Failure: &worker.UpstreamFailure{
					Kind:            worker.UpstreamFailureTransport,
					BeforeFirstByte: true,
				},
			})

			applied := map[int]appliedUpstream(nil)
			if client.appliedRuntimes != nil {
				applied = make(map[int]appliedUpstream, len(client.appliedRuntimes))
				for port, runtime := range client.appliedRuntimes {
					applied[port] = appliedUpstream{ID: string(runtime.Upstream.ID)}
				}
			}
			got := struct {
				Current string
				Applied map[int]appliedUpstream
			}{Current: workerUpstreamID(m.config.Workers["app"]), Applied: applied}
			want := struct {
				Current string
				Applied map[int]appliedUpstream
			}{Current: test.wantCurrent, Applied: test.wantApplied}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected failure event routing:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

func TestManagerWorkerFailureCircuitEventIncludesPool(t *testing.T) {
	m := New(Config{Config: config.Config{
		Workers: map[string]config.WorkerConfig{
			"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"primary": {BaseURL: "https://primary.example/v1"},
			"backup":  {BaseURL: "https://backup.example/v1"},
		},
		UpstreamPools: map[string]config.UpstreamPool{
			"coding-ha": {
				Upstreams: []string{"primary", "backup"},
				CircuitBreaker: config.CircuitBreakerConfig{
					FailureThreshold:         1,
					RecoverySuccessThreshold: 1,
					RecoveryWaitSeconds:      60,
				},
			},
		},
	}})
	defer m.Close()
	m.statuses["app"] = WorkerStateRunning

	if err := m.recordWorkerUpstreamFailure("app", "primary"); err != nil {
		t.Fatal(err)
	}
	var circuitEvent Event
	for _, event := range m.events.Replay(0) {
		if event.Type == EventUpstreamCircuitChanged {
			circuitEvent = event
			break
		}
	}
	poolName, upstreamName, state, ok := circuitEvent.AsUpstreamCircuitChanged()
	got := struct {
		Pool     string
		Upstream string
		State    CircuitState
		OK       bool
	}{poolName, upstreamName, state, ok}
	want := struct {
		Pool     string
		Upstream string
		State    CircuitState
		OK       bool
	}{"coding-ha", "primary", CircuitStateOpen, true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected worker circuit event:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerSuccessfulWorkerRequestResetsConsecutiveFailures(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Settings: config.Settings{StateDir: t.TempDir()},
			Plugins:  testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"primary": {BaseURL: "https://primary.example/v1"},
				"backup":  {BaseURL: "https://backup.example/v1"},
			},
			UpstreamPools: map[string]config.UpstreamPool{
				"coding-ha": {
					Upstreams: []string{"primary", "backup"},
					CircuitBreaker: config.CircuitBreakerConfig{
						FailureThreshold:         2,
						RecoverySuccessThreshold: 1,
						RecoveryWaitSeconds:      60,
					},
				},
			},
		},
		WorkerClient: client,
	})
	defer m.Close()
	m.statuses["app"] = WorkerStateRunning
	m.generations["app"] = 1
	failure := &worker.UpstreamFailure{Kind: worker.UpstreamFailureTransport, BeforeFirstByte: true}

	m.handleWorkerMetricEvent("app", worker.RequestMetricEvent{Timestamp: time.Now(), SnapshotGeneration: 1, Upstream: "primary", Status: 502, Failure: failure})
	m.handleWorkerMetricEvent("app", worker.RequestMetricEvent{Timestamp: time.Now(), SnapshotGeneration: 1, Upstream: "primary", Status: 200})
	m.handleWorkerMetricEvent("app", worker.RequestMetricEvent{Timestamp: time.Now(), SnapshotGeneration: 1, Upstream: "primary", Status: 502, Failure: failure})

	pool := m.config.UpstreamPools["coding-ha"]
	got := struct {
		Current string
		Circuit CircuitStatus
	}{
		Current: workerUpstreamID(m.config.Workers["app"]),
		Circuit: m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker),
	}
	want := struct {
		Current string
		Circuit CircuitStatus
	}{
		Current: "primary",
		Circuit: CircuitStatus{State: CircuitStateClosed, ConsecutiveFailures: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected consecutive failure reset:\n got %#v\nwant %#v", got, want)
	}
}

type appliedUpstream struct {
	ID string
}

func TestManagerAPIUpdatesWorkerUpstreamPool(t *testing.T) {
	m := New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: t.TempDir()},
		Plugins:  testPluginDefinitions(),
		Workers: map[string]config.WorkerConfig{
			"app": {Port: 6767, Upstream: "primary"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"primary": {BaseURL: "https://primary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-a"}},
			"backup":  {BaseURL: "https://backup.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-b"}},
		},
		UpstreamPools: map[string]config.UpstreamPool{
			"coding-ha": {Upstreams: []string{"primary", "backup"}},
		},
	}})
	defer m.Close()

	response := httptest.NewRecorder()
	m.ServeHTTP(response, httptest.NewRequest(
		http.MethodPatch,
		"http://manager.local/api/workers/app",
		strings.NewReader(`{"upstream_pool":"coding-ha"}`),
	))
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	got := struct {
		Configured string
		Response   any
	}{Configured: m.config.Workers["app"].UpstreamPool, Response: body["upstream_pool"]}
	want := struct {
		Configured string
		Response   any
	}{Configured: "coding-ha", Response: "coding-ha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected worker pool update:\n got %#v\nwant %#v", got, want)
	}
}

func TestFailoverEventAccessors(t *testing.T) {
	circuitEvent := Event{
		Type: EventUpstreamCircuitChanged,
		Payload: map[string]any{
			"pool":     "coding-ha",
			"upstream": "primary",
			"state":    CircuitStateOpen,
		},
	}
	poolEvent := Event{
		Type: EventUpstreamPoolSwitched,
		Payload: map[string]any{
			"pool":              "coding-ha",
			"previous_upstream": "primary",
			"upstream":          "backup",
		},
	}

	circuitPool, upstreamName, state, circuitOK := circuitEvent.AsUpstreamCircuitChanged()
	poolName, previous, current, poolOK := poolEvent.AsUpstreamPoolSwitched()
	got := struct {
		CircuitPool string
		Upstream    string
		State       CircuitState
		Circuit     bool
		Pool        string
		Previous    string
		Current     string
		Switched    bool
	}{circuitPool, upstreamName, state, circuitOK, poolName, previous, current, poolOK}
	want := struct {
		CircuitPool string
		Upstream    string
		State       CircuitState
		Circuit     bool
		Pool        string
		Previous    string
		Current     string
		Switched    bool
	}{"coding-ha", "primary", CircuitStateOpen, true, "coding-ha", "primary", "backup", true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected failover event accessors:\n got %#v\nwant %#v", got, want)
	}
}
