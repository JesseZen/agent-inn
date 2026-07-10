package worker

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	usageJSONRetainedStringBytes = 4 * 1024
	usageJSONNumberBytes         = 64
)

type usageJSONValues struct {
	values [responseJSONFieldCount]int64
	found  uint32
	nested bool
}

func (u *usageJSONValues) set(field responseJSONField, value int64, found bool) {
	u.values[field] = value
	bit := uint32(1) << field
	u.found &^= bit
	if found {
		u.found |= bit
	}
}

func (u *usageJSONValues) has(field responseJSONField) bool {
	return u.found&(uint32(1)<<field) != 0
}

type responseJSONContext uint8

const (
	responseJSONRoot responseJSONContext = iota
	responseJSONResponse
	responseJSONMessage
	responseJSONUsage
	responseJSONInputDetails
	responseJSONPromptDetails
	responseJSONOutputDetails
	responseJSONCompletionDetails
)

type responseJSONField uint8

const (
	responseJSONUnknown responseJSONField = iota
	responseJSONModel
	responseJSONUsageField
	responseJSONResponseField
	responseJSONMessageField
	responseJSONInputTokens
	responseJSONPromptTokens
	responseJSONOutputTokens
	responseJSONCompletionTokens
	responseJSONCacheReadTokens
	responseJSONCacheWriteTokens
	responseJSONInputCachedTokens
	responseJSONPromptCachedTokens
	responseJSONOutputReasoningTokens
	responseJSONCompletionReasoningTokens
	responseJSONTotalTokens
	responseJSONInputDetailsField
	responseJSONPromptDetailsField
	responseJSONOutputDetailsField
	responseJSONCompletionDetailsField
	responseJSONFieldCount
)

func responseJSONFieldForKey(context responseJSONContext, key string) responseJSONField {
	switch context {
	case responseJSONRoot:
		switch key {
		case "model":
			return responseJSONModel
		case "usage":
			return responseJSONUsageField
		case "response":
			return responseJSONResponseField
		case "message":
			return responseJSONMessageField
		}
	case responseJSONResponse, responseJSONMessage:
		if key == "model" {
			return responseJSONModel
		}
		if key == "usage" {
			return responseJSONUsageField
		}
	case responseJSONUsage:
		switch key {
		case "input_tokens":
			return responseJSONInputTokens
		case "prompt_tokens":
			return responseJSONPromptTokens
		case "output_tokens":
			return responseJSONOutputTokens
		case "completion_tokens":
			return responseJSONCompletionTokens
		case "cache_read_input_tokens":
			return responseJSONCacheReadTokens
		case "cache_creation_input_tokens":
			return responseJSONCacheWriteTokens
		case "input_tokens_details":
			return responseJSONInputDetailsField
		case "prompt_tokens_details":
			return responseJSONPromptDetailsField
		case "output_tokens_details":
			return responseJSONOutputDetailsField
		case "completion_tokens_details":
			return responseJSONCompletionDetailsField
		case "total_tokens":
			return responseJSONTotalTokens
		}
	case responseJSONInputDetails:
		if key == "cached_tokens" {
			return responseJSONInputCachedTokens
		}
	case responseJSONPromptDetails:
		if key == "cached_tokens" {
			return responseJSONPromptCachedTokens
		}
	case responseJSONOutputDetails:
		if key == "reasoning_tokens" {
			return responseJSONOutputReasoningTokens
		}
	case responseJSONCompletionDetails:
		if key == "reasoning_tokens" {
			return responseJSONCompletionReasoningTokens
		}
	}
	return responseJSONUnknown
}

func decodeResponseJSONString(data []byte) (string, bool) {
	var decoded strings.Builder
	decoded.Grow(len(data))
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			decoded.WriteByte(data[i])
			continue
		}
		i++
		if i == len(data) {
			return "", false
		}
		switch data[i] {
		case '"', '\\', '/':
			decoded.WriteByte(data[i])
		case 'b':
			decoded.WriteByte('\b')
		case 'f':
			decoded.WriteByte('\f')
		case 'n':
			decoded.WriteByte('\n')
		case 'r':
			decoded.WriteByte('\r')
		case 't':
			decoded.WriteByte('\t')
		case 'u':
			if i+4 >= len(data) {
				return "", false
			}
			value, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
			if err != nil {
				return "", false
			}
			i += 4
			r := rune(value)
			if utf16.IsSurrogate(r) {
				if i+6 < len(data) && data[i+1] == '\\' && data[i+2] == 'u' {
					low, lowErr := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
					if lowErr == nil && utf16.IsSurrogate(rune(low)) {
						r = utf16.DecodeRune(r, rune(low))
						i += 6
					} else {
						r = utf8.RuneError
					}
				} else {
					r = utf8.RuneError
				}
			}
			decoded.WriteRune(r)
		default:
			return "", false
		}
	}
	return decoded.String(), true
}
