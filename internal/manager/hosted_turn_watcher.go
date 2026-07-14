package manager

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/logging"
)

const (
	hostedTurnWatcherInterval    = 500 * time.Millisecond
	hostedTurnTranscriptMaxLine  = 10 * 1024 * 1024
	hostedTurnInterruptedReason  = constants.HostedTurnReasonUserInterrupt
	hostedTurnCodexFailureReason = constants.HostedTurnReasonCodexTaskFailed
	// Permanent Glob misses (e.g. claude UUIDs under ~/.codex/sessions) must not
	// rescan thousands of transcript files every 500ms. Retry after this TTL.
	hostedTurnTranscriptMissTTL  = 30 * time.Second
	codexTranscriptSessionsDir   = "~/.codex/sessions"
	codexTranscriptEventMsg      = "event_msg"
	codexTranscriptTaskStarted   = "task_started"
	codexTranscriptTaskComplete  = "task_complete"
	codexTranscriptTurnAborted   = "turn_aborted"
	codexTranscriptTurnCompleted = "turn.completed"
	codexTranscriptTurnFailed    = "turn.failed"
	codexTranscriptInterrupted   = "interrupted"
	codexTranscriptGoalUpdated   = "thread_goal_updated"
	codexTranscriptGoalActive    = "active"
	codexTranscriptGoalPaused    = "paused"
	codexTranscriptGoalComplete  = "complete"
)

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

type hostedTurnTranscriptCursor struct {
	Offset  int64
	Size    int64
	ModTime time.Time
}

type hostedTurnTranscriptResult struct {
	TurnID           string
	State            string
	Reason           string
	GoalThreadID     string
	GoalStatus       string
	TranscriptOffset int64
}

