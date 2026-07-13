package manager

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jesse/agent-inn/internal/config"
)

type upstreamPoolSummary struct {
	ID             string                      `json:"id"`
	Name           string                      `json:"name"`
	Mode           config.UpstreamPoolMode     `json:"mode"`
	Upstreams      []string                    `json:"upstreams"`
	Probe          config.PoolProbeConfig      `json:"probe"`
	CircuitBreaker config.CircuitBreakerConfig `json:"circuit_breaker"`
	ActiveUpstream string                      `json:"active_upstream,omitempty"`
	Workers        []string                    `json:"workers"`
	ProbeState     PoolProbeState              `json:"probe_state"`
	NextProbeAt    *time.Time                  `json:"next_probe_at,omitempty"`
	Readiness      []PoolReadiness             `json:"readiness"`
}

func (m *Manager) handleUpstreamPools(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(rw, http.StatusOK, map[string]any{"pools": m.upstreamPoolSummaries()})
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(rw, r)
		return
	}
	var payload struct {
		Name           string                       `json:"name"`
		Mode           *config.UpstreamPoolMode     `json:"mode"`
		Upstreams      []string                     `json:"upstreams"`
		Probe          *config.PoolProbeConfig      `json:"probe"`
		CircuitBreaker *config.CircuitBreakerConfig `json:"circuit_breaker"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" || strings.Contains(name, "/") {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "upstream pool name is required"})
		return
	}
	m.mu.RLock()
	_, exists := m.config.UpstreamPools[name]
	candidate := cloneConfig(m.config)
	m.mu.RUnlock()
	if exists {
		writeJSON(rw, http.StatusConflict, map[string]any{"error": "upstream pool already exists"})
		return
	}
	pool := config.UpstreamPool{Name: name, Upstreams: normalizedPoolMembers(payload.Upstreams)}
	if payload.Mode != nil {
		pool.Mode = *payload.Mode
	}
	if payload.Probe != nil {
		pool.Probe = *payload.Probe
	}
	if payload.CircuitBreaker != nil {
		pool.CircuitBreaker = *payload.CircuitBreaker
	}
	candidate.UpstreamPools[name] = pool
	candidate.ApplyDefaults()
	if err := candidate.Validate(); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
		return
	}

	m.failoverMu.Lock()
	m.updateConfig(func(cfgRoot *config.Config) { cfgRoot.UpstreamPools[name] = candidate.UpstreamPools[name] })
	for _, upstreamName := range candidate.UpstreamPools[name].Upstreams {
		m.invalidatePoolReadinessLocked(name, upstreamName)
	}
	m.invalidatePoolProbeIdentityLocked(name)
	m.failoverMu.Unlock()
	m.probeAllUpstreams(m.probeContext)
	writeJSON(rw, http.StatusCreated, m.upstreamPoolSummary(name))
}

func (m *Manager) handleUpstreamPoolByName(rw http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/api/upstream-pools/")
	parts := strings.Split(suffix, "/")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		http.NotFound(rw, r)
		return
	}
	name := parts[0]
	m.mu.RLock()
	current, exists := m.config.UpstreamPools[name]
	candidate := cloneConfig(m.config)
	m.mu.RUnlock()
	if !exists {
		http.NotFound(rw, r)
		return
	}
	if len(parts) == 2 {
		if r.Method == http.MethodPost && parts[1] == "switch" {
			m.handleUpstreamPoolSwitch(rw, r, name)
			return
		}
		if r.Method == http.MethodPost && parts[1] == "probe" {
			m.handleUpstreamPoolProbe(rw, r, name)
			return
		}
		http.NotFound(rw, r)
		return
	}
	if r.Method == http.MethodDelete {
		delete(candidate.UpstreamPools, name)
		candidate.ApplyDefaults()
		if err := candidate.Validate(); err != nil {
			writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		m.failoverMu.Lock()
		m.updateConfig(func(cfgRoot *config.Config) { delete(cfgRoot.UpstreamPools, name) })
		for _, upstreamName := range current.Upstreams {
			m.invalidatePoolReadinessLocked(name, upstreamName)
			m.circuits.Reset(poolCircuitKey(name, upstreamName))
		}
		m.invalidatePoolProbeIdentityLocked(name)
		delete(m.exhaustedPools, name)
		m.failoverMu.Unlock()
		m.probeAllUpstreams(m.probeContext)
		writeJSON(rw, http.StatusOK, map[string]any{"pool": name})
		return
	}
	if r.Method != http.MethodPatch {
		http.NotFound(rw, r)
		return
	}
	var patch struct {
		Mode           *config.UpstreamPoolMode     `json:"mode"`
		Upstreams      *[]string                    `json:"upstreams"`
		Probe          *config.PoolProbeConfig      `json:"probe"`
		CircuitBreaker *config.CircuitBreakerConfig `json:"circuit_breaker"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	m.failoverMu.Lock()
	m.mu.RLock()
	current, exists = m.config.UpstreamPools[name]
	candidate = cloneConfig(m.config)
	m.mu.RUnlock()
	if !exists {
		m.failoverMu.Unlock()
		http.NotFound(rw, r)
		return
	}
	next := current
	if patch.Mode != nil {
		next.Mode = *patch.Mode
	}
	if patch.Upstreams != nil {
		next.Upstreams = normalizedPoolMembers(*patch.Upstreams)
	}
	if patch.Probe != nil {
		next.Probe = *patch.Probe
	}
	if patch.CircuitBreaker != nil {
		next.CircuitBreaker = *patch.CircuitBreaker
	}
	candidate.UpstreamPools[name] = next
	candidate.ApplyDefaults()
	if err := candidate.Validate(); err != nil {
		m.failoverMu.Unlock()
		writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	next = candidate.UpstreamPools[name]

	m.updateConfig(func(cfgRoot *config.Config) { cfgRoot.UpstreamPools[name] = next })
	attached := false
	m.mu.RLock()
	for _, worker := range m.config.Workers {
		if worker.UpstreamPool == name {
			attached = true
			break
		}
	}
	m.mu.RUnlock()
	currentMembers := make(map[string]struct{}, len(current.Upstreams))
	for _, upstreamName := range current.Upstreams {
		currentMembers[upstreamName] = struct{}{}
	}
	nextMembers := make(map[string]struct{}, len(next.Upstreams))
	for _, upstreamName := range next.Upstreams {
		nextMembers[upstreamName] = struct{}{}
	}
	seen := make(map[string]struct{}, len(current.Upstreams)+len(next.Upstreams))
	for _, upstreamName := range append(append([]string(nil), current.Upstreams...), next.Upstreams...) {
		if _, ok := seen[upstreamName]; ok {
			continue
		}
		seen[upstreamName] = struct{}{}
		m.invalidatePoolReadinessLocked(name, upstreamName)
		_, existed := currentMembers[upstreamName]
		_, remains := nextMembers[upstreamName]
		if existed != remains {
			m.circuits.Reset(poolCircuitKey(name, upstreamName))
		}
	}
	scheduleChanged := current.Mode != next.Mode || current.Probe != next.Probe || !slices.Equal(current.Upstreams, next.Upstreams)
	if scheduleChanged {
		for key := range m.probeSchedules {
			if key.Pool == name {
				delete(m.probeSchedules, key)
			}
		}
		if next.Mode == config.UpstreamPoolModeActive && attached {
			for _, upstreamName := range next.Upstreams {
				m.probeSchedules[poolProbeScheduleKey{Pool: name, Upstream: upstreamName}] = poolProbeSchedule{
					NextProbeAt: m.clock(),
					Reason:      ProbeScheduleConfig,
				}
			}
		}
	}
	if current.Mode != next.Mode && next.Mode == config.UpstreamPoolModeActive {
		for _, upstreamName := range next.Upstreams {
			m.invalidatePoolReadinessLocked(name, upstreamName)
			m.circuits.Reset(poolCircuitKey(name, upstreamName))
		}
	}
	m.invalidatePoolProbeIdentityLocked(name)
	delete(m.exhaustedPools, name)
	if current.Mode != next.Mode {
		m.publishEvent(EventUpstreamPoolModeChanged, map[string]any{
			"pool": name, "previous_mode": current.Mode, "mode": next.Mode,
		})
	}
	m.failoverMu.Unlock()
	m.probeAllUpstreams(m.probeContext)
	writeJSON(rw, http.StatusOK, m.upstreamPoolSummary(name))
}

func (m *Manager) upstreamPoolSummaries() []upstreamPoolSummary {
	m.mu.RLock()
	names := make([]string, 0, len(m.config.UpstreamPools))
	for name := range m.config.UpstreamPools {
		names = append(names, name)
	}
	m.mu.RUnlock()
	sort.Strings(names)
	out := make([]upstreamPoolSummary, 0, len(names))
	for _, name := range names {
		out = append(out, m.upstreamPoolSummary(name))
	}
	return out
}

func (m *Manager) upstreamPoolSummary(name string) upstreamPoolSummary {
	m.failoverMu.Lock()
	m.mu.RLock()
	pool := m.config.UpstreamPools[name]
	workers := make([]string, 0)
	for workerName, worker := range m.config.Workers {
		if worker.UpstreamPool == name {
			workers = append(workers, workerName)
		}
	}
	m.mu.RUnlock()
	sort.Strings(workers)
	readiness := make([]PoolReadiness, 0, len(pool.Upstreams))
	for _, upstreamName := range pool.Upstreams {
		readiness = append(readiness, m.poolReadinessLocked(name, upstreamName))
	}
	probeState := m.poolProbeStateLocked(name)
	nextProbeAt := m.poolNextProbeAtLocked(name)
	active := ""
	if len(workers) > 0 {
		active = m.poolActiveUpstream(name)
	}
	m.failoverMu.Unlock()
	return upstreamPoolSummary{
		ID:             name,
		Name:           name,
		Mode:           pool.Mode,
		Upstreams:      append([]string(nil), pool.Upstreams...),
		Probe:          pool.Probe,
		CircuitBreaker: pool.CircuitBreaker,
		ActiveUpstream: active,
		Workers:        workers,
		ProbeState:     probeState,
		NextProbeAt:    nextProbeAt,
		Readiness:      readiness,
	}
}

func normalizedPoolMembers(upstreams []string) []string {
	members := append([]string(nil), upstreams...)
	for index := range members {
		members[index] = strings.TrimSpace(members[index])
	}
	return members
}
