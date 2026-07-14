package manager

import (
	"reflect"
	"strings"
	"testing"
)

func TestHostedTurnTranscriptReducesExactInputRequestEnvelopes(t *testing.T) {
	requestLine := hostedTurnTranscriptLine{
		Data:   `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call-request"}}`,
		Offset: 101,
	}
	answerLine := hostedTurnTranscriptLine{
		Data:   `{"type":"response_item","payload":{"type":"function_call_output","call_id":"call-request"}}`,
		Offset: 202,
	}

	wantRequest := hostedTurnTranscriptReduction{
		InputRequestID: "call-request",
		FinalOffset:    101,
		InputObserved:  true,
		InputChanged:   true,
	}
	got, err := reduceHostedTurnTranscript(HostedTurnWatchKindCodex, "", []hostedTurnTranscriptLine{requestLine})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, wantRequest) {
		t.Fatalf("got %#v, want %#v", got, wantRequest)
	}

	wantAnswer := hostedTurnTranscriptReduction{
		FinalOffset:   202,
		InputObserved: true,
		InputChanged:  true,
	}
	got, err = reduceHostedTurnTranscript(HostedTurnWatchKindCodexGoalPaused, "call-request", []hostedTurnTranscriptLine{answerLine})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, wantAnswer) {
		t.Fatalf("got %#v, want %#v", got, wantAnswer)
	}

	wantNetZero := hostedTurnTranscriptReduction{
		FinalOffset:   202,
		InputObserved: true,
	}
	got, err = reduceHostedTurnTranscript(HostedTurnWatchKindCodexGoal, "", []hostedTurnTranscriptLine{requestLine, answerLine})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, wantNetZero) {
		t.Fatalf("got %#v, want %#v", got, wantNetZero)
	}
}

func TestHostedTurnTranscriptKeepsOnlyFinalLifecycleObservation(t *testing.T) {
	lines := []hostedTurnTranscriptLine{
		{Data: `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_1"}}`, Offset: 100},
		{Data: `{"type":"turn.completed","turn_id":"turn_1","status":"success"}`, Offset: 200},
	}
	want := hostedTurnTranscriptReduction{
		Lifecycle: hostedTurnTranscriptResult{
			TurnID:           "turn_1",
			State:            HostedTurnStateDone,
			TranscriptOffset: 200,
		},
		LifecycleObserved: true,
		FinalOffset:       200,
	}
	got, err := reduceHostedTurnTranscript(HostedTurnWatchKindCodex, "", lines)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestHostedTurnTranscriptRejectsInvalidInputTransitions(t *testing.T) {
	lines := []hostedTurnTranscriptLine{
		{Data: `{"type":"response_item","payload":{"type":"function_call_output","call_id":"other-call"}}`, Offset: 100},
		{Data: `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"second-call"}}`, Offset: 200},
		{Data: `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"first-call"}}`, Offset: 300},
	}
	want := hostedTurnTranscriptReduction{
		InputRequestID: "first-call",
		FinalOffset:    300,
	}
	got, err := reduceHostedTurnTranscript(HostedTurnWatchKindCodex, "first-call", lines)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestHostedTurnTranscriptIgnoresUnsupportedAndInexactEnvelopes(t *testing.T) {
	request := hostedTurnTranscriptLine{
		Data:   `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call-request"}}`,
		Offset: 101,
	}
	for _, kind := range []string{"", "claudecode", "grok", "opencode", "pi"} {
		got, err := reduceHostedTurnTranscript(kind, "", []hostedTurnTranscriptLine{request})
		if err != nil {
			t.Fatal(err)
		}
		want := hostedTurnTranscriptReduction{FinalOffset: 101}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("kind %q got %#v, want %#v", kind, got, want)
		}
	}

	for _, data := range []string{
		`{"type":"response_item","payload":{"type":"function_call","name":"request_user_input"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"other","call_id":"call-request"}}`,
		`{"type":"event_msg","payload":{"type":"function_call","name":"request_user_input","call_id":"call-request"}}`,
		`{"type":"response_item","payload":{"type":"other","name":"request_user_input","call_id":"call-request"}}`,
	} {
		got, err := reduceHostedTurnTranscript(HostedTurnWatchKindCodex, "", []hostedTurnTranscriptLine{{Data: data, Offset: 101}})
		if err != nil {
			t.Fatal(err)
		}
		want := hostedTurnTranscriptReduction{FinalOffset: 101}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("line %s got %#v, want %#v", data, got, want)
		}
	}
}

func TestHostedTurnTranscriptMalformedJSONDoesNotExposeInputIdentifiers(t *testing.T) {
	secret := "call-private-value"
	_, err := reduceHostedTurnTranscript(HostedTurnWatchKindCodex, "", []hostedTurnTranscriptLine{{
		Data:   `{"type":"response_item","payload":{"call_id":"` + secret + `"`,
		Offset: 101,
	}})
	if err == nil {
		t.Fatal("expected malformed JSON error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposed call ID: %v", err)
	}
}
