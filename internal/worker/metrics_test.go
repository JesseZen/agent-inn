package worker

import (
	"net/http"
	"testing"
	"time"
)

func TestMetricsTrackerRecordsWindowSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.Local)
	clock := func() time.Time { return now }
	tracker := NewMetricsTracker(clock)

	tracker.Start()
	tracker.Finish(RequestMetricEvent{
		Timestamp:     now,
		Method:        http.MethodPost,
		Path:          "/v1/responses",
		Status:        http.StatusOK,
		DurationMS:    250,
		ResponseBytes: 512,
		Usage: UsageTokens{
			Known:        true,
			InputTokens:  100,
			OutputTokens: 40,
			TotalTokens:  140,
		},
	})
	tracker.Finish(RequestMetricEvent{
		Timestamp:  now,
		Method:     http.MethodPost,
		Path:       "/v1/responses",
		Status:     http.StatusTooManyRequests,
		DurationMS: 750,
		Usage:      UsageTokens{Known: false},
	})

	want := MetricsSnapshot{
		WindowSeconds:        MetricsWindowSeconds,
		InFlight:             0,
		Requests:             2,
		Errors:               1,
		RPM:                  2,
		TPM:                  140,
		AvgLatencyMS:         500,
		InputTokens:          100,
		OutputTokens:         40,
		TotalTokens:          140,
		UnknownUsageRequests: 1,
	}
	if got := tracker.Snapshot(); got != want {
		t.Fatalf("bad metrics snapshot:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestMetricsTrackerExpiresOldBuckets(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.Local)
	clock := func() time.Time { return now }
	tracker := NewMetricsTracker(clock)

	tracker.Start()
	tracker.Finish(RequestMetricEvent{
		Timestamp:  now,
		Method:     http.MethodPost,
		Path:       "/v1/responses",
		Status:     http.StatusTooManyRequests,
		DurationMS: 250,
		Usage: UsageTokens{
			Known:        true,
			InputTokens:  100,
			OutputTokens: 40,
			TotalTokens:  140,
		},
	})

	now = now.Add(61 * time.Second)

	want := MetricsSnapshot{
		WindowSeconds: MetricsWindowSeconds,
	}
	if got := tracker.Snapshot(); got != want {
		t.Fatalf("bad metrics snapshot:\ngot  %#v\nwant %#v", got, want)
	}
}
