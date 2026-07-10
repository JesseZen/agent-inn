package worker

import (
	"strconv"
)

func (s *responseJSONScanner) finishString() {
	s.mode = responseJSONSyntax
	if s.stringOverflow {
		if s.stringTarget == responseJSONStringKey {
			frame := &s.frames[s.frameCount-1]
			frame.field, frame.stage = responseJSONUnknown, responseJSONColon
		}
		return
	}
	value, ok := decodeResponseJSONString(s.stringData[:s.stringLength])
	if !ok {
		s.invalid = true
		return
	}
	if s.stringTarget == responseJSONStringModel {
		s.setModel(s.stringContext, value)
		return
	}
	frame := &s.frames[s.frameCount-1]
	frame.field = responseJSONFieldForKey(frame.context, value)
	frame.stage = responseJSONColon
}
func (s *responseJSONScanner) finishNumber() {
	value, found := int64(0), false
	if !s.numberOverflow {
		parsed, err := strconv.ParseInt(string(s.numberData[:s.numberLength]), 10, 64)
		if err == nil {
			value, found = parsed, true
		}
	}
	s.usage.set(s.numberField, value, found)
	s.mode = responseJSONSyntax
}
func (s *responseJSONScanner) closeObject() {
	context := s.frames[s.frameCount-1].context
	s.frameCount--
	if context == responseJSONUsage {
		usage := UsageTokens{
			Known:            true,
			InputTokens:      s.usage.values[responseJSONInputTokens],
			OutputTokens:     s.usage.values[responseJSONOutputTokens],
			CacheReadTokens:  s.usage.values[responseJSONCacheReadTokens],
			CacheWriteTokens: s.usage.values[responseJSONCacheWriteTokens],
		}
		if s.usage.has(responseJSONPromptTokens) {
			usage.InputTokens = s.usage.values[responseJSONPromptTokens]
		}
		if s.usage.has(responseJSONCompletionTokens) {
			usage.OutputTokens = s.usage.values[responseJSONCompletionTokens]
		}
		if s.usage.has(responseJSONInputCachedTokens) {
			usage.CacheReadTokens = s.usage.values[responseJSONInputCachedTokens]
		}
		if s.usage.has(responseJSONPromptCachedTokens) {
			usage.CacheReadTokens = s.usage.values[responseJSONPromptCachedTokens]
		}
		if s.usage.has(responseJSONOutputReasoningTokens) {
			usage.ReasoningTokens = s.usage.values[responseJSONOutputReasoningTokens]
		}
		if s.usage.has(responseJSONCompletionReasoningTokens) {
			usage.ReasoningTokens = s.usage.values[responseJSONCompletionReasoningTokens]
		}
		if s.usage.has(responseJSONTotalTokens) {
			usage.TotalTokens = s.usage.values[responseJSONTotalTokens]
		} else if s.usage.nested {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		} else {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheReadTokens + usage.CacheWriteTokens + usage.ReasoningTokens
		}
		s.setUsage(s.usageTarget, usage)
	}
	if s.frameCount == 0 {
		s.done = true
	}
}
func (s *responseJSONScanner) setModel(context responseJSONContext, model string) {
	switch context {
	case responseJSONRoot:
		s.root.Model = model
	case responseJSONResponse:
		s.response.Model = model
	case responseJSONMessage:
		s.message.Model = model
	}
}
func (s *responseJSONScanner) setUsage(context responseJSONContext, usage UsageTokens) {
	switch context {
	case responseJSONRoot:
		s.root.Usage = usage
	case responseJSONResponse:
		s.response.Usage = usage
	case responseJSONMessage:
		s.message.Usage = usage
	}
}
