package worker

import (
	"bytes"
	"encoding/json"
	"strings"
)

type UsageTokens struct {
	Known            bool
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	TotalTokens      int64
}

type UsageObserver struct {
	contentType string
	buffer      []byte
	usage       UsageTokens
}

func NewUsageObserver(contentType string) *UsageObserver {
	return &UsageObserver{contentType: strings.ToLower(contentType)}
}

func (u *UsageObserver) Observe(chunk []byte) {
	if u.usage.Known {
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
			if u.usage.Known {
				return
			}
		}
	}
	if strings.Contains(u.contentType, "json") {
		u.buffer = append(u.buffer, chunk...)
	}
}

func (u *UsageObserver) Finish() UsageTokens {
	if u.usage.Known {
		return u.usage
	}
	if strings.Contains(u.contentType, "text/event-stream") && len(u.buffer) > 0 {
		u.processSSEEvent(string(u.buffer))
	}
	if strings.Contains(u.contentType, "json") {
		u.usage = ExtractUsageFromJSON(u.buffer)
	}
	return u.usage
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
	u.usage = ExtractUsageFromJSON([]byte(payload))
}

func ExtractUsageFromJSON(data []byte) UsageTokens {
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return UsageTokens{Known: false}
	}

	usage, ok := mapField(root, "usage")
	if !ok {
		if response, responseOK := mapField(root, "response"); responseOK {
			usage, ok = mapField(response, "usage")
		}
	}
	if !ok {
		return UsageTokens{Known: false}
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
	return finalizeUsageTotal(result, totalTokens, hasTotalTokens)
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
