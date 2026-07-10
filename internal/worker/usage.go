package worker

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jesse/agent-inn/internal/module"
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
	parser      module.SSEParser
	usage       UsageTokens
	model       string
	jsonScanner *responseJSONScanner
}

type responseUsageMetadata struct {
	Usage UsageTokens
	Model string
}

func NewUsageObserver(contentType string) *UsageObserver {
	observer := &UsageObserver{contentType: strings.ToLower(contentType)}
	if strings.Contains(observer.contentType, "json") {
		observer.jsonScanner = &responseJSONScanner{}
	}
	return observer
}

func (u *UsageObserver) Observe(chunk []byte) {
	if strings.Contains(u.contentType, "text/event-stream") {
		events, _ := u.parser.Push(chunk, false)
		for _, event := range events {
			u.processSSEEvent(event)
		}
	}
	if u.jsonScanner != nil {
		u.jsonScanner.Write(chunk)
	}
}

func (u *UsageObserver) Finish() UsageTokens {
	if strings.Contains(u.contentType, "text/event-stream") {
		events, _ := u.parser.Push(nil, true)
		for _, event := range events {
			u.processSSEEvent(event)
		}
	}
	if u.jsonScanner != nil {
		metadata := u.jsonScanner.Finish()
		u.usage = metadata.Usage
		u.model = metadata.Model
		u.jsonScanner = nil
	}
	return u.usage
}

func (u *UsageObserver) Model() string {
	return u.model
}

func (u *UsageObserver) processSSEEvent(event module.SSEEvent) {
	if event.Done {
		return
	}
	metadata := extractUsageMetadataFromJSON([]byte(event.Data))
	if metadata.Usage.Known {
		if event.Event == "message_delta" && u.usage.Known {
			u.usage.InputTokens += metadata.Usage.InputTokens
			u.usage.OutputTokens += metadata.Usage.OutputTokens
			u.usage.CacheReadTokens += metadata.Usage.CacheReadTokens
			u.usage.CacheWriteTokens += metadata.Usage.CacheWriteTokens
			u.usage.ReasoningTokens += metadata.Usage.ReasoningTokens
			u.usage.TotalTokens += metadata.Usage.TotalTokens
		} else {
			u.usage = metadata.Usage
		}
	}
	if metadata.Model != "" {
		u.model = metadata.Model
	}
}

func ExtractUsageFromJSON(data []byte) UsageTokens {
	return extractUsageMetadataFromJSON(data).Usage
}

func extractUsageMetadataFromJSON(data []byte) responseUsageMetadata {
	scanner := &responseJSONScanner{}
	scanner.Write(data)
	return scanner.Finish()
}

func extractUsageMetadataFromJSONDecoder(decoder *json.Decoder) responseUsageMetadata {
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return responseUsageMetadata{Usage: UsageTokens{Known: false}}
	}

	var root responseUsageMetadata
	var response responseUsageMetadata
	var message responseUsageMetadata
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return responseUsageMetadata{Usage: UsageTokens{Known: false}}
		}
		key, ok := keyToken.(string)
		if !ok {
			return responseUsageMetadata{Usage: UsageTokens{Known: false}}
		}
		switch key {
		case "model":
			root.Model, _, err = decodeJSONString(decoder)
		case "usage":
			root.Usage, err = decodeUsageTokens(decoder)
		case "response":
			response, err = decodeResponseUsageMetadata(decoder)
		case "message":
			message, err = decodeResponseUsageMetadata(decoder)
		default:
			err = skipJSONValue(decoder)
		}
		if err != nil {
			return responseUsageMetadata{Usage: UsageTokens{Known: false}}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return responseUsageMetadata{Usage: UsageTokens{Known: false}}
	}
	if !root.Usage.Known {
		root.Usage = response.Usage
	}
	if !root.Usage.Known {
		root.Usage = message.Usage
	}
	if root.Model == "" {
		root.Model = response.Model
	}
	if root.Model == "" {
		root.Model = message.Model
	}
	return root
}

func decodeResponseUsageMetadata(decoder *json.Decoder) (responseUsageMetadata, error) {
	token, err := decoder.Token()
	if err != nil {
		return responseUsageMetadata{}, err
	}
	if token != json.Delim('{') {
		return responseUsageMetadata{}, skipJSONToken(decoder, token)
	}
	var result responseUsageMetadata
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return responseUsageMetadata{}, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return responseUsageMetadata{}, fmt.Errorf("json object key is not a string")
		}
		switch key {
		case "model":
			result.Model, _, err = decodeJSONString(decoder)
		case "usage":
			result.Usage, err = decodeUsageTokens(decoder)
		default:
			err = skipJSONValue(decoder)
		}
		if err != nil {
			return responseUsageMetadata{}, err
		}
	}
	_, err = decoder.Token()
	return result, err
}

