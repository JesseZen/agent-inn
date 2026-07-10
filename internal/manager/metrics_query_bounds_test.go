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
)

func TestMetricsStoreQueryLimitsOversizedJSONLRecord(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	metricsDir := filepath.Join(dir, "metrics")
	if err := os.MkdirAll(metricsDir, 0700); err != nil {
		t.Fatal(err)
	}
	record := MetricsRecord{
		Timestamp: now, Worker: "app", Port: 6767, Status: 200, UsageKnown: true, TotalTokens: 15,
	}
	valid, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	data := strings.Repeat("x", metricsQueryMaxLineBytes+1) + "\n" + string(valid) + "\n"
	if err := os.WriteFile(filepath.Join(metricsDir, metricsFileName(now)), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time { return now })

	got, err := store.Query(MetricsQuery{Range: MetricsRangeToday}, []WorkerSummary{{Name: "app", Port: 6767, Status: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	start := startOfLocalDay(now)
	want := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: start, End: start.AddDate(0, 0, 1)},
		Workers: []WorkerMetricsAggregate{
			{Worker: "app", Port: 6767, Status: "running", Totals: MetricsTotals{Requests: 1, TotalTokens: 15}},
		},
		SkippedRecords: 1,
		QueryLimited:   true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad oversized-line query:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsStoreQueryLimitsTotalScanBytes(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	metricsDir := filepath.Join(dir, "metrics")
	if err := os.MkdirAll(metricsDir, 0700); err != nil {
		t.Fatal(err)
	}
	record := MetricsRecord{
		Timestamp: now, Worker: "app", Port: 6767, Status: 200, UsageKnown: true, TotalTokens: 1,
	}
	line, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	line = append(line, '\n')
	processedRecords := metricsQueryMaxScanBytes / len(line)
	data := strings.Repeat(string(line), processedRecords+1)
	if err := os.WriteFile(filepath.Join(metricsDir, metricsFileName(now)), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time { return now })

	got, err := store.Query(MetricsQuery{Range: MetricsRangeToday}, []WorkerSummary{{Name: "app", Port: 6767, Status: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	start := startOfLocalDay(now)
	want := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: start, End: start.AddDate(0, 0, 1)},
		Workers: []WorkerMetricsAggregate{
			{Worker: "app", Port: 6767, Status: "running", Totals: MetricsTotals{Requests: int64(processedRecords), TotalTokens: int64(processedRecords)}},
		},
		QueryLimited: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad scan-limited query:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsStoreQueryCachesOnePersistedResultWithinTTL(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	store := newMetricsStore(config.Settings{StateDir: dir}, func() time.Time { return now })
	record := MetricsRecord{
		Timestamp: now, Worker: "app", Port: 6767, Status: 200, UsageKnown: true, TotalTokens: 1,
	}
	if err := store.Record(record); err != nil {
		t.Fatal(err)
	}
	workers := []WorkerSummary{{Name: "app", Port: 6767, Status: "running"}}
	initial, err := store.Query(MetricsQuery{Range: MetricsRangeToday}, workers)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(record); err != nil {
		t.Fatal(err)
	}
	now = now.Add(metricsQueryCacheTTL / 2)
	cached, err := store.Query(MetricsQuery{Range: MetricsRangeToday}, workers)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(metricsQueryCacheTTL)
	refreshed, err := store.Query(MetricsQuery{Range: MetricsRangeToday}, workers)
	if err != nil {
		t.Fatal(err)
	}
	start := startOfLocalDay(now)
	base := MetricsQueryResponse{
		Range: MetricsRange{Name: MetricsRangeToday, Start: start, End: start.AddDate(0, 0, 1)},
		Workers: []WorkerMetricsAggregate{
			{Worker: "app", Port: 6767, Status: "running", Totals: MetricsTotals{Requests: 1, TotalTokens: 1}},
		},
	}
	wantRefreshed := base
	wantRefreshed.Workers = []WorkerMetricsAggregate{
		{Worker: "app", Port: 6767, Status: "running", Totals: MetricsTotals{Requests: 2, TotalTokens: 2}},
	}
	want := struct {
		Initial   MetricsQueryResponse
		Cached    MetricsQueryResponse
		Refreshed MetricsQueryResponse
	}{base, base, wantRefreshed}
	got := struct {
		Initial   MetricsQueryResponse
		Cached    MetricsQueryResponse
		Refreshed MetricsQueryResponse
	}{initial, cached, refreshed}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bad metrics query cache behavior:\ngot  %#v\nwant %#v", got, want)
	}
}
