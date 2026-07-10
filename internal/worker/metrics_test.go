package worker

import (
	"encoding/json"
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
		Upstream:      "openai",
		Method:        http.MethodPost,
		Path:          "/v1/responses",
		Status:        http.StatusOK,
		DurationMS:    250,
		ResponseBytes: 512,
		Usage: UsageTokens{
			Known:            true,
			InputTokens:      100,
			OutputTokens:     40,
			CacheReadTokens:  30,
			CacheWriteTokens: 20,
			ReasoningTokens:  10,
			TotalTokens:      200,
		},
	})
	tracker.Start()
	tracker.Finish(RequestMetricEvent{
		Timestamp:  now,
		Upstream:   "openai",
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
		TPM:                  200,
		AvgLatencyMS:         500,
		InputTokens:          100,
		OutputTokens:         40,
		CacheReadTokens:      30,
		CacheWriteTokens:     20,
		ReasoningTokens:      10,
		TotalTokens:          200,
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
		Upstream:   "openai",
		Method:     http.MethodPost,
		Path:       "/v1/responses",
		Status:     http.StatusTooManyRequests,
		DurationMS: 250,
		Usage: UsageTokens{
			Known:            true,
			InputTokens:      100,
			OutputTokens:     40,
			CacheReadTokens:  30,
			CacheWriteTokens: 20,
			ReasoningTokens:  10,
			TotalTokens:      200,
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

func TestWorkerMetricsJSONContract(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	event := RequestMetricEvent{
		Timestamp:     now,
		Upstream:      "openai",
		Model:         "gpt-5",
		Method:        http.MethodPost,
		Path:          "/v1/responses",
		Status:        http.StatusOK,
		DurationMS:    250,
		ResponseBytes: 512,
		Usage: UsageTokens{
			Known:            true,
			InputTokens:      100,
			OutputTokens:     40,
			CacheReadTokens:  30,
			CacheWriteTokens: 20,
			ReasoningTokens:  10,
			TotalTokens:      200,
		},
	}
	gotEvent, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	wantEvent := `{"timestamp":"2026-07-10T03:00:00Z","upstream":"openai","model":"gpt-5","method":"POST","path":"/v1/responses","status":200,"duration_ms":250,"response_bytes":512,"usage":{"usage_known":true,"input_tokens":100,"output_tokens":40,"cache_read_tokens":30,"cache_write_tokens":20,"reasoning_tokens":10,"total_tokens":200}}`
	if string(gotEvent) != wantEvent {
		t.Fatalf("bad request metric JSON:\ngot  %s\nwant %s", gotEvent, wantEvent)
	}

	snapshot := MetricsSnapshot{
		WindowSeconds:        MetricsWindowSeconds,
		InFlight:             1,
		Requests:             2,
		Errors:               1,
		RPM:                  2,
		TPM:                  200,
		AvgLatencyMS:         500,
		InputTokens:          100,
		OutputTokens:         40,
		CacheReadTokens:      30,
		CacheWriteTokens:     20,
		ReasoningTokens:      10,
		TotalTokens:          200,
		UnknownUsageRequests: 1,
	}
	gotSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	wantSnapshot := `{"window_seconds":60,"in_flight":1,"requests":2,"errors":1,"rpm":2,"tpm":200,"avg_latency_ms":500,"input_tokens":100,"output_tokens":40,"cache_read_tokens":30,"cache_write_tokens":20,"reasoning_tokens":10,"total_tokens":200,"unknown_usage_requests":1,"dropped_events":0}`
	if string(gotSnapshot) != wantSnapshot {
		t.Fatalf("bad metrics snapshot JSON:\ngot  %s\nwant %s", gotSnapshot, wantSnapshot)
	}
}
