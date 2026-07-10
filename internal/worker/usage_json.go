package worker

type responseJSONScanner struct {
	mode                         responseJSONMode
	frames                       [4]responseJSONFrame
	frameCount                   int
	started, done, invalid       bool
	stringTarget                 responseJSONStringTarget
	stringContext                responseJSONContext
	stringData                   [usageJSONRetainedStringBytes]byte
	stringLength                 int
	stringOverflow, stringEscape bool
	numberField                  responseJSONField
	numberData                   [usageJSONNumberBytes]byte
	numberLength                 int
	numberOverflow               bool
	skipDepth                    int
	skipString, skipEscape       bool
	usageTarget                  responseJSONContext
	usage                        usageJSONValues
	root, response, message      responseUsageMetadata
}

type responseJSONFrame struct {
	context responseJSONContext
	stage   responseJSONStage
	field   responseJSONField
}

type responseJSONMode uint8

const (
	responseJSONSyntax responseJSONMode = iota
	responseJSONString
	responseJSONNumber
	responseJSONSkipString
	responseJSONSkipScalar
	responseJSONSkipContainer
)

type responseJSONStage uint8

const (
	responseJSONKey responseJSONStage = iota
	responseJSONColon
	responseJSONValue
	responseJSONComma
)

type responseJSONStringTarget uint8

const (
	responseJSONStringKey responseJSONStringTarget = iota
	responseJSONStringModel
)

