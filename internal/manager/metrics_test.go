package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/upstream"
	"github.com/jesse/agent-inn/internal/worker"
)

func TestMetricsStoreRecordWritesDailyJSONL(t *testing.T) {
	dir := t.TempDir()
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time {
		return time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	})

	err := store.Record(MetricsRecord{
		Timestamp:    time.Date(2026, 7, 10, 9, 30, 0, 0, time.Local),
		Worker:       "app",
		Port:         6767,
		Upstream:     "openai",
		Method:       "POST",
		Path:         "/v1/responses",
		Status:       200,
		UsageKnown:   true,
		InputTokens:  10,
		OutputTokens: 5,
		TotalTokens:  15,
	})
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "metrics", "usage-2026-07-10.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("metrics file was empty")
	}
	var persisted map[string]any
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if _, ok := persisted["usage"]; ok {
		t.Fatalf("persisted metrics should flatten usage fields, got %s", data)
	}
	if persisted["usage_known"] != true || persisted["input_tokens"] != float64(10) || persisted["output_tokens"] != float64(5) || persisted["total_tokens"] != float64(15) {
		t.Fatalf("persisted metrics missing flat usage fields: %#v", persisted)
	}
}

func TestMetricsStoreRecordAlwaysWrites(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`
settings:
  state_dir: %s
  metrics:
    persist_enabled: false
    retention_days: 30
`, dir)), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	store := newMetricsStore(cfg.Settings, func() time.Time {
		return time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	})

	err = store.Record(MetricsRecord{
		Timestamp: time.Date(2026, 7, 10, 9, 30, 0, 0, time.Local),
		Worker:    "app",
		Port:      6767,
		Status:    200,
	})
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "metrics", "usage-2026-07-10.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("metrics record was not persisted to %s: %v", path, err)
	}
}

