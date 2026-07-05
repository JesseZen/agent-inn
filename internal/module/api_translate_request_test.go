package module

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/jesse/agent-inn/internal/protocol"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
)

func TestTranslateResponsesRequestToChatCompletions(t *testing.T) {
	req := &ProxyRequest{
		Method:      "POST",
		Path:        "/v1/responses",
		Headers:     http.Header{"Content-Type": []string{"application/json"}},
		Body:        []byte(`{"model":"gpt-4o","instructions":"Be friendly","input":"Say hello","stream":true,"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object"}}],"max_output_tokens":10,"metadata":{"user_id":"u1"}}`),
		ContentType: "application/json",
	}

	m := NewAPITranslate(ModuleConfig{Enabled: true, Params: map[string]any{"api_format": "chat_completions"}})
	if err := m.ProcessRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	if req.Path != "/v1/chat/completions" {
		t.Fatalf("unexpected path %s", req.Path)
	}
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["max_tokens"].(float64) != 10 {
		t.Fatalf("max_output_tokens was not mapped: %#v", body)
	}
	if body["user"] != "u1" {
		t.Fatalf("metadata.user_id was not mapped: %#v", body)
	}
	if req.Headers.Get("Accept") != "text/event-stream" || req.Headers.Get("Accept-Encoding") != "identity" {
		t.Fatalf("missing SSE headers: %#v", req.Headers)
	}

	messages := body["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("expected system and user messages, got %#v", messages)
	}
	system := messages[0].(map[string]any)
	user := messages[1].(map[string]any)
	if system["role"] != "system" || system["content"] != "Be friendly" {
		t.Fatalf("bad system message: %#v", system)
	}
	if user["role"] != "user" || user["content"] != "Say hello" {
		t.Fatalf("bad user message: %#v", user)
	}

	tools := body["tools"].([]any)
	tool := tools[0].(map[string]any)
	function := tool["function"].(map[string]any)
	if tool["type"] != "function" || function["name"] != "get_weather" {
		t.Fatalf("bad tool mapping: %#v", tool)
	}
}

func TestResponsesBodyToProtocolRequestCapturesToolCalls(t *testing.T) {
	body := map[string]any{
		"model": "gpt-test",
		"input": []any{
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "lookup", "arguments": `{"q":"x"}`},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "ok"},
		},
		"tools": []any{
			map[string]any{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object"}},
		},
	}

	got := responsesBodyToProtocolRequest(body)
	want := protocol.Request{
		Protocol: appruntime.ProtocolResponses,
		Model:    "gpt-test",
		Input: []protocol.Message{
			{Role: "assistant", ToolCalls: []protocol.ToolCall{{ID: "call_1", Name: "lookup", Arguments: `{"q":"x"}`}}},
			{Role: "tool", ToolCallID: "call_1", Content: "ok"},
		},
		Tools: []protocol.Tool{{Name: "lookup", Parameters: map[string]any{"type": "object"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("protocol request mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestTranslateResponsesBodyToChatUsesProtocolAdapterShape(t *testing.T) {
	body := map[string]any{
		"model":  "gpt-test",
		"input":  "hello",
		"stream": true,
		"tools": []any{
			map[string]any{"type": "function", "name": "lookup"},
		},
	}

	got := translateResponsesBodyToChat(body)
	want := protocolRequestToChatBody(responsesBodyToProtocolRequest(body))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("adapter output mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}
