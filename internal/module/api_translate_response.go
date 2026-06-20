package module

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (m *APITranslate) WrapResponse(ctx context.Context, req *ProxyRequest, upstream *ProxyResponse) (*ProxyResponse, error) {
	if !m.config.Enabled || m.apiFormat() != chatCompletionsFormat {
		return upstream, nil
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return upstream, nil
	}
	if isJSONContentType(upstream.ContentType, upstream.Headers.Get("Content-Type")) {
		return wrapNonStreamingChatCompletion(upstream)
	}
	if !isEventStream(upstream.ContentType, upstream.Headers.Get("Content-Type")) {
		return upstream, nil
	}

	next := *upstream
	next.Headers = upstream.Headers.Clone()
	next.Headers.Set("Content-Type", "text/event-stream")
	next.Body = newChatToResponsesTranslator(upstream.Body)
	next.ContentType = "text/event-stream"
	return &next, nil
}

func wrapNonStreamingChatCompletion(upstream *ProxyResponse) (*ProxyResponse, error) {
	data, err := io.ReadAll(upstream.Body)
	_ = upstream.Body.Close()
	if err != nil {
		return nil, err
	}
	var completion chatCompletionResponse
	if err := json.Unmarshal(data, &completion); err != nil {
		return nil, err
	}
	body, err := buildNonStreamingResponseEvents(completion)
	if err != nil {
		return nil, err
	}
	next := *upstream
	next.Headers = upstream.Headers.Clone()
	next.Headers.Set("Content-Type", "text/event-stream")
	next.Headers.Del("Content-Length")
	next.Body = io.NopCloser(bytes.NewReader(body))
	next.ContentType = "text/event-stream"
	return &next, nil
}

type chatCompletionResponse struct {
	ID      string                 `json:"id"`
	Model   string                 `json:"model"`
	Choices []chatCompletionChoice `json:"choices"`
	Usage   chatCompletionUsage    `json:"usage"`
}

type chatCompletionChoice struct {
	Message      chatCompletionMessage `json:"message"`
	FinishReason string                `json:"finish_reason"`
}

type chatCompletionMessage struct {
	Content   string         `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls"`
}

type chatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func buildNonStreamingResponseEvents(completion chatCompletionResponse) ([]byte, error) {
	var sse strings.Builder
	writeSyntheticChunk := func(delta any, finishReason string) error {
		finish := any(nil)
		if finishReason != "" {
			finish = finishReason
		}
		chunk := map[string]any{
			"id":    completion.ID,
			"model": completion.Model,
			"choices": []any{map[string]any{
				"delta":         delta,
				"finish_reason": finish,
			}},
		}
		encoded, err := json.Marshal(chunk)
		if err != nil {
			return err
		}
		sse.WriteString("data: ")
		sse.Write(encoded)
		sse.WriteString("\n\n")
		return nil
	}

	finishReason := "stop"
	if len(completion.Choices) > 0 {
		choice := completion.Choices[0]
		if choice.Message.Content != "" {
			if err := writeSyntheticChunk(map[string]any{"content": choice.Message.Content}, ""); err != nil {
				return nil, err
			}
		}
		for _, toolCall := range choice.Message.ToolCalls {
			if err := writeSyntheticChunk(map[string]any{"tool_calls": []chatToolCall{toolCall}}, ""); err != nil {
				return nil, err
			}
		}
		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}
	}
	if err := writeSyntheticChunk(map[string]any{}, finishReason); err != nil {
		return nil, err
	}
	sse.WriteString("data: [DONE]\n\n")

	translator := newChatToResponsesTranslator(io.NopCloser(strings.NewReader(sse.String())))
	out, err := io.ReadAll(translator)
	if err != nil {
		return nil, err
	}
	if completion.Usage.PromptTokens != 0 || completion.Usage.CompletionTokens != 0 {
		out = injectUsageIntoCompletedEvent(out, completion.Usage)
	}
	return out, nil
}

func injectUsageIntoCompletedEvent(raw []byte, usage chatCompletionUsage) []byte {
	var parser SSEParser
	events, err := parser.Push(raw, true)
	if err != nil {
		return raw
	}
	var out bytes.Buffer
	for _, event := range events {
		if event.Data == "" || event.Done {
			continue
		}
		data := event.Data
		if event.Event == "response.completed" {
			var payload map[string]any
			if err := json.Unmarshal([]byte(event.Data), &payload); err == nil {
				if response, ok := payload["response"].(map[string]any); ok {
					response["usage"] = map[string]any{
						"input_tokens":  usage.PromptTokens,
						"output_tokens": usage.CompletionTokens,
					}
					if encoded, err := json.Marshal(payload); err == nil {
						data = string(encoded)
					}
				}
			}
		}
		out.WriteString("event: ")
		out.WriteString(event.Event)
		out.WriteString("\n")
		out.WriteString("data: ")
		out.WriteString(data)
		out.WriteString("\n\n")
	}
	return out.Bytes()
}

