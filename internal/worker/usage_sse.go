package worker

const messageDeltaEvent = "message_delta"

type usageSSEScanner struct {
	observer            *UsageObserver
	jsonScanner         *responseJSONScanner
	lineMode            usageSSELineMode
	fieldData           [5]byte
	fieldLength         int
	fieldOverflow       bool
	lineHasBytes        bool
	lineNonWhitespace   bool
	valueStarted        bool
	eventValueLength    int
	eventValueMatches   bool
	eventValueNonEmpty  bool
	eventIsMessageDelta bool
	dataSeen            bool
	skipLF              bool
}

type usageSSELineMode uint8

const (
	usageSSEField usageSSELineMode = iota
	usageSSEData
	usageSSEEvent
	usageSSEIgnore
)

func (s *usageSSEScanner) Write(data []byte) {
	for _, b := range data {
		if s.skipLF {
			s.skipLF = false
			if b == '\n' {
				continue
			}
		}
		switch b {
		case '\n':
			s.finishLine()
		case '\r':
			s.finishLine()
			s.skipLF = true
		default:
			s.consumeLineByte(b)
		}
	}
}

func (s *usageSSEScanner) Finish() {
	if s.lineHasBytes {
		s.finishLine()
	}
	if s.dataSeen {
		s.finishEvent()
	}
}

func (s *usageSSEScanner) consumeLineByte(b byte) {
	s.lineHasBytes = true
	if b != ' ' && b != '\t' {
		s.lineNonWhitespace = true
	}
	if s.lineMode == usageSSEField {
		if b == ':' {
			s.startValue()
			return
		}
		if s.fieldLength < len(s.fieldData) {
			s.fieldData[s.fieldLength] = b
			s.fieldLength++
		} else {
			s.fieldOverflow = true
		}
		return
	}
	if !s.valueStarted {
		s.valueStarted = true
		if b == ' ' {
			return
		}
	}
	s.consumeValueByte(b)
}

func (s *usageSSEScanner) startValue() {
	if s.fieldOverflow {
		s.lineMode = usageSSEIgnore
		return
	}
	switch string(s.fieldData[:s.fieldLength]) {
	case "data":
		s.lineMode = usageSSEData
		s.startDataLine()
	case "event":
		s.lineMode = usageSSEEvent
		s.eventValueLength = 0
		s.eventValueMatches = true
		s.eventValueNonEmpty = false
	default:
		s.lineMode = usageSSEIgnore
	}
}

func (s *usageSSEScanner) consumeValueByte(b byte) {
	switch s.lineMode {
	case usageSSEData:
		s.jsonScanner.Write([]byte{b})
	case usageSSEEvent:
		s.eventValueNonEmpty = true
		if s.eventValueLength >= len(messageDeltaEvent) || b != messageDeltaEvent[s.eventValueLength] {
			s.eventValueMatches = false
		}
		s.eventValueLength++
	}
}

func (s *usageSSEScanner) finishLine() {
	if !s.lineNonWhitespace {
		s.finishEvent()
		s.resetLine()
		return
	}
	if s.lineMode == usageSSEField && !s.fieldOverflow {
		switch string(s.fieldData[:s.fieldLength]) {
		case "data":
			s.startDataLine()
		case "event":
		}
	}
	if s.lineMode == usageSSEEvent && s.eventValueNonEmpty {
		s.eventIsMessageDelta = s.eventValueMatches && s.eventValueLength == len(messageDeltaEvent)
	}
	s.resetLine()
}

func (s *usageSSEScanner) startDataLine() {
	if s.jsonScanner == nil {
		s.jsonScanner = &responseJSONScanner{}
	} else {
		s.jsonScanner.Write([]byte{'\n'})
	}
	s.dataSeen = true
}

func (s *usageSSEScanner) finishEvent() {
	if s.dataSeen {
		metadata := s.jsonScanner.Finish()
		s.observer.processSSEMetadata(s.eventIsMessageDelta, metadata)
	}
	s.jsonScanner = nil
	s.eventIsMessageDelta = false
	s.dataSeen = false
}

func (s *usageSSEScanner) resetLine() {
	s.lineMode = usageSSEField
	s.fieldLength = 0
	s.fieldOverflow = false
	s.lineHasBytes = false
	s.lineNonWhitespace = false
	s.valueStarted = false
	s.eventValueLength = 0
	s.eventValueMatches = false
	s.eventValueNonEmpty = false
}
