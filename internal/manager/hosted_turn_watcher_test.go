package manager

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", filepath.Join(stateDir, "codex.jsonl"), "turn_1", HostedTurnWatchKindCodex)
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
	want.TurnWatchKind = ""
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, want)
	}
	wantCalls := [][]string{TmuxHostedTurnStatusCommandForRecord(settings, want)}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestHostedTurnWatcherPollOncePublishesTurnState(t *testing.T) {
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
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", filepath.Join(stateDir, "codex.jsonl"), "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(running.TurnTranscriptPath, []byte(`{"type":"turn.completed","turn_id":"turn_1","status":"success"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var published []HostedSessionRecord
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		return "", nil
	}))
	watcher.onTurnStateChanged = func(session HostedSessionRecord) {
		published = append(published, session)
	}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	want := []HostedSessionRecord{{
		SessionID:      created.SessionID,
		SessionLabel:   created.SessionLabel,
		WorkerID:       created.WorkerID,
		WorkerName:     created.WorkerName,
		WorkerPort:     created.WorkerPort,
		TmuxWindowID:   created.TmuxWindowID,
		TurnState:      HostedTurnStateDone,
		TurnGeneration: running.TurnGeneration,
		CreatedAt:      created.CreatedAt,
		LastOpenedAt:   created.LastOpenedAt,
	}}
	if !reflect.DeepEqual(published, want) {
		t.Fatalf("got %#v, want %#v", published, want)
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
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", filepath.Join(stateDir, "codex.jsonl"), "turn_1", HostedTurnWatchKindCodex)
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
	want.TurnWatchKind = ""
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
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", filepath.Join(stateDir, "codex.jsonl"), "turn_1", HostedTurnWatchKindCodex)
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
	want.TurnWatchKind = ""
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
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "019f44d7-9f27-71e1-9b4e-b8f1ad572c01", "", "", HostedTurnWatchKindCodex)
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
	want.TurnWatchKind = ""
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, want)
	}
	wantCalls := [][]string{TmuxHostedTurnStatusCommandForRecord(settings, want)}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestHostedTurnWatcherPollOnceWaitsForNewLauncherTaskStartedOnLaterTurn(t *testing.T) {
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
	launcherSessionID := "019f44d7-9f27-71e1-9b4e-b8f1ad572c01"
	firstRunning, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", launcherSessionID, "", "", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	transcriptDir := filepath.Join(home, ".codex", "sessions", "2026", "07", "09")
	if err := os.MkdirAll(transcriptDir, 0700); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(transcriptDir, "rollout-2026-07-09T11-06-49-"+launcherSessionID+".jsonl")
	transcript := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"old_turn"}}`,
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"old_turn","last_agent_message":"done"}}`,
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
	firstDone, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantFirstDone := firstRunning
	wantFirstDone.TurnState = HostedTurnStateDone
	wantFirstDone.TurnWatchKind = ""
	if !ok || !reflect.DeepEqual(firstDone, wantFirstDone) {
		t.Fatalf("first turn got %#v ok=%v, want %#v", firstDone, ok, wantFirstDone)
	}

	secondRunning, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", launcherSessionID, "", "", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	gotCalls = nil
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}
	stillRunning, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !reflect.DeepEqual(stillRunning, secondRunning) {
		t.Fatalf("second turn before task_started got %#v ok=%v, want %#v", stillRunning, ok, secondRunning)
	}
	if len(gotCalls) != 0 {
		t.Fatalf("got tmux calls before new task_started: %#v", gotCalls)
	}
	plans, err := watcher.watchPlans()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plans, []hostedTurnWatchPlan{}) {
		t.Fatalf("got cached watch plans before new task_started: %#v", plans)
	}

	transcript += strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_2"}}`,
		`{"type":"turn.completed","turn_id":"turn_2","status":"interrupted"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}
	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := secondRunning
	want.TurnState = HostedTurnStateInterrupted
	want.TurnStateReason = hostedTurnInterruptedReason
	want.TurnWatchKind = ""
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("second turn got %#v ok=%v, want %#v", updated, ok, want)
	}
	wantCalls := [][]string{TmuxHostedTurnStatusCommandForRecord(settings, want)}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestHostedTurnWatcherPollOnceMarksNextGoalTurnRunning(t *testing.T) {
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
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	transcript := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn_1","last_agent_message":"done"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_2"}}`,
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
	wantDone := running
	wantDone.TurnState = HostedTurnStateDone
	wantDone.TurnTranscriptPath = ""
	wantDone.TurnID = ""
	wantDone.TurnWatchKind = ""
	wantRunning := running
	wantRunning.TurnGeneration = running.TurnGeneration + 1
	wantRunning.TurnID = "turn_2"
	if !ok || !reflect.DeepEqual(updated, wantRunning) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantRunning)
	}
	wantCalls := [][]string{
		TmuxActiveWindowDetailsCommandForSettings(settings),
		TmuxHostedTurnStatusCommandForRecord(settings, wantDone),
		TmuxHostedTurnStatusCommandForRecord(settings, wantRunning),
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestHostedTurnWatcherPollOnceTracksActiveGoalAcrossPolls(t *testing.T) {
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
		SessionLabel:      "solve problem A",
		WorkerName:        "worker",
		WorkerPort:        11199,
		TmuxWindowID:      "@12",
		LauncherSessionID: "goal-thread",
		TurnState:         HostedTurnStateIdle,
		TurnGeneration:    8,
	})
	if err != nil {
		t.Fatal(err)
	}
	transcriptDir := filepath.Join(home, ".codex", "sessions", "2026", "07", "10")
	if err := os.MkdirAll(transcriptDir, 0700); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(transcriptDir, "rollout-2026-07-10-goal-thread.jsonl")
	goalActive := `{"type":"event_msg","payload":{"type":"thread_goal_updated","threadId":"goal-thread","goal":{"status":"active"}}}`
	firstTurnStarted := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_1"}}`
	firstTurnComplete := `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn_1","last_agent_message":"done"}}`
	firstPollTranscript := strings.Join([]string{
		goalActive,
		firstTurnStarted,
		firstTurnComplete,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(firstPollTranscript), 0600); err != nil {
		t.Fatal(err)
	}

	var gotCalls [][]string
	runner := hostedTMuxRunnerFunc(func(args []string) (string, error) {
		gotCalls = append(gotCalls, append([]string{}, args...))
		return "", nil
	})
	watcher := newHostedTurnWatcher(settings, registry, runner)
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	firstDone, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantFirstDone := created
	wantFirstDone.TurnGeneration++
	wantFirstDone.TurnState = HostedTurnStateDone
	wantFirstDone.TurnTranscriptPath = transcriptPath
	wantFirstDone.TurnID = "turn_1"
	wantFirstDone.TurnWatchKind = HostedTurnWatchKindCodexGoal
	wantFirstDone.TurnTranscriptOffset = int64(len(firstPollTranscript))
	if !ok || !reflect.DeepEqual(firstDone, wantFirstDone) {
		t.Fatalf("first turn got %#v ok=%v, want %#v", firstDone, ok, wantFirstDone)
	}
	wantFirstRunning := created
	wantFirstRunning.TurnGeneration++
	wantFirstRunning.TurnState = HostedTurnStateRunning
	wantFirstRunning.TurnTranscriptPath = transcriptPath
	wantFirstRunning.TurnTranscriptOffset = int64(len(goalActive) + len(firstTurnStarted) + 2)
	wantFirstRunning.TurnID = "turn_1"
	wantFirstRunning.TurnWatchKind = HostedTurnWatchKindCodexGoal
	wantFirstCalls := [][]string{
		TmuxHostedTurnStatusCommandForRecord(settings, wantFirstRunning),
		TmuxActiveWindowDetailsCommandForSettings(settings),
		TmuxHostedTurnStatusCommandForRecord(settings, wantFirstDone),
	}
	if !reflect.DeepEqual(gotCalls, wantFirstCalls) {
		t.Fatalf("first poll tmux calls %#v, want %#v", gotCalls, wantFirstCalls)
	}

	gotCalls = nil
	watcher = newHostedTurnWatcher(settings, registry, runner)
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}
	if len(gotCalls) != 0 {
		t.Fatalf("got tmux calls while waiting for next goal turn: %#v", gotCalls)
	}

	nextTurn := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_2"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(firstPollTranscript+nextTurn), 0600); err != nil {
		t.Fatal(err)
	}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantRunning := wantFirstDone
	wantRunning.TurnGeneration++
	wantRunning.TurnState = HostedTurnStateRunning
	wantRunning.TurnID = "turn_2"
	wantRunning.TurnTranscriptOffset = int64(len(firstPollTranscript + nextTurn))
	if !ok || !reflect.DeepEqual(updated, wantRunning) {
		t.Fatalf("next goal turn got %#v ok=%v, want %#v", updated, ok, wantRunning)
	}
	wantNextCalls := [][]string{TmuxHostedTurnStatusCommandForRecord(settings, wantRunning)}
	if !reflect.DeepEqual(gotCalls, wantNextCalls) {
		t.Fatalf("next goal turn tmux calls %#v, want %#v", gotCalls, wantNextCalls)
	}
}

