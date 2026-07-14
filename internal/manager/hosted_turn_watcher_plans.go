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
)

func (w *hostedTurnWatcher) watchPlans() ([]hostedTurnWatchPlan, error) {
	missesExpired := w.expireLauncherMisses()
	initialStat, err := os.Stat(w.registry.path)
	if os.IsNotExist(err) {
		return []hostedTurnWatchPlan{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !missesExpired && initialStat.Size() == w.registryCursor.Size && initialStat.ModTime().Equal(w.registryCursor.ModTime) {
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
					continue
				}
				pattern := filepath.Join(expandHomePath(codexTranscriptSessionsDir), "*", "*", "*", "*"+watch.LauncherSessionID+".jsonl")
				matches, err := w.globTranscripts(pattern)
				if err != nil {
					return nil, err
				}
				if len(matches) == 0 {
					if watch.GoalCandidate {
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
	finalStat, err := os.Stat(w.registry.path)
	if err != nil {
		return nil, err
	}
	registryUnchanged := initialStat.Size() == finalStat.Size() && initialStat.ModTime().Equal(finalStat.ModTime())
	if !unresolvedLauncherWatch && registryUnchanged {
		w.registryCursor = hostedTurnRegistryCursor{
			Size:    finalStat.Size(),
			ModTime: finalStat.ModTime(),
			Plans:   plans,
		}
	}
	return plans, nil
}
