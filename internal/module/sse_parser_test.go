package module

import "testing"

func TestSSEParserReassemblesJSONSplitAcrossReads(t *testing.T) {
	var parser SSEParser
	events, err := parser.Push([]byte(`data: {"choices":[{"delta":{"content":"Hel`), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no complete events, got %#v", events)
	}

	events, err = parser.Push([]byte(`lo"}}]}`+"\n\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Data != `{"choices":[{"delta":{"content":"Hello"}}]}` {
		t.Fatalf("bad reassembled event: %#v", events)
	}
}

func TestSSEParserProcessesCompleteEventsAndRetainsTrailingBytes(t *testing.T) {
	var parser SSEParser
	events, err := parser.Push([]byte("data: one\n\ndata: two\n\ndata: thr"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Data != "one" || events[1].Data != "two" {
		t.Fatalf("bad complete events: %#v", events)
	}

	events, err = parser.Push([]byte("ee\n\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Data != "three" {
		t.Fatalf("bad trailing event: %#v", events)
	}
}

func TestSSEParserReassemblesDoneSentinel(t *testing.T) {
	var parser SSEParser
	events, err := parser.Push([]byte("data: [DO"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no complete events, got %#v", events)
	}

	events, err = parser.Push([]byte("NE]\n\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !events[0].Done {
		t.Fatalf("expected done event, got %#v", events)
	}
}

func TestSSEParserProcessesTrailingEventAtEOF(t *testing.T) {
	var parser SSEParser
	events, err := parser.Push([]byte("event: custom\ndata: trailing"), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Event != "custom" || events[0].Data != "trailing" {
		t.Fatalf("bad EOF trailing event: %#v", events)
	}
}

func TestSSEParserSkipsEmptyAndCommentOnlyEvents(t *testing.T) {
	var parser SSEParser
	events, err := parser.Push([]byte("\n\n: keepalive\n\n\r\n\r\ndata: real\r\n\r\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Data != "real" {
		t.Fatalf("expected only real event, got %#v", events)
	}
}

func TestSSEParserJoinsMultilineData(t *testing.T) {
	var parser SSEParser
	events, err := parser.Push([]byte("data: first\ndata: second\n\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Data != "first\nsecond" {
		t.Fatalf("bad multiline data event: %#v", events)
	}
}
