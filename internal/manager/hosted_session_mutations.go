package manager

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
)

func (r *HostedSessionRegistry) RenameForSettings(sessionID string, sessionLabel string, settings config.Settings, runner hostedTMuxRunner) (HostedSessionRecord, error) {
	updated, _, err := r.RenameAndResolveStatus(sessionID, sessionLabel, settings, runner)
	return updated, err
}

func (r *HostedSessionRegistry) RenameByWindow(windowID string, sessionLabel string) (HostedSessionRecord, bool, error) {
	var updated HostedSessionRecord
	found := false
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		sessionID := ""
		for id, session := range file.Sessions {
			if session.TmuxWindowID == windowID {
				sessionID = id
				break
			}
		}
		if sessionID == "" {
			return nil
		}
		sessionLabel = strings.TrimSpace(sessionLabel)
		if sessionLabel == "" {
			return errors.New("session label is required")
		}
		for id, other := range file.Sessions {
			if id != sessionID && other.SessionLabel == sessionLabel {
				return hostedSessionConflictError(fmt.Sprintf("hosted session label %q already exists", sessionLabel))
			}
		}
		session := file.Sessions[sessionID]
		session.SessionLabel = sessionLabel
		file.Sessions[sessionID] = session
		updated = session
		found = true
		return nil
	})
	return updated, found, err
}

func (r *HostedSessionRegistry) FindByWindow(windowID string) (HostedSessionRecord, bool, error) {
	var foundSession HostedSessionRecord
	found := false
	err := r.withReadLockedFile(func(file *hostedSessionFile) error {
		for _, session := range file.Sessions {
			if session.TmuxWindowID == windowID {
				foundSession = session
				found = true
				return nil
			}
		}
		return nil
	})
	return foundSession, found, err
}

func (r *HostedSessionRegistry) RenameAndResolveStatus(sessionID string, sessionLabel string, settings config.Settings, runner hostedTMuxRunner) (HostedSessionRecord, HostedSessionStatus, error) {
	var updated HostedSessionRecord
	status := HostedSessionStatusStale
	err := r.withLockedFile(func(file *hostedSessionFile) error {
		session, ok := file.Sessions[sessionID]
		if !ok {
			return hostedSessionNotFoundError(fmt.Sprintf("hosted session %q not found", sessionID))
		}
		sessionLabel = strings.TrimSpace(sessionLabel)
		if sessionLabel == "" {
			return errors.New("session label is required")
		}
		if session.SessionLabel != sessionLabel {
			for _, other := range file.Sessions {
				if other.SessionID != sessionID && other.SessionLabel == sessionLabel {
					return hostedSessionConflictError(fmt.Sprintf("hosted session label %q already exists", sessionLabel))
				}
			}
		}
		if session.TmuxWindowID != "" {
			windows, err := hostedWindowDetailsFromRunnerForSettings(settings, runner)
			if err != nil {
				return err
			}
			if windowID, active := HostedSessionActiveWindowID(windows, session); active {
				if session.SessionLabel != sessionLabel {
					if _, err := runner.Run(TmuxRenameWindowCommandForSettings(settings, windowID, sessionLabel)); err != nil {
						return err
					}
				}
				session.TmuxWindowID = windowID
				status = HostedSessionStatusActive
			}
		}
		session.SessionLabel = sessionLabel
		file.Sessions[sessionID] = session
		updated = session
		return nil
	})
	return updated, status, err
}
