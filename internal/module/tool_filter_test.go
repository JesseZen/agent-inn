package module

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestToolFilterRemovesConfiguredToolsAndRewritesToolChoice(t *testing.T) {
	req := &ProxyRequest{
		Method: "POST",
		Path:   "/v1/responses",
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:        []byte(`{"tools":[{"type":"image_generation"},{"type":"web_search_preview"},{"type":"function","name":"keep"}],"tool_choice":{"type":"web_search_preview"},"input":"hello"}`),
		ContentType: "application/json",
	}

	m := NewToolFilter(ModuleConfig{
		Enabled: true,
		Params: map[string]any{
			"blocked_tools": []any{"image_generation", "web_search_preview"},
		},
	})
	if err := m.ProcessRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	var got struct {
		Tools      []map[string]any `json:"tools"`
		ToolChoice any              `json:"tool_choice"`
		Input      string           `json:"input"`
	}
	if err := json.Unmarshal(req.Body, &got); err != nil {
		t.Fatal(err)
	}
	want := struct {
		Tools      []map[string]any `json:"tools"`
		ToolChoice any              `json:"tool_choice"`
		Input      string           `json:"input"`
	}{
		Tools: []map[string]any{
			{"type": "function", "name": "keep"},
		},
		ToolChoice: "auto",
		Input:      "hello",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad filtered request:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestToolFilterKeepsUnblockedMapToolChoice(t *testing.T) {
	req := &ProxyRequest{
		Method: "POST",
		Path:   "/v1/responses",
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:        []byte(`{"tools":[{"type":"function","name":"keep"}],"tool_choice":{"type":"function","name":"keep"}}`),
		ContentType: "application/json",
	}

	m := NewToolFilter(ModuleConfig{
		Enabled: true,
		Params: map[string]any{
			"blocked_tools": []any{"image_generation"},
		},
	})
	if err := m.ProcessRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	want := `{"tools":[{"type":"function","name":"keep"}],"tool_choice":{"type":"function","name":"keep"}}`
	if string(req.Body) != want {
		t.Fatalf("request changed:\ngot  %s\nwant %s", req.Body, want)
	}
}
