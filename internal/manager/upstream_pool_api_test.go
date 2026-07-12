package manager

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
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
	m, pool := newPoolRoutingTestManager(t, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		"cli": {Port: 6768, Upstream: "primary", UpstreamPool: "research-ha"},
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
	response := requestManager(t, m, http.MethodPatch, "/api/upstream-pools/coding-ha", `{"mode":"disabled"}`)
	got := map[probeExecutionKey][]string{}
	for key, spec := range m.desiredProbes {
		got[key] = spec.Pools
	}
	want := map[probeExecutionKey][]string{
		{Upstream: "primary"}: {"research-ha"},
		{Upstream: "backup"}:  {"research-ha"},
	}
	if response.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("disable did not reconcile shared probes: status %d got %#v want %#v", response.Code, got, want)
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
