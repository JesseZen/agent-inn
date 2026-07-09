package worker

import "testing"

func TestExtractUsageFromResponsesJSON(t *testing.T) {
	got := ExtractUsageFromJSON([]byte(`{"model":"gpt-5","usage":{"input_tokens":10,"output_tokens":4,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}}`))
	want := UsageTokens{Known: true, InputTokens: 10, OutputTokens: 4, CacheReadTokens: 3, ReasoningTokens: 2, TotalTokens: 14}
	if got != want {
		t.Fatalf("bad usage:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestExtractUsageFromChatCompletionsJSON(t *testing.T) {
	got := ExtractUsageFromJSON([]byte(`{"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`))
	want := UsageTokens{Known: true, InputTokens: 11, OutputTokens: 7, TotalTokens: 18}
	if got != want {
		t.Fatalf("bad usage:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestExtractUsageFromAnthropicJSON(t *testing.T) {
	got := ExtractUsageFromJSON([]byte(`{"usage":{"input_tokens":9,"output_tokens":5,"cache_read_input_tokens":4,"cache_creation_input_tokens":2}}`))
	want := UsageTokens{Known: true, InputTokens: 9, OutputTokens: 5, CacheReadTokens: 4, CacheWriteTokens: 2, TotalTokens: 20}
	if got != want {
		t.Fatalf("bad usage:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestUsageObserverExtractsSSECompletionUsage(t *testing.T) {
	observer := NewUsageObserver("text/event-stream")
	observer.Observe([]byte("event: response.completed\n"))
	observer.Observe([]byte("data: {\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"))
	want := UsageTokens{Known: true, InputTokens: 3, OutputTokens: 2, TotalTokens: 5}
	if got := observer.Finish(); got != want {
		t.Fatalf("bad usage:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestExtractUsageMetadataFromJSONIncludesRootModel(t *testing.T) {
	got := extractUsageMetadataFromJSON([]byte(`{"model":"gpt-5","usage":{"input_tokens":10,"output_tokens":4}}`))
	want := responseUsageMetadata{
		Usage: UsageTokens{Known: true, InputTokens: 10, OutputTokens: 4, TotalTokens: 14},
		Model: "gpt-5",
	}
	if got != want {
		t.Fatalf("bad response metadata:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestUsageObserverExtractsSSECompletionModel(t *testing.T) {
	observer := NewUsageObserver("text/event-stream")
	observer.Observe([]byte("event: response.completed\n"))
	observer.Observe([]byte("data: {\"response\":{\"model\":\"gpt-5-mini\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"))
	wantUsage := UsageTokens{Known: true, InputTokens: 3, OutputTokens: 2, TotalTokens: 5}
	if got := observer.Finish(); got != wantUsage {
		t.Fatalf("bad usage:\ngot  %#v\nwant %#v", got, wantUsage)
	}
	if got := observer.Model(); got != "gpt-5-mini" {
		t.Fatalf("bad model: got %q want %q", got, "gpt-5-mini")
	}
}

func TestUsageObserverKeepsSSEUsageWhenCompletedEventAddsModel(t *testing.T) {
	observer := NewUsageObserver("text/event-stream")
	observer.Observe([]byte("event: response.in_progress\n"))
	observer.Observe([]byte("data: {\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}\n\n"))
	observer.Observe([]byte("event: response.completed\n"))
	observer.Observe([]byte("data: {\"response\":{\"model\":\"gpt-5-mini\"}}\n\n"))
	wantUsage := UsageTokens{Known: true, InputTokens: 3, OutputTokens: 2, TotalTokens: 5}
	if got := observer.Finish(); got != wantUsage {
		t.Fatalf("bad usage:\ngot  %#v\nwant %#v", got, wantUsage)
	}
	if got := observer.Model(); got != "gpt-5-mini" {
		t.Fatalf("bad model: got %q want %q", got, "gpt-5-mini")
	}
}

func TestExtractUsageFromMissingUsageJSON(t *testing.T) {
	got := ExtractUsageFromJSON([]byte(`{"model":"gpt-5"}`))
	want := UsageTokens{Known: false}
	if got != want {
		t.Fatalf("bad usage:\ngot  %#v\nwant %#v", got, want)
	}
}
