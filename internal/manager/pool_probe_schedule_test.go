package manager

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
)

func TestPoolProbeScheduleStates(t *testing.T) {
	now := time.Date(2026, time.July, 13, 1, 2, 3, 0, time.UTC)

	t.Run("paused", func(t *testing.T) {
		m := newPoolProbeScheduleTestManager(t, now)
		defer m.Close()
		m.mu.Lock()
		pool := m.config.UpstreamPools["coding-ha"]
		pool.Mode = config.UpstreamPoolModeDisabled
		m.config.UpstreamPools["coding-ha"] = pool
		m.mu.Unlock()

		m.failoverMu.Lock()
		got := struct {
			State PoolProbeState
			Next  *time.Time
		}{m.poolProbeStateLocked("coding-ha"), m.poolNextProbeAtLocked("coding-ha")}
		m.failoverMu.Unlock()
		want := struct {
			State PoolProbeState
			Next  *time.Time
		}{
			State: PoolProbeStatePaused,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected paused schedule:\n got %#v\nwant %#v", got, want)
		}
	})

	t.Run("idle", func(t *testing.T) {
		m := newPoolProbeScheduleTestManager(t, now)
		defer m.Close()
		m.mu.Lock()
		m.config.Workers = map[string]config.WorkerConfig{}
		m.mu.Unlock()

		m.failoverMu.Lock()
		got := struct {
			State PoolProbeState
			Next  *time.Time
		}{m.poolProbeStateLocked("coding-ha"), m.poolNextProbeAtLocked("coding-ha")}
		m.failoverMu.Unlock()
		want := struct {
			State PoolProbeState
			Next  *time.Time
		}{
			State: PoolProbeStateIdle,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected idle schedule:\n got %#v\nwant %#v", got, want)
		}
	})

	t.Run("startup alert", func(t *testing.T) {
		m := newPoolProbeScheduleTestManager(t, now)
		defer m.Close()

		m.failoverMu.Lock()
		got := struct {
			State PoolProbeState
			Next  *time.Time
		}{m.poolProbeStateLocked("coding-ha"), m.poolNextProbeAtLocked("coding-ha")}
		m.failoverMu.Unlock()
		want := struct {
			State PoolProbeState
			Next  *time.Time
		}{
			State: PoolProbeStateAlert,
			Next:  &now,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected startup schedule:\n got %#v\nwant %#v", got, want)
		}
	})

	t.Run("healthy stable", func(t *testing.T) {
		m := newPoolProbeScheduleTestManager(t, now)
		defer m.Close()
		next := now.Add(15 * time.Minute)
		m.failoverMu.Lock()
		markPoolProbeScheduleTestHealthy(m, now, next)
		got := struct {
			State PoolProbeState
			Next  *time.Time
		}{m.poolProbeStateLocked("coding-ha"), m.poolNextProbeAtLocked("coding-ha")}
		m.failoverMu.Unlock()
		want := struct {
			State PoolProbeState
			Next  *time.Time
		}{
			State: PoolProbeStateStable,
			Next:  &next,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected healthy schedule:\n got %#v\nwant %#v", got, want)
		}
	})

	t.Run("first request failure alert", func(t *testing.T) {
		m := newPoolProbeScheduleTestManager(t, now)
		defer m.Close()
		next := now.Add(15 * time.Minute)
		m.failoverMu.Lock()
		markPoolProbeScheduleTestHealthy(m, now, next)
		pool := m.config.UpstreamPools["coding-ha"]
		m.circuits.RecordFailure(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker)
		got := struct {
			State PoolProbeState
			Next  *time.Time
		}{m.poolProbeStateLocked("coding-ha"), m.poolNextProbeAtLocked("coding-ha")}
		m.failoverMu.Unlock()
		want := struct {
			State PoolProbeState
			Next  *time.Time
		}{
			State: PoolProbeStateAlert,
			Next:  &next,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected request failure schedule:\n got %#v\nwant %#v", got, want)
		}
	})

	t.Run("exhausted alert", func(t *testing.T) {
		m := newPoolProbeScheduleTestManager(t, now)
		defer m.Close()
		next := now.Add(15 * time.Minute)
		m.failoverMu.Lock()
		markPoolProbeScheduleTestHealthy(m, now, next)
		m.exhaustedPools["coding-ha"] = "primary"
		got := struct {
			State PoolProbeState
			Next  *time.Time
		}{m.poolProbeStateLocked("coding-ha"), m.poolNextProbeAtLocked("coding-ha")}
		m.failoverMu.Unlock()
		want := struct {
			State PoolProbeState
			Next  *time.Time
		}{
			State: PoolProbeStateAlert,
			Next:  &next,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected exhausted schedule:\n got %#v\nwant %#v", got, want)
		}
	})

	t.Run("next deadline", func(t *testing.T) {
		m := newPoolProbeScheduleTestManager(t, now)
		defer m.Close()
		next := now.Add(8 * time.Minute)
		later := now.Add(15 * time.Minute)
		m.failoverMu.Lock()
		markPoolProbeScheduleTestHealthy(m, now, later)
		m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: "backup"}] = poolProbeSchedule{
			NextProbeAt: next,
			Reason:      ProbeScheduleStable,
		}
		got := struct {
			State PoolProbeState
			Next  *time.Time
		}{m.poolProbeStateLocked("coding-ha"), m.poolNextProbeAtLocked("coding-ha")}
		m.failoverMu.Unlock()
		want := struct {
			State PoolProbeState
			Next  *time.Time
		}{
			State: PoolProbeStateStable,
			Next:  &next,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected next deadline:\n got %#v\nwant %#v", got, want)
		}
	})
}

func TestPoolProbeScheduleFailureDelay(t *testing.T) {
	policy := config.PoolProbeConfig{StableIntervalSeconds: 900, AlertIntervalSeconds: 60}
	got := make([]time.Duration, 0, 5)
	for failures := 1; failures <= 5; failures++ {
		got = append(got, poolProbeFailureDelay(policy, failures))
	}
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected failure delays:\n got %#v\nwant %#v", got, want)
	}
}

func newPoolProbeScheduleTestManager(t *testing.T, now time.Time) *Manager {
	t.Helper()
	m := New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: t.TempDir()},
		Workers: map[string]config.WorkerConfig{
			"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"primary": {BaseURL: "https://primary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "probe-primary"}},
			"backup":  {BaseURL: "https://backup.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "probe-backup"}},
		},
		UpstreamPools: map[string]config.UpstreamPool{
			"coding-ha": {Upstreams: []string{"primary", "backup"}},
		},
	}})
	m.cancelProbes()
	m.clock = func() time.Time { return now }
	return m
}

func markPoolProbeScheduleTestHealthy(m *Manager, now time.Time, next time.Time) {
	for _, upstreamName := range []string{"primary", "backup"} {
		m.readiness[poolCircuitKey("coding-ha", upstreamName)] = readinessObservation{
			Result: upstream.ProbeResult{
				OK: true, StatusCode: http.StatusOK,
				Mode: upstream.ProbeModeProtocol, Authoritative: true,
			},
			CheckedAt: now,
		}
		m.probeSchedules[poolProbeScheduleKey{Pool: "coding-ha", Upstream: upstreamName}] = poolProbeSchedule{
			NextProbeAt: next,
			Reason:      ProbeScheduleStable,
		}
	}
}
