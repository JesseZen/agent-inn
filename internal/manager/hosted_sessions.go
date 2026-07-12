package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/constants"
)

const hostedSessionsFileName = "hosted-terminal-sessions.json"
const firstDuplicateLabelSuffix = 2

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

const HostedUserMarkerTodo = "todo"

const (
	HostedTurnWatchKindCodex           = "codex"
	HostedTurnWatchKindCodexGoal       = "codex-goal"
	HostedTurnWatchKindCodexGoalPaused = "codex-goal-paused"
)

type HostedSessionRegistry struct {
	path string
	lock string
}

type HostedSessionRecord struct {
	SessionID                  string    `json:"session_id"`
	SessionLabel               string    `json:"session_label"`
	WorkerID                   string    `json:"worker_id,omitempty"`
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
	TurnTranscriptPath         string    `json:"turn_transcript_path,omitempty"`
	TurnTranscriptOffset       int64     `json:"turn_transcript_offset,omitempty"`
	TurnID                     string    `json:"turn_id,omitempty"`
	TurnWatchKind              string    `json:"turn_watch_kind,omitempty"`
	UserMarker                 string    `json:"user_marker,omitempty"`
	CreatedAt                  time.Time `json:"created_at"`
	LastOpenedAt               time.Time `json:"last_opened_at"`
}

type HostedSessionSummary struct {
	HostedSessionRecord
	Status string                      `json:"status"`
	Worker *HostedSessionWorkerSummary `json:"worker,omitempty"`
}

type HostedSessionWorkerSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Missing bool   `json:"missing,omitempty"`
}

type HostedTurnWatch struct {
	SessionID            string
	TurnGeneration       int
	TranscriptPath       string
	TurnTranscriptOffset int64
	TurnID               string
	TurnWatchKind        string
	GoalCandidate        bool
	LauncherSessionID    string
	TmuxWindowID         string
	TurnState            string
	SessionSnapshot      HostedSessionRecord
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
		input.WorkerID = strings.TrimSpace(input.WorkerID)
		input.WorkerName = strings.TrimSpace(input.WorkerName)
		input.Workspace = strings.TrimSpace(input.Workspace)
		input.Model = strings.TrimSpace(input.Model)
		if input.WorkerID == "" {
			input.WorkerID = input.WorkerName
		}
		if input.WorkerName == "" {
			input.WorkerName = input.WorkerID
		}
		if input.WorkerID == "" {
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
			next := file.WorkerCounters[input.WorkerID]
			for {
				next++
				label = fmt.Sprintf("%s %d", input.WorkerID, next)
				if !hasSessionLabel(file.Sessions, label) {
					break
				}
			}
			file.WorkerCounters[input.WorkerID] = next
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

func (r *HostedSessionRegistry) Duplicate(sessionID string) (HostedSessionRecord, error) {
	var duplicated HostedSessionRecord
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return fmt.Errorf("hosted session %q not found", sessionID)
		}

		labelBase := session.SessionLabel
		nextSuffix := firstDuplicateLabelSuffix
		if index := strings.LastIndex(labelBase, " "); index >= 0 {
			suffix, err := strconv.Atoi(labelBase[index+1:])
			if err == nil && suffix > 0 {
				labelBase = labelBase[:index]
				nextSuffix = suffix + 1
			}
		}
		label := fmt.Sprintf("%s %d", labelBase, nextSuffix)
		for hasSessionLabel(file.Sessions, label) {
			nextSuffix++
			label = fmt.Sprintf("%s %d", labelBase, nextSuffix)
		}

		file.NextSessionID++
		now := time.Now().UTC()
		duplicated = HostedSessionRecord{
			SessionID:    fmt.Sprintf("hs_%d", file.NextSessionID),
			SessionLabel: label,
			WorkerID:     session.WorkerID,
			WorkerName:   session.WorkerName,
			WorkerPort:   session.WorkerPort,
			Workspace:    session.Workspace,
			Model:        session.Model,
			AddDirs:      append([]string{}, session.AddDirs...),
			CreatedAt:    now,
			LastOpenedAt: now,
		}
		file.Sessions[duplicated.SessionID] = duplicated
		return nil
	})
	if err != nil {
		return HostedSessionRecord{}, err
	}
	return duplicated, nil
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

