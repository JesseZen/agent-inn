package module

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"
)

const externalRequestTimeout = 5 * time.Second

type ExternalRequestRuntime struct {
	Command         string
	Args            []string
	ProtocolVersion string
	Stderr          io.Writer
}

type ExternalRequestMiddleware struct {
	baseMiddleware
	runtime ExternalRequestRuntime
}

type externalRequestPayload struct {
	Method       string         `json:"method"`
	Path         string         `json:"path"`
	RawQuery     *string        `json:"raw_query,omitempty"`
	Headers      http.Header    `json:"headers"`
	Body         string         `json:"body"`
	ContentType  string         `json:"content_type"`
	OriginalPath string         `json:"original_path"`
	Params       map[string]any `json:"params,omitempty"`
}

func NewExternalRequestMiddleware(name string, cfg ModuleConfig, runtime ExternalRequestRuntime) *ExternalRequestMiddleware {
	return &ExternalRequestMiddleware{
		baseMiddleware: baseMiddleware{name: name, config: cfg},
		runtime:        runtime,
	}
}

func (m *ExternalRequestMiddleware) ProcessRequest(ctx context.Context, req *ProxyRequest) error {
	if !m.config.Enabled {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, externalRequestTimeout)
	defer cancel()
	rawQuery := req.RawQuery
	payload := externalRequestPayload{
		Method:       req.Method,
		Path:         req.Path,
		RawQuery:     &rawQuery,
		Headers:      req.Headers.Clone(),
		Body:         string(req.Body),
		ContentType:  req.ContentType,
		OriginalPath: req.OriginalPath,
		Params:       CloneModuleConfig(m.config).Params,
	}
	input, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, m.runtime.Command, m.runtime.Args...)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	stderrWriter := io.Writer(&stderr)
	if m.runtime.Stderr != nil {
		stderrWriter = io.MultiWriter(m.runtime.Stderr, &stderr)
	}
	cmd.Stderr = stderrWriter
	output, err := cmd.Output()
	if err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("external request middleware %q failed: %s", m.name, stderr.String())
		}
		return fmt.Errorf("external request middleware %q failed: %w", m.name, err)
	}
	var next externalRequestPayload
	if err := json.Unmarshal(output, &next); err != nil {
		return fmt.Errorf("external request middleware %q returned invalid JSON", m.name)
	}
	req.Method = next.Method
	req.Path = next.Path
	if next.RawQuery != nil {
		req.RawQuery = *next.RawQuery
	}
	req.Headers = next.Headers.Clone()
	req.Body = []byte(next.Body)
	req.ContentType = next.ContentType
	req.OriginalPath = next.OriginalPath
	return nil
}

func (m *ExternalRequestMiddleware) RequestBodyMode(req ProxyRequestMeta) RequestBodyMode {
	if !m.config.Enabled {
		return RequestBodyStream
	}
	switch m.name {
	case "image_filter":
		if !isJSONContentType(req.ContentType, req.Headers.Get("Content-Type")) {
			return RequestBodyStream
		}
	case "model_override":
		if !isJSONContentType(req.ContentType, req.Headers.Get("Content-Type")) {
			return RequestBodyStream
		}
		if model, _ := m.config.Params["model"].(string); model == "" {
			return RequestBodyStream
		}
	case "request_log":
		return RequestBodyStream
	}
	return RequestBodyBuffer
}
