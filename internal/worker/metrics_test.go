package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMetricsTrackerRecordsWindowSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.Local)
	clock := func() time.Time { return now }
	tracker := NewMetricsTracker(clock)

	tracker.Start()
	tracker.Finish(RequestMetricEvent{
		Timestamp:     now,
		Upstream:      "openai",
		Method:        http.MethodPost,
		Path:          "/v1/responses",
		Status:        http.StatusOK,
		DurationMS:    250,
		ResponseBytes: 512,
		Usage: UsageTokens{
			Known:            true,
			InputTokens:      100,
			OutputTokens:     40,
			CacheReadTokens:  30,
			CacheWriteTokens: 20,
			ReasoningTokens:  10,
			TotalTokens:      200,
		},
	})
	tracker.Start()
	tracker.Finish(RequestMetricEvent{
		Timestamp:  now,
		Upstream:   "openai",
		Method:     http.MethodPost,
		Path:       "/v1/responses",
		Status:     http.StatusTooManyRequests,
		DurationMS: 750,
		Usage:      UsageTokens{Known: false},
	})

	want := MetricsSnapshot{
		WindowSeconds:        MetricsWindowSeconds,
		InFlight:             0,
		Requests:             2,
		Errors:               1,
		RPM:                  2,
		TPM:                  200,
		AvgLatencyMS:         500,
		InputTokens:          100,
		OutputTokens:         40,
		CacheReadTokens:      30,
		CacheWriteTokens:     20,
		ReasoningTokens:      10,
		TotalTokens:          200,
		UnknownUsageRequests: 1,
	}
	if got := tracker.Snapshot(); got != want {
		t.Fatalf("bad metrics snapshot:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsTrackerExpiresOldBuckets(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.Local)
	clock := func() time.Time { return now }
	tracker := NewMetricsTracker(clock)

	tracker.Start()
	tracker.Finish(RequestMetricEvent{
		Timestamp:  now,
		Upstream:   "openai",
		Method:     http.MethodPost,
		Path:       "/v1/responses",
		Status:     http.StatusTooManyRequests,
		DurationMS: 250,
		Usage: UsageTokens{
			Known:            true,
			InputTokens:      100,
			OutputTokens:     40,
			CacheReadTokens:  30,
			CacheWriteTokens: 20,
			ReasoningTokens:  10,
			TotalTokens:      200,
		},
	})

	now = now.Add(61 * time.Second)

	want := MetricsSnapshot{
		WindowSeconds: MetricsWindowSeconds,
	}
	if got := tracker.Snapshot(); got != want {
		t.Fatalf("bad metrics snapshot:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestWorkerMetricsJSONContract(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	event := RequestMetricEvent{
		Timestamp:     now,
		Upstream:      "openai",
		Model:         "gpt-5",
		Method:        http.MethodPost,
		Path:          "/v1/responses",
		Status:        http.StatusOK,
		DurationMS:    250,
		ResponseBytes: 512,
		Usage: UsageTokens{
			Known:            true,
			InputTokens:      100,
			OutputTokens:     40,
			CacheReadTokens:  30,
			CacheWriteTokens: 20,
			ReasoningTokens:  10,
			TotalTokens:      200,
		},
	}
	gotEvent, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	wantEvent := `{"timestamp":"2026-07-10T03:00:00Z","upstream":"openai","model":"gpt-5","method":"POST","path":"/v1/responses","status":200,"duration_ms":250,"response_bytes":512,"usage":{"usage_known":true,"input_tokens":100,"output_tokens":40,"cache_read_tokens":30,"cache_write_tokens":20,"reasoning_tokens":10,"total_tokens":200}}`
	if string(gotEvent) != wantEvent {
		t.Fatalf("bad request metric JSON:\ngot  %s\nwant %s", gotEvent, wantEvent)
	}

	snapshot := MetricsSnapshot{
		WindowSeconds:        MetricsWindowSeconds,
		InFlight:             1,
		Requests:             2,
		Errors:               1,
		RPM:                  2,
		TPM:                  200,
		AvgLatencyMS:         500,
		InputTokens:          100,
		OutputTokens:         40,
		CacheReadTokens:      30,
		CacheWriteTokens:     20,
		ReasoningTokens:      10,
		TotalTokens:          200,
		UnknownUsageRequests: 1,
	}
	gotSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	wantSnapshot := `{"window_seconds":60,"in_flight":1,"requests":2,"errors":1,"rpm":2,"tpm":200,"avg_latency_ms":500,"input_tokens":100,"output_tokens":40,"cache_read_tokens":30,"cache_write_tokens":20,"reasoning_tokens":10,"total_tokens":200,"unknown_usage_requests":1,"dropped_events":0}`
	if string(gotSnapshot) != wantSnapshot {
		t.Fatalf("bad metrics snapshot JSON:\ngot  %s\nwant %s", gotSnapshot, wantSnapshot)
	}
}

func TestMetricsEventEmitterCountsWriterFailureAndUndrainedEvents(t *testing.T) {
	writer := &failingMetricsWriteCloser{}
	emitter := newMetricsEventEmitter(writer)
	for i := range 3 {
		emitter.Emit(RequestMetricEvent{Path: string(rune('a' + i))})
	}

	emitter.Close(context.Background())

	got := struct {
		Writes  int
		Closes  int
		Dropped int64
	}{writer.writes, writer.closes, emitter.dropped.Load()}
	want := struct {
		Writes  int
		Closes  int
		Dropped int64
	}{1, 1, 3}
	if got != want {
		t.Fatalf("bad failed delivery lifecycle:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsEventEmitterDrainsQueuedEventsAndClosesWriter(t *testing.T) {
	writer := &gatedMetricsWriteCloser{entered: make(chan struct{}), release: make(chan struct{})}
	emitter := newMetricsEventEmitter(writer)
	want := []RequestMetricEvent{{Path: "a"}, {Path: "b"}, {Path: "c"}}
	emitter.Emit(want[0])
	<-writer.entered
	emitter.Emit(want[1])
	emitter.Emit(want[2])

	done := make(chan struct{})
	go func() {
		emitter.Close(context.Background())
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("emitter close returned before queued events drained")
	case <-time.After(20 * time.Millisecond):
	}
	close(writer.release)
	<-done

	lines := bytes.Split(bytes.TrimSpace(writer.Bytes()), []byte("\n"))
	got := make([]RequestMetricEvent, len(lines))
	for i, line := range lines {
		if err := json.Unmarshal(line, &got[i]); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad drained events:\ngot  %#v\nwant %#v", got, want)
	}
	if writer.CloseCount() != 1 || emitter.dropped.Load() != 0 {
		t.Fatalf("bad healthy close: closes=%d dropped=%d", writer.CloseCount(), emitter.dropped.Load())
	}
}

func TestMetricsEventEmitterCancellationCountsUndrainedEvents(t *testing.T) {
	writer := &cancelBlockingMetricsWriteCloser{entered: make(chan struct{}), closed: make(chan struct{})}
	emitter := newMetricsEventEmitter(writer)
	emitter.Emit(RequestMetricEvent{Path: "a"})
	<-writer.entered
	emitter.Emit(RequestMetricEvent{Path: "b"})
	emitter.Emit(RequestMetricEvent{Path: "c"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	emitter.Close(ctx)

	if writer.CloseCount() != 1 || emitter.dropped.Load() != 3 {
		t.Fatalf("bad canceled close: closes=%d dropped=%d", writer.CloseCount(), emitter.dropped.Load())
	}
}

func TestMetricsEventEmitterConcurrentCloseAndEmitDropsWithoutPanic(t *testing.T) {
	emitter := newMetricsEventEmitter(io.Discard)
	const (
		senderCount = 32
		emitCount   = 1024
	)
	start := make(chan struct{})
	var panics atomic.Int64
	var senders sync.WaitGroup
	senders.Add(senderCount)
	for range senderCount {
		go func() {
			defer senders.Done()
			<-start
			for range emitCount {
				func() {
					defer func() {
						if recover() != nil {
							panics.Add(1)
						}
					}()
					emitter.Emit(RequestMetricEvent{})
				}()
			}
		}()
	}
	closed := make(chan struct{})
	go func() {
		<-start
		emitter.Close(context.Background())
		close(closed)
	}()

	close(start)
	senders.Wait()
	<-closed

	if got := panics.Load(); got != 0 {
		t.Fatalf("concurrent close/send panicked %d times", got)
	}
	dropped := emitter.dropped.Load()
	emitter.Emit(RequestMetricEvent{})
	if got := emitter.dropped.Load(); got != dropped+1 {
		t.Fatalf("post-close emit did not increment dropped count: got %d want %d", got, dropped+1)
	}
}

type failingMetricsWriteCloser struct {
	writes int
	closes int
}

func (w *failingMetricsWriteCloser) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("metrics pipe failed")
}

func (w *failingMetricsWriteCloser) Close() error {
	w.closes++
	return nil
}

type gatedMetricsWriteCloser struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	buf     bytes.Buffer
	closes  int
}

func (w *gatedMetricsWriteCloser) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *gatedMetricsWriteCloser) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closes++
	return nil
}

func (w *gatedMetricsWriteCloser) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...)
}

func (w *gatedMetricsWriteCloser) CloseCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closes
}

type cancelBlockingMetricsWriteCloser struct {
	entered   chan struct{}
	closed    chan struct{}
	writeOnce sync.Once
	closeOnce sync.Once
	mu        sync.Mutex
	closes    int
}

func (w *cancelBlockingMetricsWriteCloser) Write([]byte) (int, error) {
	w.writeOnce.Do(func() { close(w.entered) })
	<-w.closed
	return 0, errors.New("metrics writer closed")
}

func (w *cancelBlockingMetricsWriteCloser) Close() error {
	w.closeOnce.Do(func() { close(w.closed) })
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closes++
	return nil
}

func (w *cancelBlockingMetricsWriteCloser) CloseCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closes
}
