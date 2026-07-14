package manager

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jesse/agent-inn/internal/constants"
)

const (
	hostedTurnTranscriptMaxLine     = 10 * 1024 * 1024
	hostedTurnInterruptedReason     = constants.HostedTurnReasonUserInterrupt
	hostedTurnCodexFailureReason    = constants.HostedTurnReasonCodexTaskFailed
	codexTranscriptEventMsg         = "event_msg"
	codexTranscriptTaskStarted      = "task_started"
	codexTranscriptTaskComplete     = "task_complete"
	codexTranscriptTurnAborted      = "turn_aborted"
	codexTranscriptTurnCompleted    = "turn.completed"
	codexTranscriptTurnFailed       = "turn.failed"
	codexTranscriptInterrupted      = "interrupted"
	codexTranscriptGoalUpdated      = "thread_goal_updated"
	codexTranscriptGoalActive       = "active"
	codexTranscriptGoalPaused       = "paused"
	codexTranscriptGoalComplete     = "complete"
	codexTranscriptResponseItem     = "response_item"
	codexTranscriptFunctionCall     = "function_call"
	codexTranscriptFunctionOutput   = "function_call_output"
	codexTranscriptRequestUserInput = "request_user_input"
)

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

type hostedTurnTranscriptLine struct {
	Data   string
	Offset int64
}

type hostedTurnTranscriptParseFailure struct {
	Offset int64
	Err    error
}

func (e hostedTurnTranscriptParseFailure) Error() string { return e.Err.Error() }

func (e hostedTurnTranscriptParseFailure) Unwrap() error { return e.Err }

type hostedTurnTranscriptReduction struct {
	Lifecycle         hostedTurnTranscriptResult
	LifecycleObserved bool
	InputRequestID    string
	FinalOffset       int64
	InputObserved     bool
	InputChanged      bool
}

func reduceHostedTurnTranscript(kind string, currentRequestID string, lines []hostedTurnTranscriptLine) (hostedTurnTranscriptReduction, error) {
	reduction := hostedTurnTranscriptReduction{InputRequestID: currentRequestID}
	if len(lines) > 0 {
		reduction.FinalOffset = lines[len(lines)-1].Offset
	}
	if kind != HostedTurnWatchKindCodex && kind != HostedTurnWatchKindCodexGoal && kind != HostedTurnWatchKindCodexGoalPaused {
		return reduction, nil
	}

	for _, line := range lines {
		result, ok, err := parseHostedTurnTranscriptLine(line.Data)
		if err != nil {
			return hostedTurnTranscriptReduction{}, hostedTurnTranscriptParseFailure{Offset: line.Offset, Err: fmt.Errorf("invalid hosted turn transcript JSON: %w", err)}
		}
		if ok {
			result.TranscriptOffset = line.Offset
			reduction.Lifecycle = result
			reduction.LifecycleObserved = true
		}

		var envelope struct {
			Type    string `json:"type"`
			Payload struct {
				Type   string `json:"type"`
				Name   string `json:"name"`
				CallID string `json:"call_id"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line.Data), &envelope); err != nil {
			return hostedTurnTranscriptReduction{}, hostedTurnTranscriptParseFailure{Offset: line.Offset, Err: fmt.Errorf("invalid hosted turn transcript JSON: %w", err)}
		}
		if envelope.Type != codexTranscriptResponseItem || envelope.Payload.CallID == "" {
			continue
		}
		switch envelope.Payload.Type {
		case codexTranscriptFunctionCall:
			if envelope.Payload.Name != codexTranscriptRequestUserInput {
				continue
			}
			if reduction.InputRequestID == "" {
				reduction.InputRequestID = envelope.Payload.CallID
				reduction.InputObserved = true
			}
		case codexTranscriptFunctionOutput:
			if reduction.InputRequestID != "" && envelope.Payload.CallID == reduction.InputRequestID {
				reduction.InputRequestID = ""
				reduction.InputObserved = true
			}
		}
	}
	reduction.InputChanged = reduction.InputRequestID != currentRequestID
	return reduction, nil
}

func (w *hostedTurnWatcher) pollTranscript(transcriptPath string) ([]hostedTurnTranscriptResult, []hostedTurnTranscriptLine, hostedTurnTranscriptCursor, bool, error) {
	cursor := w.files[transcriptPath]
	stat, err := os.Stat(transcriptPath)
	if os.IsNotExist(err) {
		return nil, nil, cursor, false, nil
	}
	if err != nil {
		return nil, nil, cursor, false, err
	}
	if stat.Size() == cursor.Size && stat.ModTime().Equal(cursor.ModTime) {
		return nil, nil, cursor, false, nil
	}
	if cursor.Offset > stat.Size() {
		cursor.Offset = 0
	}

	file, err := os.Open(transcriptPath)
	if err != nil {
		return nil, nil, cursor, false, err
	}
	defer file.Close()
	if _, err := file.Seek(cursor.Offset, io.SeekStart); err != nil {
		return nil, nil, cursor, false, err
	}

	results := []hostedTurnTranscriptResult{}
	lines := []hostedTurnTranscriptLine{}
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			cursor.Offset += int64(len(line))
			if len(line) > hostedTurnTranscriptMaxLine {
				return nil, nil, hostedTurnTranscriptCursor{}, false, fmt.Errorf("line exceeds %d bytes", hostedTurnTranscriptMaxLine)
			}
			lines = append(lines, hostedTurnTranscriptLine{Data: line, Offset: cursor.Offset})
			result, ok, parseErr := parseHostedTurnTranscriptLine(line)
			if parseErr != nil {
				return nil, nil, hostedTurnTranscriptCursor{}, false, hostedTurnTranscriptParseFailure{Offset: cursor.Offset, Err: parseErr}
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
			return nil, nil, hostedTurnTranscriptCursor{}, false, err
		}
	}
	cursor.Size = stat.Size()
	cursor.ModTime = stat.ModTime()
	return results, lines, cursor, true, nil
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
