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
	codexTranscriptSessionsDir   = "~/.codex/sessions"
	codexTranscriptEventMsg      = "event_msg"
	codexTranscriptTaskStarted   = "task_started"
	codexTranscriptTaskComplete  = "task_complete"
	codexTranscriptTurnAborted   = "turn_aborted"
	codexTranscriptTurnCompleted = "turn.completed"
	codexTranscriptTurnFailed    = "turn.failed"
	codexTranscriptInterrupted   = "interrupted"
)

type hostedTurnWatcher struct {
	settings       config.Settings
	registry       *HostedSessionRegistry
	registryCursor hostedTurnRegistryCursor
	runner         hostedTMuxRunner
	files          map[string]hostedTurnTranscriptCursor
	launcherPaths  map[string]string
}

type hostedTurnRegistryCursor struct {
	Size    int64
	ModTime time.Time
	Plans   []hostedTurnWatchPlan
}

type hostedTurnWatchPlan struct {
	TranscriptPath string
	TurnsByID      map[string][]HostedTurnWatch
}

type hostedTurnTranscriptCursor struct {
	Offset  int64
	Size    int64
	ModTime time.Time
}

type hostedTurnTranscriptResult struct {
	TurnID string
	State  string
	Reason string
}

func (m *Manager) StartHostedTurnWatcher(interval time.Duration) func() {
	if interval <= 0 {
		interval = hostedTurnWatcherInterval
	}
	done := make(chan struct{})
	watcher := newHostedTurnWatcher(m.config.Settings, m.hostedSessions, hostedTMuxRunnerFactory())
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
		settings:      settings,
		registry:      registry,
		runner:        runner,
		files:         map[string]hostedTurnTranscriptCursor{},
		launcherPaths: map[string]string{},
	}
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
		for _, result := range results {
			for _, watch := range plan.TurnsByID[result.TurnID] {
				session, ok, err := w.registry.CompleteWatchedTurn(watch.SessionID, watch.TurnGeneration, result.State, result.Reason)
				if err != nil {
					return err
				}
				if !ok || session.TmuxWindowID == "" {
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
				if _, err := w.runner.Run(TmuxHostedTurnStatusCommandForRecord(w.settings, session)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (w *hostedTurnWatcher) watchPlans() ([]hostedTurnWatchPlan, error) {
	stat, err := os.Stat(w.registry.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && stat.Size() == w.registryCursor.Size && stat.ModTime().Equal(w.registryCursor.ModTime) {
		return w.registryCursor.Plans, nil
	}

	watches, err := w.registry.WatchedTurns()
	if err != nil {
		return nil, err
	}
	plansByPath := map[string]int{}
	plans := []hostedTurnWatchPlan{}
	unresolvedLauncherWatch := false
	for _, watch := range watches {
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
				pattern := filepath.Join(expandHomePath(codexTranscriptSessionsDir), "*", "*", "*", "*"+watch.LauncherSessionID+".jsonl")
				matches, err := filepath.Glob(pattern)
				if err != nil {
					return nil, err
				}
				if len(matches) == 0 {
					unresolvedLauncherWatch = true
					continue
				}
				sort.Strings(matches)
				transcriptPath = matches[len(matches)-1]
				w.launcherPaths[watch.LauncherSessionID] = transcriptPath
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
			TurnID           string          `json:"turn_id"`
			LastAgentMessage json.RawMessage `json:"last_agent_message"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return hostedTurnTranscriptResult{}, false, err
	}
	switch event.Type {
	case codexTranscriptEventMsg:
		if event.Payload.TurnID == "" {
			return hostedTurnTranscriptResult{}, false, nil
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
