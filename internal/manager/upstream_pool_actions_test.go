package manager

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
)

func TestManagerPoolSwitch(t *testing.T) {
	now := time.Date(2026, time.July, 13, 6, 7, 8, 0, time.UTC)
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantActive string
		forced     bool
	}{
		{"eligible normal", `{"upstream":"backup","mode":"normal"}`, http.StatusOK, "backup", false},
		{"ineligible normal", `{"upstream":"backup","mode":"normal"}`, http.StatusConflict, "primary", false},
		{"ineligible force", `{"upstream":"backup","mode":"force"}`, http.StatusOK, "backup", true},
		{"unknown member", `{"upstream":"outside","mode":"normal"}`, http.StatusBadRequest, "primary", false},
		{"invalid mode", `{"upstream":"backup","mode":"unsafe"}`, http.StatusBadRequest, "primary", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := newPoolActionTestManager(t, config.UpstreamPoolModeActive, []string{"primary", "backup"}, map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
				"cli": {Port: 6768, Upstream: "primary", UpstreamPool: "coding-ha"},
			})
			defer m.Close()
			m.clock = func() time.Time { return now }
			if test.name == "eligible normal" {
				m.readiness[poolCircuitKey("coding-ha", "backup")] = readinessObservation{
					Result: readinessTestSuccess(3), CheckedAt: now, ExpiresAt: now.Add(time.Hour),
				}
			}
			m.mu.RLock()
			pool := m.config.UpstreamPools["coding-ha"]
			wantWorkers := map[string]config.WorkerConfig{}
			for name, worker := range m.config.Workers {
				wantWorkers[name] = cloneWorkerConfig(worker)
				if test.wantStatus == http.StatusOK {
					worker.Upstream = test.wantActive
					worker.UpstreamID = test.wantActive
					wantWorkers[name] = worker
				}
			}
			m.mu.RUnlock()

			response := requestManager(t, m, http.MethodPost, "/api/upstream-pools/coding-ha/switch", test.body)
			var summary upstreamPoolSummary
			if response.Code == http.StatusOK {
				if err := json.Unmarshal(response.Body.Bytes(), &summary); err != nil {
					t.Fatal(err)
				}
			}
			m.mu.RLock()
			gotWorkers := map[string]config.WorkerConfig{}
			for name, worker := range m.config.Workers {
				gotWorkers[name] = cloneWorkerConfig(worker)
			}
			m.mu.RUnlock()
			got := struct {
				Status  int
				Body    string
				Summary upstreamPoolSummary
				Workers map[string]config.WorkerConfig
				Events  []map[string]any
			}{response.Code, response.Body.String(), summary, gotWorkers, poolRoutingEvents(m, EventUpstreamPoolSwitched)}
			want := struct {
				Status  int
				Body    string
				Summary upstreamPoolSummary
				Workers map[string]config.WorkerConfig
				Events  []map[string]any
			}{Status: test.wantStatus, Workers: wantWorkers}
			if test.wantStatus == http.StatusOK {
				backupReadiness := PoolReadiness{Upstream: "backup", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol, Authoritative: true, Readiness: ReadinessStateUnknown}
				if test.name == "eligible normal" {
					backupReadiness.Readiness = ReadinessStateReady
					backupReadiness.Eligible = true
					backupReadiness.CheckedAt = &now
					backupReadiness.OK = true
					backupReadiness.StatusCode = http.StatusOK
					backupReadiness.LatencyMS = 3
				}
				want.Summary = upstreamPoolSummary{
					ID: "coding-ha", Name: "coding-ha", Mode: config.UpstreamPoolModeActive,
					Upstreams: pool.Upstreams, Probe: pool.Probe, CircuitBreaker: pool.CircuitBreaker,
					ActiveUpstream: test.wantActive, Workers: []string{"app", "cli"},
					ProbeState: PoolProbeStateAlert, NextProbeAt: &now,
					Readiness: []PoolReadiness{
						{Upstream: "primary", Pool: "coding-ha", Mode: upstream.ProbeModeProtocol, Authoritative: true, Readiness: ReadinessStateUnknown},
						backupReadiness,
					},
				}
				want.Body = response.Body.String()
				event := map[string]any{"pool": "coding-ha", "previous_upstream": "primary", "upstream": "backup"}
				if test.forced {
					event["forced"] = true
				}
				want.Events = []map[string]any{event}
			} else {
				switch test.name {
				case "ineligible normal":
					want.Body = "{\"error\":\"target upstream is not eligible\"}\n"
				case "unknown member":
					want.Body = "{\"error\":\"target upstream is not a pool member\"}\n"
				case "invalid mode":
					want.Body = "{\"error\":\"pool switch mode must be normal or force\"}\n"
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected switch result:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

func TestManagerPoolSwitchRejectsPoolWithoutWorkers(t *testing.T) {
	m := newPoolActionTestManager(t, config.UpstreamPoolModeActive, []string{"primary", "backup"}, nil)
	defer m.Close()
	response := requestManager(t, m, http.MethodPost, "/api/upstream-pools/coding-ha/switch", `{"upstream":"backup","mode":"force"}`)
	got := struct {
		Status int
		Body   string
		Events []map[string]any
	}{response.Code, response.Body.String(), poolRoutingEvents(m, EventUpstreamPoolSwitched)}
	want := struct {
		Status int
		Body   string
		Events []map[string]any
	}{http.StatusConflict, "{\"error\":\"upstream pool has no attached workers\"}\n", nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected no-worker switch result:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPoolModeChangedEventContract(t *testing.T) {
	m := newPoolActionTestManager(t, config.UpstreamPoolModeActive, []string{"primary", "backup"}, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()

	response := requestManager(t, m, http.MethodPatch, "/api/upstream-pools/coding-ha", `{"mode":"disabled"}`)
	events := m.events.Replay(0)
	gotEvent := events[len(events)-1]
	gotPool, gotPrevious, gotMode, gotOK := gotEvent.AsUpstreamPoolModeChanged()
	got := struct {
		Status   int
		Event    Event
		Pool     string
		Previous config.UpstreamPoolMode
		Mode     config.UpstreamPoolMode
		OK       bool
	}{response.Code, gotEvent, gotPool, gotPrevious, gotMode, gotOK}
	want := struct {
		Status   int
		Event    Event
		Pool     string
		Previous config.UpstreamPoolMode
		Mode     config.UpstreamPoolMode
		OK       bool
	}{http.StatusOK, Event{
		ID: gotEvent.ID, Type: EventUpstreamPoolModeChanged, At: gotEvent.At,
		Payload: map[string]any{
			"pool": "coding-ha", "previous_mode": config.UpstreamPoolModeActive, "mode": config.UpstreamPoolModeDisabled,
		},
	}, "coding-ha", config.UpstreamPoolModeActive, config.UpstreamPoolModeDisabled, true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected pool mode event contract:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPoolSwitchRollsBackPartialFailure(t *testing.T) {
	client := &recordingWorkerClient{
		runtimeStates: map[int]appruntime.WorkerRuntime{
			6767: {Upstream: appruntime.UpstreamRuntime{ID: "primary"}},
			6768: {Upstream: appruntime.UpstreamRuntime{ID: "primary"}},
		},
		applyErrors: map[int][]error{6768: {errors.New("worker rejected runtime")}},
	}
	m := newPoolActionTestManager(t, config.UpstreamPoolModeActive, []string{"primary", "backup"}, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		"cli": {Port: 6768, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	m.workerClient = client
	defer m.Close()
	m.mu.Lock()
	m.statuses["app"] = WorkerStateRunning
	m.statuses["cli"] = WorkerStateRunning
	m.mu.Unlock()

	response := requestManager(t, m, http.MethodPost, "/api/upstream-pools/coding-ha/switch", `{"upstream":"backup","mode":"force"}`)
	got := struct {
		Status     int
		Body       string
		Configured map[string]string
		Applied    map[int]string
		Events     []map[string]any
	}{response.Code, response.Body.String(), map[string]string{
		"app": workerUpstreamID(m.config.Workers["app"]),
		"cli": workerUpstreamID(m.config.Workers["cli"]),
	}, map[int]string{
		6767: string(client.runtimeStates[6767].Upstream.ID),
		6768: string(client.runtimeStates[6768].Upstream.ID),
	}, poolRoutingEvents(m, EventUpstreamPoolSwitched)}
	want := struct {
		Status     int
		Body       string
		Configured map[string]string
		Applied    map[int]string
		Events     []map[string]any
	}{http.StatusInternalServerError, "{\"error\":\"worker cli: worker rejected runtime\"}\n",
		map[string]string{"app": "primary", "cli": "primary"},
		map[int]string{6767: "primary", 6768: "primary"}, nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partial switch did not fully roll back:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerPoolActionsRejectInvalidPathsWithoutMutation(t *testing.T) {
	paths := []string{
		"/api/upstream-pools/coding-ha/",
		"/api/upstream-pools/coding-ha/probe/extra",
		"/api/upstream-pools/coding-ha/unknown",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			m := newPoolActionTestManager(t, config.UpstreamPoolModeActive, []string{"primary", "backup"}, map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
			})
			defer m.Close()

			response := requestManager(t, m, http.MethodPost, path, `{"upstream":"backup","mode":"force"}`)
			m.mu.RLock()
			worker := cloneWorkerConfig(m.config.Workers["app"])
			m.mu.RUnlock()
			got := struct {
				Status     int
				Body       string
				Active     string
				Worker     config.WorkerConfig
				Desired    map[probeExecutionKey]probeSpec
				Manual     map[probeExecutionKey]probeSpec
				Pending    map[probeExecutionKey]probeSpec
				Schedules  map[poolProbeScheduleKey]poolProbeSchedule
				Readiness  map[string]readinessObservation
				PoolEvents []map[string]any
			}{response.Code, response.Body.String(), m.poolActiveUpstream("coding-ha"), worker,
				maps.Clone(m.desiredProbes), maps.Clone(m.manualProbes), maps.Clone(m.pendingProbes),
				maps.Clone(m.probeSchedules), maps.Clone(m.readiness), poolRoutingEvents(m, EventUpstreamPoolSwitched)}
			want := struct {
				Status     int
				Body       string
				Active     string
				Worker     config.WorkerConfig
				Desired    map[probeExecutionKey]probeSpec
				Manual     map[probeExecutionKey]probeSpec
				Pending    map[probeExecutionKey]probeSpec
				Schedules  map[poolProbeScheduleKey]poolProbeSchedule
				Readiness  map[string]readinessObservation
				PoolEvents []map[string]any
			}{http.StatusNotFound, "404 page not found\n", "primary", config.WorkerConfig{
				Name: "app", Role: "cli", Launcher: "codex", Port: 6767,
				Upstream: "primary", UpstreamID: "primary", UpstreamPool: "coding-ha", LogLevel: "simple",
				RequestModules: map[string]config.ModuleConfig{}, Hooks: map[string]config.ModuleConfig{},
			}, map[probeExecutionKey]probeSpec{}, map[probeExecutionKey]probeSpec{}, map[probeExecutionKey]probeSpec{},
				map[poolProbeScheduleKey]poolProbeSchedule{}, map[string]readinessObservation{}, nil}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("invalid action path mutated state:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

func TestManagerPoolSwitchRejectsInvalidTargetRuntime(t *testing.T) {
	m := newPoolActionTestManager(t, config.UpstreamPoolModeActive, []string{"primary", "backup"}, map[string]config.WorkerConfig{
		"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
	})
	defer m.Close()
	m.mu.Lock()
	profile := m.config.Upstreams["backup"]
	profile.BaseURL = ""
	m.config.Upstreams["backup"] = profile
	m.mu.Unlock()

	response := requestManager(t, m, http.MethodPost, "/api/upstream-pools/coding-ha/switch", `{"upstream":"backup","mode":"force"}`)
	got := struct {
		Status int
		Body   string
		Active string
		Worker config.WorkerConfig
		Events []map[string]any
	}{response.Code, response.Body.String(), m.poolActiveUpstream("coding-ha"), cloneWorkerConfig(m.config.Workers["app"]), poolRoutingEvents(m, EventUpstreamPoolSwitched)}
	want := struct {
		Status int
		Body   string
		Active string
		Worker config.WorkerConfig
		Events []map[string]any
	}{http.StatusInternalServerError, "{\"error\":\"upstream base URL is required\"}\n", "primary", config.WorkerConfig{
		Name: "app", Role: "cli", Launcher: "codex", Port: 6767,
		Upstream: "primary", UpstreamID: "primary", UpstreamPool: "coding-ha", LogLevel: "simple",
		RequestModules: map[string]config.ModuleConfig{}, Hooks: map[string]config.ModuleConfig{},
	}, nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid target runtime was not rejected:\n got %#v\nwant %#v", got, want)
	}
}

func newPoolActionTestManager(t *testing.T, mode config.UpstreamPoolMode, members []string, workers map[string]config.WorkerConfig) *Manager {
	t.Helper()
	return New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: t.TempDir()},
		Plugins:  testPluginDefinitions(),
		Workers:  workers,
		Upstreams: map[string]config.UpstreamProfile{
			"primary": {BaseURL: "https://primary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-primary"}},
			"backup":  {BaseURL: "https://backup.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "model-backup"}},
		},
		UpstreamPools: map[string]config.UpstreamPool{
			"coding-ha": {Mode: mode, Upstreams: members},
		},
	}, WorkerClient: &recordingWorkerClient{}})
}
