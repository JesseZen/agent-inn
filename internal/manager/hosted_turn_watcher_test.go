package manager

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
)

func TestHostedTurnWatcherPollOnceMarksInterruptedTurn(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{
		StateDir: stateDir,
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", filepath.Join(stateDir, "codex.jsonl"), "turn_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(running.TurnTranscriptPath, []byte(`{"type":"turn.completed","turn_id":"turn_1","status":"interrupted"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var gotCalls [][]string
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		gotCalls = append(gotCalls, append([]string{}, args...))
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := running
	want.TurnState = HostedTurnStateInterrupted
	want.TurnStateReason = hostedTurnInterruptedReason
	want.TurnTranscriptPath = ""
	want.TurnID = ""
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, want)
	}
	wantCalls := [][]string{TmuxHostedTurnStatusCommandForRecord(settings, want)}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestHostedTurnWatcherPollOncePreservesTodoMarker(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{
		StateDir: stateDir,
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
		UserMarker:   HostedUserMarkerTodo,
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", filepath.Join(stateDir, "codex.jsonl"), "turn_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(running.TurnTranscriptPath, []byte(`{"type":"turn.completed","turn_id":"turn_1","status":"success"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var gotCalls [][]string
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		gotCalls = append(gotCalls, append([]string{}, args...))
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := running
	want.TurnState = HostedTurnStateDone
	want.TurnTranscriptPath = ""
	want.TurnID = ""
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, want)
	}
	wantCalls := [][]string{
		TmuxActiveWindowDetailsCommandForSettings(settings),
		TmuxHostedTurnStatusCommandForRecord(settings, want),
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestHostedTurnWatcherPollOnceMarksEventMsgTurnAborted(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{
		StateDir: stateDir,
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", filepath.Join(stateDir, "codex.jsonl"), "turn_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(running.TurnTranscriptPath, []byte(`{"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn_1","reason":"interrupted"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var gotCalls [][]string
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		gotCalls = append(gotCalls, append([]string{}, args...))
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := running
	want.TurnState = HostedTurnStateInterrupted
	want.TurnStateReason = hostedTurnInterruptedReason
	want.TurnTranscriptPath = ""
	want.TurnID = ""
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, want)
	}
	wantCalls := [][]string{TmuxHostedTurnStatusCommandForRecord(settings, want)}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestHostedTurnWatcherPollOnceInfersMissingWatchFromLauncherSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stateDir := t.TempDir()
	settings := config.Settings{
		StateDir: stateDir,
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnState(created.SessionID, HostedTurnStateRunning, "", "019f44d7-9f27-71e1-9b4e-b8f1ad572c01")
	if err != nil {
		t.Fatal(err)
	}
	transcriptDir := filepath.Join(home, ".codex", "sessions", "2026", "07", "09")
	if err := os.MkdirAll(transcriptDir, 0700); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(transcriptDir, "rollout-2026-07-09T11-06-49-019f44d7-9f27-71e1-9b4e-b8f1ad572c01.jsonl")
	transcript := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"old_turn"}}`,
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"old_turn","last_agent_message":"done"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_1"}}`,
		`{"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn_1","reason":"interrupted"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}

	var gotCalls [][]string
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		gotCalls = append(gotCalls, append([]string{}, args...))
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := running
	want.TurnState = HostedTurnStateInterrupted
	want.TurnStateReason = hostedTurnInterruptedReason
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, want)
	}
	wantCalls := [][]string{TmuxHostedTurnStatusCommandForRecord(settings, want)}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestHostedTurnWatcherPollOnceMarksDoneTurnReadWhenActive(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{
		StateDir: stateDir,
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", filepath.Join(stateDir, "codex.jsonl"), "turn_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(running.TurnTranscriptPath, []byte(`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn_1","last_agent_message":"done"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var gotCalls [][]string
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		gotCalls = append(gotCalls, append([]string{}, args...))
		if len(gotCalls) == 1 {
			return "@12\tsolve problem A\n", nil
		}
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := running
	want.TurnState = HostedTurnStateDone
	want.TurnTranscriptPath = ""
	want.TurnID = ""
	want.TurnAcknowledgedGeneration = running.TurnGeneration
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, want)
	}
	wantCalls := [][]string{
		TmuxActiveWindowDetailsCommandForSettings(settings),
		TmuxHostedTurnStatusCommandForRecord(settings, want),
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestHostedTurnWatcherPollOnceCorrectsStopDoneToInterrupted(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{
		StateDir: stateDir,
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", filepath.Join(stateDir, "codex.jsonl"), "turn_1")
	if err != nil {
		t.Fatal(err)
	}
	done, err := registry.MarkTurnState(created.SessionID, HostedTurnStateDone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(running.TurnTranscriptPath, []byte(`{"type":"turn.completed","turn_id":"turn_1","status":"interrupted"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var gotCalls [][]string
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		gotCalls = append(gotCalls, append([]string{}, args...))
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := done
	want.TurnState = HostedTurnStateInterrupted
	want.TurnStateReason = hostedTurnInterruptedReason
	want.TurnTranscriptPath = ""
	want.TurnID = ""
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, want)
	}
	wantCalls := [][]string{TmuxHostedTurnStatusCommandForRecord(settings, want)}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestHostedTurnWatcherPollOnceSkipsUnchangedTranscript(t *testing.T) {
	stateDir := t.TempDir()
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"event_msg","payload":{"type":"unrelated"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1"); err != nil {
		t.Fatal(err)
	}

	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		t.Fatalf("unexpected tmux call: %#v", args)
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}
	before := watcher.files[transcriptPath]
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}
	after := watcher.files[transcriptPath]
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("got cursor %#v, want %#v", after, before)
	}
}
