package protocol

import appruntime "github.com/jesse/agent-inn/internal/runtime"

type Request struct {
	Protocol        appruntime.ProtocolKind
	Model           any
	Stream          any
	Input           []Message
	Tools           []Tool
	ToolChoice      any
	MaxOutputTokens any
	Metadata        map[string]any
	Extra           map[string]any
}

type Message struct {
	Role       string
	Content    any
	ToolCalls  []ToolCall
	ToolCallID string
}

type ContentPart struct {
	Type     string
	Text     string
	ImageURL string
	Payload  map[string]any
}

type Tool struct {
	Name        any
	Description string
	Parameters  any
}

type ToolCall struct {
	ID        string
	Name      any
	Arguments string
}

type StreamEventKind string

const (
	StreamEventTextDelta     StreamEventKind = "text_delta"
	StreamEventToolCallDelta StreamEventKind = "tool_call_delta"
	StreamEventCompleted     StreamEventKind = "completed"
	StreamEventFailed        StreamEventKind = "failed"
)

type StreamEvent struct {
	Protocol appruntime.ProtocolKind
	Kind     StreamEventKind
	Text     string
	ToolCall *ToolCall
	Payload  map[string]any
}
