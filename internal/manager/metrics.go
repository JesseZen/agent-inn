package manager

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/logging"
	"github.com/jesse/agent-inn/internal/worker"
)

const (
	MetricsRangeToday   MetricsRangeName = "today"
	MetricsRangeLast24H MetricsRangeName = "last_24h"
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
	Worker   string                 `json:"worker"`
	Port     int                    `json:"port"`
	Status   string                 `json:"status"`
	Upstream string                 `json:"upstream,omitempty"`
	Live     worker.MetricsSnapshot `json:"live"`
	Totals   MetricsTotals          `json:"totals"`
}

type MetricsQueryResponse struct {
	Range          MetricsRange             `json:"range"`
	Workers        []WorkerMetricsAggregate `json:"workers"`
	SkippedRecords int                      `json:"skipped_records"`
}

type metricsStore struct {
	settings config.Settings
	clock    func() time.Time
	mu       sync.Mutex
}

type workerMetricSource struct {
	name string
	port int
}

func newMetricsStore(settings config.Settings, clock func() time.Time) *metricsStore {
	cfg := config.Config{Settings: settings}
	cfg.ApplyDefaults()
	if clock == nil {
		clock = time.Now
	}
	return &metricsStore{settings: cfg.Settings, clock: clock}
}

func (s *metricsStore) Record(record MetricsRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.settings.Metrics.PersistEnabled != nil && !*s.settings.Metrics.PersistEnabled {
		return nil
	}
	if err := os.MkdirAll(s.metricsDir(), 0700); err != nil {
		return err
	}
	path := filepath.Join(s.metricsDir(), metricsFileName(record.Timestamp))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(file).Encode(record); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return s.cleanupRetentionLocked()
}

func (s *metricsStore) Query(query MetricsQuery, workers []WorkerSummary) (MetricsQueryResponse, error) {
	resolved := s.resolveRange(query.Range)
	response := MetricsQueryResponse{Range: resolved}
	aggregates := map[string]*WorkerMetricsAggregate{}
	summaries := map[string]WorkerSummary{}
	for _, summary := range workers {
		summaries[summary.Name] = summary
		if query.Worker != "" && summary.Name != query.Worker {
			continue
		}
		if query.Upstream != "" && summary.Upstream.Name != query.Upstream {
			continue
		}
		aggregates[summary.Name] = &WorkerMetricsAggregate{
			Worker:   summary.Name,
			Port:     summary.Port,
			Status:   summary.Status,
			Upstream: summary.Upstream.Name,
			Live:     liveMetricsForQuery(summary, query),
		}
	}

	durationByWorker := map[string]int64{}
	for _, path := range s.filesForRange(resolved) {
		file, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return MetricsQueryResponse{}, err
		}
		err = readJSONLLines(file, func(line []byte) {
			var record MetricsRecord
			if err := json.Unmarshal(line, &record); err != nil {
				response.SkippedRecords++
				return
			}
			if !record.Timestamp.Before(resolved.End) || record.Timestamp.Before(resolved.Start) {
				return
			}
			if !recordMatchesQuery(record, query) {
				return
			}
			aggregate := aggregates[record.Worker]
			if aggregate == nil {
				summary, ok := summaries[record.Worker]
				if !ok {
					return
				}
				aggregate = &WorkerMetricsAggregate{
					Worker:   record.Worker,
					Port:     record.Port,
					Status:   summary.Status,
					Upstream: record.Upstream,
					Live:     liveMetricsForQuery(summary, query),
				}
				aggregates[record.Worker] = aggregate
			}
			aggregate.Totals.Requests++
			if record.Status >= 400 {
				aggregate.Totals.Errors++
			}
			durationByWorker[record.Worker] += record.DurationMS
			aggregate.Totals.ResponseBytes += record.ResponseBytes
			if record.UsageKnown {
				aggregate.Totals.InputTokens += record.InputTokens
				aggregate.Totals.OutputTokens += record.OutputTokens
				aggregate.Totals.CacheReadTokens += record.CacheReadTokens
				aggregate.Totals.CacheWriteTokens += record.CacheWriteTokens
				aggregate.Totals.ReasoningTokens += record.ReasoningTokens
				aggregate.Totals.TotalTokens += record.TotalTokens
			} else {
				aggregate.Totals.UnknownUsageRequests++
			}
		})
		if err != nil {
			_ = file.Close()
			return MetricsQueryResponse{}, err
		}
		if err := file.Close(); err != nil {
			return MetricsQueryResponse{}, err
		}
	}

	names := make([]string, 0, len(aggregates))
	for name := range aggregates {
		names = append(names, name)
	}
	sort.Strings(names)
	response.Workers = make([]WorkerMetricsAggregate, 0, len(names))
	for _, name := range names {
		aggregate := aggregates[name]
		if aggregate.Totals.Requests > 0 {
			aggregate.Totals.AvgLatencyMS = durationByWorker[name] / aggregate.Totals.Requests
		}
		response.Workers = append(response.Workers, *aggregate)
	}
	return response, nil
}

