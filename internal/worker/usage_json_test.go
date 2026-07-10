package worker

import (
	"runtime"
	"strings"
	"testing"
)

func TestUsageObserverBoundsUnknownJSONStringMemory(t *testing.T) {
	const (
		unknownStringSize     = 4 * 1024 * 1024
		observerRetainedLimit = 256 * 1024
		observationChunkSize  = 257
	)
	observer := NewUsageObserver("application/json")
	observer.Observe([]byte(`{"output":"`))

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	chunk := strings.Repeat("x", observationChunkSize)
	for remaining := unknownStringSize; remaining > 0; {
		writeSize := min(remaining, len(chunk))
		observer.Observe([]byte(chunk[:writeSize]))
		remaining -= writeSize
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	retained := uint64(0)
	if after.HeapAlloc > before.HeapAlloc {
		retained = after.HeapAlloc - before.HeapAlloc
	}

	observer.Observe([]byte(`","model":"gpt-5","usage":{"input_tokens":10,"output_tokens":4}}`))
	got := responseUsageMetadata{Usage: observer.Finish(), Model: observer.Model()}
	want := responseUsageMetadata{
		Usage: UsageTokens{Known: true, InputTokens: 10, OutputTokens: 4, TotalTokens: 14},
		Model: "gpt-5",
	}
	if got != want {
		t.Fatalf("bad response metadata:\ngot  %#v\nwant %#v", got, want)
	}
	if retained > observerRetainedLimit {
		t.Fatalf("observer retained %d bytes while skipping unknown string; limit %d", retained, observerRetainedLimit)
	}
}

func TestResponseJSONScannerPreservesSupportedShapesAndEscapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want responseUsageMetadata
	}{
		{
			name: "root",
			body: `{"m\u006fdel":"gpt-\u0035","usage":{"input_tokens":10,"output_tokens":4}}`,
			want: responseUsageMetadata{Model: "gpt-5", Usage: UsageTokens{Known: true, InputTokens: 10, OutputTokens: 4, TotalTokens: 14}},
		},
		{
			name: "response",
			body: `{"response":{"model":"gpt-5","usage":{"prompt_tokens":11,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":2}}}}`,
			want: responseUsageMetadata{Model: "gpt-5", Usage: UsageTokens{Known: true, InputTokens: 11, OutputTokens: 7, CacheReadTokens: 4, ReasoningTokens: 2, TotalTokens: 18}},
		},
		{
			name: "message",
			body: `{"message":{"model":"claude-sonnet","usage":{"input_tokens":9,"output_tokens":5,"cache_read_input_tokens":4,"cache_creation_input_tokens":2}}}`,
			want: responseUsageMetadata{Model: "claude-sonnet", Usage: UsageTokens{Known: true, InputTokens: 9, OutputTokens: 5, CacheReadTokens: 4, CacheWriteTokens: 2, TotalTokens: 20}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scanner := &responseJSONScanner{}
			for i := range len(test.body) {
				scanner.Write([]byte(test.body[i : i+1]))
			}
			if got := scanner.Finish(); got != test.want {
				t.Fatalf("bad response metadata:\ngot  %#v\nwant %#v", got, test.want)
			}
		})
	}
}