func (s *responseJSONScanner) Write(data []byte) {
	for i := 0; i < len(data); {
		if s.consume(data[i]) {
			i++
		}
	}
}
func (s *responseJSONScanner) Finish() responseUsageMetadata {
	if s.invalid || !s.done {
		return responseUsageMetadata{Usage: UsageTokens{Known: false}}
	}
	result := s.root
	if !result.Usage.Known {
		result.Usage = s.response.Usage
	}
	if !result.Usage.Known {
		result.Usage = s.message.Usage
	}
	if result.Model == "" {
		result.Model = s.response.Model
	}
	if result.Model == "" {
		result.Model = s.message.Model
	}
	return result
}
func (s *responseJSONScanner) consume(b byte) bool {
	if s.invalid || s.done {
		return true
	}
	switch s.mode {
	case responseJSONString:
		if s.stringEscape {
			s.appendStringByte(b)
			s.stringEscape = false
			return true
		}
		if b == '\\' {
			s.appendStringByte(b)
			s.stringEscape = true
			return true
		}
		if b == '"' {
			s.finishString()
			return true
		}
		if b < 0x20 {
			s.invalid = true
			return true
		}
		s.appendStringByte(b)
		return true
	case responseJSONNumber:
		if isJSONDelimiter(b) {
			s.finishNumber()
			return false
		}
		if s.numberLength < len(s.numberData) {
			s.numberData[s.numberLength] = b
			s.numberLength++
		} else {
			s.numberOverflow = true
		}
		return true
	case responseJSONSkipString:
		if s.skipEscape {
			s.skipEscape = false
			return true
		}
		if b == '\\' {
			s.skipEscape = true
			return true
		}
		if b == '"' {
			s.mode = responseJSONSyntax
			return true
		}
		if b < 0x20 {
			s.invalid = true
		}
		return true
	case responseJSONSkipScalar:
		if isJSONDelimiter(b) {
			s.mode = responseJSONSyntax
			return false
		}
		return true
	case responseJSONSkipContainer:
		if s.skipString {
			if s.skipEscape {
				s.skipEscape = false
				return true
			}
			if b == '\\' {
				s.skipEscape = true
				return true
			}
			if b == '"' {
				s.skipString = false
				return true
			}
			if b < 0x20 {
				s.invalid = true
			}
			return true
		}
		switch b {
		case '"':
			s.skipString = true
		case '{', '[':
			s.skipDepth++
		case '}', ']':
			s.skipDepth--
			if s.skipDepth == 0 {
				s.mode = responseJSONSyntax
			}
		}
		return true
	}
	if isJSONWhitespace(b) {
		return true
	}
	if !s.started {
		if b != '{' {
			s.invalid = true
			return true
		}
		s.started = true
		s.pushFrame(responseJSONRoot)
		return true
	}
	frame := &s.frames[s.frameCount-1]
	switch frame.stage {
	case responseJSONKey:
		if b == '}' {
			s.closeObject()
			return true
		}
		if b != '"' {
			s.invalid = true
			return true
		}
		s.beginString(responseJSONStringKey, frame.context)
	case responseJSONColon:
		if b != ':' {
			s.invalid = true
			return true
		}
		frame.stage = responseJSONValue
	case responseJSONValue:
		s.startValue(frame, b)
	case responseJSONComma:
		switch b {
		case ',':
			frame.stage = responseJSONKey
		case '}':
			s.closeObject()
		default:
			s.invalid = true
		}
	}
	return true
}
func (s *responseJSONScanner) startValue(frame *responseJSONFrame, b byte) {
	field := frame.field
	frame.field = responseJSONUnknown
	frame.stage = responseJSONComma
	if frame.context == responseJSONUsage {
		s.startUsageValue(field, b)
		return
	}
	if frame.context >= responseJSONInputDetails {
		s.startNumberValue(field, b)
		return
	}
	switch field {
	case responseJSONModel:
		s.setModel(frame.context, "")
		if b == '"' {
			s.beginString(responseJSONStringModel, frame.context)
		} else {
			s.startSkipValue(b)
		}
	case responseJSONUsageField:
		s.setUsage(frame.context, UsageTokens{})
		if b == '{' {
			s.usage = usageJSONValues{}
			s.usageTarget = frame.context
			s.pushFrame(responseJSONUsage)
		} else {
			s.startSkipValue(b)
		}
	case responseJSONResponseField:
		s.response = responseUsageMetadata{}
		if b == '{' {
			s.pushFrame(responseJSONResponse)
		} else {
			s.startSkipValue(b)
		}
	case responseJSONMessageField:
		s.message = responseUsageMetadata{}
		if b == '{' {
			s.pushFrame(responseJSONMessage)
		} else {
			s.startSkipValue(b)
		}
	default:
		s.startSkipValue(b)
	}
}
func (s *responseJSONScanner) startUsageValue(field responseJSONField, b byte) {
	if field >= responseJSONInputTokens && field <= responseJSONTotalTokens {
		s.startNumberValue(field, b)
		return
	}
	s.usage.nested = true
	var context responseJSONContext
	var valueField responseJSONField
	switch field {
	case responseJSONInputDetailsField:
		context, valueField = responseJSONInputDetails, responseJSONInputCachedTokens
	case responseJSONPromptDetailsField:
		context, valueField = responseJSONPromptDetails, responseJSONPromptCachedTokens
	case responseJSONOutputDetailsField:
		context, valueField = responseJSONOutputDetails, responseJSONOutputReasoningTokens
	case responseJSONCompletionDetailsField:
		context, valueField = responseJSONCompletionDetails, responseJSONCompletionReasoningTokens
	default:
		s.usage.nested = false
		s.startSkipValue(b)
		return
	}
	s.usage.set(valueField, 0, false)
	if b == '{' {
		s.pushFrame(context)
	} else {
		s.startSkipValue(b)
	}
}
func (s *responseJSONScanner) startNumberValue(field responseJSONField, b byte) {
	if field < responseJSONInputTokens || field > responseJSONTotalTokens {
		s.startSkipValue(b)
		return
	}
	s.usage.set(field, 0, false)
	if b == '-' || b >= '0' && b <= '9' {
		s.mode = responseJSONNumber
		s.numberField = field
		s.numberData[0] = b
		s.numberLength = 1
		s.numberOverflow = false
	} else {
		s.startSkipValue(b)
	}
}
func (s *responseJSONScanner) startSkipValue(b byte) {
	s.skipEscape, s.skipString = false, false
	switch b {
	case '"':
		s.mode = responseJSONSkipString
	case '{', '[':
		s.mode = responseJSONSkipContainer
		s.skipDepth = 1
	default:
		s.mode = responseJSONSkipScalar
	}
}
func (s *responseJSONScanner) beginString(target responseJSONStringTarget, context responseJSONContext) {
	s.mode = responseJSONString
	s.stringTarget, s.stringContext = target, context
	s.stringLength = 0
	s.stringOverflow, s.stringEscape = false, false
}
func (s *responseJSONScanner) appendStringByte(b byte) {
	if s.stringLength < len(s.stringData) {
		s.stringData[s.stringLength] = b
		s.stringLength++
	} else {
		s.stringOverflow = true
	}
}
func (s *responseJSONScanner) pushFrame(context responseJSONContext) {
	if s.frameCount == len(s.frames) {
		s.invalid = true
		return
	}
	s.frames[s.frameCount] = responseJSONFrame{context: context, stage: responseJSONKey}
	s.frameCount++
}
func isJSONWhitespace(b byte) bool {
	return b == ' ' || b == '\n' || b == '\r' || b == '\t'
}
func isJSONDelimiter(b byte) bool {
	return isJSONWhitespace(b) || b == ',' || b == '}' || b == ']'
}
