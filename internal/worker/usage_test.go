package worker

import (
	"reflect"
	"strings"
	"testing"
)

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

func TestUsageObserverDoesNotRetainLargeJSONResponse(t *testing.T) {
	const largeFieldSize = 4 * 1024 * 1024
	body := `{"output":"` + strings.Repeat("x", largeFieldSize) + `","model":"gpt-5","usage":{"input_tokens":10,"output_tokens":4}}`
	observer := NewUsageObserver("application/json")
	observer.Observe([]byte(body))

	got := responseUsageMetadata{Usage: observer.Finish(), Model: observer.Model()}
	want := responseUsageMetadata{
		Usage: UsageTokens{Known: true, InputTokens: 10, OutputTokens: 4, TotalTokens: 14},
		Model: "gpt-5",
	}
	if got != want {
		t.Fatalf("bad response metadata:\ngot  %#v\nwant %#v", got, want)
	}

	value := reflect.ValueOf(observer).Elem()
	for i := range value.NumField() {
		field := value.Field(i)
		if field.Kind() == reflect.Slice && field.Type().Elem().Kind() == reflect.Uint8 && field.Cap() >= largeFieldSize {
			t.Fatalf("observer retained full JSON body capacity: %d bytes", field.Cap())
		}
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

func TestUsageObserverExtractsCRLFSSEFrames(t *testing.T) {
	observer := NewUsageObserver("text/event-stream")
	observer.Observe([]byte("event: response.in_progress\r\ndata: {\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}\r\n\r\nevent: response.completed\r\ndata: {\"response\":{\"model\":\"gpt-5-mini\"}}\r\n\r\n"))
	wantUsage := UsageTokens{Known: true, InputTokens: 3, OutputTokens: 2, TotalTokens: 5}
	if got := observer.Finish(); got != wantUsage {
		t.Fatalf("bad usage:\ngot  %#v\nwant %#v", got, wantUsage)
	}
	if got := observer.Model(); got != "gpt-5-mini" {
		t.Fatalf("bad model: got %q want %q", got, "gpt-5-mini")
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

func TestUsageObserverCapturesChatCompletionStreamModelWithoutUsage(t *testing.T) {
	observer := NewUsageObserver("text/event-stream")
	observer.Observe([]byte("data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
	wantUsage := UsageTokens{Known: false}
	if got := observer.Finish(); got != wantUsage {
		t.Fatalf("bad usage:\ngot  %#v\nwant %#v", got, wantUsage)
	}
	if got := observer.Model(); got != "gpt-4o-mini" {
		t.Fatalf("bad model: got %q want %q", got, "gpt-4o-mini")
	}
}

func TestUsageObserverExtractsChatCompletionStreamCachedTokens(t *testing.T) {
	observer := NewUsageObserver("text/event-stream")
	observer.Observe([]byte("data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-mini\",\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"prompt_tokens_details\":{\"cached_tokens\":4}}}\n\n"))
	wantUsage := UsageTokens{Known: true, InputTokens: 11, OutputTokens: 7, CacheReadTokens: 4, TotalTokens: 18}
	if got := observer.Finish(); got != wantUsage {
		t.Fatalf("bad usage:\ngot  %#v\nwant %#v", got, wantUsage)
	}
	if got := observer.Model(); got != "gpt-4o-mini" {
		t.Fatalf("bad model: got %q want %q", got, "gpt-4o-mini")
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

func TestUsageObserverCombinesAnthropicMessageStartAndDeltaUsage(t *testing.T) {
	observer := NewUsageObserver("text/event-stream")
	observer.Observe([]byte("event: message_start\n"))
	observer.Observe([]byte("data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":9,\"cache_creation_input_tokens\":2,\"cache_read_input_tokens\":4}}}\n\n"))
	observer.Observe([]byte("event: message_delta\n"))
	observer.Observe([]byte("data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":5}}\n\n"))
	wantUsage := UsageTokens{Known: true, InputTokens: 9, OutputTokens: 5, CacheReadTokens: 4, CacheWriteTokens: 2, TotalTokens: 20}
	if got := observer.Finish(); got != wantUsage {
		t.Fatalf("bad usage:\ngot  %#v\nwant %#v", got, wantUsage)
	}
	if got := observer.Model(); got != "claude-sonnet-4-20250514" {
		t.Fatalf("bad model: got %q want %q", got, "claude-sonnet-4-20250514")
	}
}

func TestExtractUsageFromMissingUsageJSON(t *testing.T) {
	got := ExtractUsageFromJSON([]byte(`{"model":"gpt-5"}`))
	want := UsageTokens{Known: false}
	if got != want {
		t.Fatalf("bad usage:\ngot  %#v\nwant %#v", got, want)
	}
}