func isEventStream(values ...string) bool {
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "text/event-stream" || strings.HasPrefix(value, "text/event-stream;") {
			return true
		}
	}
	return false
}

type chatToResponsesTranslator struct {
	source io.ReadCloser
	parser SSEParser
	buf    bytes.Buffer
	state  responseTranslationState
	done   bool
}

type responseTranslationState struct {
	started          bool
	completed        bool
	outputIndex      int
	currentType      string
	messageID        string
	text             strings.Builder
	callItemID       string
	callID           string
	callName         string
	callArguments    strings.Builder
	finishReason     string
	chatCompletionID string
	model            string
	output           []map[string]any
}

func newChatToResponsesTranslator(source io.ReadCloser) *chatToResponsesTranslator {
	return &chatToResponsesTranslator{source: source}
}

func (t *chatToResponsesTranslator) Read(p []byte) (int, error) {
	if t.buf.Len() > 0 {
		return t.buf.Read(p)
	}
	if t.done {
		return 0, io.EOF
	}

	scratch := make([]byte, 32*1024)
	for t.buf.Len() == 0 && !t.done {
		n, err := t.source.Read(scratch)
		if n > 0 {
			if processErr := t.processRaw(scratch[:n], false); processErr != nil {
				return 0, processErr
			}
		}
		if err == io.EOF {
			if processErr := t.processRaw(nil, true); processErr != nil {
				return 0, processErr
			}
			t.complete()
			t.done = true
			break
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, nil
		}
	}

	if t.buf.Len() > 0 {
		return t.buf.Read(p)
	}
	return 0, io.EOF
}

func (t *chatToResponsesTranslator) Close() error {
	return t.source.Close()
}

func (t *chatToResponsesTranslator) processRaw(chunk []byte, eof bool) error {
	events, err := t.parser.Push(chunk, eof)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Done {
			t.complete()
			t.done = true
			return nil
		}
		if event.Data == "" {
			continue
		}
		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
			continue
		}
		t.processChunk(chunk)
	}
	return nil
}

type chatCompletionChunk struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   any          `json:"usage,omitempty"`
}

type chatChoice struct {
	Delta        chatDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}

type chatDelta struct {
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content"`
	ToolCalls        []chatToolCall `json:"tool_calls"`
}

