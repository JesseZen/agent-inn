package manager

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
)

func TestManagerEventsEndpointReplaysLastEventID(t *testing.T) {
	m := New(Config{Config: config.Config{}})
	first := m.events.Publish("worker.started", map[string]any{"worker": "app"})
	m.events.Publish("worker.stopped", map[string]any{"worker": "app"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "http://manager.local/api/events", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", strconvFormatInt(first.ID))
	res := httptest.NewRecorder()
	m.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, "event: worker.stopped") || strings.Contains(body, "worker.started") {
		t.Fatalf("bad replay body: %s", body)
	}
	if !strings.Contains(res.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("bad content type: %s", res.Header().Get("Content-Type"))
	}
}

func TestWriteSSEEventFormat(t *testing.T) {
	res := httptest.NewRecorder()
	if err := writeSSEEvent(res, Event{ID: 7, Type: "config.status.changed", Payload: map[string]any{"dirty": true}}); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(strings.NewReader(res.Body.String()))
	lines := []string{}
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "id: 7") || !strings.Contains(joined, "event: config.status.changed") || !strings.Contains(joined, `"dirty":true`) {
		t.Fatalf("bad SSE event:\n%s", joined)
	}
}

func TestManagerEventsEndpointStreamsReplayThenLiveUntilCancel(t *testing.T) {
	m := New(Config{Config: config.Config{}})
	first := m.events.Publish("worker.started", map[string]any{"worker": "app"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "http://manager.local/api/events", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", strconvFormatInt(first.ID-1))

	recorder := newStreamingRecorder()
	done := make(chan struct{})
	go func() {
		m.ServeHTTP(recorder, req)
		close(done)
	}()

	requireContainsEventually(t, recorder, "event: worker.started")

	m.events.Publish("worker.stopped", map[string]any{"worker": "app"})
	requireContainsEventually(t, recorder, "event: worker.stopped")

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream to stop after cancel")
	}
}

func TestManagerEventsEndpointDeliversReplayThenLiveExactlyOnce(t *testing.T) {
	m := New(Config{Config: config.Config{}})
	first := m.events.Publish("worker.started", map[string]any{"worker": "app"})
	m.events.Publish("worker.updated", map[string]any{"worker": "app"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "http://manager.local/api/events", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", strconvFormatInt(first.ID))

	recorder := newStreamingRecorder()
	done := make(chan struct{})
	go func() {
		m.ServeHTTP(recorder, req)
		close(done)
	}()

	requireContainsEventually(t, recorder, "event: worker.updated")
	body := recorder.BodyString()
	if strings.Count(body, "event: worker.updated") != 1 {
		t.Fatalf("expected exactly one replayed update event, got:\n%s", body)
	}

	m.events.Publish("worker.stopped", map[string]any{"worker": "app"})
	requireContainsEventually(t, recorder, "event: worker.stopped")

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream to stop after cancel")
	}
}

func TestManagerEventsEndpointStopsOnCloseNotify(t *testing.T) {
	m := New(Config{Config: config.Config{}})
	req := httptest.NewRequest(http.MethodGet, "http://manager.local/api/events", nil)

	recorder := newStreamingRecorder()
	done := make(chan struct{})
	go func() {
		m.ServeHTTP(recorder, req)
		close(done)
	}()

	close(recorder.closeCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream to stop after close notify")
	}
}

func TestManagerEventsEndpointEmitsConnectionScopedResyncForExpiredCursor(t *testing.T) {
	m := New(Config{Config: config.Config{}})
	m.events = newEventBus(2)
	m.events.Publish(EventWorkerStarted, map[string]any{"worker": "one"})
	m.events.Publish(EventWorkerStarted, map[string]any{"worker": "two"})
	m.events.Publish(EventWorkerStarted, map[string]any{"worker": "three"})
	m.events.Publish(EventWorkerStarted, map[string]any{"worker": "four"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "http://manager.local/api/events", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", "1")
	res := httptest.NewRecorder()
	m.ServeHTTP(res, req)

	body := res.Body.String()
	if !strings.Contains(body, "event: manager.resync-required") || !strings.Contains(body, `{"reason":"event_cursor_expired"}`) {
		t.Fatalf("missing resync control event: %s", body)
	}
	for _, event := range m.events.Replay(0) {
		if event.Type == EventManagerResyncRequired {
			t.Fatalf("resync control event entered shared ring: %#v", event)
		}
	}
}

func TestManagerEventsEndpointReplaysNonExpiredOldestCursor(t *testing.T) {
	m := New(Config{Config: config.Config{}})
	m.events = newEventBus(2)
	m.events.Publish(EventWorkerStarted, map[string]any{"worker": "one"})
	m.events.Publish(EventWorkerStarted, map[string]any{"worker": "two"})
	m.events.Publish(EventWorkerStarted, map[string]any{"worker": "three"})
	m.events.Publish(EventWorkerStarted, map[string]any{"worker": "four"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "http://manager.local/api/events", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", "2")
	res := httptest.NewRecorder()
	m.ServeHTTP(res, req)
	body := res.Body.String()
	if strings.Contains(body, "manager.resync-required") || !strings.Contains(body, `"worker":"three"`) || !strings.Contains(body, `"worker":"four"`) {
		t.Fatalf("bad non-expired replay: %s", body)
	}
}

func TestManagerEventsPreserveDecimalIDsAboveJavaScriptSafeInteger(t *testing.T) {
	m := New(Config{Config: config.Config{}})
	m.events.nextID = 9007199254740992
	event := m.events.Publish(EventWorkerStarted, map[string]any{"worker": "app"})
	if got, want := m.events.CursorString(), "9007199254740993"; got != want {
		t.Fatalf("cursor %q, want %q", got, want)
	}
	res := httptest.NewRecorder()
	if err := writeSSEEvent(res, event); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Body.String(), "id: 9007199254740993\n") {
		t.Fatalf("SSE narrowed event ID: %s", res.Body.String())
	}
}

func strconvFormatInt(id uint64) string {
	return strconv.FormatUint(id, 10)
}

type streamingRecorder struct {
	*lockedResponseRecorder
	closeCh chan bool
}

func newStreamingRecorder() *streamingRecorder {
	return &streamingRecorder{
		lockedResponseRecorder: newLockedResponseRecorder(),
		closeCh:                make(chan bool),
	}
}

func (r *streamingRecorder) CloseNotify() <-chan bool {
	return r.closeCh
}

func requireContainsEventually(t *testing.T, recorder *streamingRecorder, needle string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		text := recorder.BodyString()
		if strings.Contains(text, needle) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in body", needle)
}