func TestHostedTurnWatcherPollOnceWaitsForPausedGoalToBecomeActive(t *testing.T) {
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
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "goal-thread", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	activeGoalTranscript := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"thread_goal_updated","threadId":"goal-thread","goal":{"status":"active"}}}`,
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn_1","last_agent_message":"done"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(activeGoalTranscript), 0600); err != nil {
		t.Fatal(err)
	}

	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func([]string) (string, error) {
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	pausedGoal := `{"type":"event_msg","payload":{"type":"thread_goal_updated","threadId":"goal-thread","goal":{"status":"paused"}}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(activeGoalTranscript+pausedGoal), 0600); err != nil {
		t.Fatal(err)
	}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	paused, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantPaused := running
	wantPaused.TurnState = HostedTurnStateDone
	wantPaused.TurnWatchKind = HostedTurnWatchKindCodexGoalPaused
	wantPaused.TurnTranscriptOffset = int64(len(activeGoalTranscript))
	if !ok || !reflect.DeepEqual(paused, wantPaused) {
		t.Fatalf("paused goal got %#v ok=%v, want %#v", paused, ok, wantPaused)
	}

	pausedTurn := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_2"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(activeGoalTranscript+pausedGoal+pausedTurn), 0600); err != nil {
		t.Fatal(err)
	}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !reflect.DeepEqual(updated, wantPaused) {
		t.Fatalf("paused goal restarted as %#v ok=%v, want %#v", updated, ok, wantPaused)
	}

	resumedGoal := `{"type":"event_msg","payload":{"type":"thread_goal_updated","threadId":"goal-thread","goal":{"status":"active"}}}` + "\n"
	nextTurn := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_3"}}` + "\n"
	fullTranscript := activeGoalTranscript + pausedGoal + pausedTurn + resumedGoal + nextTurn
	if err := os.WriteFile(transcriptPath, []byte(fullTranscript), 0600); err != nil {
		t.Fatal(err)
	}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	resumed, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantResumed := wantPaused
	wantResumed.TurnGeneration++
	wantResumed.TurnState = HostedTurnStateRunning
	wantResumed.TurnWatchKind = HostedTurnWatchKindCodexGoal
	wantResumed.TurnID = "turn_3"
	wantResumed.TurnTranscriptOffset = int64(len(fullTranscript))
	if !ok || !reflect.DeepEqual(resumed, wantResumed) {
		t.Fatalf("resumed goal got %#v ok=%v, want %#v", resumed, ok, wantResumed)
	}
}

