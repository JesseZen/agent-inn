package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/worker"
)

type metricsStore struct {
	settings   config.Settings
	clock      func() time.Time
	settingsMu sync.RWMutex
	writeMu    sync.Mutex
	cleanupDay time.Time
}

func newMetricsStore(settings config.Settings, clock func() time.Time) *metricsStore {
	if clock == nil {
		clock = time.Now
	}
	store := &metricsStore{clock: clock}
	store.UpdateSettings(settings)
	return store
}

func (s *metricsStore) UpdateSettings(settings config.Settings) {
	cfg := config.Config{Settings: settings}
	cfg.ApplyDefaults()
	s.settingsMu.Lock()
	s.settings = cfg.Settings
	s.settingsMu.Unlock()
}

func (s *metricsStore) Record(record MetricsRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.settingsMu.RLock()
	settings := s.settings
	s.settingsMu.RUnlock()
	metricsDir := metricsDir(settings)

	if err := os.MkdirAll(metricsDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(metricsDir, metricsFileName(record.Timestamp))
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
	cleanupDay := startOfLocalDay(s.clock())
	if cleanupDay.Equal(s.cleanupDay) {
		return nil
	}
	s.cleanupDay = cleanupDay
	return s.cleanupRetentionLocked(settings)
}

func (s *metricsStore) Query(query MetricsQuery, workers []WorkerSummary) (MetricsQueryResponse, error) {
	s.settingsMu.RLock()
	settings := s.settings
	s.settingsMu.RUnlock()
	resolved := s.resolveRange(query.Range)
	paths := filesForRange(metricsDir(settings), resolved)
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
	for _, path := range paths {
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
				aggregate = &WorkerMetricsAggregate{
					Worker:   record.Worker,
					Port:     record.Port,
					Status:   "removed",
					Upstream: record.Upstream,
				}
				if summary, ok := summaries[record.Worker]; ok {
					aggregate.Status = summary.Status
					aggregate.Live = liveMetricsForQuery(summary, query)
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.settingsMu.RLock()
	settings := s.settings
	s.settingsMu.RUnlock()
	s.cleanupDay = startOfLocalDay(s.clock())
	return s.cleanupRetentionLocked(settings)
}

func (s *metricsStore) cleanupRetentionLocked(settings config.Settings) error {
	retentionDays := settings.Metrics.RetentionDays
	if retentionDays <= 0 {
		retentionDays = 30
	}
	cutoff := startOfLocalDay(s.clock()).AddDate(0, 0, 1-retentionDays)
	metricsDir := metricsDir(settings)
	entries, err := os.ReadDir(metricsDir)
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
		if err := os.Remove(filepath.Join(metricsDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func metricsDir(settings config.Settings) string {
	return filepath.Join(expandHomePath(settings.StateDir), "metrics")
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

func filesForRange(metricsDir string, r MetricsRange) []string {
	day := startOfLocalDay(r.Start)
	endDay := startOfLocalDay(r.End)
	if r.End.Equal(endDay) {
		endDay = endDay.AddDate(0, 0, -1)
	}
	var paths []string
	for !day.After(endDay) {
		paths = append(paths, filepath.Join(metricsDir, metricsFileName(day)))
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
