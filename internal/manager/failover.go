package manager

import (
	"fmt"
	"sync"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
)

type CircuitState string

const (
	CircuitStateClosed   CircuitState = "closed"
	CircuitStateOpen     CircuitState = "open"
	CircuitStateHalfOpen CircuitState = "half_open"
	circuitKeySeparator               = "\x00"
)

type CircuitStatus struct {
	State               CircuitState `json:"state"`
	ConsecutiveFailures int          `json:"consecutive_failures"`
	RecoverySuccesses   int          `json:"recovery_successes"`
	OpenedAt            time.Time    `json:"opened_at,omitempty"`
}

type circuitBreaker struct {
	mu     sync.Mutex
	clock  func() time.Time
	states map[string]CircuitStatus
}

func newCircuitBreaker(clock func() time.Time) *circuitBreaker {
	return &circuitBreaker{clock: clock, states: map[string]CircuitStatus{}}
}

func (b *circuitBreaker) Status(upstream string, _ config.CircuitBreakerConfig) CircuitStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.statusLocked(upstream)
}

func (b *circuitBreaker) statusLocked(upstream string) CircuitStatus {
	state, ok := b.states[upstream]
	if !ok {
		return CircuitStatus{State: CircuitStateClosed}
	}
	return state
}

func (b *circuitBreaker) Allow(upstream string, policy config.CircuitBreakerConfig) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.statusLocked(upstream)
	switch state.State {
	case CircuitStateClosed, CircuitStateHalfOpen:
		return true
	case CircuitStateOpen:
		if b.clock().Sub(state.OpenedAt) < time.Duration(policy.RecoveryWaitSeconds)*time.Second {
			return false
		}
		state.State = CircuitStateHalfOpen
		state.RecoverySuccesses = 0
		b.states[upstream] = state
		return true
	default:
		return false
	}
}

func (b *circuitBreaker) RecordFailure(upstream string, policy config.CircuitBreakerConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.statusLocked(upstream)
	state.ConsecutiveFailures++
	state.RecoverySuccesses = 0
	if state.State == CircuitStateHalfOpen || state.ConsecutiveFailures >= policy.FailureThreshold {
		state.State = CircuitStateOpen
		state.OpenedAt = b.clock()
	}
	b.states[upstream] = state
}

func (b *circuitBreaker) RecordSuccess(upstream string, policy config.CircuitBreakerConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.statusLocked(upstream)
	if state.State == CircuitStateHalfOpen {
		state.RecoverySuccesses++
		if state.RecoverySuccesses < policy.RecoverySuccessThreshold {
			b.states[upstream] = state
			return
		}
	}
	b.states[upstream] = CircuitStatus{State: CircuitStateClosed}
}

func (b *circuitBreaker) RecordRecoveryFailure(upstream string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.statusLocked(upstream)
	state.State = CircuitStateOpen
	state.RecoverySuccesses = 0
	state.OpenedAt = b.clock()
	b.states[upstream] = state
}

func (b *circuitBreaker) Reset(upstream string) {
	b.mu.Lock()
	b.states[upstream] = CircuitStatus{State: CircuitStateClosed}
	b.mu.Unlock()
}

type workerUpstreamOutcome int

const (
	workerUpstreamSuccess workerUpstreamOutcome = iota
	workerUpstreamFailure
)

func (m *Manager) recordWorkerUpstreamFailure(workerName string, upstreamName string) error {
	return m.recordWorkerUpstreamOutcome(workerName, upstreamName, m.workerGeneration(workerName), workerUpstreamFailure)
}

func (m *Manager) recordWorkerUpstreamSuccess(workerName string, upstreamName string) error {
	return m.recordWorkerUpstreamOutcome(workerName, upstreamName, m.workerGeneration(workerName), workerUpstreamSuccess)
}

