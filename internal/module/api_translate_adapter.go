package module

import (
	"github.com/jesse/agent-inn/internal/protocol"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
)

func responsesBodyToProtocolRequest(body map[string]any) protocol.Request {
	req := protocol.Request{
		Protocol:        appruntime.ProtocolResponses,
		Model:           body["model"],
		Stream:          body["stream"],
		ToolChoice:      body["tool_choice"],
		MaxOutputTokens: body["max_output_tokens"],
		Input:           translateInputToMessages(body),
		Tools:           translateTools(body["tools"]),
	}
	if metadata, ok := body["metadata"].(map[string]any); ok {
		req.Metadata = metadata
	}
	for _, key := range []string{"temperature", "top_p"} {
		if value, ok := body[key]; ok {
			if req.Extra == nil {
				req.Extra = map[string]any{}
			}
			req.Extra[key] = value
		}
	}
	return req
}

func protocolRequestToChatBody(req protocol.Request) map[string]any {
	out := map[string]any{}

	if req.Model != nil {
		out["model"] = req.Model
	}
	if req.Stream != nil {
		out["stream"] = req.Stream
	}
	for _, key := range []string{"temperature", "top_p"} {
		if value, ok := req.Extra[key]; ok {
			out[key] = value
		}
	}
	if req.MaxOutputTokens != nil {
		out["max_tokens"] = req.MaxOutputTokens
	}
	if req.Metadata != nil {
		if userID, ok := req.Metadata["user_id"]; ok {
			out["user"] = userID
		}
	}

	messages := protocolMessagesToChat(req.Input)
	if len(messages) > 0 {
		out["messages"] = messages
	}
	if tools := protocolToolsToChat(req.Tools); len(tools) > 0 {
		out["tools"] = tools
	}
	if toolChoice, ok := translateToolChoice(req.ToolChoice); ok {
		out["tool_choice"] = toolChoice
	}
	return out
}

func chatChunkToProtocolEvents(chunk chatCompletionChunk) []protocol.StreamEvent {
	var events []protocol.StreamEvent
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			events = append(events, protocol.StreamEvent{
				Protocol: appruntime.ProtocolChatCompletions,
				Kind:     protocol.StreamEventTextDelta,
				Text:     choice.Delta.Content,
			})
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			events = append(events, protocol.StreamEvent{
				Protocol: appruntime.ProtocolChatCompletions,
				Kind:     protocol.StreamEventToolCallDelta,
				ToolCall: &protocol.ToolCall{
					ID:        toolCall.ID,
					Name:      toolCall.Function.Name,
					Arguments: toolCall.Function.Arguments,
				},
			})
		}
	}
	return events
}