func TestMetricsStoreQueryTodayUsesLocalDay(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local)
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time {
		return start.Add(12 * time.Hour)
	})
	records := []MetricsRecord{
		{
			Timestamp: start.Add(9 * time.Hour),
			Worker:    "app", Port: 6767, Status: 200,
			UsageKnown: true, InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
		},
		{
			Timestamp: start.Add(-time.Hour),
			Worker:    "app", Port: 6767, Status: 200,
			UsageKnown: true, InputTokens: 100, OutputTokens: 50, TotalTokens: 150,
		},
	}
	for _, record := range records {
		if err := store.Record(record); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.Query(MetricsQuery{Range: MetricsRangeToday}, []WorkerSummary{{Name: "app", Port: 6767, Status: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	want := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: start, End: start.Add(24 * time.Hour)},
		Workers: []WorkerMetricsAggregate{
			{Worker: "app", Port: 6767, Status: "running", Totals: MetricsTotals{Requests: 1, InputTokens: 10, OutputTokens: 5, TotalTokens: 15}},
		},
		SkippedRecords: 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad metrics query:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsStoreQueryTodayUsesDSTLocalDay(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	previousLocal := time.Local
	time.Local = location
	t.Cleanup(func() { time.Local = previousLocal })

	dir := t.TempDir()
	start := time.Date(2026, 3, 8, 0, 0, 0, 0, time.Local)
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time {
		return time.Date(2026, 3, 8, 12, 0, 0, 0, time.Local)
	})
	for _, record := range []MetricsRecord{
		{Timestamp: time.Date(2026, 3, 8, 23, 30, 0, 0, time.Local), Worker: "app", Port: 6767, Status: 200, UsageKnown: true, TotalTokens: 15},
		{Timestamp: time.Date(2026, 3, 9, 0, 30, 0, 0, time.Local), Worker: "app", Port: 6767, Status: 200, UsageKnown: true, TotalTokens: 150},
	} {
		if err := store.Record(record); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.Query(MetricsQuery{Range: MetricsRangeToday}, []WorkerSummary{{Name: "app", Port: 6767, Status: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	want := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: start, End: start.AddDate(0, 0, 1)},
		Workers: []WorkerMetricsAggregate{
			{Worker: "app", Port: 6767, Status: "running", Totals: MetricsTotals{Requests: 1, TotalTokens: 15}},
		},
		SkippedRecords: 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad metrics query:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsStoreQueryLast24HUsesRollingWindow(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time { return now })
	for _, record := range []MetricsRecord{
		{Timestamp: now.Add(-23 * time.Hour), Worker: "app", Port: 6767, Status: 200, UsageKnown: true, TotalTokens: 15},
		{Timestamp: now.Add(-25 * time.Hour), Worker: "app", Port: 6767, Status: 200, UsageKnown: true, TotalTokens: 150},
	} {
		if err := store.Record(record); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.Query(MetricsQuery{Range: MetricsRangeLast24H}, []WorkerSummary{{Name: "app", Port: 6767, Status: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	want := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeLast24H, Start: now.Add(-24 * time.Hour), End: now},
		Workers: []WorkerMetricsAggregate{
			{Worker: "app", Port: 6767, Status: "running", Totals: MetricsTotals{Requests: 1, TotalTokens: 15}},
		},
		SkippedRecords: 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad metrics query:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsStoreQueryWorkerFilter(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time { return now })
	for _, record := range []MetricsRecord{
		{Timestamp: now, Worker: "app", Port: 6767, Status: 200, UsageKnown: true, TotalTokens: 15},
		{Timestamp: now, Worker: "cli", Port: 6768, Status: 200, UsageKnown: true, TotalTokens: 25},
	} {
		if err := store.Record(record); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.Query(MetricsQuery{Range: MetricsRangeToday, Worker: "cli"}, []WorkerSummary{
		{Name: "app", Port: 6767, Status: "running"},
		{Name: "cli", Port: 6768, Status: "running"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local), End: time.Date(2026, 7, 11, 0, 0, 0, 0, time.Local)},
		Workers: []WorkerMetricsAggregate{
			{Worker: "cli", Port: 6768, Status: "running", Totals: MetricsTotals{Requests: 1, TotalTokens: 25}},
		},
		SkippedRecords: 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad metrics query:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsStoreQueryIncludesRemovedWorkerHistoryWithFilters(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time { return now })
	records := []MetricsRecord{
		{
			Timestamp: now, Worker: "removed", Port: 6767, Upstream: "openai", Model: "gpt-5",
			Method: "POST", Path: "/v1/responses", Status: 500, DurationMS: 120, ResponseBytes: 64,
			UsageKnown: true, InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
		},
		{Timestamp: now, Worker: "other", Port: 6768, Upstream: "openai", Model: "gpt-5", Path: "/v1/responses", Status: 500},
		{Timestamp: now, Worker: "removed", Port: 6767, Upstream: "anthropic", Model: "gpt-5", Path: "/v1/responses", Status: 500},
		{Timestamp: now, Worker: "removed", Port: 6767, Upstream: "openai", Model: "gpt-4.1", Path: "/v1/responses", Status: 500},
		{Timestamp: now, Worker: "removed", Port: 6767, Upstream: "openai", Model: "gpt-5", Path: "/v1/chat/completions", Status: 500},
		{Timestamp: now, Worker: "removed", Port: 6767, Upstream: "openai", Model: "gpt-5", Path: "/v1/responses", Status: 429},
	}
	for _, record := range records {
		if err := store.Record(record); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.Query(MetricsQuery{
		Range: MetricsRangeToday, Worker: "removed", Upstream: "openai", Model: "gpt-5", Path: "/v1/responses", Status: 500,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := startOfLocalDay(now)
	want := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: start, End: start.AddDate(0, 0, 1)},
		Workers: []WorkerMetricsAggregate{
			{
				Worker: "removed", Port: 6767, Status: "removed", Upstream: "openai",
				Totals: MetricsTotals{
					Requests: 1, Errors: 1, AvgLatencyMS: 120, ResponseBytes: 64,
					InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
				},
			},
		},
		SkippedRecords: 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad removed-worker metrics query:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsStoreQueryWorkerFilterKeepsLiveMetrics(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time { return now })
	for _, record := range []MetricsRecord{
		{Timestamp: now, Worker: "app", Port: 6767, Status: 200, UsageKnown: true, TotalTokens: 15},
		{Timestamp: now, Worker: "cli", Port: 6768, Status: 200, UsageKnown: true, TotalTokens: 25},
	} {
		if err := store.Record(record); err != nil {
			t.Fatal(err)
		}
	}
	live := worker.MetricsSnapshot{
		WindowSeconds: worker.MetricsWindowSeconds,
		InFlight:      1,
		Requests:      2,
		RPM:           2,
		TPM:           30,
		TotalTokens:   30,
	}

	got, err := store.Query(MetricsQuery{Range: MetricsRangeToday, Worker: "app"}, []WorkerSummary{
		{Name: "app", Port: 6767, Status: "running", Upstream: upstream.RedactedUpstream{Name: "openai"}, Metrics: live},
		{Name: "cli", Port: 6768, Status: "running", Upstream: upstream.RedactedUpstream{Name: "openai"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local), End: time.Date(2026, 7, 11, 0, 0, 0, 0, time.Local)},
		Workers: []WorkerMetricsAggregate{
			{Worker: "app", Port: 6767, Status: "running", Upstream: "openai", Live: live, Totals: MetricsTotals{Requests: 1, TotalTokens: 15}},
		},
		SkippedRecords: 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad metrics query:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsStoreQueryDimensionedFiltersZeroLiveMetrics(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time { return now })
	if err := store.Record(MetricsRecord{
		Timestamp:   now,
		Worker:      "app",
		Port:        6767,
		Upstream:    "openai",
		Model:       "gpt-5",
		Path:        "/v1/responses",
		Status:      500,
		UsageKnown:  true,
		TotalTokens: 15,
	}); err != nil {
		t.Fatal(err)
	}
	live := worker.MetricsSnapshot{
		WindowSeconds: worker.MetricsWindowSeconds,
		InFlight:      1,
		Requests:      2,
		Errors:        1,
		RPM:           2,
		TPM:           30,
		TotalTokens:   30,
	}
	workers := []WorkerSummary{{Name: "app", Port: 6767, Status: "running", Upstream: upstream.RedactedUpstream{Name: "openai"}, Metrics: live}}
	want := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local), End: time.Date(2026, 7, 11, 0, 0, 0, 0, time.Local)},
		Workers: []WorkerMetricsAggregate{
			{Worker: "app", Port: 6767, Status: "running", Upstream: "openai", Totals: MetricsTotals{Requests: 1, Errors: 1, TotalTokens: 15}},
		},
		SkippedRecords: 0,
	}
	for _, query := range []MetricsQuery{
		{Range: MetricsRangeToday, Model: "gpt-5"},
		{Range: MetricsRangeToday, Path: "/v1/responses"},
		{Range: MetricsRangeToday, Status: 500},
	} {
		got, err := store.Query(query, workers)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("bad metrics query for %#v:\ngot  %#v\nwant %#v", query, got, want)
		}
	}
}

func TestMetricsStoreQueryUpstreamFilterAlwaysZeroesLiveMetrics(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time { return now })
	if err := store.Record(MetricsRecord{
		Timestamp:   now,
		Worker:      "app",
		Port:        6767,
		Upstream:    "openai",
		Status:      200,
		UsageKnown:  true,
		TotalTokens: 15,
	}); err != nil {
		t.Fatal(err)
	}
	live := worker.MetricsSnapshot{
		WindowSeconds: worker.MetricsWindowSeconds,
		InFlight:      1,
		Requests:      2,
		RPM:           2,
		TPM:           30,
		TotalTokens:   30,
	}
	workers := []WorkerSummary{{Name: "app", Port: 6767, Status: "running", Upstream: upstream.RedactedUpstream{Name: "anthropic"}, Metrics: live}}

	got, err := store.Query(MetricsQuery{Range: MetricsRangeToday, Upstream: "openai"}, workers)
	if err != nil {
		t.Fatal(err)
	}
	want := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local), End: time.Date(2026, 7, 11, 0, 0, 0, 0, time.Local)},
		Workers: []WorkerMetricsAggregate{
			{Worker: "app", Port: 6767, Status: "running", Upstream: "openai", Totals: MetricsTotals{Requests: 1, TotalTokens: 15}},
		},
		SkippedRecords: 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad openai metrics query:\ngot  %#v\nwant %#v", got, want)
	}

	got, err = store.Query(MetricsQuery{Range: MetricsRangeToday, Upstream: "anthropic"}, workers)
	if err != nil {
		t.Fatal(err)
	}
	want = MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local), End: time.Date(2026, 7, 11, 0, 0, 0, 0, time.Local)},
		Workers: []WorkerMetricsAggregate{
			{Worker: "app", Port: 6767, Status: "running", Upstream: "anthropic"},
		},
		SkippedRecords: 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad anthropic metrics query:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsStoreQueryCountsCorruptJSONL(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	metricsDir := filepath.Join(dir, "metrics")
	if err := os.MkdirAll(metricsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metricsDir, "usage-2026-07-10.jsonl"), []byte("{bad json}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time { return now })

	got, err := store.Query(MetricsQuery{Range: MetricsRangeToday}, []WorkerSummary{{Name: "app", Port: 6767, Status: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	want := MetricsQueryResponse{
		Range:          MetricsRange{Name: MetricsRangeToday, Start: time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local), End: time.Date(2026, 7, 11, 0, 0, 0, 0, time.Local)},
		Workers:        []WorkerMetricsAggregate{{Worker: "app", Port: 6767, Status: "running"}},
		SkippedRecords: 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad metrics query:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsStoreQueryContinuesAfterOversizedCorruptJSONLRecord(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	metricsDir := filepath.Join(dir, "metrics")
	if err := os.MkdirAll(metricsDir, 0700); err != nil {
		t.Fatal(err)
	}
	record := MetricsRecord{
		Timestamp:   now,
		Worker:      "app",
		Port:        6767,
		Status:      200,
		UsageKnown:  true,
		TotalTokens: 15,
	}
	valid, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(strings.Repeat("x", 70*1024) + "\n" + string(valid) + "\n")
	if err := os.WriteFile(filepath.Join(metricsDir, "usage-2026-07-10.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time { return now })

	got, err := store.Query(MetricsQuery{Range: MetricsRangeToday}, []WorkerSummary{{Name: "app", Port: 6767, Status: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	want := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local), End: time.Date(2026, 7, 11, 0, 0, 0, 0, time.Local)},
		Workers: []WorkerMetricsAggregate{
			{Worker: "app", Port: 6767, Status: "running", Totals: MetricsTotals{Requests: 1, TotalTokens: 15}},
		},
		SkippedRecords: 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad metrics query:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsStoreCleanupRetentionRemovesOldFiles(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	if err := os.MkdirAll(metricsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(metricsDir, "usage-2026-06-09.jsonl")
	keepPath := filepath.Join(metricsDir, "usage-2026-06-20.jsonl")
	for _, path := range []string{oldPath, keepPath} {
		if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store := newMetricsStore(config.Settings{StateDir: dir, Metrics: config.MetricsSettings{RetentionDays: 30}}, func() time.Time {
		return time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	})

	if err := store.CleanupRetention(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old metrics file should be removed: %v", err)
	}
	if _, err := os.Stat(keepPath); err != nil {
		t.Fatalf("recent metrics file should remain: %v", err)
	}
}

func TestMetricsStoreCleanupRetentionKeepsExactNumberOfLocalDates(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	if err := os.MkdirAll(metricsDir, 0700); err != nil {
		t.Fatal(err)
	}
	removePath := filepath.Join(metricsDir, "usage-2026-06-10.jsonl")
	keepPath := filepath.Join(metricsDir, "usage-2026-06-11.jsonl")
	for _, path := range []string{removePath, keepPath} {
		if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store := newMetricsStore(config.Settings{StateDir: dir, Metrics: config.MetricsSettings{RetentionDays: 30}}, func() time.Time {
		return time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	})

	if err := store.CleanupRetention(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(removePath); !os.IsNotExist(err) {
		t.Fatalf("cutoff metrics file should be removed: %v", err)
	}
	if _, err := os.Stat(keepPath); err != nil {
		t.Fatalf("first retained metrics file should remain: %v", err)
	}
}

func TestMetricsStoreCleanupRetentionPreservesNonPositiveDefaults(t *testing.T) {
	for _, retentionDays := range []int{0, -1} {
		t.Run(fmt.Sprintf("retention_%d", retentionDays), func(t *testing.T) {
			dir := t.TempDir()
			metricsDir := filepath.Join(dir, "metrics")
			if err := os.MkdirAll(metricsDir, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"usage-2026-06-10.jsonl", "usage-2026-06-11.jsonl"} {
				if err := os.WriteFile(filepath.Join(metricsDir, name), []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			store := newMetricsStore(config.Settings{
				StateDir: dir,
				Metrics:  config.MetricsSettings{RetentionDays: retentionDays},
			}, func() time.Time {
				return time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
			})

			if err := store.CleanupRetention(); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(metricsDir)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(entries))
			for _, entry := range entries {
				got = append(got, entry.Name())
			}
			want := []string{"usage-2026-06-11.jsonl"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("bad files for retention %d: got %v want %v", retentionDays, got, want)
			}
		})
	}
}

func TestMetricsStoreRecordRunsRetentionCleanupOncePerLocalDay(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	if err := os.MkdirAll(metricsDir, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	store := newMetricsStore(config.Settings{StateDir: dir, Metrics: config.MetricsSettings{RetentionDays: 30}}, func() time.Time {
		return now
	})

	if err := store.Record(MetricsRecord{
		Timestamp:   now,
		Worker:      "app",
		Port:        6767,
		Status:      200,
		UsageKnown:  true,
		TotalTokens: 15,
	}); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(metricsDir, metricsFileName(now.AddDate(0, 0, -30)))
	if err := os.WriteFile(oldPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(MetricsRecord{
		Timestamp:   now.Add(time.Hour),
		Worker:      "app",
		Port:        6767,
		Status:      200,
		UsageKnown:  true,
		TotalTokens: 15,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("second record on the same local day should not rescan retention: %v", err)
	}

	now = now.AddDate(0, 0, 1)
	if err := store.Record(MetricsRecord{
		Timestamp:   now,
		Worker:      "app",
		Port:        6767,
		Status:      200,
		UsageKnown:  true,
		TotalTokens: 15,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("first record on the next local day should run retention cleanup: %v", err)
	}
}

func TestMetricsStoreConcurrentRecordsSerializeRetentionCleanup(t *testing.T) {
	const (
		recordCount      = 32
		expiredFileCount = 256
	)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	if err := os.MkdirAll(metricsDir, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	oldPath := filepath.Join(metricsDir, metricsFileName(now.AddDate(0, 0, -60)))
	for i := 0; i < expiredFileCount; i++ {
		path := filepath.Join(metricsDir, metricsFileName(now.AddDate(0, 0, -60-i)))
		if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store := newMetricsStore(config.Settings{StateDir: dir, Metrics: config.MetricsSettings{RetentionDays: 30}}, func() time.Time {
		return now
	})

	start := make(chan struct{})
	errors := make(chan error, recordCount)
	var wg sync.WaitGroup
	for i := 0; i < recordCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errors <- store.Record(MetricsRecord{
				Timestamp:   now,
				Worker:      "app",
				Port:        6767,
				Status:      200,
				UsageKnown:  true,
				TotalTokens: 1,
			})
		}()
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent record failed: %v", err)
		}
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expired metrics file should be removed: %v", err)
	}

	got, err := store.Query(MetricsQuery{Range: MetricsRangeToday}, []WorkerSummary{{Name: "app", Port: 6767, Status: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	startOfDay := startOfLocalDay(now)
	want := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: startOfDay, End: startOfDay.AddDate(0, 0, 1)},
		Workers: []WorkerMetricsAggregate{
			{Worker: "app", Port: 6767, Status: "running", Totals: MetricsTotals{Requests: recordCount, TotalTokens: recordCount}},
		},
		SkippedRecords: 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad concurrent metrics records:\ngot  %#v\nwant %#v", got, want)
	}
}
