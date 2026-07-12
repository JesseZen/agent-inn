package manager

import (
	"encoding/json"
	"errors"
	"net/http"

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
	attached := false
	for _, worker := range m.config.Workers {
		if worker.UpstreamPool == poolName {
			attached = true
			break
		}
	}
	m.mu.RUnlock()
	now := m.clock()
	if pool.Mode == config.UpstreamPoolModeActive && attached {
		for _, upstreamName := range pool.Upstreams {
			key := poolProbeScheduleKey{Pool: poolName, Upstream: upstreamName}
			schedule := m.probeSchedules[key]
			schedule.NextProbeAt = now
			schedule.Reason = ProbeScheduleManual
			m.probeSchedules[key] = schedule
		}
	}
	m.failoverMu.Unlock()
	m.probeAllUpstreams(m.probeContext)
	writeJSON(rw, http.StatusOK, m.upstreamPoolSummary(poolName))
}
