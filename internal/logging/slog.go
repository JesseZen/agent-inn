package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Line format (four fixed columns + attrs):
//
//	2006-01-02T15:04:05.000Z INFO  worker.proxy  request.start  req=a1b2 method=POST
//
// The slog message IS the event name (column 4); the component (column 3) is
// bound at handler construction. This is the shape EVENTS.md documents and the
// shape WorkerLogSink's severity parser reads.
const (
	timestampLayout = "2006-01-02T15:04:05.000Z07:00"
	levelWidth      = 5
)

type handler struct {
	mu        *sync.Mutex
	w         io.Writer
	level     slog.Level
	component string
	attrs     []slog.Attr
}

// New builds a logger that writes the standard line format to w. level is the
// same "simple"|"detail" string used everywhere else: simple emits INFO and
// above, detail emits everything. component is the dot-namespaced origin.
func New(w io.Writer, level string, component string) *slog.Logger {
	return slog.New(&handler{
		mu:        &sync.Mutex{},
		w:         w,
		level:     slogLevel(level),
		component: component,
	})
}

func slogLevel(level string) slog.Level {
	if normalizeLevel(level) == "detail" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	var b strings.Builder
	b.WriteString(record.Time.UTC().Format(timestampLayout))
	b.WriteByte(' ')
	b.WriteString(padLevel(record.Level))
	b.WriteByte(' ')
	b.WriteString(h.component)
	b.WriteByte(' ')
	b.WriteString(record.Message)

	var attrs strings.Builder
	if id := RequestIDFromContext(ctx); id != "" {
		writeAttr(&attrs, "req", slog.StringValue(id))
	}
	for _, attr := range h.attrs {
		writeAttr(&attrs, attr.Key, attr.Value)
	}
	record.Attrs(func(attr slog.Attr) bool {
		writeAttr(&attrs, attr.Key, attr.Value)
		return true
	})
	if attrs.Len() > 0 {
		b.WriteByte(' ')
		b.WriteString(Redact(attrs.String()))
	}
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &clone
}

// WithGroup is unused by this codebase; groups would complicate the flat
// key=value format, so the handler ignores them.
func (h *handler) WithGroup(string) slog.Handler { return h }

func padLevel(level slog.Level) string {
	name := level.String()
	for len(name) < levelWidth {
		name += " "
	}
	return name
}

func writeAttr(b *strings.Builder, key string, value slog.Value) {
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	b.WriteString(key)
	b.WriteByte('=')
	b.WriteString(formatValue(value.String()))
}

func formatValue(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\"") {
		return strconv.Quote(s)
	}
	return s
}

type requestIDKey struct{}

// ContextWithRequestID carries a correlation id so every log line derived from
// ctx is tagged req=<id> without threading it through each call.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// NewRequestID returns a short correlation id (8 hex chars) for one request.
func NewRequestID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf[:])
}