func (r *HostedSessionRegistry) UpdateWorker(sessionID string, workerID string, workerPort int) (HostedSessionRecord, error) {
	var updated HostedSessionRecord
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return fmt.Errorf("hosted session %q not found", sessionID)
		}
		workerID = strings.TrimSpace(workerID)
		if workerID == "" {
			return errors.New("worker name is required")
		}
		if workerPort <= 0 {
			return errors.New("worker port is required")
		}
		session.WorkerID = workerID
		session.WorkerName = workerID
		session.WorkerPort = workerPort
		file.Sessions[sessionID] = session
		updated = session
		return nil
	})
	return updated, err
}

func (r *HostedSessionRegistry) RenameForSettings(sessionID string, sessionLabel string, settings config.Settings, runner hostedTMuxRunner) (HostedSessionRecord, error) {
	var updated HostedSessionRecord
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return fmt.Errorf("hosted session %q not found", sessionID)
		}
		sessionLabel = strings.TrimSpace(sessionLabel)
		if sessionLabel == "" {
			return errors.New("session label is required")
		}
		if session.SessionLabel == sessionLabel {
			updated = session
			return nil
		}
		for _, other := range file.Sessions {
			if other.SessionID != sessionID && other.SessionLabel == sessionLabel {
				return fmt.Errorf("hosted session label %q already exists", sessionLabel)
			}
		}
		if session.TmuxWindowID != "" {
			windows, err := hostedWindowDetailsFromRunnerForSettings(settings, runner)
			if err != nil {
				return err
			}
			if windowID, active := HostedSessionActiveWindowID(windows, session); active {
				if _, err := runner.Run(TmuxRenameWindowCommandForSettings(settings, windowID, sessionLabel)); err != nil {
					return err
				}
				session.TmuxWindowID = windowID
			}
		}
		session.SessionLabel = sessionLabel
		file.Sessions[sessionID] = session
		updated = session
		return nil
	})
	return updated, err
}

func (r *HostedSessionRegistry) MarkTurnState(sessionID string, state string, reason string, launcherSessionID string) (HostedSessionRecord, error) {
	return r.MarkTurnStateWithWatch(sessionID, state, reason, launcherSessionID, "", "", "")
}

func (r *HostedSessionRegistry) MarkTurnStateWithWatch(sessionID string, state string, reason string, launcherSessionID string, transcriptPath string, turnID string, watchKind string) (HostedSessionRecord, error) {
	var updated HostedSessionRecord
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return fmt.Errorf("hosted session %q not found", sessionID)
		}
		if state == HostedTurnStateIdle && session.TurnState == HostedTurnStateRunning {
			updated = session
			return nil
		}
		if state == HostedTurnStateRunning {
			session.TurnGeneration++
			session.TurnStateReason = ""
			session.TurnTranscriptPath = strings.TrimSpace(transcriptPath)
			session.TurnTranscriptOffset = 0
			session.TurnID = strings.TrimSpace(turnID)
			session.TurnWatchKind = strings.TrimSpace(watchKind)
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

func (r *HostedSessionRegistry) CompleteWatchedTurn(sessionID string, turnGeneration int, state string, reason string, transcriptPath string, turnID string, transcriptOffset int64) (HostedSessionRecord, bool, error) {
	var updated HostedSessionRecord
	applied := false
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok || session.TurnGeneration != turnGeneration {
			return nil
		}
		if session.TurnState != HostedTurnStateRunning && session.TurnState != HostedTurnStateDone {
			return nil
		}
		if state == HostedTurnStateDone &&
			(session.TurnState == HostedTurnStateFailed || session.TurnState == HostedTurnStateInterrupted) {
			updated = session
			return nil
		}
		if session.TurnState == HostedTurnStateDone && (state == HostedTurnStateFailed || state == HostedTurnStateInterrupted) {
			session.TurnAcknowledgedGeneration = 0
		}
		session.TurnState = state
		session.TurnStateReason = strings.TrimSpace(reason)
		if session.TurnWatchKind == HostedTurnWatchKindCodexGoal || session.TurnWatchKind == HostedTurnWatchKindCodexGoalPaused {
			session.TurnTranscriptPath = strings.TrimSpace(transcriptPath)
			session.TurnID = strings.TrimSpace(turnID)
			session.TurnTranscriptOffset = transcriptOffset
		} else {
			session.TurnTranscriptPath = ""
			session.TurnTranscriptOffset = 0
			session.TurnID = ""
			session.TurnWatchKind = ""
		}
		file.Sessions[sessionID] = session
		updated = session
		applied = true
		return nil
	})
	return updated, applied, err
}

