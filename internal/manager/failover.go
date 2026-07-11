package manager

import (
	"errors"
	"fmt"
	"sort"
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
	defer m.failoverMu.Unlock()
	m.mu.RLock()
	workerConfig, ok := m.config.Workers[workerName]
	status := m.workerStatusLocked(workerName)
	currentGeneration := m.workerGenerationLocked(workerName)
	if supervisor := m.supervisors[workerName]; supervisor != nil && supervisor.AppliedGeneration() > 0 {
		currentGeneration = supervisor.AppliedGeneration()
	}
	if !ok || status != WorkerStateRunning || generation < 1 || generation != currentGeneration || workerConfig.UpstreamPool == "" || workerUpstreamID(workerConfig) != upstreamName {
		m.mu.RUnlock()
		return nil
	}
	poolName := workerConfig.UpstreamPool
	pool, ok := m.config.UpstreamPools[poolName]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("upstream pool %q not found", poolName)
	}

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
	if outcome != workerUpstreamFailure || current.State != CircuitStateOpen {
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
		return nil
	}
	return m.switchUpstreamPool(poolName, upstreamName, next)
}

func (m *Manager) recordUpstreamProbeResult(upstreamName string, probe upstream.ProbeResult) error {
	if probe.Mode != upstream.ProbeModeProtocol || !probe.Authoritative {
		return nil
	}
	m.failoverMu.Lock()
	defer m.failoverMu.Unlock()
	m.mu.RLock()
	poolNames := make([]string, 0, len(m.config.UpstreamPools))
	for poolName, pool := range m.config.UpstreamPools {
		for _, candidate := range pool.Upstreams {
			if candidate == upstreamName {
				poolNames = append(poolNames, poolName)
				break
			}
		}
	}
	m.mu.RUnlock()
	sort.Strings(poolNames)

	var recoveryErrors []error
	for _, poolName := range poolNames {
		if err := m.recordPoolProbeResultLocked(poolName, upstreamName, probe); err != nil {
			recoveryErrors = append(recoveryErrors, err)
		}
	}
	return errors.Join(recoveryErrors...)
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

func (m *Manager) poolActiveUpstream(poolName string) string {
	m.mu.RLock()
	names := make([]string, 0, len(m.config.Workers))
	for name, workerConfig := range m.config.Workers {
		if workerConfig.UpstreamPool == poolName {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	active := ""
	if len(names) > 0 {
		active = workerUpstreamID(m.config.Workers[names[0]])
	}
	m.mu.RUnlock()
	return active
}

func (m *Manager) publishCircuitTransition(poolName string, upstreamName string, previous CircuitStatus, current CircuitStatus) {
	if previous.State == current.State {
		return
	}
	m.publishEvent(EventUpstreamCircuitChanged, map[string]any{
		"pool":     poolName,
		"upstream": upstreamName,
		"state":    current.State,
	})
}

func poolCircuitKey(poolName string, upstreamName string) string {
	return poolName + circuitKeySeparator + upstreamName
}

func (m *Manager) switchUpstreamPool(poolName string, previous string, next string) error {
	m.mu.RLock()
	names := make([]string, 0, len(m.config.Workers))
	workers := make(map[string]config.WorkerConfig, len(m.config.Workers))
	for name, workerConfig := range m.config.Workers {
		if workerConfig.UpstreamPool != poolName {
			continue
		}
		names = append(names, name)
		workers[name] = cloneWorkerConfig(workerConfig)
	}
	m.mu.RUnlock()
	sort.Strings(names)
	statuses := make(map[string]WorkerState, len(names))
	for _, name := range names {
		statuses[name] = m.workerStatus(name)
	}

	for index, name := range names {
		current := workers[name]
		if workerUpstreamID(current) == next {
			continue
		}
		updated := current
		updated.Upstream = next
		updated.UpstreamID = next
		if err := m.UpdateWorker(name, current, updated); err != nil {
			switchErrors := []error{fmt.Errorf("worker %s: %w", name, err)}
			for rollbackIndex := index; rollbackIndex >= 0; rollbackIndex-- {
				rollbackName := names[rollbackIndex]
				original := workers[rollbackName]
				configured := original
				configured.Upstream = next
				configured.UpstreamID = next
				m.mu.Lock()
				m.statuses[rollbackName] = statuses[rollbackName]
				m.mu.Unlock()
				if rollbackErr := m.UpdateWorker(rollbackName, configured, original); rollbackErr != nil {
					switchErrors = append(switchErrors, fmt.Errorf("rollback worker %s: %w", rollbackName, rollbackErr))
				}
			}
			return errors.Join(switchErrors...)
		}
	}
	m.publishEvent(EventUpstreamPoolSwitched, map[string]any{
		"pool":              poolName,
		"previous_upstream": previous,
		"upstream":          next,
	})
	return nil
}
