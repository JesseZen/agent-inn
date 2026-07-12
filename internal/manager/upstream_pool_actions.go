package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
)

func (m *Manager) handleUpstreamPoolSwitch(rw http.ResponseWriter, r *http.Request, poolName string) {
	var payload struct {
		Upstream string `json:"upstream"`
		Mode     string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	mode := poolSwitchNormal
	switch payload.Mode {
	case "normal":
	case "force":
		mode = poolSwitchForced
	default:
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "pool switch mode must be normal or force"})
		return
	}

	m.failoverMu.Lock()
	err := m.switchPoolActiveLocked(poolName, payload.Upstream, mode)
	m.failoverMu.Unlock()
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errPoolTargetNotMember) {
			status = http.StatusBadRequest
		}
		if errors.Is(err, errPoolHasNoWorkers) || errors.Is(err, errPoolTargetIneligible) {
			status = http.StatusConflict
		}
		writeJSON(rw, status, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	writeJSON(rw, http.StatusOK, m.upstreamPoolSummary(poolName))
}

func (m *Manager) handleUpstreamPoolProbe(rw http.ResponseWriter, _ *http.Request, poolName string) {
	m.failoverMu.Lock()
	m.mu.RLock()
	pool := m.config.UpstreamPools[poolName]
	profiles := make(map[string]config.UpstreamProfile, len(pool.Upstreams))
	for _, upstreamName := range pool.Upstreams {
		profiles[upstreamName] = m.config.Upstreams[upstreamName]
	}
	workerNames := make([]string, 0)
	for workerName, worker := range m.config.Workers {
		if worker.UpstreamPool == poolName {
			workerNames = append(workerNames, workerName)
		}
	}
	sort.Strings(workerNames)
	proxyURL := ""
	if len(workerNames) > 0 {
		proxyURL = strings.TrimSpace(m.config.Workers[workerNames[0]].ProxyURL)
	}
	m.mu.RUnlock()
	now := m.clock()
	if pool.Mode == config.UpstreamPoolModeActive && len(workerNames) > 0 {
		for _, upstreamName := range pool.Upstreams {
			key := poolProbeScheduleKey{Pool: poolName, Upstream: upstreamName}
			schedule := m.probeSchedules[key]
			schedule.NextProbeAt = now
			schedule.Reason = ProbeScheduleManual
			m.probeSchedules[key] = schedule
		}
		m.failoverMu.Unlock()
		m.probeAllUpstreams(m.probeContext)
		writeJSON(rw, http.StatusOK, m.upstreamPoolSummary(poolName))
		return
	}

	specs := make([]probeSpec, 0, len(pool.Upstreams))
	for _, upstreamName := range pool.Upstreams {
		spec, err := buildProbeSpecification(upstreamName, profiles[upstreamName], proxyURL)
		if err != nil {
			m.failoverMu.Unlock()
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		spec.Due = true
		spec.Reason = ProbeScheduleManual
		specs = append(specs, spec)
	}
	for _, built := range specs {
		spec := built
		desired, scheduled := m.desiredProbes[spec.Key]
		manual, manualExists := m.manualProbes[spec.Key]
		if (scheduled && desired.Fingerprint != spec.Fingerprint) || (manualExists && manual.Fingerprint != spec.Fingerprint) {
			m.probeGenerations[spec.Key]++
			delete(m.desiredProbes, spec.Key)
			delete(m.manualProbes, spec.Key)
			delete(m.pendingProbes, spec.Key)
			scheduled = false
			manualExists = false
		}
		if scheduled {
			spec.Generation = desired.Generation
		} else if manualExists {
			spec.Generation = manual.Generation
			spec.ManualPools = append(spec.ManualPools, manual.ManualPools...)
		} else {
			m.probeGenerations[spec.Key]++
			spec.Generation = m.probeGenerations[spec.Key]
		}
		if !slices.Contains(spec.ManualPools, poolName) {
			spec.ManualPools = append(spec.ManualPools, poolName)
		}
		sort.Strings(spec.ManualPools)
		if pending, exists := m.pendingProbes[spec.Key]; exists && pending.Generation == spec.Generation && pending.Fingerprint == spec.Fingerprint {
			spec.Pools = append(spec.Pools, pending.Pools...)
			for _, manualPool := range pending.ManualPools {
				if !slices.Contains(spec.ManualPools, manualPool) {
					spec.ManualPools = append(spec.ManualPools, manualPool)
				}
			}
			sort.Strings(spec.ManualPools)
		}
		m.manualProbes[spec.Key] = spec
		if _, inFlight := m.inFlightProbes[spec.Key]; inFlight {
			m.pendingProbes[spec.Key] = spec
			continue
		}
		m.startProbeLocked(spec)
	}
	m.failoverMu.Unlock()
	writeJSON(rw, http.StatusOK, m.upstreamPoolSummary(poolName))
}
