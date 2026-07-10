package worker

import (
	"strings"

	"github.com/jesse/agent-inn/internal/module"
)

type UsageTokens struct {
	Known            bool  `json:"usage_known"`
	InputTokens      int64 `json:"input_tokens,omitempty"`
	OutputTokens     int64 `json:"output_tokens,omitempty"`
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int64 `json:"reasoning_tokens,omitempty"`
	TotalTokens      int64 `json:"total_tokens,omitempty"`
}

type UsageObserver struct {
	contentType string
	parser      module.SSEParser
	usage       UsageTokens
	model       string
	jsonScanner *responseJSONScanner
}

type responseUsageMetadata struct {
	Usage UsageTokens
	Model string
}

func NewUsageObserver(contentType string) *UsageObserver {
	observer := &UsageObserver{contentType: strings.ToLower(contentType)}
	if strings.Contains(observer.contentType, "json") {
		observer.jsonScanner = &responseJSONScanner{}
	}
	return observer
}

func (u *UsageObserver) Observe(chunk []byte) {
	if strings.Contains(u.contentType, "text/event-stream") {
		events, _ := u.parser.Push(chunk, false)
		for _, event := range events {
			u.processSSEEvent(event)
		}
	}
	if u.jsonScanner != nil {
		u.jsonScanner.Write(chunk)
	}
}

func (u *UsageObserver) Finish() UsageTokens {
	if strings.Contains(u.contentType, "text/event-stream") {
		events, _ := u.parser.Push(nil, true)
		for _, event := range events {
			u.processSSEEvent(event)
		}
	}
	if u.jsonScanner != nil {
		metadata := u.jsonScanner.Finish()
		u.usage = metadata.Usage
		u.model = metadata.Model
		u.jsonScanner = nil
	}
	return u.usage
}

func (u *UsageObserver) Model() string {
	return u.model
}

func (u *UsageObserver) processSSEEvent(event module.SSEEvent) {
	if event.Done {
		return
	}
	metadata := extractUsageMetadataFromJSON([]byte(event.Data))
	if metadata.Usage.Known {
		if event.Event == "message_delta" && u.usage.Known {
			u.usage.InputTokens += metadata.Usage.InputTokens
			u.usage.OutputTokens += metadata.Usage.OutputTokens
			u.usage.CacheReadTokens += metadata.Usage.CacheReadTokens
			u.usage.CacheWriteTokens += metadata.Usage.CacheWriteTokens
			u.usage.ReasoningTokens += metadata.Usage.ReasoningTokens
			u.usage.TotalTokens += metadata.Usage.TotalTokens
		} else {
			u.usage = metadata.Usage
		}
	}
	if metadata.Model != "" {
		u.model = metadata.Model
	}
}

func ExtractUsageFromJSON(data []byte) UsageTokens {
	return extractUsageMetadataFromJSON(data).Usage
}

func extractUsageMetadataFromJSON(data []byte) responseUsageMetadata {
	scanner := &responseJSONScanner{}
	scanner.Write(data)
	return scanner.Finish()
}