func (m *Manager) recordWorkerUpstreamOutcome(workerName string, upstreamName string, generation int, outcome workerUpstreamOutcome) error {
	m.failoverMu.Lock()
	m.mu.RLock()
	workerConfig, ok := m.config.Workers[workerName]
	status := m.workerStatusLocked(workerName)
	currentGeneration := m.workerGenerationLocked(workerName)
	if supervisor := m.supervisors[workerName]; supervisor != nil && supervisor.AppliedGeneration() > 0 {
		currentGeneration = supervisor.AppliedGeneration()
	}
	if !ok || status != WorkerStateRunning || generation < 1 || generation != currentGeneration || workerConfig.UpstreamPool == "" || workerUpstreamID(workerConfig) != upstreamName {
		m.mu.RUnlock()
		m.failoverMu.Unlock()
		return nil
	}
	poolName := workerConfig.UpstreamPool
	pool, ok := m.config.UpstreamPools[poolName]
	m.mu.RUnlock()
	if !ok {
		m.failoverMu.Unlock()
		return fmt.Errorf("upstream pool %q not found", poolName)
	}
	if pool.Mode == config.UpstreamPoolModeDisabled {
		m.failoverMu.Unlock()
		return nil
	}
	previousProbeState := m.poolProbeStateLocked(poolName)
	previousNextProbeAt := m.poolNextProbeAtLocked(poolName)

	key := poolCircuitKey(poolName, upstreamName)
	previous := m.circuits.Status(key, pool.CircuitBreaker)
	switch previous.State {
	case CircuitStateClosed:
		if outcome == workerUpstreamFailure {
			m.circuits.RecordFailure(key, pool.CircuitBreaker)
		} else {
			m.circuits.RecordSuccess(key, pool.CircuitBreaker)
		}
	case CircuitStateHalfOpen:
		if outcome == workerUpstreamFailure {
			m.circuits.RecordRecoveryFailure(key)
		}
	case CircuitStateOpen:
	}
	current := m.circuits.Status(key, pool.CircuitBreaker)
	m.publishCircuitTransition(poolName, upstreamName, previous, current)
	refreshFallbacks := outcome == workerUpstreamFailure && previous.State == CircuitStateClosed && previous.ConsecutiveFailures == 0 && current.ConsecutiveFailures == 1
	if refreshFallbacks {
		now := m.clock()
		for _, candidate := range pool.Upstreams {
			if candidate == upstreamName {
				continue
			}
			m.probeSchedules[poolProbeScheduleKey{Pool: poolName, Upstream: candidate}] = poolProbeSchedule{
				NextProbeAt: now,
				Reason:      ProbeScheduleWorkerFailure,
			}
		}
	}
	if outcome != workerUpstreamFailure || current.State != CircuitStateOpen {
		m.publishPoolStateTransitionLocked(poolName, previousProbeState, previousNextProbeAt)
		m.failoverMu.Unlock()
		if refreshFallbacks {
			m.probeAllUpstreams(m.probeContext)
		}
		return nil
	}

	next := ""
	for _, candidate := range pool.Upstreams {
		if candidate != upstreamName && m.poolReadinessLocked(poolName, candidate).Eligible {
			next = candidate
			break
		}
	}
	if next == "" || next == upstreamName {
		if m.exhaustedPools[poolName] != upstreamName {
			m.exhaustedPools[poolName] = upstreamName
			m.publishEvent(EventUpstreamPoolExhausted, map[string]any{"pool": poolName, "upstream": upstreamName, "reason": "no_eligible_fallback"})
		}
		m.publishPoolStateTransitionLocked(poolName, previousProbeState, previousNextProbeAt)
		m.failoverMu.Unlock()
		if refreshFallbacks {
			m.probeAllUpstreams(m.probeContext)
		}
		return nil
	}
	err := m.switchUpstreamPool(poolName, upstreamName, next)
	m.publishPoolStateTransitionLocked(poolName, previousProbeState, previousNextProbeAt)
	m.failoverMu.Unlock()
	if refreshFallbacks {
		m.probeAllUpstreams(m.probeContext)
	}
	return err
}

