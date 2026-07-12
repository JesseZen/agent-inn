package manager

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jesse/agent-inn/internal/logging"
	"github.com/jesse/agent-inn/internal/worker"
)

const (
	MetricsRangeToday          MetricsRangeName = "today"
	MetricsRangeLast24H        MetricsRangeName = "last_24h"
	metricsUpdatedPublishDelay                  = 100 * time.Millisecond
)

type MetricsRangeName string

type MetricsRecord struct {
	Timestamp        time.Time `json:"timestamp"`
	Worker           string    `json:"worker"`
	Port             int       `json:"port"`
	Upstream         string    `json:"upstream,omitempty"`
	Model            string    `json:"model,omitempty"`
	Method           string    `json:"method"`
	Path             string    `json:"path"`
	Status           int       `json:"status"`
	DurationMS       int64     `json:"duration_ms"`
	ResponseBytes    int64     `json:"response_bytes"`
	UsageKnown       bool      `json:"usage_known"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	ReasoningTokens  int64     `json:"reasoning_tokens"`
	TotalTokens      int64     `json:"total_tokens"`
}

type MetricsQuery struct {
	Range    MetricsRangeName
	Worker   string
	Upstream string
	Model    string
	Path     string
	Status   int
}

type MetricsRange struct {
	Name  MetricsRangeName `json:"name"`
	Start time.Time        `json:"start"`
	End   time.Time        `json:"end"`
}

type MetricsTotals struct {
	Requests             int64 `json:"requests"`
	Errors               int64 `json:"errors"`
	AvgLatencyMS         int64 `json:"avg_latency_ms"`
	ResponseBytes        int64 `json:"response_bytes"`
	InputTokens          int64 `json:"input_tokens"`
	OutputTokens         int64 `json:"output_tokens"`
	CacheReadTokens      int64 `json:"cache_read_tokens"`
	CacheWriteTokens     int64 `json:"cache_write_tokens"`
	ReasoningTokens      int64 `json:"reasoning_tokens"`
	TotalTokens          int64 `json:"total_tokens"`
	UnknownUsageRequests int64 `json:"unknown_usage_requests"`
}

type WorkerMetricsAggregate struct {
	Worker        string                 `json:"worker"`
	Port          int                    `json:"port"`
	Status        string                 `json:"status"`
	Upstream      string                 `json:"upstream,omitempty"`
	LiveAvailable bool                   `json:"live_available"`
	Live          worker.MetricsSnapshot `json:"live"`
	Totals        MetricsTotals          `json:"totals"`
}

type MetricsQueryResponse struct {
	Range             MetricsRange             `json:"range"`
	Workers           []WorkerMetricsAggregate `json:"workers"`
	SkippedRecords    int                      `json:"skipped_records"`
	QueryLimited      bool                     `json:"query_limited"`
	PersistenceErrors uint64                   `json:"persistence_errors"`
}

type workerMetricSource struct {
	name string
	port int
}

type pendingMetricsUpdate struct {
	port    int
	metrics worker.MetricsSnapshot
	timer   *time.Timer
}

func metricsStatusFromQuery(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	status, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid status")
	}
	return status, nil
}

func (m *Manager) readWorkerMetrics(name string, r io.Reader) {
	m.mu.RLock()
	workerConfig, ok := m.config.Workers[name]
	m.mu.RUnlock()
	if !ok {
		return
	}
	m.readWorkerMetricsFrom(workerMetricSource{name: name, port: workerConfig.Port}, r)
}

func (m *Manager) readWorkerMetricsFrom(source workerMetricSource, r io.Reader) {
	skipped, _ := readJSONLLines(r, func(line []byte) {
		var event worker.RequestMetricEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return
		}
		m.handleWorkerMetricEventFrom(source, event)
	})
	if skipped == 0 {
		return
	}
	m.mu.RLock()
	store := m.metricsStore
	m.mu.RUnlock()
	if store != nil {
		store.persistenceErrors.Add(uint64(skipped))
	}
	m.logger.Error(logging.EventMetricsPersist,
		"worker", source.name,
		"port", source.port,
		"err", "metrics event exceeds size limit",
	)
}

func (m *Manager) handleWorkerMetricEvent(name string, event worker.RequestMetricEvent) {
	m.mu.Lock()
	workerConfig, ok := m.config.Workers[name]
	if !ok {
		m.mu.Unlock()
		return
	}
	source := workerMetricSource{name: name, port: workerConfig.Port}
	m.mu.Unlock()
	m.handleWorkerMetricEventFrom(source, event)
}

func (m *Manager) handleWorkerMetricEventFrom(source workerMetricSource, event worker.RequestMetricEvent) {
	m.mu.Lock()
	tracker := m.metricsTrackers[source.name]
	if tracker == nil {
		tracker = worker.NewMetricsTracker(m.clock)
		m.metricsTrackers[source.name] = tracker
	}
	tracker.Start()
	tracker.Finish(event)
	snapshot := tracker.Snapshot()
	store := m.metricsStore
	pending := m.pendingMetrics[source.name]
	if pending == nil && m.events != nil {
		workerName := source.name
		pending = &pendingMetricsUpdate{}
		m.pendingMetrics[workerName] = pending
		pending.timer = time.AfterFunc(metricsUpdatedPublishDelay, func() {
			m.mu.Lock()
			latest := m.pendingMetrics[workerName]
			if latest == nil {
				m.mu.Unlock()
				return
			}
			delete(m.pendingMetrics, workerName)
			events := m.events
			payload := map[string]any{"worker": workerName, "port": latest.port, "metrics": latest.metrics}
			m.mu.Unlock()
			if events != nil {
				events.Publish(EventMetricsUpdated, payload)
			}
		})
	}
	if pending != nil {
		pending.port = source.port
		pending.metrics = snapshot
	}
	m.mu.Unlock()

	if store != nil {
		if err := store.Record(MetricsRecord{
			Timestamp:        event.Timestamp,
			Worker:           source.name,
			Port:             source.port,
			Upstream:         event.Upstream,
			Model:            event.Model,
			Method:           event.Method,
			Path:             event.Path,
			Status:           event.Status,
			DurationMS:       event.DurationMS,
			ResponseBytes:    event.ResponseBytes,
			UsageKnown:       event.Usage.Known,
			InputTokens:      event.Usage.InputTokens,
			OutputTokens:     event.Usage.OutputTokens,
			CacheReadTokens:  event.Usage.CacheReadTokens,
			CacheWriteTokens: event.Usage.CacheWriteTokens,
			ReasoningTokens:  event.Usage.ReasoningTokens,
			TotalTokens:      event.Usage.TotalTokens,
		}); err != nil {
			m.logger.Error(logging.EventMetricsPersist,
				"worker", source.name,
				"port", source.port,
				"err", err.Error(),
			)
		}
	}
	if event.Failure != nil && qualifiedUpstreamFailure(event.Failure.Kind) {
		if err := m.recordWorkerUpstreamOutcome(source.name, event.Upstream, event.SnapshotGeneration, workerUpstreamFailure); err != nil {
			m.logger.Error(logging.EventUpstreamFailover,
				"worker", source.name,
				"upstream", event.Upstream,
				"err", redactedErrorMessage(err),
			)
		}
	} else if event.Status >= http.StatusOK && event.Status < http.StatusMultipleChoices {
		if err := m.recordWorkerUpstreamOutcome(source.name, event.Upstream, event.SnapshotGeneration, workerUpstreamSuccess); err != nil {
			m.logger.Error(logging.EventUpstreamFailover,
				"worker", source.name,
				"upstream", event.Upstream,
				"err", redactedErrorMessage(err),
			)
		}
	}
}

func qualifiedUpstreamFailure(kind worker.UpstreamFailureKind) bool {
	switch kind {
	case worker.UpstreamFailureTransport,
		worker.UpstreamFailureStatus,
		worker.UpstreamFailureFirstByteTimeout,
		worker.UpstreamFailureIdleTimeout:
		return true
	default:
		return false
	}
}

func readJSONLLines(r io.Reader, handle func([]byte)) (int, error) {
	reader := bufio.NewReaderSize(r, metricsQueryMaxLineBytes)
	skipped := 0
	oversizedLine := false
	for {
		line, err := reader.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			if !oversizedLine {
				skipped++
				oversizedLine = true
			}
			continue
		}
		if oversizedLine {
			oversizedLine = false
		} else if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			if len(bytes.TrimSpace(line)) > 0 {
				handle(line)
			}
		}
		if err == io.EOF {
			return skipped, nil
		}
		if err != nil {
			return skipped, err
		}
	}
}
