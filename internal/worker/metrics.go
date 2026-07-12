package worker

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const MetricsWindowSeconds = 60
const errorStatusFloor = 400
const metricsEventQueueSize = 256

type UpstreamFailureKind string

const (
	UpstreamFailureTransport        UpstreamFailureKind = "transport"
	UpstreamFailureStatus           UpstreamFailureKind = "upstream_status"
	UpstreamFailureFirstByteTimeout UpstreamFailureKind = "first_byte_timeout"
	UpstreamFailureIdleTimeout      UpstreamFailureKind = "idle_timeout"
)

type UpstreamFailure struct {
	Kind            UpstreamFailureKind `json:"kind"`
	BeforeFirstByte bool                `json:"before_first_byte"`
	StatusCode      int                 `json:"status_code,omitempty"`
}

type RequestMetricEvent struct {
	Timestamp          time.Time        `json:"timestamp"`
	SnapshotGeneration int              `json:"snapshot_generation"`
	Upstream           string           `json:"upstream"`
	Model              string           `json:"model,omitempty"`
	Method             string           `json:"method"`
	Path               string           `json:"path"`
	Status             int              `json:"status"`
	DurationMS         int64            `json:"duration_ms"`
	ResponseBytes      int64            `json:"response_bytes"`
	Usage              UsageTokens      `json:"usage"`
	Failure            *UpstreamFailure `json:"failure,omitempty"`
}

type MetricsSnapshot struct {
	WindowSeconds        int   `json:"window_seconds"`
	InFlight             int64 `json:"in_flight"`
	Requests             int64 `json:"requests"`
	Errors               int64 `json:"errors"`
	RPM                  int64 `json:"rpm"`
	TPM                  int64 `json:"tpm"`
	AvgLatencyMS         int64 `json:"avg_latency_ms"`
	InputTokens          int64 `json:"input_tokens"`
	OutputTokens         int64 `json:"output_tokens"`
	CacheReadTokens      int64 `json:"cache_read_tokens"`
	CacheWriteTokens     int64 `json:"cache_write_tokens"`
	ReasoningTokens      int64 `json:"reasoning_tokens"`
	TotalTokens          int64 `json:"total_tokens"`
	UnknownUsageRequests int64 `json:"unknown_usage_requests"`
	DroppedEvents        int64 `json:"dropped_events"`
}

type MetricsTracker struct {
	mu       sync.Mutex
	clock    func() time.Time
	inFlight int64
	buckets  [MetricsWindowSeconds]metricsBucket
}

type metricsBucket struct {
	second               int64
	requests             int64
	errors               int64
	durationMS           int64
	responseBytes        int64
	inputTokens          int64
	outputTokens         int64
	cacheReadTokens      int64
	cacheWriteTokens     int64
	reasoningTokens      int64
	totalTokens          int64
	unknownUsageRequests int64
}

type metricsEventEmitter struct {
	mu          sync.Mutex
	pending     chan RequestMetricEvent
	writer      io.Writer
	closer      io.Closer
	done        chan struct{}
	closeWriter sync.Once
	closed      bool
	dropped     atomic.Int64
}

func newMetricsEventEmitter(writer io.Writer) *metricsEventEmitter {
	if writer == nil {
		return nil
	}
	emitter := &metricsEventEmitter{
		pending: make(chan RequestMetricEvent, metricsEventQueueSize),
		writer:  writer,
		done:    make(chan struct{}),
	}
	emitter.closer, _ = writer.(io.Closer)
	go emitter.run()
	return emitter
}

func (e *metricsEventEmitter) run() {
	defer close(e.done)
	encoder := json.NewEncoder(e.writer)
	failed := false
	for event := range e.pending {
		if failed {
			e.dropped.Add(1)
			continue
		}
		if err := encoder.Encode(event); err != nil {
			e.dropped.Add(1)
			failed = true
		}
	}
	e.closeMetricsWriter()
}

func (e *metricsEventEmitter) Emit(event RequestMetricEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		e.dropped.Add(1)
		return
	}
	select {
	case e.pending <- event:
	default:
		e.dropped.Add(1)
	}
}

func (e *metricsEventEmitter) Close(ctx context.Context) {
	e.mu.Lock()
	if !e.closed {
		e.closed = true
		close(e.pending)
	}
	e.mu.Unlock()
	select {
	case <-e.done:
	case <-ctx.Done():
		e.closeMetricsWriter()
	}
}

func (e *metricsEventEmitter) closeMetricsWriter() {
	if e.closer != nil {
		e.closeWriter.Do(func() { _ = e.closer.Close() })
	}
}

func (w *Worker) Close(ctx context.Context) {
	finished := make(chan struct{})
	go func() {
		w.metricFinishes.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-ctx.Done():
	}
	if w.metricsEmitter != nil {
		w.metricsEmitter.Close(ctx)
	}
}

func NewMetricsTracker(clock func() time.Time) *MetricsTracker {
	return &MetricsTracker{clock: clock}
}

func (m *MetricsTracker) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.inFlight++
}

func (m *MetricsTracker) Finish(event RequestMetricEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.inFlight--

	second := event.Timestamp.Unix()
	bucket := &m.buckets[second%MetricsWindowSeconds]
	if bucket.second != second {
		*bucket = metricsBucket{second: second}
	}

	bucket.requests++
	if event.Status >= errorStatusFloor {
		bucket.errors++
	}
	bucket.durationMS += event.DurationMS
	bucket.responseBytes += event.ResponseBytes
	if event.Usage.Known {
		bucket.inputTokens += event.Usage.InputTokens
		bucket.outputTokens += event.Usage.OutputTokens
		bucket.cacheReadTokens += event.Usage.CacheReadTokens
		bucket.cacheWriteTokens += event.Usage.CacheWriteTokens
		bucket.reasoningTokens += event.Usage.ReasoningTokens
		bucket.totalTokens += event.Usage.TotalTokens
	} else {
		bucket.unknownUsageRequests++
	}
}

func (m *MetricsTracker) Snapshot() MetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock().Unix()
	snapshot := MetricsSnapshot{
		WindowSeconds: MetricsWindowSeconds,
		InFlight:      m.inFlight,
	}
	var durationMS int64
	for _, bucket := range m.buckets {
		if now-bucket.second >= MetricsWindowSeconds {
			continue
		}
		snapshot.Requests += bucket.requests
		snapshot.Errors += bucket.errors
		durationMS += bucket.durationMS
		snapshot.InputTokens += bucket.inputTokens
		snapshot.OutputTokens += bucket.outputTokens
		snapshot.CacheReadTokens += bucket.cacheReadTokens
		snapshot.CacheWriteTokens += bucket.cacheWriteTokens
		snapshot.ReasoningTokens += bucket.reasoningTokens
		snapshot.TotalTokens += bucket.totalTokens
		snapshot.UnknownUsageRequests += bucket.unknownUsageRequests
	}
	snapshot.RPM = snapshot.Requests
	snapshot.TPM = snapshot.TotalTokens
	if snapshot.Requests > 0 {
		snapshot.AvgLatencyMS = durationMS / snapshot.Requests
	}
	return snapshot
}
