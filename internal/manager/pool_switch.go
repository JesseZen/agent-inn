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

func (m *Manager) switchUpstreamPool(poolName string, previous string, next string) error {
	return m.switchUpstreamPoolMode(poolName, previous, next, poolSwitchNormal)
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
