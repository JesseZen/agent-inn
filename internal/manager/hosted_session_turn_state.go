package manager

import (
	"fmt"
	"sort"
	"strings"
)

func (r *HostedSessionRegistry) MarkTurnState(sessionID string, state string, reason string, launcherSessionID string) (HostedSessionRecord, error) {
	return r.MarkTurnStateWithWatch(sessionID, state, reason, launcherSessionID, "", "", "")
}

func (r *HostedSessionRegistry) MarkTurnStateWithWatch(sessionID string, state string, reason string, launcherSessionID string, transcriptPath string, turnID string, watchKind string) (HostedSessionRecord, error) {
	return r.MarkTurnStateWithWatchAtOffset(sessionID, state, reason, launcherSessionID, transcriptPath, turnID, watchKind, 0)
}

func (r *HostedSessionRegistry) MarkTurnStateWithWatchAtOffset(sessionID string, state string, reason string, launcherSessionID string, transcriptPath string, turnID string, watchKind string, transcriptOffset int64) (HostedSessionRecord, error) {
	var updated HostedSessionRecord
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return hostedSessionNotFoundError(fmt.Sprintf("hosted session %q not found", sessionID))
		}
		if state == HostedTurnStateIdle && session.TurnState == HostedTurnStateRunning {
			updated = session
			return nil
		}
		if state == HostedTurnStateRunning {
			session.TurnGeneration++
			session.TurnStateReason = ""
			session.TurnTranscriptPath = strings.TrimSpace(transcriptPath)
			session.TurnTranscriptOffset = transcriptOffset
			session.TurnID = strings.TrimSpace(turnID)
			session.TurnWatchKind = strings.TrimSpace(watchKind)
			session.TurnInputRequestID = ""
		}
		if state == HostedTurnStateDone &&
			(session.TurnState == HostedTurnStateFailed || session.TurnState == HostedTurnStateInterrupted) {
			updated = session
			return nil
		}
		session.TurnState = state
		session.TurnStateReason = strings.TrimSpace(reason)
		if isHostedTurnTerminalState(state) {
			session.TurnInputRequestID = ""
		}
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
		session.TurnInputRequestID = ""
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
			return hostedSessionNotFoundError(fmt.Sprintf("hosted session %q not found", sessionID))
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
				session.TurnInputRequestID = ""
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
		session.TurnInputRequestID = ""
		file.Sessions[sessionID] = session
		updated = session
		started = true
		return nil
	})
	return updated, started, err
}

func (r *HostedSessionRegistry) CommitWatchedTurnInput(sessionID string, turnGeneration int, transcriptPath string, turnID string, transcriptOffset int64, requestID string) (HostedSessionRecord, bool, error) {
	var updated HostedSessionRecord
	applied := false
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok || session.TurnState != HostedTurnStateRunning || session.TurnGeneration != turnGeneration || session.TurnTranscriptPath != transcriptPath || session.TurnID != turnID {
			return nil
		}
		if transcriptOffset <= session.TurnTranscriptOffset {
			updated = session
			return nil
		}
		session.TurnTranscriptOffset = transcriptOffset
		session.TurnInputRequestID = requestID
		file.Sessions[sessionID] = session
		updated = session
		applied = true
		return nil
	})
	return updated, applied, err
}

func (r *HostedSessionRegistry) WatchedTurns() ([]HostedTurnWatch, error) {
	var watched []HostedTurnWatch
	err := r.withReadLockedFile(func(file *hostedSessionFile) error {
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
	err := r.withReadLockedFile(func(file *hostedSessionFile) error {
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

func (r *HostedSessionRegistry) SetUserMarker(sessionID string, marker string) (HostedSessionRecord, error) {
	var updated HostedSessionRecord
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return hostedSessionNotFoundError(fmt.Sprintf("hosted session %q not found", sessionID))
		}
		if marker != "" && marker != HostedUserMarkerTodo {
			return fmt.Errorf("invalid hosted session marker %q", marker)
		}
		session.UserMarker = marker
		file.Sessions[sessionID] = session
		updated = session
		return nil
	})
	return updated, err
}

func (r *HostedSessionRegistry) MarkTurnUnread(sessionID string) (HostedSessionRecord, error) {
	var updated HostedSessionRecord
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return hostedSessionNotFoundError(fmt.Sprintf("hosted session %q not found", sessionID))
		}
		if !isHostedTurnTerminalState(session.TurnState) {
			return hostedSessionConflictError(fmt.Sprintf("hosted session %q turn state %q cannot be marked unread", sessionID, session.TurnState))
		}
		if session.TurnGeneration <= 0 {
			return hostedSessionConflictError(fmt.Sprintf("hosted session %q turn generation %d cannot be marked unread", sessionID, session.TurnGeneration))
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
