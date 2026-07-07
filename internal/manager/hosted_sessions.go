package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/constants"
)

const hostedSessionsFileName = "hosted-terminal-sessions.json"

const (
	hostedSessionStatusActive = "active"
	hostedSessionStatusStale  = "stale"
)

const (
	HostedTurnStateIdle        = constants.HostedTurnStateIdle
	HostedTurnStateRunning     = constants.HostedTurnStateRunning
	HostedTurnStateDone        = constants.HostedTurnStateDone
	HostedTurnStateFailed      = constants.HostedTurnStateFailed
	HostedTurnStateInterrupted = constants.HostedTurnStateInterrupted
)

type HostedSessionRegistry struct {
	path string
	lock string
}

type HostedSessionRecord struct {
	SessionID                  string    `json:"session_id"`
	SessionLabel               string    `json:"session_label"`
	WorkerName                 string    `json:"worker_name"`
	WorkerPort                 int       `json:"worker_port"`
	Workspace                  string    `json:"workspace,omitempty"`
	Model                      string    `json:"model,omitempty"`
	AddDirs                    []string  `json:"add_dirs,omitempty"`
	TmuxWindowID               string    `json:"tmux_window_id,omitempty"`
	LauncherSessionID          string    `json:"launcher_session_id,omitempty"`
	TurnState                  string    `json:"turn_state,omitempty"`
	TurnStateReason            string    `json:"turn_state_reason,omitempty"`
	TurnGeneration             int       `json:"turn_generation,omitempty"`
	TurnAcknowledgedGeneration int       `json:"turn_acknowledged_generation,omitempty"`
	CreatedAt                  time.Time `json:"created_at"`
	LastOpenedAt               time.Time `json:"last_opened_at"`
}

type HostedSessionSummary struct {
	HostedSessionRecord
	Status string `json:"status"`
}

func (r *HostedSessionRegistry) Summaries() ([]HostedSessionSummary, error) {
	return r.SummariesForSettings(defaultTmuxSettings())
}

func (r *HostedSessionRegistry) SummariesForSettings(settings config.Settings) ([]HostedSessionSummary, error) {
	return r.summaries(settings, hostedTMuxRunnerFactory())
}

type hostedSessionFile struct {
	NextSessionID  int                            `json:"next_session_id"`
	WorkerCounters map[string]int                 `json:"worker_counters"`
	Sessions       map[string]HostedSessionRecord `json:"sessions"`
}

func HostedSessionRegistryPath(stateDir string) string {
	if stateDir == "" {
		stateDir = "~/.ainn"
	}
	return filepath.Join(expandHomePath(stateDir), hostedSessionsFileName)
}

func NewHostedSessionRegistry(path string) *HostedSessionRegistry {
	return &HostedSessionRegistry{path: path, lock: path + ".lock"}
}

func (r *HostedSessionRegistry) List() ([]HostedSessionRecord, error) {
	var records []HostedSessionRecord
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		records = make([]HostedSessionRecord, 0, len(file.Sessions))
		for _, session := range file.Sessions {
			records = append(records, session)
		}
		sort.Slice(records, func(i, j int) bool {
			if records[i].LastOpenedAt.Equal(records[j].LastOpenedAt) {
				return records[i].SessionID < records[j].SessionID
			}
			return records[i].LastOpenedAt.After(records[j].LastOpenedAt)
		})
		return nil
	})
	return records, err
}

func (r *HostedSessionRegistry) summaries(settings config.Settings, runner hostedTMuxRunner) ([]HostedSessionSummary, error) {
	records, err := r.List()
	if err != nil {
		return nil, err
	}
	out := make([]HostedSessionSummary, 0, len(records))
	windows, err := hostedWindowDetailsFromRunnerForSettings(settings, runner)
	if err != nil {
		return nil, err
	}
	for _, session := range records {
		status := hostedSessionStatusForWindow(windows, session)
		out = append(out, HostedSessionSummary{
			HostedSessionRecord: session,
			Status:              status,
		})
	}
	return out, nil
}

func (r *HostedSessionRegistry) RemoveWithRunner(sessionID string, runner hostedTMuxRunner) error {
	return r.Remove(sessionID, runner)
}

func (r *HostedSessionRegistry) RemoveForSettings(sessionID string, settings config.Settings, runner hostedTMuxRunner) error {
	return r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return fmt.Errorf("hosted session %q not found", sessionID)
		}
		if session.TmuxWindowID == "" {
			delete(file.Sessions, sessionID)
			return nil
		}
		windows, err := hostedWindowDetailsFromRunnerForSettings(settings, runner)
		if err != nil {
			return err
		}
		if windowID, active := HostedSessionActiveWindowID(windows, session); active {
			if _, err := runner.Run(TmuxKillWindowCommandForSettings(settings, windowID)); err != nil {
				return err
			}
		}
		delete(file.Sessions, sessionID)
		return nil
	})
}

func (r *HostedSessionRegistry) Get(sessionID string) (HostedSessionRecord, bool, error) {
	var out HostedSessionRecord
	found := false
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return nil
		}
		out = session
		found = true
		return nil
	})
	return out, found, err
}