func (r *HostedSessionRegistry) SetCodexGoalStatus(sessionID string, status string) (HostedSessionRecord, error) {
	var updated HostedSessionRecord
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return fmt.Errorf("hosted session %q not found", sessionID)
		}
		switch status {
		case codexTranscriptGoalActive:
			session.TurnWatchKind = HostedTurnWatchKindCodexGoal
		case codexTranscriptGoalPaused:
			session.TurnWatchKind = HostedTurnWatchKindCodexGoalPaused
		case codexTranscriptGoalComplete:
			if isHostedTurnTerminalState(session.TurnState) {
				session.TurnTranscriptPath = ""
				session.TurnTranscriptOffset = 0
				session.TurnID = ""
				session.TurnWatchKind = ""
			} else {
				session.TurnWatchKind = HostedTurnWatchKindCodex
			}
		}
		file.Sessions[sessionID] = session
		updated = session
		return nil
	})
	return updated, err
}

func (r *HostedSessionRegistry) StartNextGoalTurn(sessionID string, turnGeneration int, transcriptPath string, turnID string, transcriptOffset int64) (HostedSessionRecord, bool, error) {
	var updated HostedSessionRecord
	started := false
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok || session.TurnGeneration != turnGeneration || session.TurnWatchKind != HostedTurnWatchKindCodexGoal || (session.TurnState != HostedTurnStateIdle && !isHostedTurnTerminalState(session.TurnState)) {
			return nil
		}
		session.TurnGeneration++
		session.TurnState = HostedTurnStateRunning
		session.TurnStateReason = ""
		session.TurnTranscriptPath = strings.TrimSpace(transcriptPath)
		session.TurnTranscriptOffset = transcriptOffset
		session.TurnID = strings.TrimSpace(turnID)
		file.Sessions[sessionID] = session
		updated = session
		started = true
		return nil
	})
	return updated, started, err
}

func (r *HostedSessionRegistry) WatchedTurns() ([]HostedTurnWatch, error) {
	var watched []HostedTurnWatch
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		for _, session := range file.Sessions {
			if session.TurnGeneration <= 0 {
				continue
			}
			isGoalTerminalWatch := (session.TurnWatchKind == HostedTurnWatchKindCodexGoal || session.TurnWatchKind == HostedTurnWatchKindCodexGoalPaused) && isHostedTurnTerminalState(session.TurnState)
			if session.TurnState != HostedTurnStateRunning && session.TurnState != HostedTurnStateDone && !isGoalTerminalWatch {
				continue
			}
			hasExplicitWatch := session.TurnTranscriptPath != "" && session.TurnID != ""
			hasLauncherWatch := (session.TurnWatchKind == HostedTurnWatchKindCodex || session.TurnWatchKind == HostedTurnWatchKindCodexGoal || session.TurnWatchKind == HostedTurnWatchKindCodexGoalPaused) &&
				session.TurnTranscriptPath == "" && session.TurnID == "" && session.LauncherSessionID != ""
			if !hasExplicitWatch && !hasLauncherWatch {
				continue
			}
			watched = append(watched, HostedTurnWatch{
				SessionID:            session.SessionID,
				TurnGeneration:       session.TurnGeneration,
				TranscriptPath:       session.TurnTranscriptPath,
				TurnTranscriptOffset: session.TurnTranscriptOffset,
				TurnID:               session.TurnID,
				TurnWatchKind:        session.TurnWatchKind,
				LauncherSessionID:    session.LauncherSessionID,
				TmuxWindowID:         session.TmuxWindowID,
				TurnState:            session.TurnState,
				SessionSnapshot:      session,
			})
		}
		sort.Slice(watched, func(i, j int) bool {
			if watched[i].TranscriptPath == watched[j].TranscriptPath {
				if watched[i].TurnID == watched[j].TurnID {
					return watched[i].SessionID < watched[j].SessionID
				}
				return watched[i].TurnID < watched[j].TurnID
			}
			return watched[i].TranscriptPath < watched[j].TranscriptPath
		})
		return nil
	})
	return watched, err
}

