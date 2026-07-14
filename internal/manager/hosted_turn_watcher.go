package manager

import (
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/jesse/agent-inn/internal/config"
)

const hostedTurnWatcherInterval = 500 * time.Millisecond
const hostedTurnTranscriptMissTTL = 30 * time.Second
const codexTranscriptSessionsDir = "~/.codex/sessions"

type hostedTurnWatcher struct {
	settings           config.Settings
	registry           *HostedSessionRegistry
	registryCursor     hostedTurnRegistryCursor
	runner             hostedTMuxRunner
	files              map[string]hostedTurnTranscriptCursor
	launcherPaths      map[string]string
	launcherMissUntil  map[string]time.Time
	now                func() time.Time
	globTranscripts    func(pattern string) ([]string, error)
	onTurnStateChanged func(HostedSessionRecord)
	startupReconciled  bool
}

type hostedTurnRegistryCursor struct {
	Size    int64
	ModTime time.Time
	Plans   []hostedTurnWatchPlan
}

type hostedTurnWatchPlan struct {
	TranscriptPath string
	TurnsByID      map[string][]HostedTurnWatch
	PendingGoals   []HostedTurnWatch
	GoalCandidates []HostedTurnWatch
}

func (m *Manager) StartHostedTurnWatcher(interval time.Duration) func() {
	return m.startHostedTurnWatcher(interval, nil)
}

func (m *Manager) StartHostedTurnWatcherWithPollGuard(interval time.Duration, beforePoll func() bool) func() {
	return m.startHostedTurnWatcher(interval, beforePoll)
}

func (m *Manager) startHostedTurnWatcher(interval time.Duration, beforePoll func() bool) func() {
	if interval <= 0 {
		interval = hostedTurnWatcherInterval
	}
	watcher := newHostedTurnWatcher(m.config.Settings, m.hostedSessions, hostedTMuxRunnerFactory())
	poll := func() error {
		pollErr := watcher.pollWithStartupReconciliation()
		reconcileErr := m.reconcileHostedSessionSnapshots()
		if reconcileErr != nil {
			reconcileErr = hostedTurnPollFailureWith(hostedTurnReconciliationCategory, m.hostedSessions.path, 0, "", reconcileErr)
		}
		return errors.Join(pollErr, reconcileErr)
	}
	return startHostedTurnWatcherLoop(interval, beforePoll, poll, func(err error) {
		logHostedTurnPollErrors(m.logger, err)
	})
}

func (w *hostedTurnWatcher) pollWithStartupReconciliation() error {
	if !w.startupReconciled {
		sessions, err := w.registry.List()
		if err != nil {
			return err
		}
		windows, err := hostedWindowDetailsFromRunnerForSettings(w.settings, w.runner)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			windowID, active := HostedSessionActiveWindowID(windows, session)
			if !active {
				continue
			}
			session.TmuxWindowID = windowID
			if err := w.runTmuxStatus(session, session.TurnTranscriptPath, session.TurnTranscriptOffset); err != nil {
				return err
			}
		}
		w.startupReconciled = true
	}
	return w.pollOnce()
}

func newHostedTurnWatcher(settings config.Settings, registry *HostedSessionRegistry, runner hostedTMuxRunner) *hostedTurnWatcher {
	return &hostedTurnWatcher{
		settings:           settings,
		registry:           registry,
		runner:             runner,
		files:              map[string]hostedTurnTranscriptCursor{},
		launcherPaths:      map[string]string{},
		launcherMissUntil:  map[string]time.Time{},
		now:                time.Now,
		globTranscripts:    filepath.Glob,
		onTurnStateChanged: func(HostedSessionRecord) {},
	}
}

func (w *hostedTurnWatcher) currentTime() time.Time {
	return w.now()
}

func (w *hostedTurnWatcher) rememberLauncherMiss(launcherSessionID string) {
	w.launcherMissUntil[launcherSessionID] = w.currentTime().Add(hostedTurnTranscriptMissTTL)
}

func (w *hostedTurnWatcher) launcherMissActive(launcherSessionID string) bool {
	until, ok := w.launcherMissUntil[launcherSessionID]
	return ok && w.currentTime().Before(until)
}

func (w *hostedTurnWatcher) expireLauncherMisses() bool {
	now := w.currentTime()
	expired := false
	for launcherSessionID, until := range w.launcherMissUntil {
		if now.Before(until) {
			continue
		}
		delete(w.launcherMissUntil, launcherSessionID)
		expired = true
	}
	return expired
}