func (r *HostedSessionRegistry) Create(input HostedSessionRecord) (HostedSessionRecord, error) {
	var created HostedSessionRecord
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		if file.WorkerCounters == nil {
			file.WorkerCounters = map[string]int{}
		}
		if file.Sessions == nil {
			file.Sessions = map[string]HostedSessionRecord{}
		}
		input.SessionID = strings.TrimSpace(input.SessionID)
		input.SessionLabel = strings.TrimSpace(input.SessionLabel)
		input.WorkerName = strings.TrimSpace(input.WorkerName)
		input.Workspace = strings.TrimSpace(input.Workspace)
		input.Model = strings.TrimSpace(input.Model)
		if input.WorkerName == "" {
			return errors.New("worker name is required")
		}
		if input.WorkerPort <= 0 {
			return errors.New("worker port is required")
		}
		if input.SessionID == "" {
			file.NextSessionID++
			input.SessionID = fmt.Sprintf("hs_%d", file.NextSessionID)
		} else if _, ok := file.Sessions[input.SessionID]; ok {
			return fmt.Errorf("hosted session %q already exists", input.SessionID)
		} else if n, err := parseHostedSessionID(input.SessionID); err == nil && n > file.NextSessionID {
			file.NextSessionID = n
		}

		label := input.SessionLabel
		if label == "" {
			next := file.WorkerCounters[input.WorkerName]
			for {
				next++
				label = fmt.Sprintf("%s %d", input.WorkerName, next)
				if !hasSessionLabel(file.Sessions, label) {
					break
				}
			}
			file.WorkerCounters[input.WorkerName] = next
		} else if hasSessionLabel(file.Sessions, label) {
			return fmt.Errorf("hosted session label %q already exists", label)
		}
		input.SessionLabel = label

		now := time.Now().UTC()
		if input.CreatedAt.IsZero() {
			input.CreatedAt = now
		}
		if input.LastOpenedAt.IsZero() {
			input.LastOpenedAt = now
		}
		file.Sessions[input.SessionID] = input
		created = input
		return nil
	})
	if err != nil {
		return HostedSessionRecord{}, err
	}
	return created, nil
}

func (r *HostedSessionRegistry) UpdateWindowID(sessionID string, windowID string) error {
	return r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return fmt.Errorf("hosted session %q not found", sessionID)
		}
		session.TmuxWindowID = windowID
		session.LastOpenedAt = time.Now().UTC()
		file.Sessions[sessionID] = session
		return nil
	})
}

func (r *HostedSessionRegistry) MarkTurnState(sessionID string, state string, reason string, launcherSessionID string) (HostedSessionRecord, error) {
	var updated HostedSessionRecord
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return fmt.Errorf("hosted session %q not found", sessionID)
		}
		if state == HostedTurnStateRunning {
			session.TurnGeneration++
			session.TurnStateReason = ""
		}
		if state == HostedTurnStateDone &&
			(session.TurnState == HostedTurnStateFailed || session.TurnState == HostedTurnStateInterrupted) {
			updated = session
			return nil
		}
		session.TurnState = state
		session.TurnStateReason = strings.TrimSpace(reason)
		launcherSessionID = strings.TrimSpace(launcherSessionID)
		if launcherSessionID != "" {
			session.LauncherSessionID = launcherSessionID
		}
		file.Sessions[sessionID] = session
		updated = session
		return nil
	})
	return updated, err
}

func (r *HostedSessionRegistry) AcknowledgeTurnByWindow(windowID string, windowName string) (HostedSessionRecord, bool, error) {
	var updated HostedSessionRecord
	found := false
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		for sessionID, session := range file.Sessions {
			if session.TmuxWindowID != windowID && session.TmuxWindowID != windowName {
				continue
			}
			found = true
			if isHostedTurnTerminalState(session.TurnState) && session.TurnGeneration > session.TurnAcknowledgedGeneration {
				session.TurnAcknowledgedGeneration = session.TurnGeneration
				file.Sessions[sessionID] = session
			}
			updated = session
			return nil
		}
		return nil
	})
	return updated, found, err
}

func isHostedTurnTerminalState(state string) bool {
	return state == HostedTurnStateDone || state == HostedTurnStateFailed || state == HostedTurnStateInterrupted
}

func (r *HostedSessionRegistry) Delete(sessionID string) error {
	return r.withLockedFile(func(file *hostedSessionFile) error {
		delete(file.Sessions, sessionID)
		return nil
	})
}

func (r *HostedSessionRegistry) Remove(sessionID string, runner hostedTMuxRunner) error {
	return r.RemoveForSettings(sessionID, defaultTmuxSettings(), runner)
}

func (r *HostedSessionRegistry) withLockedFile(fn func(*hostedSessionFile) error) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	unlock, err := lockFile(r.lock)
	if err != nil {
		return err
	}
	defer unlock()

	file, err := r.loadFile()
	if err != nil {
		return err
	}
	if err := fn(file); err != nil {
		return err
	}
	return r.saveFile(file)
}

func (r *HostedSessionRegistry) loadFile() (*hostedSessionFile, error) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &hostedSessionFile{
				WorkerCounters: map[string]int{},
				Sessions:       map[string]HostedSessionRecord{},
			}, nil
		}
		return nil, err
	}
	var file hostedSessionFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.WorkerCounters == nil {
		file.WorkerCounters = map[string]int{}
	}
	if file.Sessions == nil {
		file.Sessions = map[string]HostedSessionRecord{}
	}
	return &file, nil
}

func (r *HostedSessionRegistry) saveFile(file *hostedSessionFile) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return writeTextFile(r.path, string(data), 0600)
}

func hasSessionLabel(sessions map[string]HostedSessionRecord, label string) bool {
	for _, session := range sessions {
		if session.SessionLabel == label {
			return true
		}
	}
	return false
}

func parseHostedSessionID(value string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(value, "hs_%d", &n); err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, errors.New("invalid session id")
	}
	return n, nil
}
