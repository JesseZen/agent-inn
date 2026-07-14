package manager

import (
	"reflect"
	"testing"
)

func TestHostedSessionTurnStateCommitsInputRequestAndMatchingOutput(t *testing.T) {
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(t.TempDir()))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", "/tmp/codex.jsonl", "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}

	waiting, applied, err := registry.CommitWatchedTurnInput(running.SessionID, running.TurnGeneration, running.TurnTranscriptPath, running.TurnID, 101, "call-request")
	if err != nil {
		t.Fatal(err)
	}
	wantWaiting := running
	wantWaiting.TurnTranscriptOffset = 101
	wantWaiting.TurnInputRequestID = "call-request"
	if !applied || !reflect.DeepEqual(waiting, wantWaiting) {
		t.Fatalf("got %#v applied=%v, want %#v", waiting, applied, wantWaiting)
	}

	answered, applied, err := registry.CommitWatchedTurnInput(running.SessionID, running.TurnGeneration, running.TurnTranscriptPath, running.TurnID, 202, "")
	if err != nil {
		t.Fatal(err)
	}
	wantAnswered := wantWaiting
	wantAnswered.TurnTranscriptOffset = 202
	wantAnswered.TurnInputRequestID = ""
	if !applied || !reflect.DeepEqual(answered, wantAnswered) {
		t.Fatalf("got %#v applied=%v, want %#v", answered, applied, wantAnswered)
	}
}

func TestHostedSessionTurnStateRejectsStaleInputCommit(t *testing.T) {
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(t.TempDir()))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", "/tmp/codex.jsonl", "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}

	for _, input := range []struct {
		name       string
		generation int
		path       string
		turnID     string
	}{
		{name: "generation", generation: running.TurnGeneration + 1, path: running.TurnTranscriptPath, turnID: running.TurnID},
		{name: "path", generation: running.TurnGeneration, path: "/tmp/other.jsonl", turnID: running.TurnID},
		{name: "turn", generation: running.TurnGeneration, path: running.TurnTranscriptPath, turnID: "turn_2"},
	} {
		t.Run(input.name, func(t *testing.T) {
			got, applied, err := registry.CommitWatchedTurnInput(running.SessionID, input.generation, input.path, input.turnID, 101, "call-request")
			if err != nil {
				t.Fatal(err)
			}
			if applied || !reflect.DeepEqual(got, HostedSessionRecord{}) {
				t.Fatalf("got %#v applied=%v, want rejected commit", got, applied)
			}
			persisted, found, err := registry.Get(running.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if !found || !reflect.DeepEqual(persisted, running) {
				t.Fatalf("got %#v found=%v, want %#v", persisted, found, running)
			}
		})
	}
}

func TestHostedSessionTurnStateClearsInputOnTerminalAndNewGeneration(t *testing.T) {
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(t.TempDir()))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", "/tmp/codex.jsonl", "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	waiting, _, err := registry.CommitWatchedTurnInput(running.SessionID, running.TurnGeneration, running.TurnTranscriptPath, running.TurnID, 101, "call-request")
	if err != nil {
		t.Fatal(err)
	}

	done, applied, err := registry.CompleteWatchedTurn(waiting.SessionID, waiting.TurnGeneration, HostedTurnStateDone, "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	wantDone := waiting
	wantDone.TurnState = HostedTurnStateDone
	wantDone.TurnTranscriptPath = ""
	wantDone.TurnTranscriptOffset = 0
	wantDone.TurnID = ""
	wantDone.TurnWatchKind = ""
	wantDone.TurnInputRequestID = ""
	if !applied || !reflect.DeepEqual(done, wantDone) {
		t.Fatalf("got %#v applied=%v, want %#v", done, applied, wantDone)
	}

	next, err := registry.MarkTurnStateWithWatch(waiting.SessionID, HostedTurnStateRunning, "", "", "/tmp/codex.jsonl", "turn_2", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	wantNext := wantDone
	wantNext.TurnGeneration++
	wantNext.TurnState = HostedTurnStateRunning
	wantNext.TurnTranscriptPath = "/tmp/codex.jsonl"
	wantNext.TurnID = "turn_2"
	wantNext.TurnWatchKind = HostedTurnWatchKindCodex
	if !reflect.DeepEqual(next, wantNext) {
		t.Fatalf("got %#v, want %#v", next, wantNext)
	}
}

func TestHostedSessionTurnStatePreservesInputAcrossGoalAndLateIdleUpdates(t *testing.T) {
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(t.TempDir()))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:      "solve problem A",
		WorkerName:        "worker",
		WorkerPort:        11199,
		LauncherSessionID: "goal-thread",
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "goal-thread", "/tmp/codex.jsonl", "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	waiting, _, err := registry.CommitWatchedTurnInput(running.SessionID, running.TurnGeneration, running.TurnTranscriptPath, running.TurnID, 101, "call-request")
	if err != nil {
		t.Fatal(err)
	}

	active, err := registry.SetCodexGoalStatus(waiting.SessionID, codexTranscriptGoalActive)
	if err != nil {
		t.Fatal(err)
	}
	wantActive := waiting
	wantActive.TurnWatchKind = HostedTurnWatchKindCodexGoal
	if !reflect.DeepEqual(active, wantActive) {
		t.Fatalf("got %#v, want %#v", active, wantActive)
	}
	paused, err := registry.SetCodexGoalStatus(waiting.SessionID, codexTranscriptGoalPaused)
	if err != nil {
		t.Fatal(err)
	}
	wantPaused := wantActive
	wantPaused.TurnWatchKind = HostedTurnWatchKindCodexGoalPaused
	if !reflect.DeepEqual(paused, wantPaused) {
		t.Fatalf("got %#v, want %#v", paused, wantPaused)
	}
	lateIdle, err := registry.MarkTurnState(waiting.SessionID, HostedTurnStateIdle, "", "goal-thread")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lateIdle, wantPaused) {
		t.Fatalf("got %#v, want %#v", lateIdle, wantPaused)
	}
}