func (s *metricsStore) CleanupRetention() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupRetentionLocked()
}

func (s *metricsStore) cleanupRetentionLocked() error {
	retentionDays := s.settings.Metrics.RetentionDays
	if retentionDays <= 0 {
		retentionDays = 30
	}
	cutoff := startOfLocalDay(s.clock()).AddDate(0, 0, 1-retentionDays)
	entries, err := os.ReadDir(s.metricsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		day, ok := dayFromMetricsFileName(entry.Name())
		if !ok || !day.Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(s.metricsDir(), entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (s *metricsStore) metricsDir() string {
	return filepath.Join(expandHomePath(s.settings.StateDir), "metrics")
}

func (s *metricsStore) resolveRange(name MetricsRangeName) MetricsRange {
	if name == "" {
		name = MetricsRangeToday
	}
	now := s.clock()
	switch name {
	case MetricsRangeLast24H:
		return MetricsRange{Name: name, Start: now.Add(-24 * time.Hour), End: now}
	default:
		start := startOfLocalDay(now)
		return MetricsRange{Name: MetricsRangeToday, Start: start, End: start.AddDate(0, 0, 1)}
	}
}

func (s *metricsStore) filesForRange(r MetricsRange) []string {
	day := startOfLocalDay(r.Start)
	endDay := startOfLocalDay(r.End)
	if r.End.Equal(endDay) {
		endDay = endDay.AddDate(0, 0, -1)
	}
	var paths []string
	for !day.After(endDay) {
		paths = append(paths, filepath.Join(s.metricsDir(), metricsFileName(day)))
		day = day.AddDate(0, 0, 1)
	}
	return paths
}

func recordMatchesQuery(record MetricsRecord, query MetricsQuery) bool {
	if query.Worker != "" && record.Worker != query.Worker {
		return false
	}
	if query.Upstream != "" && record.Upstream != query.Upstream {
		return false
	}
	if query.Model != "" && record.Model != query.Model {
		return false
	}
	if query.Path != "" && record.Path != query.Path {
		return false
	}
	if query.Status != 0 && record.Status != query.Status {
		return false
	}
	return true
}

func liveMetricsForQuery(summary WorkerSummary, query MetricsQuery) worker.MetricsSnapshot {
	if query.Upstream != "" || query.Model != "" || query.Path != "" || query.Status != 0 {
		return worker.MetricsSnapshot{}
	}
	return summary.Metrics
}

func startOfLocalDay(t time.Time) time.Time {
	year, month, day := t.In(time.Local).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local)
}

func metricsFileName(t time.Time) string {
	return "usage-" + t.In(time.Local).Format("2006-01-02") + ".jsonl"
}

func dayFromMetricsFileName(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, "usage-") || !strings.HasSuffix(name, ".jsonl") {
		return time.Time{}, false
	}
	day, err := time.ParseInLocation("2006-01-02", strings.TrimSuffix(strings.TrimPrefix(name, "usage-"), ".jsonl"), time.Local)
	return day, err == nil
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
	_ = readJSONLLines(r, func(line []byte) {
		var event worker.RequestMetricEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return
		}
		m.handleWorkerMetricEventFrom(source, event)
	})
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
	m.publishEvent(EventMetricsUpdated, map[string]any{"worker": source.name, "port": source.port, "metrics": snapshot})
}

func readJSONLLines(r io.Reader, handle func([]byte)) error {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			if len(bytes.TrimSpace(line)) > 0 {
				handle(line)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
