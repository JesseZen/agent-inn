package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkerLogSinkRedactsAndKeepsRingTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker-6767.log")
	sink, err := NewWorkerLogSink(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	_, _ = sink.Write([]byte("first\n"))
	_, _ = sink.Write([]byte("Authorization: Bearer sk-secret\n"))
	_, _ = sink.Write([]byte(`{"api_key":"abc"}` + "\n"))

	lines := sink.Lines()
	if len(lines) != 2 {
		t.Fatalf("expected two ring lines, got %#v", lines)
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "sk-secret") || strings.Contains(joined, "abc") {
		t.Fatalf("ring leaked secret: %#v", lines)
	}
	if strings.Contains(joined, "first") {
		t.Fatalf("ring did not keep tail only: %#v", lines)
	}

	fileBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fileText := string(fileBytes)
	if strings.Contains(fileText, "sk-secret") || strings.Contains(fileText, "abc") {
		t.Fatalf("file leaked secret: %s", fileText)
	}
}

func TestWorkerLogSinkSimpleModeKeepsWarnAndErrorOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker-11199.log")
	sink, err := NewWorkerLogSink(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	sink.SetLevel("simple")

	_, _ = sink.Write([]byte("INFO request started\n"))
	_, _ = sink.Write([]byte("DEBUG sse chunk bytes=24\n"))
	_, _ = sink.Write([]byte("WARN upstream retrying\n"))
	_, _ = sink.Write([]byte("ERROR upstream failed\n"))

	lines := sink.Lines()
	if len(lines) != 2 {
		t.Fatalf("expected only warn/error lines in ring, got %#v", lines)
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "INFO") || strings.Contains(joined, "DEBUG") {
		t.Fatalf("simple mode kept info/debug lines: %#v", lines)
	}
	if !strings.Contains(joined, "WARN upstream retrying") || !strings.Contains(joined, "ERROR upstream failed") {
		t.Fatalf("simple mode dropped warn/error lines: %#v", lines)
	}

	fileBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fileText := string(fileBytes)
	if !strings.Contains(fileText, "WARN upstream retrying") || !strings.Contains(fileText, "ERROR upstream failed") {
		t.Fatalf("simple mode file missing warn/error lines: %s", fileText)
	}
	if strings.Contains(fileText, "INFO request started") || strings.Contains(fileText, "DEBUG sse chunk bytes=24") {
		t.Fatalf("simple mode file kept info/debug lines: %s", fileText)
	}
}

func TestWorkerLogSinkDetailModeKeepsAllLevels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker-11200.log")
	sink, err := NewWorkerLogSink(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	sink.SetLevel("detail")

	_, _ = sink.Write([]byte("INFO request started\n"))
	_, _ = sink.Write([]byte("DEBUG sse chunk bytes=24\n"))
	_, _ = sink.Write([]byte("WARN upstream retrying\n"))
	_, _ = sink.Write([]byte("ERROR upstream failed\n"))

	lines := sink.Lines()
	if len(lines) != 4 {
		t.Fatalf("expected all log levels in detail mode, got %#v", lines)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"INFO request started",
		"DEBUG sse chunk bytes=24",
		"WARN upstream retrying",
		"ERROR upstream failed",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("detail mode missing %q: %#v", want, lines)
		}
	}
}

func TestWorkerLogSinkSimpleModeParsesTimestampLedLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker-fmt.log")
	sink, err := NewWorkerLogSink(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	sink.SetLevel("simple")

	// New structured format: timestamp leads, level in column 2.
	_, _ = sink.Write([]byte("2026-07-07T14:32:01.123Z INFO  worker.proxy request.start req=a1\n"))
	_, _ = sink.Write([]byte("2026-07-07T14:32:01.200Z DEBUG worker.proxy sse.chunk req=a1\n"))
	_, _ = sink.Write([]byte("2026-07-07T14:32:03.456Z ERROR worker.proxy upstream.fail req=a1\n"))
	// Unstructured subprocess output / panic: no level, must be kept.
	_, _ = sink.Write([]byte("panic: runtime error: index out of range\n"))

	lines := sink.Lines()
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "request.start") || strings.Contains(joined, "sse.chunk") {
		t.Fatalf("simple mode kept info/debug from new format: %#v", lines)
	}
	if !strings.Contains(joined, "upstream.fail") {
		t.Fatalf("simple mode dropped error from new format: %#v", lines)
	}
	if !strings.Contains(joined, "panic: runtime error") {
		t.Fatalf("simple mode dropped unknown-severity panic line: %#v", lines)
	}
}

func TestWorkerLogSinkSubscribeReceivesRedactedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker-11201.log")
	sink, err := NewWorkerLogSink(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	sub, cancel := sink.Subscribe()
	defer cancel()

	if _, err := sink.Write([]byte("WARN Authorization: Bearer sk-secret\n")); err != nil {
		t.Fatal(err)
	}

	select {
	case line := <-sub:
		if strings.Contains(line, "sk-secret") {
			t.Fatalf("subscribe leaked secret: %q", line)
		}
		if !strings.Contains(line, "***REDACTED***") {
			t.Fatalf("subscribe did not redact line: %q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscribed line")
	}
}

func TestWorkerLogSinkSubscribeAfterCloseReturnsClosedChannel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker-closed.log")
	sink, err := NewWorkerLogSink(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	sub, cancel := sink.Subscribe()
	defer cancel()

	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("expected closed subscription channel")
		}
	default:
		t.Fatal("expected closed subscription channel to be immediately readable")
	}
}

func TestWorkerLogSinkCloseClosesActiveSubscribers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker-subscribers.log")
	sink, err := NewWorkerLogSink(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	sub, cancel := sink.Subscribe()
	defer cancel()

	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("expected active subscriber to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscriber close")
	}
}

func TestWorkerLogSinkSlowSubscriberDoesNotBlockWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker-backpressure.log")
	sink, err := NewWorkerLogSink(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	sink.SetLevel("detail")

	sub, cancel := sink.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			_, _ = sink.Write([]byte("INFO line\n"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("writes should not block on slow subscriber")
	}

	received := 0
drain:
	for {
		select {
		case <-sub:
			received++
		default:
			break drain
		}
	}
	if received == 0 {
		t.Fatal("expected subscriber to receive at least some lines")
	}
}

func TestWorkerLogSinkSnapshotAndSubscribeBridgesReplayToLive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker-bridge.log")
	sink, err := NewWorkerLogSink(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	sink.SetLevel("detail")

	if _, err := sink.Write([]byte("INFO first\n")); err != nil {
		t.Fatal(err)
	}

	lines, sub, cancel := sink.SnapshotAndSubscribe()
	defer cancel()

	if len(lines) != 1 || lines[0] != "INFO first" {
		t.Fatalf("bad snapshot: %#v", lines)
	}

	if _, err := sink.Write([]byte("INFO second\n")); err != nil {
		t.Fatal(err)
	}

	select {
	case line := <-sub:
		if line != "INFO second" {
			t.Fatalf("expected live line after snapshot, got %q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live line after snapshot")
	}
}

func TestWorkerLogSinkWriteAfterCloseIsIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker-write-after-close.log")
	sink, err := NewWorkerLogSink(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	sink.SetLevel("detail")
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := sink.Write([]byte("INFO ignored\n")); err != nil {
		t.Fatal(err)
	}
	if len(sink.Lines()) != 0 {
		t.Fatalf("expected write after close to be ignored, got %#v", sink.Lines())
	}
}
