package manager

import (
	"os"
	"reflect"
	"time"

	"github.com/jesse/agent-inn/internal/config"
)

type HostedSessionWorkerSnapshot struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Port    int    `json:"port"`
	Missing bool   `json:"missing"`
}

type HostedSessionTurnSnapshot struct {
	State      string `json:"state"`
	Reason     string `json:"reason"`
	Unread     bool   `json:"unread"`
	NeedsInput bool   `json:"needs_input"`
}

type HostedSessionSnapshot struct {
	SessionID    string                      `json:"session_id"`
	SessionLabel string                      `json:"session_label"`
	Worker       HostedSessionWorkerSnapshot `json:"worker"`
	Workspace    string                      `json:"workspace"`
	Model        string                      `json:"model"`
	AddDirs      []string                    `json:"add_dirs"`
	Status       HostedSessionStatus         `json:"status"`
	UserMarker   string                      `json:"user_marker"`
	Turn         HostedSessionTurnSnapshot   `json:"turn"`
	CreatedAt    time.Time                   `json:"created_at"`
	LastOpenedAt time.Time                   `json:"last_opened_at"`
}

type HostedSessionListResponse struct {
	Sessions    []HostedSessionSnapshot `json:"sessions"`
	EventCursor string                  `json:"event_cursor"`
}

type hostedSessionProjection struct {
	Snapshot HostedSessionSnapshot
	WindowID string
}

func MapHostedSessionSnapshot(record HostedSessionRecord, status HostedSessionStatus, worker HostedSessionWorkerSnapshot) HostedSessionSnapshot {
	state := record.TurnState
	if state == "" {
		state = HostedTurnStateIdle
	}
	addDirs := append([]string{}, record.AddDirs...)
	return HostedSessionSnapshot{
		SessionID:    record.SessionID,
		SessionLabel: record.SessionLabel,
		Worker:       worker,
		Workspace:    record.Workspace,
		Model:        record.Model,
		AddDirs:      addDirs,
		Status:       status,
		UserMarker:   record.UserMarker,
		Turn: HostedSessionTurnSnapshot{
			State:      state,
			Reason:     record.TurnStateReason,
			Unread:     isHostedTurnTerminalState(state) && record.TurnGeneration > record.TurnAcknowledgedGeneration,
			NeedsInput: state == HostedTurnStateRunning && record.TurnInputRequestID != "",
		},
		CreatedAt:    record.CreatedAt,
		LastOpenedAt: record.LastOpenedAt,
	}
}

func hostedSessionWorkerSnapshot(record HostedSessionRecord, cfg config.Config) HostedSessionWorkerSnapshot {
	workerID := record.WorkerID
	if workerID == "" {
		workerID = record.WorkerName
	}
	workerName := workerID
	missing := true
	if worker, ok := cfg.Workers[workerID]; ok {
		missing = false
		if worker.Name != "" {
			workerName = worker.Name
		}
	}
	return HostedSessionWorkerSnapshot{ID: workerID, Name: workerName, Port: record.WorkerPort, Missing: missing}
}

func mapHostedSessionSnapshot(record HostedSessionRecord, cfg config.Config, windows map[string]string) HostedSessionSnapshot {
	status := HostedSessionStatus(hostedSessionStatusForWindow(windows, record))
	return MapHostedSessionSnapshot(record, status, hostedSessionWorkerSnapshot(record, cfg))
}

func (m *Manager) hostedSessionSnapshots(cfg config.Config) ([]HostedSessionSnapshot, error) {
	projections, err := m.hostedSessionProjections(cfg, hostedTMuxRunnerFactory())
	if err != nil {
		return nil, err
	}
	snapshots := make([]HostedSessionSnapshot, 0, len(projections))
	for _, projection := range projections {
		snapshots = append(snapshots, projection.Snapshot)
	}
	return snapshots, nil
}

func (m *Manager) hostedSessionProjections(cfg config.Config, runner hostedTMuxRunner) ([]hostedSessionProjection, error) {
	records, err := m.hostedSessions.List()
	if err != nil {
		return nil, err
	}
	windows, err := hostedWindowDetailsFromRunnerForSettings(cfg.Settings, runner)
	if err != nil {
		return nil, err
	}
	projections := make([]hostedSessionProjection, 0, len(records))
	for _, record := range records {
		windowID, _ := HostedSessionActiveWindowID(windows, record)
		projections = append(projections, hostedSessionProjection{
			Snapshot: mapHostedSessionSnapshot(record, cfg, windows),
			WindowID: windowID,
		})
	}
	return projections, nil
}

func (m *Manager) hostedSessionSnapshot(record HostedSessionRecord, cfg config.Config) (HostedSessionSnapshot, error) {
	windows := map[string]string{}
	if record.TmuxWindowID != "" {
		var err error
		windows, err = hostedWindowDetailsFromRunnerForSettings(cfg.Settings, hostedTMuxRunnerFactory())
		if err != nil {
			return HostedSessionSnapshot{}, err
		}
	}
	return mapHostedSessionSnapshot(record, cfg, windows), nil
}

func (m *Manager) reconcileHostedSessionSnapshots() error {
	m.hostedSnapshotMu.Lock()
	defer m.hostedSnapshotMu.Unlock()
	stat, statErr := os.Stat(m.hostedSessions.path)
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if m.hostedSnapshotReady {
		if exists && stat.Size() == m.hostedSnapshotSize && stat.ModTime().Equal(m.hostedSnapshotMtime) {
			return nil
		}
		if !exists && m.hostedSnapshotSize == 0 && m.hostedSnapshotMtime.IsZero() {
			return nil
		}
	}

	cfg, _ := m.syncConfigFromStore()
	projections := []hostedSessionProjection{}
	if exists {
		runner := hostedTMuxRunnerFactory()
		var err error
		projections, err = m.hostedSessionProjections(cfg, runner)
		if err != nil {
			return err
		}
		for _, projection := range projections {
			snapshot := projection.Snapshot
			previous, found := m.hostedSnapshotCache[snapshot.SessionID]
			if !found || !reflect.DeepEqual(previous, snapshot) {
				if projection.WindowID != "" {
					if _, err := runner.Run(TmuxHostedTurnStatusCommandForSnapshot(cfg.Settings, projection.WindowID, snapshot)); err != nil {
						return err
					}
				}
			}
		}
	}
	next := make(map[string]HostedSessionSnapshot, len(projections))
	for _, projection := range projections {
		snapshot := projection.Snapshot
		next[snapshot.SessionID] = snapshot
		previous, found := m.hostedSnapshotCache[snapshot.SessionID]
		if !found || !reflect.DeepEqual(previous, snapshot) {
			m.publishEvent(EventHostedSessionSnapshotChanged, map[string]any{"snapshot": snapshot})
		}
	}
	for sessionID := range m.hostedSnapshotCache {
		if _, found := next[sessionID]; !found {
			m.publishEvent(EventHostedSessionDeleted, map[string]any{"session_id": sessionID})
		}
	}
	m.hostedSnapshotCache = next
	m.hostedSnapshotReady = true
	if exists {
		m.hostedSnapshotSize = stat.Size()
		m.hostedSnapshotMtime = stat.ModTime()
	} else {
		m.hostedSnapshotSize = 0
		m.hostedSnapshotMtime = time.Time{}
	}
	return nil
}
