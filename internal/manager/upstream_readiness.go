package manager

import (
	"time"

	"github.com/jesse/agent-inn/internal/logging"
	"github.com/jesse/agent-inn/internal/upstream"
)

const readinessFreshness = 120 * time.Second

type ReadinessState string

const (
	ReadinessStateUnknown  ReadinessState = "unknown"
	ReadinessStateReady    ReadinessState = "ready"
	ReadinessStateNotReady ReadinessState = "not_ready"
)

type PoolReadiness struct {
	Upstream      string             `json:"upstream"`
	Pool          string             `json:"pool"`
	Mode          upstream.ProbeMode `json:"mode"`
	Authoritative bool               `json:"authoritative"`
	Readiness     ReadinessState     `json:"readiness"`
	Eligible      bool               `json:"eligible"`
	CheckedAt     *time.Time         `json:"checked_at,omitempty"`
	OK            bool               `json:"ok"`
	StatusCode    int                `json:"status_code"`
	LatencyMS     int64              `json:"latency_ms"`
	Degraded      bool               `json:"degraded,omitempty"`
	Stale         bool               `json:"stale,omitempty"`
	Error         string             `json:"error,omitempty"`
}

type readinessObservation struct {
	Result      upstream.ProbeResult
	CheckedAt   time.Time
	Generation  int
	Fingerprint string
}

func (m *Manager) poolReadiness(poolName string, upstreamName string) PoolReadiness {
	m.failoverMu.Lock()
	defer m.failoverMu.Unlock()
	return m.poolReadinessLocked(poolName, upstreamName)
}

func (m *Manager) poolReadinessLocked(poolName string, upstreamName string) PoolReadiness {
	result := PoolReadiness{
		Upstream:      upstreamName,
		Pool:          poolName,
		Mode:          upstream.ProbeModeProtocol,
		Authoritative: true,
		Readiness:     ReadinessStateUnknown,
	}
	observation, ok := m.readiness[poolCircuitKey(poolName, upstreamName)]
	if !ok {
		return result
	}
	checkedAt := observation.CheckedAt
	result.Mode = observation.Result.Mode
	result.Authoritative = observation.Result.Authoritative
	result.CheckedAt = &checkedAt
	result.OK = observation.Result.OK
	result.StatusCode = observation.Result.StatusCode
	result.LatencyMS = observation.Result.LatencyMS
	result.Degraded = observation.Result.Degraded
	result.Error = observation.Result.Error
	if !m.clock().Before(observation.CheckedAt.Add(readinessFreshness)) {
		result.Stale = true
		return result
	}
	if observation.Result.OK {
		result.Readiness = ReadinessStateReady
		m.mu.RLock()
		pool, exists := m.config.UpstreamPools[poolName]
		m.mu.RUnlock()
		if exists {
			circuit := m.circuits.Status(poolCircuitKey(poolName, upstreamName), pool.CircuitBreaker)
			result.Eligible = circuit.State == CircuitStateClosed
		}
	} else {
		result.Readiness = ReadinessStateNotReady
	}
	return result
}

func (m *Manager) recordScheduledProbeResult(spec probeSpec, result upstream.ProbeResult) {
	m.failoverMu.Lock()
	desired, exists := m.desiredProbes[spec.Key]
	if !exists || desired.Generation != spec.Generation || desired.Fingerprint != spec.Fingerprint {
		m.failoverMu.Unlock()
		return
	}
	checkedAt := m.clock().UTC()
	readinessValues := make([]PoolReadiness, 0, len(spec.Pools))
	for _, poolName := range spec.Pools {
		key := poolCircuitKey(poolName, spec.Upstream)
		m.readiness[key] = readinessObservation{
			Result:      result,
			CheckedAt:   checkedAt,
			Generation:  spec.Generation,
			Fingerprint: spec.Fingerprint,
		}
		if timer := m.readinessTimers[key]; timer != nil {
			timer.Stop()
		}
		pool := poolName
		upstreamName := spec.Upstream
		generation := spec.Generation
		m.readinessTimers[key] = time.AfterFunc(readinessFreshness, func() {
			m.expirePoolReadiness(pool, upstreamName, generation, checkedAt)
		})
		readinessValues = append(readinessValues, m.poolReadinessLocked(poolName, spec.Upstream))
	}
	m.failoverMu.Unlock()
	for _, readiness := range readinessValues {
		m.publishEvent(EventUpstreamProbed, poolReadinessPayload(readiness))
	}
	if result.Authoritative {
		if err := m.recordUpstreamProbeResult(spec.Upstream, result); err != nil {
			m.logger.Error(logging.EventUpstreamFailover, "upstream", spec.Upstream, "err", redactedErrorMessage(err))
		}
	}
}

func (m *Manager) expirePoolReadiness(poolName string, upstreamName string, generation int, checkedAt time.Time) {
	m.failoverMu.Lock()
	key := poolCircuitKey(poolName, upstreamName)
	observation, exists := m.readiness[key]
	if !exists || observation.Generation != generation || !observation.CheckedAt.Equal(checkedAt) || m.clock().Before(checkedAt.Add(readinessFreshness)) {
		m.failoverMu.Unlock()
		return
	}
	delete(m.readinessTimers, key)
	readiness := m.poolReadinessLocked(poolName, upstreamName)
	m.failoverMu.Unlock()
	m.publishEvent(EventUpstreamProbed, poolReadinessPayload(readiness))
}

func (m *Manager) invalidatePoolReadinessLocked(poolName string, upstreamName string) {
	key := poolCircuitKey(poolName, upstreamName)
	delete(m.readiness, key)
	if timer := m.readinessTimers[key]; timer != nil {
		timer.Stop()
		delete(m.readinessTimers, key)
	}
}

func poolReadinessPayload(readiness PoolReadiness) map[string]any {
	payload := map[string]any{
		"upstream":      readiness.Upstream,
		"pool":          readiness.Pool,
		"mode":          readiness.Mode,
		"authoritative": readiness.Authoritative,
		"readiness":     readiness.Readiness,
		"eligible":      readiness.Eligible,
		"ok":            readiness.OK,
		"status_code":   readiness.StatusCode,
		"latency_ms":    readiness.LatencyMS,
	}
	if readiness.CheckedAt != nil {
		payload["checked_at"] = readiness.CheckedAt.Format(time.RFC3339)
	}
	if readiness.Degraded {
		payload["degraded"] = true
	}
	if readiness.Stale {
		payload["stale"] = true
	}
	if readiness.Error != "" {
		payload["error"] = readiness.Error
	}
	return payload
}