func (w *hostedTurnWatcher) pollOnce() error {
	plans, err := w.watchPlans()
	if err != nil {
		return err
	}
	for _, plan := range plans {
		committedOffsets := map[string]int64{}
		for _, watches := range plan.TurnsByID {
			for _, watch := range watches {
				committedOffsets[watch.SessionID] = watch.TurnTranscriptOffset
			}
		}
		if _, found := w.files[plan.TranscriptPath]; !found {
			w.files[plan.TranscriptPath] = hostedTurnTranscriptCursor{}
		}
		results, lines, nextCursor, changed, err := w.pollTranscript(plan.TranscriptPath)
		if err != nil {
			category := hostedTurnTranscriptReadCategory
			position := w.files[plan.TranscriptPath].Offset
			var parseFailure hostedTurnTranscriptParseFailure
			if errors.As(err, &parseFailure) {
				category = hostedTurnTranscriptParseCategory
				position = parseFailure.Offset
			}
			return hostedTurnPollFailureWith(category, plan.TranscriptPath, position, "", err)
		}
		if !changed {
			continue
		}
		completed := []HostedSessionRecord{}
		for _, result := range results {
			if result.GoalStatus != "" {
				updatedSessions := map[string]bool{}
				goalWatches := []HostedTurnWatch{}
				for _, watches := range plan.TurnsByID {
					goalWatches = append(goalWatches, watches...)
				}
				goalWatches = append(goalWatches, plan.GoalCandidates...)
				for _, watch := range goalWatches {
					if updatedSessions[watch.SessionID] || watch.LauncherSessionID != result.GoalThreadID {
						continue
					}
					updatedSessions[watch.SessionID] = true
					session, err := w.registry.SetCodexGoalStatus(watch.SessionID, result.GoalStatus)
					if err != nil {
						return err
					}
					if session.TurnWatchKind == HostedTurnWatchKindCodexGoal && (session.TurnState == HostedTurnStateIdle || isHostedTurnTerminalState(session.TurnState)) {
						plan.PendingGoals = append(plan.PendingGoals, HostedTurnWatch{
							SessionID:         session.SessionID,
							TurnGeneration:    session.TurnGeneration,
							TranscriptPath:    session.TurnTranscriptPath,
							TurnID:            session.TurnID,
							LauncherSessionID: session.LauncherSessionID,
							TmuxWindowID:      session.TmuxWindowID,
							TurnState:         session.TurnState,
							SessionSnapshot:   session,
						})
					}
				}
				continue
			}
			if result.State == HostedTurnStateRunning {
				for _, completedSession := range completed {
					var session HostedSessionRecord
					if completedSession.TurnWatchKind == HostedTurnWatchKindCodexGoal {
						started := false
						session, started, err = w.registry.StartNextGoalTurn(completedSession.SessionID, completedSession.TurnGeneration, plan.TranscriptPath, result.TurnID, result.TranscriptOffset)
						if err != nil {
							return err
						}
						if !started {
							continue
						}
					} else {
						session, err = w.registry.MarkTurnStateWithWatchAtOffset(completedSession.SessionID, HostedTurnStateRunning, "", "", plan.TranscriptPath, result.TurnID, HostedTurnWatchKindCodex, result.TranscriptOffset)
						if err != nil {
							return err
						}
					}
					plan.TurnsByID[result.TurnID] = append(plan.TurnsByID[result.TurnID], HostedTurnWatch{
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
					if session.TmuxWindowID == "" {
						continue
					}
					if err := w.runTmuxStatus(session, plan.TranscriptPath, result.TranscriptOffset); err != nil {
						return err
					}
				}
				for _, pending := range plan.PendingGoals {
					session, started, err := w.registry.StartNextGoalTurn(pending.SessionID, pending.TurnGeneration, plan.TranscriptPath, result.TurnID, result.TranscriptOffset)
					if err != nil {
						return err
					}
					if !started {
						continue
					}
					plan.TurnsByID[result.TurnID] = append(plan.TurnsByID[result.TurnID], HostedTurnWatch{
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
					w.onTurnStateChanged(session)
					if session.TmuxWindowID == "" {
						continue
					}
					if err := w.runTmuxStatus(session, plan.TranscriptPath, result.TranscriptOffset); err != nil {
						return err
					}
				}
				completed = nil
				continue
			}
			watches := plan.TurnsByID[result.TurnID]
			for index := range watches {
				watch := watches[index]
				session, ok, err := w.registry.CompleteWatchedTurn(watch.SessionID, watch.TurnGeneration, result.State, result.Reason, plan.TranscriptPath, result.TurnID, result.TranscriptOffset)
				if err != nil {
					return err
				}
				if !ok {
					watches[index].TurnState = result.State
					continue
				}
				watches[index].TurnState = session.TurnState
				watches[index].TurnTranscriptOffset = session.TurnTranscriptOffset
				watches[index].SessionSnapshot = session
				w.onTurnStateChanged(session)
				if session.TmuxWindowID == "" {
					completed = append(completed, session)
					continue
				}
				if result.State == HostedTurnStateDone {
					activeWindowOut, err := w.runner.Run(TmuxActiveWindowDetailsCommandForSettings(w.settings))
					if err != nil {
						return err
					}
					parts := strings.SplitN(strings.TrimSpace(activeWindowOut), "\t", 2)
					if len(parts) == 2 {
						activeWindows := map[string]string{parts[0]: parts[1]}
						if _, active := HostedSessionActiveWindowID(activeWindows, session); active {
							acknowledged, found, err := w.registry.AcknowledgeTurnByWindow(parts[0], parts[1])
							if err != nil {
								return err
							}
							if found {
								session = acknowledged
							}
						}
					}
				}
				completed = append(completed, session)
				if err := w.runTmuxStatus(session, plan.TranscriptPath, result.TranscriptOffset); err != nil {
					return err
				}
			}
			plan.TurnsByID[result.TurnID] = watches
		}
		runningWatches := []HostedTurnWatch{}
		for _, watches := range plan.TurnsByID {
			for _, watch := range watches {
				if watch.TurnState == HostedTurnStateRunning {
					runningWatches = append(runningWatches, watch)
				}
			}
		}
		if len(runningWatches) == 1 {
			watch := runningWatches[0]
			inputLines := lines
			committedOffset, committed := committedOffsets[watch.SessionID]
			if committed && committedOffset > w.files[plan.TranscriptPath].Offset {
				if watch.TmuxWindowID != "" {
					if err := w.runTmuxStatus(watch.SessionSnapshot, plan.TranscriptPath, committedOffset); err != nil {
						return err
					}
				}
			}
			if watch.SessionSnapshot.TurnInputRequestID == "" && watch.TurnTranscriptOffset > 0 {
				firstInputLine := len(lines)
				for index, line := range lines {
					if line.Offset > watch.TurnTranscriptOffset {
						firstInputLine = index
						break
					}
				}
				inputLines = lines[firstInputLine:]
			}
			reduction, err := reduceHostedTurnTranscript(watch.TurnWatchKind, watch.SessionSnapshot.TurnInputRequestID, inputLines)
			if err != nil {
				position := watch.TurnTranscriptOffset
				var parseFailure hostedTurnTranscriptParseFailure
				if errors.As(err, &parseFailure) {
					position = parseFailure.Offset
				}
				return hostedTurnPollFailureWith(hostedTurnTranscriptParseCategory, plan.TranscriptPath, position, watch.SessionID, err)
			}
			if reduction.InputObserved {
				session, applied, err := w.registry.CommitWatchedTurnInput(watch.SessionID, watch.TurnGeneration, plan.TranscriptPath, watch.TurnID, reduction.FinalOffset, reduction.InputRequestID)
				if err != nil {
					return hostedTurnPollFailureWith(hostedTurnRegistryWriteCategory, plan.TranscriptPath, reduction.FinalOffset, watch.SessionID, err)
				}
				committedReplay := false
				if !applied {
					committed, found, err := w.registry.Get(watch.SessionID)
					if err != nil {
						return err
					}
					if found && committed.TurnGeneration == watch.TurnGeneration && committed.TurnTranscriptPath == plan.TranscriptPath && committed.TurnID == watch.TurnID && committed.TurnTranscriptOffset >= reduction.FinalOffset {
						session = committed
						committedReplay = true
					}
				}
				if (reduction.InputChanged || committedReplay) && session.SessionID != "" && session.TmuxWindowID != "" {
					if err := w.runTmuxStatus(session, plan.TranscriptPath, reduction.FinalOffset); err != nil {
						return err
					}
				}
			}
		}
		w.files[plan.TranscriptPath] = nextCursor
	}
	return nil
}
