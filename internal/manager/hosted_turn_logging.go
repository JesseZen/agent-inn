package manager

import (
	"log/slog"

	"github.com/jesse/agent-inn/internal/logging"
)

const (
	hostedTurnPollCategory            = "poll"
	hostedTurnTranscriptReadCategory  = "transcript_read"
	hostedTurnTranscriptParseCategory = "transcript_parse"
	hostedTurnRegistryWriteCategory   = "registry_write"
	hostedTurnProjectionCategory      = "tmux_projection"
	hostedTurnReconciliationCategory  = "snapshot_reconciliation"
)

type hostedTurnPollFailure struct {
	Category  string
	Path      string
	Position  int64
	SessionID string
	Err       error
}

func (e hostedTurnPollFailure) Error() string {
	if e.Err == nil {
		return e.Category
	}
	return e.Err.Error()
}

func (e hostedTurnPollFailure) Unwrap() error { return e.Err }

func logHostedTurnPollErrors(logger *slog.Logger, err error) {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			logHostedTurnPollErrors(logger, child)
		}
		return
	}
	if failure, ok := err.(hostedTurnPollFailure); ok {
		attrs := []any{"category", failure.Category}
		if failure.Path != "" {
			attrs = append(attrs, slog.String("path", failure.Path))
		}
		if failure.Position > 0 {
			attrs = append(attrs, slog.Int64("position", failure.Position))
		}
		if failure.SessionID != "" {
			attrs = append(attrs, slog.String("session_id", failure.SessionID))
		}
		logger.Warn(logging.EventHostedTurnPoll, attrs...)
		return
	}
	logger.Warn(logging.EventHostedTurnPoll, "category", hostedTurnPollCategory)
}

func hostedTurnPollFailureWith(category string, path string, position int64, sessionID string, err error) error {
	return hostedTurnPollFailure{Category: category, Path: path, Position: position, SessionID: sessionID, Err: err}
}
