package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
)

type hostedSessionCreateRequest struct {
	WorkerID     string   `json:"worker_id"`
	SessionLabel *string  `json:"session_label"`
	Workspace    string   `json:"workspace"`
	Model        string   `json:"model"`
	AddDirs      []string `json:"add_dirs"`
}

type hostedSessionPatchRequest struct {
	SessionLabel *string `json:"session_label"`
	WorkerID     *string `json:"worker_id"`
	UserMarker   *string `json:"user_marker"`
}

func (request *hostedSessionCreateRequest) UnmarshalJSON(data []byte) error {
	type plain hostedSessionCreateRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded plain
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request body must contain one JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"session_label", "workspace", "model", "add_dirs"} {
		value, present := fields[name]
		if present && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s cannot be null", name)
		}
	}
	*request = hostedSessionCreateRequest(decoded)
	return nil
}

func decodeHostedSessionCommand(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request body must contain one JSON object")
	}
	return nil
}

func (m *Manager) handleHostedSessions(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cursor := m.events.CursorString()
		cfg, _ := m.syncConfigFromStore()
		sessions, err := m.hostedSessionSnapshots(cfg)
		if err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		writeJSON(rw, http.StatusOK, HostedSessionListResponse{Sessions: sessions, EventCursor: cursor})
		return
	}
	if r.Method == http.MethodPost {
		var request hostedSessionCreateRequest
		if err := decodeHostedSessionCommand(r, &request); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid hosted session command"})
			return
		}
		request.WorkerID = strings.TrimSpace(request.WorkerID)
		if request.WorkerID == "" {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker id is required"})
			return
		}
		sessionLabel := ""
		if request.SessionLabel != nil {
			sessionLabel = strings.TrimSpace(*request.SessionLabel)
			if sessionLabel == "" {
				writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "session label is required"})
				return
			}
		}
		cfg, _ := m.syncConfigFromStore()
		worker, ok := cfg.Workers[request.WorkerID]
		if !ok {
			writeJSON(rw, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("worker %q not found", request.WorkerID)})
			return
		}
		record, err := m.hostedSessions.Create(HostedSessionRecord{
			SessionLabel: sessionLabel,
			WorkerID:     request.WorkerID,
			WorkerName:   request.WorkerID,
			WorkerPort:   worker.Port,
			Workspace:    strings.TrimSpace(request.Workspace),
			Model:        strings.TrimSpace(request.Model),
			AddDirs:      append([]string{}, request.AddDirs...),
		})
		if err != nil {
			status := http.StatusInternalServerError
			var conflict hostedSessionConflictError
			if errors.As(err, &conflict) {
				status = http.StatusConflict
			}
			writeJSON(rw, status, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		snapshot, err := m.hostedSessionSnapshot(record, cfg)
		if err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		writeJSON(rw, http.StatusCreated, snapshot)
		return
	}
	http.NotFound(rw, r)
}

func (m *Manager) handleHostedSessionByID(rw http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/hosted-sessions/")
	if id == "" {
		writeJSON(rw, http.StatusNotFound, map[string]any{"error": "hosted session not found"})
		return
	}
	if strings.HasSuffix(id, "/duplicate") {
		m.handleHostedSessionDuplicate(rw, r, strings.TrimSuffix(id, "/duplicate"))
		return
	}
	if strings.HasSuffix(id, "/mark-unread") {
		m.handleHostedSessionMarkUnread(rw, r, strings.TrimSuffix(id, "/mark-unread"))
		return
	}

	record, found, err := m.hostedSessions.Get(id)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	if !found {
		writeJSON(rw, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("hosted session %q not found", id)})
		return
	}
	cfg, _ := m.syncConfigFromStore()
	switch r.Method {
	case http.MethodGet:
		snapshot, err := m.hostedSessionSnapshot(record, cfg)
		if err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		writeJSON(rw, http.StatusOK, snapshot)
	case http.MethodPatch:
		m.patchHostedSession(rw, r, record, cfg)
	case http.MethodDelete:
		if err := m.hostedSessions.RemoveForSettings(id, cfg.Settings, hostedTMuxRunnerFactory()); err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"session_id": id})
	default:
		http.NotFound(rw, r)
	}
}