func (m *Manager) publishPoolStateTransitionLocked(poolName string, previousState PoolProbeState, previousNext *time.Time) {
	state := m.poolProbeStateLocked(poolName)
	next := m.poolNextProbeAtLocked(poolName)
	deadlineChanged := (previousNext == nil) != (next == nil)
	if previousNext != nil && next != nil && !previousNext.Equal(*next) {
		deadlineChanged = true
	}
	if previousState == state && !deadlineChanged {
		return
	}
	payload := map[string]any{"pool": poolName, "probe_state": state}
	if next != nil {
		payload["next_probe_at"] = next.UTC().Format(time.RFC3339)
	}
	m.publishEvent(EventUpstreamPoolStateChanged, payload)
}

func (m *Manager) recordPoolProbeResultLocked(poolName string, upstreamName string, probe upstream.ProbeResult) error {
	m.mu.RLock()
	pool, exists := m.config.UpstreamPools[poolName]
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("upstream pool %q not found", poolName)
	}
	key := poolCircuitKey(poolName, upstreamName)
	previous := m.circuits.Status(key, pool.CircuitBreaker)
	beforeProbe := previous
	if previous.State == CircuitStateOpen {
		if !m.circuits.Allow(key, pool.CircuitBreaker) {
			return nil
		}
		beforeProbe = m.circuits.Status(key, pool.CircuitBreaker)
		m.publishCircuitTransition(poolName, upstreamName, previous, beforeProbe)
	}
	if beforeProbe.State == CircuitStateHalfOpen {
		if probe.OK {
			m.circuits.RecordSuccess(key, pool.CircuitBreaker)
		} else {
			m.circuits.RecordRecoveryFailure(key)
		}
	}
	afterProbe := m.circuits.Status(key, pool.CircuitBreaker)
	m.publishCircuitTransition(poolName, upstreamName, beforeProbe, afterProbe)

	active := m.poolActiveUpstream(poolName)
	activeReadiness := m.poolReadinessLocked(poolName, active)
	if active != "" && !activeReadiness.Eligible {
		next := ""
		for _, candidate := range pool.Upstreams {
			if candidate != active && m.poolReadinessLocked(poolName, candidate).Eligible {
				next = candidate
				break
			}
		}
		if next != "" {
			return m.switchUpstreamPool(poolName, active, next)
		}
		if m.exhaustedPools[poolName] != active {
			m.exhaustedPools[poolName] = active
			m.publishEvent(EventUpstreamPoolExhausted, map[string]any{"pool": poolName, "upstream": active, "reason": "no_eligible_fallback"})
		}
		return nil
	}
	delete(m.exhaustedPools, poolName)
	recoveredIndex := -1
	activeIndex := -1
	for index, candidate := range pool.Upstreams {
		if candidate == upstreamName {
			recoveredIndex = index
		}
		if candidate == active {
			activeIndex = index
		}
	}
	if probe.OK && m.poolReadinessLocked(poolName, upstreamName).Eligible && recoveredIndex >= 0 && activeIndex >= 0 && recoveredIndex < activeIndex {
		return m.switchUpstreamPool(poolName, active, upstreamName)
	}
	return nil
}

func (m *Manager) publishCircuitTransition(poolName string, upstreamName string, previous CircuitStatus, current CircuitStatus, reasons ...string) {
	if previous.State == current.State {
		return
	}
	payload := map[string]any{
		"pool":     poolName,
		"upstream": upstreamName,
		"state":    current.State,
	}
	if len(reasons) > 0 {
		payload["reason"] = reasons[0]
	}
	m.publishEvent(EventUpstreamCircuitChanged, payload)
}

func poolCircuitKey(poolName string, upstreamName string) string {
	return poolName + circuitKeySeparator + upstreamName
}
