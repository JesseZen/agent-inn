package module

import (
	"context"
	"encoding/json"
	"strings"
)

type ToolFilter struct {
	baseMiddleware
}

func NewToolFilter(cfg ModuleConfig) *ToolFilter {
	return &ToolFilter{baseMiddleware: baseMiddleware{name: "tool_filter", config: cfg}}
}

func (m *ToolFilter) ProcessRequest(ctx context.Context, req *ProxyRequest) error {
	if !m.config.Enabled || !isJSONContentType(req.ContentType, req.Headers.Get("Content-Type")) {
		return nil
	}
	blockedTools := blockedToolSet(m.config)
	if len(blockedTools) == 0 {
		return nil
	}

	var body any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return err
	}

	next, changed := filterToolJSONBody(body, blockedTools)
	if !changed {
		return nil
	}

	encoded, err := json.Marshal(next)
	if err != nil {
		return err
	}
	req.Body = encoded
	req.Headers.Set("Content-Type", "application/json")
	req.ContentType = "application/json"
	return nil
}

func (m *ToolFilter) RequestBodyMode(req ProxyRequestMeta) RequestBodyMode {
	if !m.config.Enabled || len(blockedToolSet(m.config)) == 0 || !isJSONContentType(req.ContentType, req.Headers.Get("Content-Type")) {
		return RequestBodyStream
	}
	return RequestBodyBuffer
}

func isJSONContentType(values ...string) bool {
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "application/json" || strings.HasPrefix(value, "application/json;") {
			return true
		}
	}
	return false
}

func blockedToolSet(cfg ModuleConfig) map[string]struct{} {
	out := map[string]struct{}{}
	if cfg.Params == nil {
		return out
	}
	switch tools := cfg.Params["blocked_tools"].(type) {
	case []any:
		for _, tool := range tools {
			name, ok := tool.(string)
			if ok && strings.TrimSpace(name) != "" {
				out[strings.TrimSpace(name)] = struct{}{}
			}
		}
	case []string:
		for _, name := range tools {
			if strings.TrimSpace(name) != "" {
				out[strings.TrimSpace(name)] = struct{}{}
			}
		}
	}
	return out
}

func filterToolJSONBody(body any, blockedTools map[string]struct{}) (any, bool) {
	object, ok := body.(map[string]any)
	if !ok {
		return body, false
	}

	changed := false
	next := make(map[string]any, len(object))
	for key, value := range object {
		next[key] = value
	}

	if tools, ok := next["tools"].([]any); ok {
		filtered := make([]any, 0, len(tools))
		for _, tool := range tools {
			if toolBlocked(tool, blockedTools) {
				changed = true
				continue
			}
			filtered = append(filtered, tool)
		}
		next["tools"] = filtered
	}

	if toolChoice, ok := next["tool_choice"]; ok {
		sanitized, sanitizedChanged := sanitizeBlockedToolChoice(toolChoice, blockedTools)
		if sanitizedChanged {
			changed = true
		}
		next["tool_choice"] = sanitized
	}

	return next, changed
}

func toolBlocked(tool any, blockedTools map[string]struct{}) bool {
	name := toolName(tool)
	_, ok := blockedTools[name]
	return ok
}

func sanitizeBlockedToolChoice(toolChoice any, blockedTools map[string]struct{}) (any, bool) {
	if toolBlocked(toolChoice, blockedTools) {
		return "auto", true
	}
	object, ok := toolChoice.(map[string]any)
	if !ok {
		return toolChoice, false
	}
	if nested, ok := object["tool"].(map[string]any); ok && toolBlocked(nested, blockedTools) {
		return "auto", true
	}
	return toolChoice, false
}

func toolName(tool any) string {
	switch typed := tool.(type) {
	case string:
		return typed
	case map[string]any:
		if name, _ := typed["type"].(string); name != "" {
			return name
		}
		if name, _ := typed["name"].(string); name != "" {
			return name
		}
	}
	return ""
}
