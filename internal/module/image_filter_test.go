package module

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestImageFilterRemovesImageGenerationToolsAndRewritesToolChoice(t *testing.T) {
	req := &ProxyRequest{
		Method:      "POST",
		Path:        "/v1/responses",
		Headers:     http.Header{"Content-Type": []string{"application/json"}},
		Body:        []byte(`{"tools":[{"type":"image_generation"},{"type":"function","name":"keep"}],"tool_choice":"image_generation","input":"hello"}`),
		ContentType: "application/json",
	}

	m := NewImageFilter(ModuleConfig{Enabled: true})
	if err := m.ProcessRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatal(err)
	}
	tools := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected one remaining tool, got %#v", tools)
	}
	if body["tool_choice"] != "auto" {
		t.Fatalf("expected tool_choice auto, got %#v", body["tool_choice"])
	}
}
