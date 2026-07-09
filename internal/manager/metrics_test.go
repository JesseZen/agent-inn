package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestMetricsStoreRecordSkipsPersistenceWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	disabled := false
	store := newMetricsStore(config.Settings{
		StateDir: dir,
		Metrics:  config.MetricsSettings{PersistEnabled: &disabled, RetentionDays: 30},
	}, func() time.Time {
		return time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	})

	err := store.Record(MetricsRecord{
		Timestamp: time.Date(2026, 7, 10, 9, 30, 0, 0, time.Local),
		Worker:    "app",
		Port:      6767,
		Status:    200,
	})
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "metrics", "usage-2026-07-10.jsonl")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("disabled persistence should not write %s: %v", path, err)
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

func TestMetricsStoreQueryUpstreamFilterUsesLiveMetricsOnlyForCurrentUpstream(t *testing.T) {
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
			{Worker: "app", Port: 6767, Status: "running", Upstream: "anthropic", Live: live},
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

func TestMetricsStoreRecordRunsRetentionCleanup(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	if err := os.MkdirAll(metricsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(metricsDir, "usage-2026-06-09.jsonl")
	if err := os.WriteFile(oldPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store := newMetricsStore(config.Settings{StateDir: dir, Metrics: config.MetricsSettings{RetentionDays: 30}}, func() time.Time {
		return time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	})

	err := store.Record(MetricsRecord{
		Timestamp:   time.Date(2026, 7, 10, 9, 30, 0, 0, time.Local),
		Worker:      "app",
		Port:        6767,
		Status:      200,
		UsageKnown:  true,
		TotalTokens: 15,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("record should run retention cleanup for old metrics file: %v", err)
	}
}
