package worker

import (
	"sync"
	"time"
)

const MetricsWindowSeconds = 60
const errorStatusFloor = 400

type RequestMetricEvent struct {
	Timestamp     time.Time
	Method        string
	Path          string
	Status        int
	DurationMS    int64
	ResponseBytes int64
	Usage         UsageTokens
}

type MetricsSnapshot struct {
	WindowSeconds        int64
	InFlight             int64
	Requests             int64
	Errors               int64
	RPM                  int64
	TPM                  int64
	AvgLatencyMS         int64
	InputTokens          int64
	OutputTokens         int64
	TotalTokens          int64
	UnknownUsageRequests int64
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

	if m.inFlight > 0 {
		m.inFlight--
	}

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
