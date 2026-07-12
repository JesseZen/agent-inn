package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
)

type poolSwitchMode int

const (
	poolSwitchNormal poolSwitchMode = iota
	poolSwitchForced
)

var (
	errPoolTargetNotMember  = errors.New("target upstream is not a pool member")
	errPoolHasNoWorkers     = errors.New("upstream pool has no attached workers")
	errPoolTargetIneligible = errors.New("target upstream is not eligible")
)

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

func (m *Manager) poolProxyURL(poolName string) string {
	m.mu.RLock()
	names := make([]string, 0, len(m.config.Workers))
	for name, workerConfig := range m.config.Workers {
		if workerConfig.UpstreamPool == poolName {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	proxyURL := ""
	if len(names) > 0 {
		proxyURL = m.config.Workers[names[0]].ProxyURL
	}
	m.mu.RUnlock()
	return proxyURL
}

func (m *Manager) validatePoolAttachmentLocked(workerName string, worker config.WorkerConfig) error {
	m.mu.RLock()
	pool, exists := m.config.UpstreamPools[worker.UpstreamPool]
	workers := make(map[string]config.WorkerConfig, len(m.config.Workers))
	for name, configured := range m.config.Workers {
		workers[name] = configured
	}
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("upstream pool %q not found", worker.UpstreamPool)
	}
	upstreamName := workerUpstreamID(worker)
	member := false
	for _, candidate := range pool.Upstreams {
		if candidate == upstreamName {
			member = true
			break
		}
	}
	if !member {
		return errors.New("worker upstream is not a member of target pool")
	}
	names := make([]string, 0, len(workers))
	for name, configured := range workers {
		if name != workerName && configured.UpstreamPool == worker.UpstreamPool {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil
	}
	active := workers[names[0]]
	if workerUpstreamID(active) != upstreamName {
		return errors.New("worker upstream does not match target pool active upstream")
	}
	if active.ProxyURL != worker.ProxyURL {
		return errors.New("worker proxy_url does not match target pool proxy_url")
	}
	return nil
}

func (m *Manager) switchUpstreamPool(poolName string, previous string, next string) error {
	return m.switchUpstreamPoolMode(poolName, previous, next, poolSwitchNormal)
}

func (m *Manager) switchPoolActiveLocked(poolName string, next string, mode poolSwitchMode) error {
	m.mu.RLock()
	pool, exists := m.config.UpstreamPools[poolName]
	workers := make(map[string]config.WorkerConfig)
	for name, worker := range m.config.Workers {
		if worker.UpstreamPool == poolName {
			workers[name] = cloneWorkerConfig(worker)
		}
	}
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("upstream pool %q not found", poolName)
	}
	if !slices.Contains(pool.Upstreams, next) {
		return errPoolTargetNotMember
	}
	if len(workers) == 0 {
		return errPoolHasNoWorkers
	}
	for name, worker := range workers {
		worker.Upstream = next
		worker.UpstreamID = next
		if err := m.validateWorkerRuntime(name, worker); err != nil {
			return err
		}
	}
	if mode == poolSwitchNormal && !m.poolReadinessLocked(poolName, next).Eligible {
		return errPoolTargetIneligible
	}
	previous := m.poolActiveUpstream(poolName)
	return m.switchUpstreamPoolMode(poolName, previous, next, mode)
}

func (m *Manager) switchUpstreamPoolMode(poolName string, previous string, next string, mode poolSwitchMode) error {
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

	switched := make([]string, 0, len(names))
	for _, name := range names {
		current := workers[name]
		if workerUpstreamID(current) == next {
			continue
		}
		updated := current
		updated.Upstream = next
		updated.UpstreamID = next
		if err := m.UpdateWorker(name, current, updated); err != nil {
			switchErrors := []error{fmt.Errorf("worker %s: %w", name, err)}
			m.mu.Lock()
			m.statuses[name] = statuses[name]
			m.mu.Unlock()
			if rollbackErr := m.UpdateWorker(name, updated, current); rollbackErr != nil {
				switchErrors = append(switchErrors, fmt.Errorf("rollback worker %s: %w", name, rollbackErr))
			}
			for index := len(switched) - 1; index >= 0; index-- {
				rollbackName := switched[index]
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
		switched = append(switched, name)
	}
	payload := map[string]any{"pool": poolName, "previous_upstream": previous, "upstream": next}
	if mode == poolSwitchForced {
		payload["forced"] = true
	}
	delete(m.exhaustedPools, poolName)
	m.publishEvent(EventUpstreamPoolSwitched, payload)
	return nil
}

func (m *Manager) resetPoolIdentityLocked(poolName string) {
	m.mu.RLock()
	pool := m.config.UpstreamPools[poolName]
	m.mu.RUnlock()
	for _, upstreamName := range pool.Upstreams {
		m.invalidatePoolReadinessLocked(poolName, upstreamName)
		key := poolCircuitKey(poolName, upstreamName)
		previous := m.circuits.Status(key, pool.CircuitBreaker)
		m.circuits.Reset(key)
		m.publishCircuitTransition(poolName, upstreamName, previous, CircuitStatus{State: CircuitStateClosed}, "identity_changed")
	}
}

func (m *Manager) invalidatePoolProbeIdentityLocked(poolName string) {
	for key, spec := range m.desiredProbes {
		if !slices.Contains(spec.Pools, poolName) {
			continue
		}
		m.probeGenerations[key]++
		delete(m.desiredProbes, key)
		delete(m.pendingProbes, key)
	}
}

func workerHealthFingerprint(upstreamName string, profile config.UpstreamProfile, proxyURL string) (string, error) {
	runtime, err := upstream.ResolveRuntime(upstreamName, profile)
	if err != nil {
		return "", err
	}
	compiled, err := upstream.Compile(runtime)
	if err != nil {
		return "", err
	}
	credentialHash := sha256.Sum256([]byte(compiled.AuthorizationHeader))
	fingerprintHash := sha256.Sum256([]byte(strings.Join([]string{
		compiled.BaseURL.String(), string(compiled.APIFormat), hex.EncodeToString(credentialHash[:]), proxyURL,
	}, probeFingerprintSeparator)))
	return hex.EncodeToString(fingerprintHash[:]), nil
}