func (r *HostedSessionRegistry) GoalCandidates() ([]HostedTurnWatch, error) {
	var candidates []HostedTurnWatch
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		for _, session := range file.Sessions {
			if session.LauncherSessionID == "" || session.TurnWatchKind != "" {
				continue
			}
			candidates = append(candidates, HostedTurnWatch{
				SessionID:         session.SessionID,
				TurnGeneration:    session.TurnGeneration,
				LauncherSessionID: session.LauncherSessionID,
				TmuxWindowID:      session.TmuxWindowID,
				TurnState:         session.TurnState,
				SessionSnapshot:   session,
				GoalCandidate:     true,
			})
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].LauncherSessionID < candidates[j].LauncherSessionID
		})
		return nil
	})
	return candidates, err
}

func (r *HostedSessionRegistry) AcknowledgeTurnByWindow(windowID string, windowName string) (HostedSessionRecord, bool, error) {
	var updated HostedSessionRecord
	found := false
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		for sessionID, session := range file.Sessions {
			matchesWindowID := session.TmuxWindowID == windowID
			matchesLegacyName := windowName != "" && session.TmuxWindowID == windowName
			if !matchesWindowID && !matchesLegacyName {
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

func (r *HostedSessionRegistry) ToggleUserMarkerByWindow(windowID string, windowName string) (HostedSessionRecord, bool, error) {
	var updated HostedSessionRecord
	found := false
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		for sessionID, session := range file.Sessions {
			matchesWindowID := session.TmuxWindowID == windowID
			matchesLegacyName := windowName != "" && session.TmuxWindowID == windowName
			if !matchesWindowID && !matchesLegacyName {
				continue
			}
			found = true
			if session.UserMarker == HostedUserMarkerTodo {
				session.UserMarker = ""
			} else {
				session.UserMarker = HostedUserMarkerTodo
			}
			file.Sessions[sessionID] = session
			updated = session
			return nil
		}
		return nil
	})
	return updated, found, err
}

func (r *HostedSessionRegistry) MarkTurnUnread(sessionID string) (HostedSessionRecord, error) {
	var updated HostedSessionRecord
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return fmt.Errorf("hosted session %q not found", sessionID)
		}
		if !isHostedTurnTerminalState(session.TurnState) {
			return fmt.Errorf("hosted session %q turn state %q cannot be marked unread", sessionID, session.TurnState)
		}
		if session.TurnGeneration <= 0 {
			return fmt.Errorf("hosted session %q turn generation %d cannot be marked unread", sessionID, session.TurnGeneration)
		}
		session.TurnAcknowledgedGeneration = 0
		file.Sessions[sessionID] = session
		updated = session
		return nil
	})
	return updated, err
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
	for sessionID, session := range file.Sessions {
		session.WorkerID = strings.TrimSpace(session.WorkerID)
		session.WorkerName = strings.TrimSpace(session.WorkerName)
		if session.WorkerID == "" {
			session.WorkerID = session.WorkerName
		}
		if session.WorkerName == "" {
			session.WorkerName = session.WorkerID
		}
		file.Sessions[sessionID] = session
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