type chatToolCall struct {
	Index    int              `json:"index"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

type chatToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (t *chatToResponsesTranslator) processChunk(chunk chatCompletionChunk) {
	if chunk.ID != "" {
		t.state.chatCompletionID = chunk.ID
	}
	if chunk.Model != "" {
		t.state.model = chunk.Model
	}
	t.ensureStarted()

	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil {
			t.state.finishReason = *choice.FinishReason
		}
		if choice.Delta.Content != "" {
			t.emitTextDelta(choice.Delta.Content)
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			t.emitToolCallDelta(toolCall)
		}
	}
}

func (t *chatToResponsesTranslator) ensureStarted() {
	if t.state.started {
		return
	}
	t.state.started = true
	response := t.baseResponse("in_progress")
	t.writeResponseEvent("response.created", map[string]any{"response": response})
	t.writeResponseEvent("response.in_progress", map[string]any{"response": response})
}

func (t *chatToResponsesTranslator) emitTextDelta(delta string) {
	if t.state.currentType != "message" {
		t.closeCurrentItem()
		t.openMessageItem()
	}
	t.state.text.WriteString(delta)
	t.writeResponseEvent("response.output_text.delta", map[string]any{
		"item_id":       t.state.messageID,
		"output_index":  t.state.outputIndex,
		"content_index": 0,
		"delta":         delta,
	})
}

func (t *chatToResponsesTranslator) openMessageItem() {
	t.state.currentType = "message"
	t.state.messageID = fmt.Sprintf("msg_%d", t.state.outputIndex+1)
	item := map[string]any{
		"id":      t.state.messageID,
		"type":    "message",
		"role":    "assistant",
		"status":  "in_progress",
		"content": []any{},
	}
	t.writeResponseEvent("response.output_item.added", map[string]any{
		"output_index": t.state.outputIndex,
		"item":         item,
	})
	t.writeResponseEvent("response.content_part.added", map[string]any{
		"item_id":       t.state.messageID,
		"output_index":  t.state.outputIndex,
		"content_index": 0,
		"part": map[string]any{
			"type": "output_text",
			"text": "",
		},
	})
}

func (t *chatToResponsesTranslator) emitToolCallDelta(toolCall chatToolCall) {
	if t.state.currentType != "function_call" || (toolCall.ID != "" && t.state.callID != "" && toolCall.ID != t.state.callID) {
		t.closeCurrentItem()
		t.openFunctionCallItem(toolCall)
	}
	if t.state.currentType != "function_call" {
		t.openFunctionCallItem(toolCall)
	}
	if toolCall.ID != "" {
		t.state.callID = toolCall.ID
	}
	if toolCall.Function.Name != "" {
		t.state.callName = toolCall.Function.Name
	}
	if toolCall.Function.Arguments != "" {
		t.state.callArguments.WriteString(toolCall.Function.Arguments)
		t.writeResponseEvent("response.function_call_arguments.delta", map[string]any{
			"item_id":      t.state.callItemID,
			"output_index": t.state.outputIndex,
			"delta":        toolCall.Function.Arguments,
		})
	}
}

func (t *chatToResponsesTranslator) openFunctionCallItem(toolCall chatToolCall) {
	t.state.currentType = "function_call"
	t.state.callItemID = fmt.Sprintf("fc_%d", t.state.outputIndex+1)
	t.state.callID = toolCall.ID
	t.state.callName = toolCall.Function.Name
	t.writeResponseEvent("response.output_item.added", map[string]any{
		"output_index": t.state.outputIndex,
		"item": map[string]any{
			"id":        t.state.callItemID,
			"type":      "function_call",
			"call_id":   t.state.callID,
			"name":      t.state.callName,
			"arguments": "",
			"status":    "in_progress",
		},
	})
}

func (t *chatToResponsesTranslator) closeCurrentItem() {
	switch t.state.currentType {
	case "message":
		text := t.state.text.String()
		t.writeResponseEvent("response.output_text.done", map[string]any{
			"item_id":       t.state.messageID,
			"output_index":  t.state.outputIndex,
			"content_index": 0,
			"text":          text,
		})
		t.writeResponseEvent("response.content_part.done", map[string]any{
			"item_id":       t.state.messageID,
			"output_index":  t.state.outputIndex,
			"content_index": 0,
			"part": map[string]any{
				"type": "output_text",
				"text": text,
			},
		})
		item := map[string]any{
			"id":     t.state.messageID,
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []any{map[string]any{
				"type": "output_text",
				"text": text,
			}},
		}
		t.writeResponseEvent("response.output_item.done", map[string]any{
			"output_index": t.state.outputIndex,
			"item":         item,
		})
		t.state.output = append(t.state.output, item)
		t.state.outputIndex++
		t.state.text.Reset()
		t.state.messageID = ""
	case "function_call":
		args := t.state.callArguments.String()
		t.writeResponseEvent("response.function_call_arguments.done", map[string]any{
			"item_id":      t.state.callItemID,
			"output_index": t.state.outputIndex,
			"arguments":    args,
		})
		item := map[string]any{
			"id":        t.state.callItemID,
			"type":      "function_call",
			"call_id":   t.state.callID,
			"name":      t.state.callName,
			"arguments": args,
			"status":    "completed",
		}
		t.writeResponseEvent("response.output_item.done", map[string]any{
			"output_index": t.state.outputIndex,
			"item":         item,
		})
		t.state.output = append(t.state.output, item)
		t.state.outputIndex++
		t.state.callArguments.Reset()
		t.state.callItemID = ""
		t.state.callID = ""
		t.state.callName = ""
	}
	t.state.currentType = ""
}

func (t *chatToResponsesTranslator) complete() {
	if t.state.completed {
		return
	}
	t.ensureStarted()
	t.closeCurrentItem()
	status := "completed"
	if t.state.finishReason == "length" || t.state.finishReason == "content_filter" {
		status = "incomplete"
	}
	t.writeResponseEvent("response.completed", map[string]any{
		"response": t.baseResponse(status),
	})
	t.state.completed = true
}

func (t *chatToResponsesTranslator) baseResponse(status string) map[string]any {
	id := t.state.chatCompletionID
	if id == "" {
		id = "resp_1"
	}
	return map[string]any{
		"id":     id,
		"object": "response",
		"status": status,
		"model":  t.state.model,
		"output": t.state.output,
	}
}

func (t *chatToResponsesTranslator) writeResponseEvent(eventType string, data map[string]any) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return
	}
	t.buf.WriteString("event: ")
	t.buf.WriteString(eventType)
	t.buf.WriteString("\n")
	t.buf.WriteString("data: ")
	t.buf.Write(encoded)
	t.buf.WriteString("\n\n")
}

var _ io.ReadCloser = (*chatToResponsesTranslator)(nil)
var _ = http.StatusOK