func (m *Manager) StartHostedTurnWatcher(interval time.Duration) func() {
	if interval <= 0 {
		interval = hostedTurnWatcherInterval
	}
	done := make(chan struct{})
	watcher := newHostedTurnWatcher(m.config.Settings, m.hostedSessions, hostedTMuxRunnerFactory())
	watcher.onTurnStateChanged = func(session HostedSessionRecord) {
		m.publishEvent(EventHostedSessionTurnStateChanged, map[string]any{
			"session_id": session.SessionID,
			"turn_state": session.TurnState,
		})
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := watcher.pollOnce(); err != nil {
					m.logger.Warn(logging.EventHostedTurnPoll, "error", err.Error())
				}
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

func newHostedTurnWatcher(settings config.Settings, registry *HostedSessionRegistry, runner hostedTMuxRunner) *hostedTurnWatcher {
	return &hostedTurnWatcher{
		settings:          settings,
		registry:          registry,
		runner:            runner,
		files:             map[string]hostedTurnTranscriptCursor{},
		launcherPaths:     map[string]string{},
		launcherMissUntil: map[string]time.Time{},
		now:               time.Now,
		globTranscripts:   filepath.Glob,
		onTurnStateChanged: func(HostedSessionRecord) {},
	}
}

func (w *hostedTurnWatcher) currentTime() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

func (w *hostedTurnWatcher) globTranscriptMatches(pattern string) ([]string, error) {
	if w.globTranscripts != nil {
		return w.globTranscripts(pattern)
	}
	return filepath.Glob(pattern)
}

func (w *hostedTurnWatcher) rememberLauncherMiss(launcherSessionID string) {
	if launcherSessionID == "" {
		return
	}
	if w.launcherMissUntil == nil {
		w.launcherMissUntil = map[string]time.Time{}
	}
	w.launcherMissUntil[launcherSessionID] = w.currentTime().Add(hostedTurnTranscriptMissTTL)
}

func (w *hostedTurnWatcher) launcherMissActive(launcherSessionID string) bool {
	if launcherSessionID == "" || len(w.launcherMissUntil) == 0 {
		return false
	}
	until, ok := w.launcherMissUntil[launcherSessionID]
	return ok && w.currentTime().Before(until)
}

// expireLauncherMisses drops elapsed miss TTLs. Returns true when at least one
// miss expired so watchPlans can rebuild and Glob again.
func (w *hostedTurnWatcher) expireLauncherMisses() bool {
	if len(w.launcherMissUntil) == 0 {
		return false
	}
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
		results, err := w.pollTranscript(plan.TranscriptPath)
		if err != nil {
			return err
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
						session, err = w.registry.MarkTurnStateWithWatch(completedSession.SessionID, HostedTurnStateRunning, "", "", plan.TranscriptPath, result.TurnID, HostedTurnWatchKindCodex)
						if err != nil {
							return err
						}
					}
					plan.TurnsByID[result.TurnID] = append(plan.TurnsByID[result.TurnID], HostedTurnWatch{
						SessionID:         session.SessionID,
						TurnGeneration:    session.TurnGeneration,
						TranscriptPath:    session.TurnTranscriptPath,
						TurnID:            session.TurnID,
						LauncherSessionID: session.LauncherSessionID,
						TmuxWindowID:      session.TmuxWindowID,
						TurnState:         session.TurnState,
						SessionSnapshot:   session,
					})
					if session.TmuxWindowID == "" {
						continue
					}
					if _, err := w.runner.Run(TmuxHostedTurnStatusCommandForRecord(w.settings, session)); err != nil {
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
						SessionID:         session.SessionID,
						TurnGeneration:    session.TurnGeneration,
						TranscriptPath:    session.TurnTranscriptPath,
						TurnID:            session.TurnID,
						LauncherSessionID: session.LauncherSessionID,
						TmuxWindowID:      session.TmuxWindowID,
						TurnState:         session.TurnState,
						SessionSnapshot:   session,
					})
					w.onTurnStateChanged(session)
					if session.TmuxWindowID == "" {
						continue
					}
					if _, err := w.runner.Run(TmuxHostedTurnStatusCommandForRecord(w.settings, session)); err != nil {
						return err
					}
				}
				completed = nil
				continue
			}
			for _, watch := range plan.TurnsByID[result.TurnID] {
				session, ok, err := w.registry.CompleteWatchedTurn(watch.SessionID, watch.TurnGeneration, result.State, result.Reason, plan.TranscriptPath, result.TurnID, result.TranscriptOffset)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
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
				if _, err := w.runner.Run(TmuxHostedTurnStatusCommandForRecord(w.settings, session)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (w *hostedTurnWatcher) watchPlans() ([]hostedTurnWatchPlan, error) {
	missesExpired := w.expireLauncherMisses()
	stat, err := os.Stat(w.registry.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && !missesExpired && stat.Size() == w.registryCursor.Size && stat.ModTime().Equal(w.registryCursor.ModTime) {
		return w.registryCursor.Plans, nil
	}

	watches, err := w.registry.WatchedTurns()
	if err != nil {
		return nil, err
	}
	goalCandidates, err := w.registry.GoalCandidates()
	if err != nil {
		return nil, err
	}
	watches = append(watches, goalCandidates...)
	plansByPath := map[string]int{}
	plans := []hostedTurnWatchPlan{}
	unresolvedLauncherWatch := false
	for _, watch := range watches {
		if watch.TranscriptPath != "" && watch.TurnTranscriptOffset > 0 {
			if _, found := w.files[watch.TranscriptPath]; !found {
				w.files[watch.TranscriptPath] = hostedTurnTranscriptCursor{Offset: watch.TurnTranscriptOffset}
			}
		}
		if watch.TranscriptPath == "" {
			transcriptPath := w.launcherPaths[watch.LauncherSessionID]
			if transcriptPath != "" {
				if _, err := os.Stat(transcriptPath); os.IsNotExist(err) {
					delete(w.launcherPaths, watch.LauncherSessionID)
					transcriptPath = ""
				} else if err != nil {
					return nil, err
				}
			}
			if transcriptPath == "" {
				if watch.GoalCandidate && w.launcherMissActive(watch.LauncherSessionID) {
					// Permanent/noisy misses (e.g. claude UUIDs) skip expensive rescans.
					continue
				}
				pattern := filepath.Join(expandHomePath(codexTranscriptSessionsDir), "*", "*", "*", "*"+watch.LauncherSessionID+".jsonl")
				matches, err := w.globTranscriptMatches(pattern)
				if err != nil {
					return nil, err
				}
				if len(matches) == 0 {
					if watch.GoalCandidate {
						// Goal candidates without a codex transcript can stay unresolved forever.
						// Cache the miss so watchPlans remains cacheable and cheap.
						w.rememberLauncherMiss(watch.LauncherSessionID)
						continue
					}
					// Active codex launcher watches must re-Glob until the transcript appears.
					unresolvedLauncherWatch = true
					continue
				}
				delete(w.launcherMissUntil, watch.LauncherSessionID)
				sort.Strings(matches)
				transcriptPath = matches[len(matches)-1]
				w.launcherPaths[watch.LauncherSessionID] = transcriptPath
			}
			if watch.GoalCandidate {
				index, ok := plansByPath[transcriptPath]
				if !ok {
					index = len(plans)
					plansByPath[transcriptPath] = index
					plans = append(plans, hostedTurnWatchPlan{
						TranscriptPath: transcriptPath,
						TurnsByID:      map[string][]HostedTurnWatch{},
					})
				}
				watch.TranscriptPath = transcriptPath
				plans[index].GoalCandidates = append(plans[index].GoalCandidates, watch)
				continue
			}

			file, err := os.Open(transcriptPath)
			if os.IsNotExist(err) {
				unresolvedLauncherWatch = true
				continue
			}
			if err != nil {
				return nil, err
			}
			stat, err := file.Stat()
			if err != nil {
				_ = file.Close()
				return nil, err
			}
			cursor, hasCursor := w.files[transcriptPath]
			if cursor.Offset > stat.Size() {
				cursor.Offset = 0
			}
			if !hasCursor && watch.TurnGeneration > 1 {
				w.files[transcriptPath] = hostedTurnTranscriptCursor{Offset: stat.Size(), Size: stat.Size(), ModTime: stat.ModTime()}
				_ = file.Close()
				unresolvedLauncherWatch = true
				continue
			}
			if _, err := file.Seek(cursor.Offset, io.SeekStart); err != nil {
				_ = file.Close()
				return nil, err
			}
			latestTurnID := ""
			nextOffset := cursor.Offset
			reader := bufio.NewReader(file)
			scanErr := error(nil)
			for {
				line, err := reader.ReadString('\n')
				if len(line) > 0 {
					nextOffset += int64(len(line))
					if len(line) > hostedTurnTranscriptMaxLine {
						scanErr = fmt.Errorf("line exceeds %d bytes", hostedTurnTranscriptMaxLine)
						break
					}
					var event struct {
						Type    string `json:"type"`
						Payload struct {
							Type   string `json:"type"`
							TurnID string `json:"turn_id"`
						} `json:"payload"`
					}
					if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &event); err != nil {
						scanErr = err
						break
					}
					if event.Type == codexTranscriptEventMsg && event.Payload.Type == codexTranscriptTaskStarted && event.Payload.TurnID != "" {
						latestTurnID = event.Payload.TurnID
					}
				}
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					scanErr = err
					break
				}
			}
			if err := file.Close(); err != nil && scanErr == nil {
				scanErr = err
			}
			if scanErr != nil {
				return nil, scanErr
			}
			if latestTurnID == "" {
				w.files[transcriptPath] = hostedTurnTranscriptCursor{Offset: nextOffset, Size: stat.Size(), ModTime: stat.ModTime()}
				unresolvedLauncherWatch = true
				continue
			}
			watch.TranscriptPath = transcriptPath
			watch.TurnID = latestTurnID
		}
		index, ok := plansByPath[watch.TranscriptPath]
		if !ok {
			index = len(plans)
			plansByPath[watch.TranscriptPath] = index
			plans = append(plans, hostedTurnWatchPlan{
				TranscriptPath: watch.TranscriptPath,
				TurnsByID:      map[string][]HostedTurnWatch{},
			})
		}
		plans[index].TurnsByID[watch.TurnID] = append(plans[index].TurnsByID[watch.TurnID], watch)
		if watch.TurnWatchKind == HostedTurnWatchKindCodexGoal && isHostedTurnTerminalState(watch.TurnState) {
			plans[index].PendingGoals = append(plans[index].PendingGoals, watch)
		}
	}
	stat, err = os.Stat(w.registry.path)
	if err != nil {
		return nil, err
	}
	if !unresolvedLauncherWatch {
		w.registryCursor = hostedTurnRegistryCursor{
			Size:    stat.Size(),
			ModTime: stat.ModTime(),
			Plans:   plans,
		}
	}
	return plans, nil
}

func (w *hostedTurnWatcher) pollTranscript(transcriptPath string) ([]hostedTurnTranscriptResult, error) {
	cursor := w.files[transcriptPath]
	stat, err := os.Stat(transcriptPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if stat.Size() == cursor.Size && stat.ModTime().Equal(cursor.ModTime) {
		return nil, nil
	}
	if cursor.Offset > stat.Size() {
		cursor.Offset = 0
	}

	file, err := os.Open(transcriptPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(cursor.Offset, io.SeekStart); err != nil {
		return nil, err
	}

	results := []hostedTurnTranscriptResult{}
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			cursor.Offset += int64(len(line))
			if len(line) > hostedTurnTranscriptMaxLine {
				return nil, fmt.Errorf("line exceeds %d bytes", hostedTurnTranscriptMaxLine)
			}
			result, ok, parseErr := parseHostedTurnTranscriptLine(line)
			if parseErr != nil {
				return nil, parseErr
			}
			if ok {
				result.TranscriptOffset = cursor.Offset
				results = append(results, result)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	cursor.Size = stat.Size()
	cursor.ModTime = stat.ModTime()
	w.files[transcriptPath] = cursor
	return results, nil
}

func parseHostedTurnTranscriptLine(line string) (hostedTurnTranscriptResult, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return hostedTurnTranscriptResult{}, false, nil
	}
	var event struct {
		Type    string `json:"type"`
		TurnID  string `json:"turn_id"`
		Status  string `json:"status"`
		Payload struct {
			Type             string          `json:"type"`
			ThreadID         string          `json:"threadId"`
			TurnID           string          `json:"turn_id"`
			LastAgentMessage json.RawMessage `json:"last_agent_message"`
			Goal             struct {
				Status string `json:"status"`
			} `json:"goal"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return hostedTurnTranscriptResult{}, false, err
	}
	switch event.Type {
	case codexTranscriptEventMsg:
		if event.Payload.Type == codexTranscriptGoalUpdated && event.Payload.ThreadID != "" {
			switch event.Payload.Goal.Status {
			case codexTranscriptGoalActive, codexTranscriptGoalPaused, codexTranscriptGoalComplete:
				return hostedTurnTranscriptResult{GoalThreadID: event.Payload.ThreadID, GoalStatus: event.Payload.Goal.Status}, true, nil
			}
		}
		if event.Payload.TurnID == "" {
			return hostedTurnTranscriptResult{}, false, nil
		}
		if event.Payload.Type == codexTranscriptTaskStarted {
			return hostedTurnTranscriptResult{TurnID: event.Payload.TurnID, State: HostedTurnStateRunning}, true, nil
		}
		if event.Payload.Type == codexTranscriptTurnAborted {
			return hostedTurnTranscriptResult{TurnID: event.Payload.TurnID, State: HostedTurnStateInterrupted, Reason: hostedTurnInterruptedReason}, true, nil
		}
		if event.Payload.Type != codexTranscriptTaskComplete {
			return hostedTurnTranscriptResult{}, false, nil
		}
		result := hostedTurnTranscriptResult{TurnID: event.Payload.TurnID, State: HostedTurnStateDone}
		lastAgentMessage := strings.TrimSpace(string(event.Payload.LastAgentMessage))
		if lastAgentMessage == "" || lastAgentMessage == "null" {
			result.State = HostedTurnStateFailed
			result.Reason = hostedTurnCodexFailureReason
		}
		return result, true, nil
	case codexTranscriptTurnCompleted:
		if event.TurnID == "" {
			return hostedTurnTranscriptResult{}, false, nil
		}
		result := hostedTurnTranscriptResult{TurnID: event.TurnID, State: HostedTurnStateDone}
		if event.Status == codexTranscriptInterrupted {
			result.State = HostedTurnStateInterrupted
			result.Reason = hostedTurnInterruptedReason
		}
		return result, true, nil
	case codexTranscriptTurnFailed:
		if event.TurnID == "" {
			return hostedTurnTranscriptResult{}, false, nil
		}
		return hostedTurnTranscriptResult{TurnID: event.TurnID, State: HostedTurnStateFailed, Reason: hostedTurnCodexFailureReason}, true, nil
	default:
		return hostedTurnTranscriptResult{}, false, nil
	}
}
