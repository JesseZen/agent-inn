package worker

import "testing"

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
