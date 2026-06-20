package module

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestAPITranslateResponseEmitsResponsesTextEvents(t *testing.T) {
	upstream := &ProxyResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			chatChunk(`{"content":"Hello"}`, ""),
			chatChunk(`{"content":" world"}`, ""),
			chatChunk(`{}`, "stop"),
			"data: [DONE]\n\n",
		}, ""))),
		ContentType: "text/event-stream",
	}
	m := NewAPITranslate(ModuleConfig{Enabled: true, Params: map[string]any{"api_format": "chat_completions"}})

	wrapped, err := m.WrapResponse(context.Background(), &ProxyRequest{Path: "/v1/responses"}, upstream)
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(wrapped.Body)
	if err != nil {
		t.Fatal(err)
	}
	events := parseResponseSSE(t, out)
	types := eventTypes(events)

	for _, want := range []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
	} {
		if !contains(types, want) {
			t.Fatalf("missing event %s in %#v", want, types)
		}
	}

	deltas := eventsByType(events, "response.output_text.delta")
	if len(deltas) != 2 || deltas[0]["delta"] != "Hello" || deltas[1]["delta"] != " world" {
		t.Fatalf("bad deltas: %#v", deltas)
	}
	completed := eventsByType(events, "response.completed")
	response := completed[0]["response"].(map[string]any)
	output := response["output"].([]any)
	message := output[0].(map[string]any)
	content := message["content"].([]any)
	textPart := content[0].(map[string]any)
	if textPart["text"] != "Hello world" {
		t.Fatalf("bad completed output: %#v", response)
	}
}

func TestAPITranslateResponseEmitsFunctionCallEvents(t *testing.T) {
	upstream := &ProxyResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			chatToolChunk(0, "call_abc", "get_weather", ""),
			chatToolChunk(0, "", "", `{"loc`),
			chatToolChunk(0, "", "", `ation":"NYC"}`),
			chatChunk(`{}`, "tool_calls"),
			"data: [DONE]\n\n",
		}, ""))),
		ContentType: "text/event-stream",
	}
	m := NewAPITranslate(ModuleConfig{Enabled: true, Params: map[string]any{"api_format": "chat_completions"}})

	wrapped, err := m.WrapResponse(context.Background(), &ProxyRequest{Path: "/v1/responses"}, upstream)
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(wrapped.Body)
	if err != nil {
		t.Fatal(err)
	}
	events := parseResponseSSE(t, out)
	types := eventTypes(events)
	for _, want := range []string{
		"response.output_item.added",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.completed",
	} {
		if !contains(types, want) {
			t.Fatalf("missing event %s in %#v", want, types)
		}
	}
	completed := eventsByType(events, "response.completed")
	response := completed[0]["response"].(map[string]any)
	output := response["output"].([]any)
	call := output[0].(map[string]any)
	if call["type"] != "function_call" || call["name"] != "get_weather" || call["arguments"] != `{"location":"NYC"}` {
		t.Fatalf("bad function call output: %#v", call)
	}
}

func TestAPITranslateResponsePassesThroughNonOK(t *testing.T) {
	upstream := &ProxyResponse{
		StatusCode:  429,
		Headers:     http.Header{"Content-Type": []string{"application/json"}},
		Body:        io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
		ContentType: "application/json",
	}
	m := NewAPITranslate(ModuleConfig{Enabled: true, Params: map[string]any{"api_format": "chat_completions"}})

	wrapped, err := m.WrapResponse(context.Background(), &ProxyRequest{Path: "/v1/responses"}, upstream)
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(wrapped.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"error":"rate limited"}` {
		t.Fatalf("non-OK response was modified: %s", out)
	}
}

func TestAPITranslateResponseConvertsNonStreamingJSONToResponseEvents(t *testing.T) {
	upstream := &ProxyResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl-json",
			"model":"gpt-4o",
			"choices":[{"message":{"role":"assistant","content":"Hello JSON"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":2}
		}`)),
		ContentType: "application/json",
	}
	m := NewAPITranslate(ModuleConfig{Enabled: true, Params: map[string]any{"api_format": "chat_completions"}})

	wrapped, err := m.WrapResponse(context.Background(), &ProxyRequest{Path: "/v1/responses"}, upstream)
	if err != nil {
		t.Fatal(err)
	}
	if !isEventStream(wrapped.Headers.Get("Content-Type")) {
		t.Fatalf("expected SSE response, got %#v", wrapped.Headers)
	}
	out, err := io.ReadAll(wrapped.Body)
	if err != nil {
		t.Fatal(err)
	}
	events := parseResponseSSE(t, out)
	types := eventTypes(events)
	for _, want := range []string{"response.created", "response.output_text.delta", "response.output_text.done", "response.completed"} {
		if !contains(types, want) {
			t.Fatalf("missing event %s in %#v\n%s", want, types, out)
		}
	}
	completed := eventsByType(events, "response.completed")
	response := completed[0]["response"].(map[string]any)
	output := response["output"].([]any)
	message := output[0].(map[string]any)
	content := message["content"].([]any)
	textPart := content[0].(map[string]any)
	if textPart["text"] != "Hello JSON" {
		t.Fatalf("bad converted output: %#v", response)
	}
	usage := response["usage"].(map[string]any)
	if usage["input_tokens"] != float64(3) || usage["output_tokens"] != float64(2) {
		t.Fatalf("bad usage mapping: %#v", usage)
	}
}

func chatChunk(delta string, finishReason string) string {
	finish := "null"
	if finishReason != "" {
		finish = `"` + finishReason + `"`
	}
	return `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":` + delta + `,"finish_reason":` + finish + `}]}` + "\n\n"
}

func chatToolChunk(index int, id string, name string, args string) string {
	fields := []string{`"index":` + strconv.Itoa(index)}
	if id != "" {
		fields = append(fields, `"id":"`+id+`"`, `"type":"function"`)
	}
	functionFields := []string{}
	if name != "" {
		functionFields = append(functionFields, `"name":"`+name+`"`)
	}
	if args != "" || name != "" {
		functionFields = append(functionFields, `"arguments":`+quoteJSON(args))
	}
	if len(functionFields) > 0 {
		fields = append(fields, `"function":{`+strings.Join(functionFields, ",")+`}`)
	}
	return `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{` + strings.Join(fields, ",") + `}]},"finish_reason":null}]}` + "\n\n"
}

func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

type parsedEvent struct {
	event string
	data  map[string]any
}

func parseResponseSSE(t *testing.T, raw []byte) []parsedEvent {
	t.Helper()
	var parser SSEParser
	frames, err := parser.Push(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	var events []parsedEvent
	for _, frame := range frames {
		if frame.Data == "" || frame.Done {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(frame.Data), &data); err != nil {
			t.Fatalf("bad JSON for %s: %v\n%s", frame.Event, err, frame.Data)
		}
		events = append(events, parsedEvent{event: frame.Event, data: data})
	}
	return events
}

func eventTypes(events []parsedEvent) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.event)
	}
	return out
}

func eventsByType(events []parsedEvent, eventType string) []map[string]any {
	var out []map[string]any
	for _, event := range events {
		if event.event == eventType {
			out = append(out, event.data)
		}
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
