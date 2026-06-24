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
	body := recorder.Body.String()
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

func strconvFormatInt(id int64) string {
	return strconv.FormatInt(id, 10)
}

type streamingRecorder struct {
	*httptest.ResponseRecorder
	closeCh chan bool
}

func newStreamingRecorder() *streamingRecorder {
	return &streamingRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closeCh:          make(chan bool),
	}
}

func (r *streamingRecorder) CloseNotify() <-chan bool {
	return r.closeCh
}

func requireContainsEventually(t *testing.T, recorder *streamingRecorder, needle string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		text := recorder.Body.String()
		if strings.Contains(text, needle) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in body", needle)
}
