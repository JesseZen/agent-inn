package manager

import (
	"time"

	"github.com/jesse/agent-inn/internal/config"
)

type PoolProbeState string

const (
	PoolProbeStatePaused PoolProbeState = "paused"
	PoolProbeStateIdle   PoolProbeState = "idle"
	PoolProbeStateStable PoolProbeState = "stable"
	PoolProbeStateAlert  PoolProbeState = "alert"
)

type ProbeScheduleReason string

const (
	ProbeScheduleStartup       ProbeScheduleReason = "startup"
	ProbeScheduleStable        ProbeScheduleReason = "stable"
	ProbeScheduleWorkerFailure ProbeScheduleReason = "worker_failure"
	ProbeScheduleRecovery      ProbeScheduleReason = "recovery"
	ProbeScheduleManual        ProbeScheduleReason = "manual"
	ProbeScheduleConfig        ProbeScheduleReason = "config"
)

type poolProbeScheduleKey struct {
	Pool     string
	Upstream string
}

type poolProbeSchedule struct {
	NextProbeAt         time.Time
	ConsecutiveFailures int
	Reason              ProbeScheduleReason
}

func poolProbeFailureDelay(policy config.PoolProbeConfig, consecutiveFailures int) time.Duration {
	delaySeconds := policy.AlertIntervalSeconds
	for failure := 1; failure < consecutiveFailures && delaySeconds < policy.StableIntervalSeconds; failure++ {
		if delaySeconds > policy.StableIntervalSeconds-delaySeconds {
			delaySeconds = policy.StableIntervalSeconds
			break
		}
		delaySeconds *= 2
	}
	return time.Duration(delaySeconds) * time.Second
}

func (m *Manager) poolProbeStateLocked(poolName string) PoolProbeState {
	m.mu.RLock()
	pool := m.config.UpstreamPools[poolName]
	attachedWorkers := 0
	for _, worker := range m.config.Workers {
		if worker.UpstreamPool == poolName {
			attachedWorkers++
		}
	}
	m.mu.RUnlock()

	if pool.Mode == config.UpstreamPoolModeDisabled {
		return PoolProbeStatePaused
	}
	if attachedWorkers == 0 {
		return PoolProbeStateIdle
	}
	if m.exhaustedPools[poolName] != "" {
		return PoolProbeStateAlert
	}
	for _, upstreamName := range pool.Upstreams {
		schedule, scheduled := m.probeSchedules[poolProbeScheduleKey{Pool: poolName, Upstream: upstreamName}]
		if !scheduled || schedule.ConsecutiveFailures > 0 {
			return PoolProbeStateAlert
		}
		if !m.poolReadinessLocked(poolName, upstreamName).Eligible {
			return PoolProbeStateAlert
		}
		circuit := m.circuits.Status(poolCircuitKey(poolName, upstreamName), pool.CircuitBreaker)
		if circuit.State != CircuitStateClosed || circuit.ConsecutiveFailures > 0 {
			return PoolProbeStateAlert
		}
	}
	return PoolProbeStateStable
}

func (m *Manager) poolNextProbeAtLocked(poolName string) *time.Time {
	m.mu.RLock()
	pool := m.config.UpstreamPools[poolName]
	attachedWorkers := 0
	for _, worker := range m.config.Workers {
		if worker.UpstreamPool == poolName {
			attachedWorkers++
		}
	}
	m.mu.RUnlock()

	if pool.Mode == config.UpstreamPoolModeDisabled || attachedWorkers == 0 {
		return nil
	}
	var next *time.Time
	for _, upstreamName := range pool.Upstreams {
		schedule, scheduled := m.probeSchedules[poolProbeScheduleKey{Pool: poolName, Upstream: upstreamName}]
		if !scheduled {
			now := m.clock()
			return &now
		}
		if next == nil || schedule.NextProbeAt.Before(*next) {
			deadline := schedule.NextProbeAt
			next = &deadline
		}
	}
	return next
}