func decodeUsageTokens(decoder *json.Decoder) (UsageTokens, error) {
	token, err := decoder.Token()
	if err != nil {
		return UsageTokens{}, err
	}
	if token != json.Delim('{') {
		return UsageTokens{}, skipJSONToken(decoder, token)
	}

	result := UsageTokens{Known: true}
	var inputTokens int64
	var promptTokens int64
	var hasPromptTokens bool
	var outputTokens int64
	var completionTokens int64
	var hasCompletionTokens bool
	var cacheReadTokens int64
	var inputCachedTokens int64
	var hasInputCachedTokens bool
	var promptCachedTokens int64
	var hasPromptCachedTokens bool
	var outputReasoningTokens int64
	var hasOutputReasoningTokens bool
	var completionReasoningTokens int64
	var hasCompletionReasoningTokens bool
	var totalTokens int64
	var hasTotalTokens bool
	var hasNestedDetails bool

	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return UsageTokens{}, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return UsageTokens{}, fmt.Errorf("json object key is not a string")
		}
		switch key {
		case "input_tokens":
			inputTokens, _, err = decodeJSONInt64(decoder)
		case "prompt_tokens":
			promptTokens, hasPromptTokens, err = decodeJSONInt64(decoder)
		case "output_tokens":
			outputTokens, _, err = decodeJSONInt64(decoder)
		case "completion_tokens":
			completionTokens, hasCompletionTokens, err = decodeJSONInt64(decoder)
		case "cache_read_input_tokens":
			cacheReadTokens, _, err = decodeJSONInt64(decoder)
		case "cache_creation_input_tokens":
			result.CacheWriteTokens, _, err = decodeJSONInt64(decoder)
		case "input_tokens_details":
			hasNestedDetails = true
			inputCachedTokens, hasInputCachedTokens, err = decodeUsageDetail(decoder, "cached_tokens")
		case "prompt_tokens_details":
			hasNestedDetails = true
			promptCachedTokens, hasPromptCachedTokens, err = decodeUsageDetail(decoder, "cached_tokens")
		case "output_tokens_details":
			hasNestedDetails = true
			outputReasoningTokens, hasOutputReasoningTokens, err = decodeUsageDetail(decoder, "reasoning_tokens")
		case "completion_tokens_details":
			hasNestedDetails = true
			completionReasoningTokens, hasCompletionReasoningTokens, err = decodeUsageDetail(decoder, "reasoning_tokens")
		case "total_tokens":
			totalTokens, hasTotalTokens, err = decodeJSONInt64(decoder)
		default:
			err = skipJSONValue(decoder)
		}
		if err != nil {
			return UsageTokens{}, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return UsageTokens{}, err
	}

	result.InputTokens = inputTokens
	if hasPromptTokens {
		result.InputTokens = promptTokens
	}
	result.OutputTokens = outputTokens
	if hasCompletionTokens {
		result.OutputTokens = completionTokens
	}
	result.CacheReadTokens = cacheReadTokens
	if hasInputCachedTokens {
		result.CacheReadTokens = inputCachedTokens
	}
	if hasPromptCachedTokens {
		result.CacheReadTokens = promptCachedTokens
	}
	if hasOutputReasoningTokens {
		result.ReasoningTokens = outputReasoningTokens
	}
	if hasCompletionReasoningTokens {
		result.ReasoningTokens = completionReasoningTokens
	}

	if !hasTotalTokens && hasNestedDetails {
		totalTokens = result.InputTokens + result.OutputTokens
		hasTotalTokens = true
	}
	return finalizeUsageTotal(result, totalTokens, hasTotalTokens), nil
}

func decodeUsageDetail(decoder *json.Decoder, fieldName string) (int64, bool, error) {
	token, err := decoder.Token()
	if err != nil {
		return 0, false, err
	}
	if token != json.Delim('{') {
		return 0, false, skipJSONToken(decoder, token)
	}
	var result int64
	var found bool
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return 0, false, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return 0, false, fmt.Errorf("json object key is not a string")
		}
		if key == fieldName {
			result, found, err = decodeJSONInt64(decoder)
		} else {
			err = skipJSONValue(decoder)
		}
		if err != nil {
			return 0, false, err
		}
	}
	_, err = decoder.Token()
	return result, found, err
}

func decodeJSONInt64(decoder *json.Decoder) (int64, bool, error) {
	token, err := decoder.Token()
	if err != nil {
		return 0, false, err
	}
	number, ok := token.(json.Number)
	if !ok {
		return 0, false, skipJSONToken(decoder, token)
	}
	value, err := number.Int64()
	if err != nil {
		return 0, false, nil
	}
	return value, true, nil
}

func decodeJSONString(decoder *json.Decoder) (string, bool, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", false, err
	}
	value, ok := token.(string)
	if !ok {
		return "", false, skipJSONToken(decoder, token)
	}
	return value, true, nil
}

func skipJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	return skipJSONToken(decoder, token)
}

func skipJSONToken(decoder *json.Decoder, token json.Token) error {
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for decoder.More() {
			if _, err := decoder.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	_, err := decoder.Token()
	return err
}

func finalizeUsageTotal(usage UsageTokens, totalTokens int64, hasTotalTokens bool) UsageTokens {
	if hasTotalTokens {
		usage.TotalTokens = totalTokens
		return usage
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheReadTokens + usage.CacheWriteTokens + usage.ReasoningTokens
	return usage
}
