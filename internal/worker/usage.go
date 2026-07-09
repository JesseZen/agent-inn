package worker

import (
	"bytes"
	"encoding/json"
	"strings"
)

type UsageTokens struct {
	Known            bool  `json:"usage_known"`
	InputTokens      int64 `json:"input_tokens,omitempty"`
	OutputTokens     int64 `json:"output_tokens,omitempty"`
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int64 `json:"reasoning_tokens,omitempty"`
	TotalTokens      int64 `json:"total_tokens,omitempty"`
}

type UsageObserver struct {
	contentType string
	buffer      []byte
	usage       UsageTokens
	model       string
}

type responseUsageMetadata struct {
	Usage UsageTokens
	Model string
}

func NewUsageObserver(contentType string) *UsageObserver {
	return &UsageObserver{contentType: strings.ToLower(contentType)}
}

func (u *UsageObserver) Observe(chunk []byte) {
	if u.usage.Known && u.model != "" {
		return
	}
	if strings.Contains(u.contentType, "text/event-stream") {
		u.buffer = append(u.buffer, chunk...)
		for {
			end := bytes.Index(u.buffer, []byte("\n\n"))
			if end < 0 {
				return
			}
			u.processSSEEvent(string(u.buffer[:end]))
			u.buffer = u.buffer[end+2:]
			if u.usage.Known && u.model != "" {
				return
			}
		}
	}
	if strings.Contains(u.contentType, "json") {
		u.buffer = append(u.buffer, chunk...)
	}
}

func (u *UsageObserver) Finish() UsageTokens {
	if u.usage.Known && u.model != "" {
		return u.usage
	}
	if strings.Contains(u.contentType, "text/event-stream") && len(u.buffer) > 0 {
		u.processSSEEvent(string(u.buffer))
	}
	if strings.Contains(u.contentType, "json") {
		metadata := extractUsageMetadataFromJSON(u.buffer)
		u.usage = metadata.Usage
		u.model = metadata.Model
	}
	return u.usage
}

func (u *UsageObserver) Model() string {
	return u.model
}

func (u *UsageObserver) processSSEEvent(event string) {
	var eventName string
	var data []string
	for _, line := range strings.Split(event, "\n") {
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	payload := strings.Join(data, "\n")
	if eventName != "response.completed" && !strings.Contains(payload, `"usage"`) {
		return
	}
	metadata := extractUsageMetadataFromJSON([]byte(payload))
	if metadata.Usage.Known {
		u.usage = metadata.Usage
	}
	if metadata.Model != "" {
		u.model = metadata.Model
	}
}

func ExtractUsageFromJSON(data []byte) UsageTokens {
	return extractUsageMetadataFromJSON(data).Usage
}

func extractUsageMetadataFromJSON(data []byte) responseUsageMetadata {
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return responseUsageMetadata{Usage: UsageTokens{Known: false}}
	}

	model, _ := stringField(root, "model")
	usage, ok := mapField(root, "usage")
	if !ok {
		if response, responseOK := mapField(root, "response"); responseOK {
			usage, ok = mapField(response, "usage")
			if model == "" {
				model, _ = stringField(response, "model")
			}
		}
	}
	if !ok {
		return responseUsageMetadata{Usage: UsageTokens{Known: false}, Model: model}
	}

	result := UsageTokens{Known: true}
	if value, ok := int64Field(usage, "input_tokens"); ok {
		result.InputTokens = value
	}
	if value, ok := int64Field(usage, "prompt_tokens"); ok {
		result.InputTokens = value
	}
	if value, ok := int64Field(usage, "output_tokens"); ok {
		result.OutputTokens = value
	}
	if value, ok := int64Field(usage, "completion_tokens"); ok {
		result.OutputTokens = value
	}
	if value, ok := int64Field(usage, "cache_read_input_tokens"); ok {
		result.CacheReadTokens = value
	}
	if value, ok := int64Field(usage, "cache_creation_input_tokens"); ok {
		result.CacheWriteTokens = value
	}
	if details, ok := mapField(usage, "input_tokens_details"); ok {
		if value, valueOK := int64Field(details, "cached_tokens"); valueOK {
			result.CacheReadTokens = value
		}
	}
	if details, ok := mapField(usage, "output_tokens_details"); ok {
		if value, valueOK := int64Field(details, "reasoning_tokens"); valueOK {
			result.ReasoningTokens = value
		}
	}
	if details, ok := mapField(usage, "completion_tokens_details"); ok {
		if value, valueOK := int64Field(details, "reasoning_tokens"); valueOK {
			result.ReasoningTokens = value
		}
	}

	totalTokens, hasTotalTokens := int64Field(usage, "total_tokens")
	hasNestedDetails := result.ReasoningTokens > 0 || mapHasField(usage, "input_tokens_details") || mapHasField(usage, "output_tokens_details") || mapHasField(usage, "completion_tokens_details")
	if !hasTotalTokens && hasNestedDetails {
		totalTokens = result.InputTokens + result.OutputTokens
		hasTotalTokens = true
	}
	return responseUsageMetadata{Usage: finalizeUsageTotal(result, totalTokens, hasTotalTokens), Model: model}
}

func int64Field(values map[string]any, name string) (int64, bool) {
	value, ok := values[name]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	case float64:
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	}
	return 0, false
}

func mapField(values map[string]any, name string) (map[string]any, bool) {
	value, ok := values[name]
	if !ok {
		return nil, false
	}
	typed, ok := value.(map[string]any)
	return typed, ok
}

func stringField(values map[string]any, name string) (string, bool) {
	value, ok := values[name]
	if !ok {
		return "", false
	}
	typed, ok := value.(string)
	return typed, ok
}

func mapHasField(values map[string]any, name string) bool {
	_, ok := values[name]
	return ok
}

func finalizeUsageTotal(usage UsageTokens, totalTokens int64, hasTotalTokens bool) UsageTokens {
	if hasTotalTokens {
		usage.TotalTokens = totalTokens
		return usage
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheReadTokens + usage.CacheWriteTokens + usage.ReasoningTokens
	return usage
}
