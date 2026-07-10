package manager

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/worker"
)

const (
	metricsMinimumRetentionDays = 2
	metricsQueryCacheTTL        = time.Second
	metricsQueryMaxLineBytes    = 256 * 1024
	metricsQueryMaxScanBytes    = 8 * 1024 * 1024
)

type metricsQueryCache struct {
	query          MetricsQuery
	metricsDir     string
	cachedAt       time.Time
	rangeValue     MetricsRange
	workers        []WorkerMetricsAggregate
	skippedRecords int
	queryLimited   bool
	valid          bool
}

type metricsStore struct {
	settings          config.Settings
	clock             func() time.Time
	settingsMu        sync.RWMutex
	writeMu           sync.Mutex
	queryMu           sync.Mutex
	queryCache        metricsQueryCache
	cleanupDay        time.Time
	persistenceErrors atomic.Uint64
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

func (s *metricsStore) Record(record MetricsRecord) (err error) {
	defer func() {
		if err != nil {
			s.persistenceErrors.Add(1)
		}
	}()
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

	s.queryMu.Lock()
	defer s.queryMu.Unlock()
	now := s.clock()
	metricsPath := metricsDir(settings)
	cached := s.queryCache
	var persistedWorkers []WorkerMetricsAggregate
	var resolved MetricsRange
	var skippedRecords int
	var queryLimited bool
	if cached.valid && cached.query == query && cached.metricsDir == metricsPath && now.Sub(cached.cachedAt) >= 0 && now.Sub(cached.cachedAt) < metricsQueryCacheTTL {
		resolved = cached.rangeValue
		persistedWorkers = append([]WorkerMetricsAggregate(nil), cached.workers...)
		skippedRecords = cached.skippedRecords
		queryLimited = cached.queryLimited
	} else {
		resolved = s.resolveRange(query.Range)
		paths := filesForRange(metricsPath, resolved)
		aggregates := map[string]*WorkerMetricsAggregate{}
		durationByWorker := map[string]int64{}
		scannedBytes := 0
		stopScan := false
		for _, path := range paths {
			file, err := os.Open(path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return MetricsQueryResponse{}, err
			}
			remainingBytes := metricsQueryMaxScanBytes - scannedBytes
			reader := bufio.NewReaderSize(io.LimitReader(file, int64(remainingBytes+1)), metricsQueryMaxLineBytes)
			oversizedLine := false
			for {
				line, readErr := reader.ReadSlice('\n')
				scannedBytes += len(line)
				if scannedBytes > metricsQueryMaxScanBytes {
					queryLimited = true
					stopScan = true
					break
				}
				if readErr == bufio.ErrBufferFull {
					if !oversizedLine {
						skippedRecords++
						queryLimited = true
						oversizedLine = true
					}
					continue
				}
				if oversizedLine {
					oversizedLine = false
				} else {
					line = bytes.TrimRight(line, "\r\n")
					if len(bytes.TrimSpace(line)) > 0 {
						var record MetricsRecord
						if err := json.Unmarshal(line, &record); err != nil {
							skippedRecords++
						} else if record.Timestamp.Before(resolved.End) && !record.Timestamp.Before(resolved.Start) && recordMatchesQuery(record, query) {
							aggregate := aggregates[record.Worker]
							if aggregate == nil {
								aggregate = &WorkerMetricsAggregate{
									Worker: record.Worker, Port: record.Port, Status: "removed", Upstream: record.Upstream,
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
						}
					}
				}
				if readErr == io.EOF {
					break
				}
				if readErr != nil {
					_ = file.Close()
					return MetricsQueryResponse{}, readErr
				}
			}
			if err := file.Close(); err != nil {
				return MetricsQueryResponse{}, err
			}
			if stopScan {
				break
			}
		}

		names := make([]string, 0, len(aggregates))
		for name := range aggregates {
			names = append(names, name)
		}
		sort.Strings(names)
		persistedWorkers = make([]WorkerMetricsAggregate, 0, len(names))
		for _, name := range names {
			aggregate := aggregates[name]
			if aggregate.Totals.Requests > 0 {
				aggregate.Totals.AvgLatencyMS = durationByWorker[name] / aggregate.Totals.Requests
			}
			persistedWorkers = append(persistedWorkers, *aggregate)
		}
		s.queryCache = metricsQueryCache{
			query:          query,
			metricsDir:     metricsPath,
			cachedAt:       now,
			rangeValue:     resolved,
			workers:        append([]WorkerMetricsAggregate(nil), persistedWorkers...),
			skippedRecords: skippedRecords,
			queryLimited:   queryLimited,
			valid:          true,
		}
	}

	response := MetricsQueryResponse{
		Range:             resolved,
		SkippedRecords:    skippedRecords,
		QueryLimited:      queryLimited,
		PersistenceErrors: s.persistenceErrors.Load(),
	}
	aggregates := make(map[string]*WorkerMetricsAggregate, len(persistedWorkers)+len(workers))
	for i := range persistedWorkers {
		aggregates[persistedWorkers[i].Worker] = &persistedWorkers[i]
	}
	summaries := map[string]WorkerSummary{}
	for _, summary := range workers {
		summaries[summary.Name] = summary
		if query.Worker != "" && summary.Name != query.Worker {
			continue
		}
		if query.Upstream != "" && summary.Upstream.Name != query.Upstream {
			continue
		}
		aggregate := aggregates[summary.Name]
		if aggregate == nil {
			aggregate = &WorkerMetricsAggregate{Worker: summary.Name}
			aggregates[summary.Name] = aggregate
		}
		aggregate.Port = summary.Port
		aggregate.Status = summary.Status
		aggregate.Upstream = summary.Upstream.Name
		aggregate.Live = liveMetricsForQuery(summary, query)
	}
	for name, aggregate := range aggregates {
		summary, ok := summaries[name]
		if !ok {
			continue
		}
		aggregate.Status = summary.Status
		if query.Upstream != "" && summary.Upstream.Name != query.Upstream {
			aggregate.Live = liveMetricsForQuery(summary, query)
		}
	}

	names := make([]string, 0, len(aggregates))
	for name := range aggregates {
		names = append(names, name)
	}
	sort.Strings(names)
	response.Workers = make([]WorkerMetricsAggregate, 0, len(names))
	for _, name := range names {
		response.Workers = append(response.Workers, *aggregates[name])
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
	retentionDays = max(retentionDays, metricsMinimumRetentionDays)
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
