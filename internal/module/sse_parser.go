package module

import (
	"bytes"
	"strings"
)

type SSEEvent struct {
	Event string
	Data  string
	Done  bool
}

type SSEParser struct {
	pending []byte
}

func (p *SSEParser) Push(chunk []byte, eof bool) ([]SSEEvent, error) {
	if len(chunk) > 0 {
		p.pending = append(p.pending, chunk...)
	}

	var events []SSEEvent
	for {
		frame, rest, ok := splitSSEFrame(p.pending)
		if !ok {
			break
		}
		p.pending = rest
		if event, ok := parseSSEFrame(frame); ok {
			events = append(events, event)
		}
	}

	if eof && len(bytes.TrimSpace(p.pending)) > 0 {
		if event, ok := parseSSEFrame(p.pending); ok {
			events = append(events, event)
		}
		p.pending = nil
	}
	return events, nil
}

func splitSSEFrame(data []byte) (frame []byte, rest []byte, ok bool) {
	for lineStart := 0; lineStart < len(data); {
		lineEnd := lineStart
		for lineEnd < len(data) && data[lineEnd] != '\n' && data[lineEnd] != '\r' {
			lineEnd++
		}
		if lineEnd == len(data) {
			return nil, data, false
		}

		next := lineEnd + 1
		if data[lineEnd] == '\r' && next < len(data) && data[next] == '\n' {
			next++
		}

		line := data[lineStart:lineEnd]
		if len(bytes.TrimSpace(line)) == 0 {
			return data[:lineStart], data[next:], true
		}
		lineStart = next
	}
	return nil, data, false
}

func parseSSEFrame(frame []byte) (SSEEvent, bool) {
	normalized := strings.ReplaceAll(string(frame), "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")

	eventType := "message"
	var dataLines []string
	nonCommentFields := 0

	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		nonCommentFields++
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			field = line
			value = ""
		} else if strings.HasPrefix(value, " ") {
			value = value[1:]
		}

		switch field {
		case "event":
			if value != "" {
				eventType = value
			}
		case "data":
			dataLines = append(dataLines, value)
		}
	}

	if nonCommentFields == 0 || len(dataLines) == 0 {
		return SSEEvent{}, false
	}

	data := strings.Join(dataLines, "\n")
	return SSEEvent{
		Event: eventType,
		Data:  data,
		Done:  data == "[DONE]",
	}, true
}
