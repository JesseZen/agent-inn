package worker

import "testing"

func TestDecodeResponseJSONString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: `m\u006fdel`, want: "model"},
		{input: `gpt-5\/mini`, want: "gpt-5/mini"},
		{input: `\ud83d\ude00`, want: "😀"},
	}
	for _, test := range tests {
		got, ok := decodeResponseJSONString([]byte(test.input))
		if !ok || got != test.want {
			t.Fatalf("bad decoded string for %q: got %q ok=%v want %q", test.input, got, ok, test.want)
		}
	}
}
