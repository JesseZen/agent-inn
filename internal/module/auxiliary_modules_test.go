package module

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestModelOverrideUpdatesJSONModel(t *testing.T) {
	req := &ProxyRequest{
		Method:      http.MethodPost,
		Path:        "/v1/responses",
		Headers:     http.Header{"Content-Type": []string{"application/json"}},
		ContentType: "application/json",
		Body:        []byte(`{"model":"old","input":"hello"}`),
	}
	m := NewModelOverride(ModuleConfig{Enabled: true, Params: map[string]any{"model": "new-model"}})
	if err := m.ProcessRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "new-model" || body["input"] != "hello" {
		t.Fatalf("bad model override: %#v", body)
	}
}

func TestRequestLogWritesSummary(t *testing.T) {
	var buf bytes.Buffer
	m := NewRequestLog(ModuleConfig{Enabled: true}, &buf)
	req := &ProxyRequest{Method: http.MethodPost, Path: "/v1/responses"}
	if err := m.ProcessRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "INFO POST /v1/responses") {
		t.Fatalf("missing request log summary: %s", buf.String())
	}
}

func TestDebugSSEWrapsWithoutChangingContent(t *testing.T) {
	var buf bytes.Buffer
	m := NewDebugSSE(ModuleConfig{Enabled: true}, &buf)
	resp, err := m.WrapResponse(context.Background(), &ProxyRequest{}, &ProxyResponse{
		StatusCode:  http.StatusOK,
		Headers:     http.Header{"Content-Type": []string{"text/event-stream"}},
		ContentType: "text/event-stream",
		Body:        io.NopCloser(strings.NewReader("data: hello\n\n")),
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "data: hello\n\n" {
		t.Fatalf("debug sse changed content: %q", out)
	}
	if !strings.Contains(buf.String(), "DEBUG sse chunk bytes=") {
		t.Fatalf("missing debug log: %s", buf.String())
	}
}
