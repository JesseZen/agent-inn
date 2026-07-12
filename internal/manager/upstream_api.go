package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
)

func (m *Manager) handleUpstreams(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(rw, r)
		return
	}
	out := map[string]any{}
	m.mu.RLock()
	bindings := make(map[string][]string, len(m.config.Upstreams))
	for poolName, pool := range m.config.UpstreamPools {
		for _, upstreamName := range pool.Upstreams {
			bindings[upstreamName] = append(bindings[upstreamName], poolName)
		}
	}
	m.mu.RUnlock()
	for upstreamName := range bindings {
		sort.Strings(bindings[upstreamName])
	}
	for name, profile := range m.upstreamProfileSnapshot() {
		runtime, _ := upstream.ResolveWithDisplayName(name, profile.Name, profile)
		entry := map[string]any{
			"id": name, "name": runtime.Name, "base_url": profile.BaseURL, "has_api_key": runtime.APIKey != "", "api_format": profile.APIFormat,
		}
		if profile.ProtocolProbe.Model != "" {
			entry["protocol_probe"] = profile.ProtocolProbe
		}
		poolNames := bindings[name]
		readiness := make([]PoolReadiness, 0, len(poolNames))
		for _, poolName := range poolNames {
			readiness = append(readiness, m.poolReadiness(poolName, name))
		}
		entry["pool_readiness"] = readiness
		out[name] = entry
	}
	writeJSON(rw, http.StatusOK, map[string]any{"upstreams": out})
}

func (m *Manager) handleUpstreamByName(rw http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/upstreams/")
	if name == "test" && r.Method == http.MethodPost {
		m.handleUpstreamTestAll(rw, r)
		return
	}
	if strings.HasSuffix(name, "/test") && r.Method == http.MethodPost {
		parts := strings.Split(name, "/")
		if len(parts) == 2 && parts[1] == "test" {
			m.handleUpstreamTest(rw, r)
			return
		}
	}
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(rw, r)
		return
	}
	if r.Method == http.MethodDelete {
		m.mu.RLock()
		_, exists := m.config.Upstreams[name]
		candidate := cloneConfig(m.config)
		m.mu.RUnlock()
		if !exists {
			http.NotFound(rw, r)
			return
		}
		delete(candidate.Upstreams, name)
		candidate.ApplyDefaults()
		if err := candidate.Validate(); err != nil {
			writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		for workerName, worker := range m.workerConfigSnapshot() {
			if workerUpstreamID(worker) != name {
				continue
			}
			if m.workerStatus(workerName) == WorkerStateRunning {
				if err := m.StopWorker(workerName); err != nil {
					writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
					return
				}
			}
		}
		m.updateConfig(func(cfgRoot *config.Config) { delete(cfgRoot.Upstreams, name) })
		writeJSON(rw, http.StatusOK, map[string]any{"upstream": name})
		return
	}
	if r.Method != http.MethodPatch {
		http.NotFound(rw, r)
		return
	}
	type upstreamPatch struct {
		Name          *string                     `json:"name,omitempty"`
		BaseURL       *string                     `json:"base_url,omitempty"`
		APIKey        *string                     `json:"api_key,omitempty"`
		APIFormat     *string                     `json:"api_format,omitempty"`
		ProtocolProbe *config.ProtocolProbeConfig `json:"protocol_probe,omitempty"`
	}
	var patch upstreamPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	current := m.upstreamProfileSnapshot()[name]
	profile := current
	if patch.Name != nil {
		profile.Name = strings.TrimSpace(*patch.Name)
		if profile.Name == "" {
			profile.Name = name
		}
	}
	if patch.BaseURL != nil {
		profile.BaseURL = *patch.BaseURL
	}
	if patch.APIKey != nil {
		profile.APIKey = *patch.APIKey
	}
	if patch.APIFormat != nil {
		profile.APIFormat = *patch.APIFormat
	}
	if patch.ProtocolProbe != nil {
		profile.ProtocolProbe.Model = strings.TrimSpace(patch.ProtocolProbe.Model)
	}
	runtime, err := upstream.ResolveWithDisplayName(name, profile.Name, profile)
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	m.mu.RLock()
	poolNames := make([]string, 0, len(m.config.UpstreamPools))
	for poolName, pool := range m.config.UpstreamPools {
		for _, member := range pool.Upstreams {
			if member == name {
				poolNames = append(poolNames, poolName)
				break
			}
		}
	}
	m.mu.RUnlock()
	sort.Strings(poolNames)
	if profile.ProtocolProbe.Model == "" && len(poolNames) > 0 {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("upstream pool %q member %q requires protocol_probe.model", poolNames[0], name)})
		return
	}
	m.failoverMu.Lock()
	healthChanged := make(map[string]bool, len(poolNames))
	for _, poolName := range poolNames {
		proxyURL := m.poolProxyURL(poolName)
		before, beforeErr := workerHealthFingerprint(name, current, proxyURL)
		after, afterErr := workerHealthFingerprint(name, profile, proxyURL)
		if beforeErr != nil || afterErr != nil {
			m.failoverMu.Unlock()
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(errors.Join(beforeErr, afterErr))})
			return
		}
		healthChanged[poolName] = before != after
	}
	m.updateConfig(func(cfgRoot *config.Config) { cfgRoot.Upstreams[name] = profile })
	m.bumpLiveWorkersUsingUpstream(name)
	for _, poolName := range poolNames {
		m.invalidatePoolReadinessLocked(poolName, name)
		m.invalidatePoolProbeIdentityLocked(poolName)
		if !healthChanged[poolName] {
			continue
		}
		m.mu.RLock()
		pool := m.config.UpstreamPools[poolName]
		m.mu.RUnlock()
		key := poolCircuitKey(poolName, name)
		previous := m.circuits.Status(key, pool.CircuitBreaker)
		m.circuits.Reset(key)
		m.publishCircuitTransition(poolName, name, previous, CircuitStatus{State: CircuitStateClosed}, "identity_changed")
	}
	m.failoverMu.Unlock()
	applyErrors := m.applyRuntimeToLiveWorkersUsingUpstream(name)
	m.probeAllUpstreams(m.probeContext)
	m.publishEvent(EventUpstreamUpdated, map[string]any{"upstream": name})
	body := map[string]any{"id": name, "name": runtime.Name, "base_url": profile.BaseURL, "has_api_key": runtime.APIKey != "", "api_format": profile.APIFormat}
	if profile.ProtocolProbe.Model != "" {
		body["protocol_probe"] = profile.ProtocolProbe
	}
	if len(applyErrors) > 0 {
		body["apply_errors"] = applyErrors
	}
	writeJSON(rw, http.StatusOK, body)
}