func (m *Manager) handleHostedSessionDuplicate(rw http.ResponseWriter, r *http.Request, id string) {
	if id == "" || r.Method != http.MethodPost {
		http.NotFound(rw, r)
		return
	}
	record, err := m.hostedSessions.Duplicate(id)
	if err != nil {
		status := http.StatusInternalServerError
		var notFound hostedSessionNotFoundError
		if errors.As(err, &notFound) {
			status = http.StatusNotFound
		}
		writeJSON(rw, status, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	cfg, _ := m.syncConfigFromStore()
	snapshot, err := m.hostedSessionSnapshot(record, cfg)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	writeJSON(rw, http.StatusCreated, snapshot)
}

func (m *Manager) handleHostedSessionMarkUnread(rw http.ResponseWriter, r *http.Request, id string) {
	if id == "" || r.Method != http.MethodPost {
		http.NotFound(rw, r)
		return
	}
	cfg, _ := m.syncConfigFromStore()
	record, err := m.hostedSessions.MarkTurnUnread(id)
	if err != nil {
		status := http.StatusInternalServerError
		var notFound hostedSessionNotFoundError
		var conflict hostedSessionConflictError
		if errors.As(err, &notFound) {
			status = http.StatusNotFound
		} else if errors.As(err, &conflict) {
			status = http.StatusConflict
		}
		writeJSON(rw, status, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	runner := hostedTMuxRunnerFactory()
	status := HostedSessionStatusStale
	if record.TmuxWindowID != "" {
		windows, err := hostedWindowDetailsFromRunnerForSettings(cfg.Settings, runner)
		if err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		if windowID, active := HostedSessionActiveWindowID(windows, record); active {
			status = HostedSessionStatusActive
			record.TmuxWindowID = windowID
			snapshot := MapHostedSessionSnapshot(record, status, hostedSessionWorkerSnapshot(record, cfg))
			if _, err := runner.Run(TmuxHostedTurnStatusCommandForSnapshot(cfg.Settings, record.TmuxWindowID, snapshot)); err != nil {
				writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
		}
	}
	writeJSON(rw, http.StatusOK, MapHostedSessionSnapshot(record, status, hostedSessionWorkerSnapshot(record, cfg)))
}

func (m *Manager) patchHostedSession(rw http.ResponseWriter, r *http.Request, record HostedSessionRecord, cfg config.Config) {
	var request hostedSessionPatchRequest
	if err := decodeHostedSessionCommand(r, &request); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid hosted session command"})
		return
	}
	fieldCount := 0
	if request.SessionLabel != nil {
		fieldCount++
	}
	if request.WorkerID != nil {
		fieldCount++
	}
	if request.UserMarker != nil {
		fieldCount++
	}
	if fieldCount != 1 {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "exactly one hosted session field is required"})
		return
	}

	var updated HostedSessionRecord
	var err error
	workerRebound := false
	resolvedStatus := HostedSessionStatus("")
	if request.SessionLabel != nil {
		label := strings.TrimSpace(*request.SessionLabel)
		if label == "" {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "session label is required"})
			return
		}
		updated, resolvedStatus, err = m.hostedSessions.RenameAndResolveStatus(record.SessionID, label, cfg.Settings, hostedTMuxRunnerFactory())
	} else if request.UserMarker != nil {
		if *request.UserMarker != "" && *request.UserMarker != HostedUserMarkerTodo {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid hosted session marker"})
			return
		}
		updated, err = m.hostedSessions.SetUserMarker(record.SessionID, *request.UserMarker)
	} else {
		workerID := strings.TrimSpace(*request.WorkerID)
		if workerID == "" {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker id is required"})
			return
		}
		worker, found := cfg.Workers[workerID]
		if !found {
			writeJSON(rw, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("worker %q not found", workerID)})
			return
		}
		currentWorkerID := record.WorkerID
		if currentWorkerID == "" {
			currentWorkerID = record.WorkerName
		}
		currentWorker, currentFound := cfg.Workers[currentWorkerID]
		if currentFound && currentWorker.Launcher != worker.Launcher {
			writeJSON(rw, http.StatusConflict, map[string]any{"error": fmt.Sprintf("hosted session worker launcher cannot change from %q to %q", currentWorker.Launcher, worker.Launcher)})
			return
		}
		if workerID != currentWorkerID && record.TurnState == HostedTurnStateRunning {
			writeJSON(rw, http.StatusConflict, map[string]any{"error": fmt.Sprintf("hosted session %q turn state %q cannot change worker", record.SessionID, record.TurnState)})
			return
		}
		workerRebound = workerID != currentWorkerID
		if workerID != currentWorkerID && record.TmuxWindowID != "" {
			runner := hostedTMuxRunnerFactory()
			windows, err := hostedWindowDetailsFromRunnerForSettings(cfg.Settings, runner)
			if err != nil {
				writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
			if windowID, active := HostedSessionActiveWindowID(windows, record); active {
				if _, err := runner.Run(TmuxKillWindowCommandForSettings(cfg.Settings, windowID)); err != nil {
					writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
					return
				}
			}
		}
		updated, err = m.hostedSessions.UpdateWorker(record.SessionID, workerID, worker.Port)
	}
	if err != nil {
		status := http.StatusInternalServerError
		var notFound hostedSessionNotFoundError
		var conflict hostedSessionConflictError
		if errors.As(err, &notFound) {
			status = http.StatusNotFound
		} else if errors.As(err, &conflict) {
			status = http.StatusConflict
		}
		writeJSON(rw, status, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	if workerRebound {
		writeJSON(rw, http.StatusOK, MapHostedSessionSnapshot(updated, HostedSessionStatusStale, hostedSessionWorkerSnapshot(updated, cfg)))
		return
	}
	if resolvedStatus != "" {
		writeJSON(rw, http.StatusOK, MapHostedSessionSnapshot(updated, resolvedStatus, hostedSessionWorkerSnapshot(updated, cfg)))
		return
	}
	snapshot, err := m.hostedSessionSnapshot(updated, cfg)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	writeJSON(rw, http.StatusOK, snapshot)
}
