package manager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

func (m *Manager) handleEvents(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(rw, r)
		return
	}

	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")

	afterID, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	lastID := afterID
	for _, event := range m.events.Replay(afterID) {
		if err := writeSSEEvent(rw, event); err != nil {
			return
		}
		lastID = event.ID
	}

	flusher, ok := rw.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()

	sub := m.events.Subscribe(lastID)
	defer sub.Close()

	var closeNotify <-chan bool
	if closeNotifier, ok := rw.(http.CloseNotifier); ok {
		closeNotify = closeNotifier.CloseNotify()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-closeNotify:
			return
		case event, ok := <-sub.C:
			if !ok {
				return
			}
			if err := writeSSEEvent(rw, event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (m *Manager) handleWorkerStream(rw http.ResponseWriter, r *http.Request, workerName string) {
	if r.Method != http.MethodGet {
		http.NotFound(rw, r)
		return
	}

	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")

	sink := m.LogSink(workerName)
	lines, sub, cancel := sink.SnapshotAndSubscribe()
	defer cancel()
	for _, line := range lines {
		if err := writeSSEEvent(rw, Event{Type: EventStreamRawRedacted, Payload: map[string]any{"worker": workerName, "line": line}}); err != nil {
			return
		}
	}

	flusher, ok := rw.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case line, ok := <-sub:
			if !ok {
				return
			}
			if err := writeSSEEvent(rw, Event{Type: EventStreamRawRedacted, Payload: map[string]any{"worker": workerName, "line": line}}); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSEEvent(rw http.ResponseWriter, event Event) error {
	encoded, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(rw, "id: %d\n", event.ID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(rw, "event: %s\n", event.Type); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(rw, "data: %s\n\n", encoded); err != nil {
		return err
	}
	return nil
}
