package worker

import (
	"sync"
	"time"
)

const MetricsWindowSeconds = 60
const errorStatusFloor = 400

type RequestMetricEvent struct {
	Timestamp     time.Time   `json:"timestamp"`
	Model         string      `json:"model,omitempty"`
	Method        string      `json:"method"`
	Path          string      `json:"path"`
	Status        int         `json:"status"`
	DurationMS    int64       `json:"duration_ms"`
	ResponseBytes int64       `json:"response_bytes"`
	Usage         UsageTokens `json:"usage"`
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
