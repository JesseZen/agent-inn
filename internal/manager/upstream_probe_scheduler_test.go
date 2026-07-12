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
	}{
		First:     first,
		Second:    second,
		Circuit:   m.circuits.Status(poolCircuitKey("coding-ha", "primary"), pool.CircuitBreaker),
		Readiness: m.poolReadiness("coding-ha", "primary"),
	}
	want := struct {
		First     probeSpec
		Second    probeSpec
		Circuit   CircuitStatus
		Readiness PoolReadiness
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