func TestHostedTurnWatcherPollOnceRechecksUnresolvedLauncherWatchAfterTranscriptAppears(t *testing.T) {
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
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "019f44d7-9f27-71e1-9b4e-b8f1ad572c01", "", "", HostedTurnWatchKindCodex)
	if err != nil {
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
	if len(gotCalls) != 0 {
		t.Fatalf("got tmux calls before transcript existed: %#v", gotCalls)
	}

	transcriptDir := filepath.Join(home, ".codex", "sessions", "2026", "07", "09")
	if err := os.MkdirAll(transcriptDir, 0700); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(transcriptDir, "rollout-2026-07-09T11-06-49-019f44d7-9f27-71e1-9b4e-b8f1ad572c01.jsonl")
	transcript := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_1"}}`,
		`{"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn_1","reason":"interrupted"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
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
	want.TurnWatchKind = ""
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
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", filepath.Join(stateDir, "codex.jsonl"), "turn_1", HostedTurnWatchKindCodex)
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
	want.TurnWatchKind = ""
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
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", filepath.Join(stateDir, "codex.jsonl"), "turn_1", HostedTurnWatchKindCodex)
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
	want.TurnWatchKind = ""
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
	if _, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex); err != nil {
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


func TestHostedTurnWatcherBacksOffMissingTranscriptGlob(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:      "missing-transcript",
		WorkerName:        "cli",
		WorkerPort:        33333,
		LauncherSessionID: "claude-uuid-never-in-codex",
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1_700_000_000, 0).UTC()
	globCalls := 0
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		t.Fatalf("unexpected tmux call: %#v", args)
		return "", nil
	}))
	watcher.now = func() time.Time { return now }
	watcher.globTranscripts = func(pattern string) ([]string, error) {
		globCalls++
		if !strings.Contains(pattern, "claude-uuid-never-in-codex") {
			t.Fatalf("unexpected glob pattern %q", pattern)
		}
		return nil, nil
	}

	if _, err := watcher.watchPlans(); err != nil {
		t.Fatal(err)
	}
	if globCalls != 1 {
		t.Fatalf("first watchPlans globCalls=%d, want 1", globCalls)
	}
	if _, err := watcher.watchPlans(); err != nil {
		t.Fatal(err)
	}
	if globCalls != 1 {
		t.Fatalf("second watchPlans during miss TTL globCalls=%d, want 1", globCalls)
	}
	if watcher.registryCursor.Plans == nil {
		t.Fatal("expected registryCursor to cache plans after Glob miss")
	}

	// Force rebuild with same registry mtime; miss TTL still active so no Glob.
	watcher.registryCursor = hostedTurnRegistryCursor{}
	if _, err := watcher.watchPlans(); err != nil {
		t.Fatal(err)
	}
	if globCalls != 1 {
		t.Fatalf("rebuild during miss TTL globCalls=%d, want 1", globCalls)
	}

	// After TTL, miss expires and Glob runs again.
	now = now.Add(hostedTurnTranscriptMissTTL + time.Second)
	watcher.registryCursor = hostedTurnRegistryCursor{}
	if _, err := watcher.watchPlans(); err != nil {
		t.Fatal(err)
	}
	if globCalls != 2 {
		t.Fatalf("after miss TTL globCalls=%d, want 2", globCalls)
	}

	// Positive path still works and clears miss cache.
	transcriptPath := filepath.Join(stateDir, "found.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_1"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	now = now.Add(hostedTurnTranscriptMissTTL + time.Second)
	watcher.registryCursor = hostedTurnRegistryCursor{}
	watcher.globTranscripts = func(pattern string) ([]string, error) {
		globCalls++
		return []string{transcriptPath}, nil
	}
	plans, err := watcher.watchPlans()
	if err != nil {
		t.Fatal(err)
	}
	if globCalls != 3 {
		t.Fatalf("found-path globCalls=%d, want 3", globCalls)
	}
	if len(plans) != 1 || plans[0].TranscriptPath != transcriptPath {
		t.Fatalf("got plans %#v, want transcript %q", plans, transcriptPath)
	}
	if _, active := watcher.launcherMissUntil[created.LauncherSessionID]; active {
		t.Fatal("expected miss cache cleared after successful Glob")
	}
}
