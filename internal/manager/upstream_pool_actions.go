package manager

import (
	"encoding/json"
	"errors"
	"net/http"
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
