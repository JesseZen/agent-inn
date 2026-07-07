package logging

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewFormatsFixedColumns(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "detail", ComponentWorkerProxy)

	ctx := ContextWithRequestID(context.Background(), "a1b2c3d4")
	logger.InfoContext(ctx, EventRequestStart, "method", "POST", "path", "/v1/messages")

	got := strings.TrimRight(buf.String(), "\n")
	// Level is padded for column alignment, so tokenize on collapsed
	// whitespace. Values in this line contain no spaces.
	fields := strings.Fields(got)
	if len(fields) < 4 {
		t.Fatalf("expected at least 4 columns, got %d: %q", len(fields), got)
	}
	header := struct {
		level     string
		component string
		event     string
	}{fields[1], fields[2], fields[3]}
	want := struct {
		level     string
		component string
		event     string
	}{"INFO", ComponentWorkerProxy, EventRequestStart}
	if header != want {
		t.Fatalf("header mismatch:\n got %#v\nwant %#v\n(full line %q)", header, want, got)
	}
	if _, err := time.Parse(timestampLayout, fields[0]); err != nil {
		t.Fatalf("first column is not an RFC3339 timestamp: %q (%v)", fields[0], err)
	}
	if !strings.HasSuffix(got, "req=a1b2c3d4 method=POST path=/v1/messages") {
		t.Fatalf("attrs mismatch in line: %q", got)
	}
}

func TestNewSimpleLevelDropsDebug(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "simple", ComponentManagerSuper)

	logger.Debug(EventConfigPatch, "field", "log_dir")
	logger.Info(EventWorkerSpawn, "worker", "claude", "port", "6767")

	lines := nonEmptyLines(buf.String())
	if len(lines) != 1 {
		t.Fatalf("simple mode should drop debug, got %#v", lines)
	}
	if !strings.Contains(lines[0], EventWorkerSpawn) || strings.Contains(lines[0], EventConfigPatch) {
		t.Fatalf("simple mode kept wrong line: %#v", lines)
	}
}

func TestNewRedactsAttrValues(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "detail", ComponentWorkerProxy)

	logger.Info(EventUpstreamFail, "auth", "Bearer sk-secret-token")

	if strings.Contains(buf.String(), "sk-secret-token") {
		t.Fatalf("attr value leaked secret: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "***REDACTED***") {
		t.Fatalf("attr value not redacted: %q", buf.String())
	}
}

func TestNewRedactsJSONAttrValuesBeforeQuoting(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "detail", ComponentWorkerProxy)

	logger.Error(EventModuleFail, "err", `{"api_key":"sk-live"}`)

	got := struct {
		leaked   bool
		redacted bool
	}{
		leaked:   strings.Contains(buf.String(), "sk-live"),
		redacted: strings.Contains(buf.String(), `err="{\"api_key\":\"***REDACTED***\"}"`),
	}
	want := struct {
		leaked   bool
		redacted bool
	}{}
	want.redacted = true
	if got != want {
		t.Fatalf("JSON attr redaction mismatch:\n got %#v\nwant %#v\n(log %q)", got, want, buf.String())
	}
}

func TestNewQuotesValuesWithSpaces(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "detail", ComponentManagerSuper)

	logger.Error(EventWorkerExit, "err", "connection refused")

	if !strings.Contains(buf.String(), `err="connection refused"`) {
		t.Fatalf("value with space not quoted: %q", buf.String())
	}
}

func TestNewQuotesValuesWithNewlines(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "detail", ComponentWorkerProxy)

	logger.Info(EventRequestStart, "path", "/foo\nERROR")

	lines := nonEmptyLines(buf.String())
	if len(lines) != 1 {
		t.Fatalf("expected one physical log line, got %#v", lines)
	}
	if !strings.Contains(lines[0], `path="/foo\nERROR"`) {
		t.Fatalf("newline value was not quoted: %q", lines[0])
	}
}

func TestRotatingWriterKeepsBoundedBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ainn.log")
	w, err := NewRotatingWriter(path, 16, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for i := 0; i < 20; i++ {
		if _, err := w.Write([]byte("0123456789\n")); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// active + keep backups = 3 files max: ainn.log, ainn.log.1, ainn.log.2
	if len(names) > 3 {
		t.Fatalf("rotation did not bound backups, got %#v", names)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active log missing after rotation: %v", err)
	}
}

func TestRotatingWriterWriteAfterCloseReturnsClosedError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ainn.log")
	w, err := NewRotatingWriter(path, 16, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	n, err := w.Write([]byte("late log\n"))
	if n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after close = %d, %v; want 0, %v", n, err, os.ErrClosed)
	}
}

func nonEmptyLines(s string) []string {
	out := []string{}
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
