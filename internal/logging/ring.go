package logging

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type WorkerLogSink struct {
	mu          sync.Mutex
	writer      io.WriteCloser
	capacity    int
	level       string
	lines       []string
	pending     bytes.Buffer
	subscribers map[chan string]struct{}
	closed      bool
}

func NewWorkerLogSink(path string, capacity int) (*WorkerLogSink, error) {
	return newWorkerLogSink(path, capacity, DefaultRotateMaxBytes, DefaultRotateKeep)
}

func newWorkerLogSink(path string, capacity int, maxBytes int64, keep int) (*WorkerLogSink, error) {
	if capacity <= 0 {
		capacity = 1000
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	writer, err := NewRotatingWriter(path, maxBytes, keep)
	if err != nil {
		return nil, err
	}
	return &WorkerLogSink{
		writer:      writer,
		capacity:    capacity,
		level:       "simple",
		subscribers: make(map[chan string]struct{}),
	}, nil
}

func (s *WorkerLogSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	written := len(p)
	if s.closed {
		return written, nil
	}
	for len(p) > 0 {
		index := bytes.IndexByte(p, '\n')
		if index == -1 {
			_, _ = s.pending.Write(p)
			break
		}
		_, _ = s.pending.Write(p[:index])
		s.appendLineLocked(s.pending.String())
		s.pending.Reset()
		p = p[index+1:]
	}
	return written, nil
}

func (s *WorkerLogSink) SetLevel(level string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.level = normalizeLevel(level)
}

func (s *WorkerLogSink) Lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.lines))
	out = append(out, s.lines...)
	if s.pending.Len() > 0 {
		out = append(out, Redact(s.pending.String()))
	}
	return out
}

func (s *WorkerLogSink) Subscribe() (<-chan string, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan string, 64)
	if s.closed {
		close(ch)
		return ch, func() {}
	}
	if s.subscribers == nil {
		s.subscribers = make(map[chan string]struct{})
	}
	s.subscribers[ch] = struct{}{}

	cancelled := false
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if cancelled {
			return
		}
		cancelled = true
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
	}

	return ch, cancel
}

func (s *WorkerLogSink) SnapshotAndSubscribe() ([]string, <-chan string, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lines := append([]string(nil), s.lines...)
	if s.pending.Len() > 0 {
		lines = append(lines, Redact(s.pending.String()))
	}

	ch := make(chan string, 64)
	if s.closed {
		close(ch)
		return lines, ch, func() {}
	}
	if s.subscribers == nil {
		s.subscribers = make(map[chan string]struct{})
	}
	s.subscribers[ch] = struct{}{}

	cancelled := false
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if cancelled {
			return
		}
		cancelled = true
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
	}

	return lines, ch, cancel
}

func (s *WorkerLogSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.pending.Len() > 0 {
		s.appendLineLocked(s.pending.String())
		s.pending.Reset()
	}
	if s.writer == nil {
		for ch := range s.subscribers {
			delete(s.subscribers, ch)
			close(ch)
		}
		return nil
	}
	err := s.writer.Close()
	s.writer = nil
	for ch := range s.subscribers {
		delete(s.subscribers, ch)
		close(ch)
	}
	return err
}

func (s *WorkerLogSink) appendLineLocked(line string) {
	line = Redact(strings.TrimRight(line, "\r"))
	if !shouldKeepLine(line, s.level) {
		return
	}
	s.lines = append(s.lines, line)
	if len(s.lines) > s.capacity {
		s.lines = append([]string(nil), s.lines[len(s.lines)-s.capacity:]...)
	}
	if s.writer != nil {
		_, _ = s.writer.Write([]byte(line + "\n"))
	}
	for ch := range s.subscribers {
		select {
		case ch <- line:
		default:
		}
	}
}

func normalizeLevel(level string) string {
	if level == "detail" {
		return "detail"
	}
	return "simple"
}

func shouldKeepLine(line string, level string) bool {
	if normalizeLevel(level) == "detail" {
		return true
	}
	if isWorkerLifecycleLine(line) {
		return true
	}
	switch logSeverity(line) {
	case "ERROR", "WARN", "UNKNOWN":
		return true
	default:
		return false
	}
}

func isWorkerLifecycleLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) > 1 && fields[1] == ComponentWorkerLife {
		return true
	}
	return len(fields) > 2 && looksLikeTimestamp(fields[0]) && fields[2] == ComponentWorkerLife
}

// logSeverity extracts the level from a log line. Structured lines from this
// package lead with a timestamp and carry the level in column 2; raw subprocess
// stdout/stderr and panics carry it in column 1 (or not at all). It checks both
// positions so simple-mode filtering works for either shape; anything without a
// recognizable level is UNKNOWN and kept (so stack traces and third-party output
// are never silently dropped).
func logSeverity(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "UNKNOWN"
	}
	if level := knownLevel(fields[0]); level != "" {
		return level
	}
	if len(fields) > 1 && looksLikeTimestamp(fields[0]) {
		if level := knownLevel(fields[1]); level != "" {
			return level
		}
	}
	return "UNKNOWN"
}

func knownLevel(token string) string {
	switch token {
	case "ERROR", "WARN", "INFO", "DEBUG":
		return token
	default:
		return ""
	}
}

func looksLikeTimestamp(token string) bool {
	_, err := time.Parse(timestampLayout, token)
	return err == nil
}
