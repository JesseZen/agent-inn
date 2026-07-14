package manager

import (
	"errors"
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
	wantCalls := [][]string{hostedTestStatusCommand(settings, want)}
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
		hostedTestStatusCommand(settings, want),
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
	wantCalls := [][]string{hostedTestStatusCommand(settings, want)}
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
	wantCalls := [][]string{hostedTestStatusCommand(settings, want)}
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
	wantCalls := [][]string{hostedTestStatusCommand(settings, want)}
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
	wantRunning.TurnTranscriptOffset = int64(len(transcript))
	wantRunning.TurnID = "turn_2"
	if !ok || !reflect.DeepEqual(updated, wantRunning) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantRunning)
	}
	wantCalls := [][]string{
		TmuxActiveWindowDetailsCommandForSettings(settings),
		hostedTestStatusCommand(settings, wantDone),
		hostedTestStatusCommand(settings, wantRunning),
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestHostedTurnWatcherPollOnceDoesNotCarryInputAcrossGeneration(t *testing.T) {
	for _, tc := range []struct {
		name             string
		tail             string
		wantRequestID    string
		wantOffsetAtTail bool
	}{
		{
			name: "no next request",
			tail: `{"type":"response_item","payload":{"type":"message","role":"assistant"}}` + "\n",
		},
		{
			name:             "next request",
			tail:             `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"next-call"}}` + "\n",
			wantRequestID:    "next-call",
			wantOffsetAtTail: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			transcriptPath := filepath.Join(stateDir, "codex.jsonl")
			registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
			created, err := registry.Create(HostedSessionRecord{SessionLabel: "solve problem A", WorkerName: "worker", WorkerPort: 11199})
			if err != nil {
				t.Fatal(err)
			}
			running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
			if err != nil {
				t.Fatal(err)
			}
			oldRequest := `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"old-call"}}` + "\n"
			terminal := `{"type":"turn.completed","turn_id":"turn_1","status":"success"}` + "\n"
			nextStarted := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_2"}}` + "\n"
			transcript := oldRequest + terminal + nextStarted + tc.tail
			if err := os.WriteFile(transcriptPath, []byte(transcript), 0600); err != nil {
				t.Fatal(err)
			}
			watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func([]string) (string, error) { return "", nil }))
			if err := watcher.pollOnce(); err != nil {
				t.Fatal(err)
			}
			got, found, err := registry.Get(running.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			want := running
			want.TurnGeneration++
			want.TurnState = HostedTurnStateRunning
			want.TurnID = "turn_2"
			want.TurnTranscriptOffset = int64(len(oldRequest + terminal + nextStarted))
			want.TurnInputRequestID = tc.wantRequestID
			if tc.wantOffsetAtTail {
				want.TurnTranscriptOffset = int64(len(transcript))
			}
			if !found || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v found=%v, want next generation %#v", got, found, want)
			}
		})
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
		hostedTestStatusCommand(settings, wantFirstRunning),
		TmuxActiveWindowDetailsCommandForSettings(settings),
		hostedTestStatusCommand(settings, wantFirstDone),
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
	plans, err := watcher.watchPlans()
	if err != nil {
		t.Fatal(err)
	}
	runningWatchCount := 0
	for _, plan := range plans {
		for _, watches := range plan.TurnsByID {
			for _, watch := range watches {
				if watch.TurnState == HostedTurnStateRunning {
					runningWatchCount++
				}
			}
		}
	}
	if runningWatchCount != 0 {
		t.Fatalf("running watches before next goal turn = %d, want 0", runningWatchCount)
	}

	nextTurnStarted := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_2"}}` + "\n"
	nextTurn := nextTurnStarted + `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call_2"}}` + "\n"
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
	wantRunning.TurnInputRequestID = "call_2"
	if !ok || !reflect.DeepEqual(updated, wantRunning) {
		t.Fatalf("next goal turn got %#v ok=%v, want %#v", updated, ok, wantRunning)
	}
	wantStarted := wantRunning
	wantStarted.TurnTranscriptOffset = int64(len(firstPollTranscript + nextTurnStarted))
	wantStarted.TurnInputRequestID = ""
	wantNextCalls := [][]string{
		hostedTestStatusCommand(settings, wantStarted),
		hostedTestStatusCommand(settings, wantRunning),
	}
	if !reflect.DeepEqual(gotCalls, wantNextCalls) {
		t.Fatalf("next goal turn tmux calls %#v, want %#v", gotCalls, wantNextCalls)
	}
}

func TestHostedTurnWatcherPollOnceAttributesInputToGoalStartedInSamePoll(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	base := `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn_1","last_agent_message":"done"}}` + "\n"
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:         "solve problem A",
		WorkerName:           "worker",
		WorkerPort:           11199,
		LauncherSessionID:    "goal-thread",
		TurnState:            HostedTurnStateDone,
		TurnGeneration:       1,
		TurnTranscriptPath:   transcriptPath,
		TurnTranscriptOffset: int64(len(base)),
		TurnID:               "turn_1",
		TurnWatchKind:        HostedTurnWatchKindCodexGoal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func([]string) (string, error) {
		return "", nil
	}))
	watcher.files[transcriptPath] = hostedTurnTranscriptCursor{Offset: int64(len(base)), Size: stat.Size(), ModTime: stat.ModTime()}
	next := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_2"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call_2"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(base+next), 0600); err != nil {
		t.Fatal(err)
	}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := created
	want.TurnGeneration++
	want.TurnState = HostedTurnStateRunning
	want.TurnStateReason = ""
	want.TurnTranscriptOffset = int64(len(base + next))
	want.TurnID = "turn_2"
	want.TurnInputRequestID = "call_2"
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, want)
	}
}

func TestHostedTurnWatcherPollOnceDoesNotCarryOldInputIntoNextGoalTurn(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	base := `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn_1","last_agent_message":"done"}}` + "\n"
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:         "solve problem A",
		WorkerName:           "worker",
		WorkerPort:           11199,
		LauncherSessionID:    "goal-thread",
		TurnState:            HostedTurnStateDone,
		TurnGeneration:       1,
		TurnTranscriptPath:   transcriptPath,
		TurnTranscriptOffset: int64(len(base)),
		TurnID:               "turn_1",
		TurnWatchKind:        HostedTurnWatchKindCodexGoal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func([]string) (string, error) { return "", nil }))
	watcher.files[transcriptPath] = hostedTurnTranscriptCursor{Offset: int64(len(base)), Size: stat.Size(), ModTime: stat.ModTime()}
	oldInput := `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"old_call"}}` + "\n"
	terminal := `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn_1","last_agent_message":"done"}}` + "\n"
	next := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_2"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(base+oldInput+terminal+next), 0600); err != nil {
		t.Fatal(err)
	}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := created
	want.TurnGeneration++
	want.TurnState = HostedTurnStateRunning
	want.TurnStateReason = ""
	want.TurnTranscriptOffset = int64(len(base + oldInput + terminal + next))
	want.TurnID = "turn_2"
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, want)
	}
}

func TestHostedTurnWatcherPollOnceDoesNotCarryOldInputIntoNextCodexTurn(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	base := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_1"}}` + "\n"
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:         "solve problem A",
		WorkerName:           "worker",
		WorkerPort:           11199,
		TurnState:            HostedTurnStateRunning,
		TurnGeneration:       1,
		TurnTranscriptPath:   transcriptPath,
		TurnTranscriptOffset: int64(len(base)),
		TurnID:               "turn_1",
		TurnWatchKind:        HostedTurnWatchKindCodex,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func([]string) (string, error) { return "", nil }))
	watcher.files[transcriptPath] = hostedTurnTranscriptCursor{Offset: int64(len(base)), Size: stat.Size(), ModTime: stat.ModTime()}
	oldInput := `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"old_call"}}` + "\n"
	terminal := `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn_1","last_agent_message":"done"}}` + "\n"
	next := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_2"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(base+oldInput+terminal+next), 0600); err != nil {
		t.Fatal(err)
	}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := created
	want.TurnGeneration++
	want.TurnID = "turn_2"
	want.TurnTranscriptOffset = int64(len(base + oldInput + terminal + next))
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, want)
	}
}

func TestHostedTurnWatcherPollOncePreservesGoalStatusAfterTerminalEvent(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	base := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn_1"}}` + "\n"
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:         "solve problem A",
		WorkerName:           "worker",
		WorkerPort:           11199,
		LauncherSessionID:    "goal-thread",
		TurnState:            HostedTurnStateRunning,
		TurnGeneration:       1,
		TurnTranscriptPath:   transcriptPath,
		TurnTranscriptOffset: int64(len(base)),
		TurnID:               "turn_1",
		TurnWatchKind:        HostedTurnWatchKindCodexGoal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func([]string) (string, error) { return "", nil }))
	watcher.files[transcriptPath] = hostedTurnTranscriptCursor{Offset: int64(len(base)), Size: stat.Size(), ModTime: stat.ModTime()}
	terminal := `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn_1","last_agent_message":"done"}}` + "\n"
	paused := `{"type":"event_msg","payload":{"type":"thread_goal_updated","threadId":"goal-thread","goal":{"status":"paused"}}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(base+terminal+paused), 0600); err != nil {
		t.Fatal(err)
	}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := created
	want.TurnState = HostedTurnStateDone
	want.TurnWatchKind = HostedTurnWatchKindCodexGoalPaused
	want.TurnTranscriptOffset = int64(len(base + terminal))
	if !ok || !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, want)
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
	wantCalls := [][]string{hostedTestStatusCommand(settings, want)}
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
		hostedTestStatusCommand(settings, want),
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
	wantCalls := [][]string{hostedTestStatusCommand(settings, want)}
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

func TestHostedTurnWatcherPollOnceCommitsInputRequestBeforeProjection(t *testing.T) {
	stateDir := t.TempDir()
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
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
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call-request"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}

	var projected []HostedSessionRecord
	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func([]string) (string, error) {
		got, found, err := registry.Get(running.SessionID)
		if err != nil || !found {
			t.Fatalf("projection read found=%v err=%v", found, err)
		}
		projected = append(projected, got)
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}

	want := running
	want.TurnTranscriptOffset = int64(len(line))
	want.TurnInputRequestID = "call-request"
	got, found, err := registry.Get(running.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v found=%v, want %#v", got, found, want)
	}
	if !reflect.DeepEqual(projected, []HostedSessionRecord{want}) {
		t.Fatalf("projected %#v, want committed %#v", projected, []HostedSessionRecord{want})
	}
	wantCursor := hostedTurnTranscriptCursor{Offset: int64(len(line)), Size: int64(len(line))}
	gotCursor := watcher.files[transcriptPath]
	wantCursor.ModTime = gotCursor.ModTime
	if !reflect.DeepEqual(gotCursor, wantCursor) {
		t.Fatalf("cursor %#v, want %#v", gotCursor, wantCursor)
	}
}

func TestHostedTurnWatcherPollOnceDoesNotProjectSamePollInputNetZero(t *testing.T) {
	stateDir := t.TempDir()
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{SessionLabel: "solve problem A", WorkerName: "worker", WorkerPort: 11199, TmuxWindowID: "@12"})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	transcript := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call-request"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"call-request"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		t.Fatalf("unexpected net-zero projection: %#v", args)
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}
	want := running
	want.TurnTranscriptOffset = int64(len(transcript))
	got, found, err := registry.Get(running.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v found=%v, want %#v", got, found, want)
	}
}

func TestHostedTurnWatcherPollOnceConsumesAmbiguousInputWithoutMutation(t *testing.T) {
	stateDir := t.TempDir()
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	for _, label := range []string{"solve problem A", "solve problem B"} {
		created, err := registry.Create(HostedSessionRecord{SessionLabel: label, WorkerName: "worker", WorkerPort: 11199})
		if err != nil {
			t.Fatal(err)
		}
		_, err = registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
		if err != nil {
			t.Fatal(err)
		}
	}
	want, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call-request"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		t.Fatalf("unexpected ambiguous projection: %#v", args)
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}
	got, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want unchanged %#v", got, want)
	}
	if watcher.files[transcriptPath].Offset != int64(len(line)) {
		t.Fatalf("cursor %#v, want consumed offset %d", watcher.files[transcriptPath], len(line))
	}
}

func TestHostedTurnWatcherPollOnceReplaysCommittedInputAfterProjectionFailure(t *testing.T) {
	stateDir := t.TempDir()
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{SessionLabel: "solve problem A", WorkerName: "worker", WorkerPort: 11199, TmuxWindowID: "@12"})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call-request"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	projectionErr := errors.New("tmux projection failed")
	projections := 0
	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func([]string) (string, error) {
		projections++
		if projections == 1 {
			return "", projectionErr
		}
		return "", nil
	}))
	published := 0
	watcher.onTurnStateChanged = func(HostedSessionRecord) { published++ }
	if err := watcher.pollOnce(); !errors.Is(err, projectionErr) {
		t.Fatalf("got %v, want %v", err, projectionErr)
	}
	want := running
	want.TurnTranscriptOffset = int64(len(line))
	want.TurnInputRequestID = "call-request"
	got, found, err := registry.Get(running.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v found=%v, want committed %#v", got, found, want)
	}
	if watcher.files[transcriptPath].Offset != 0 {
		t.Fatalf("cursor advanced after projection failure: %#v", watcher.files[transcriptPath])
	}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}
	got, found, err = registry.Get(running.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(got, want) || projections != 2 || published != 0 {
		t.Fatalf("got %#v found=%v projections=%d published=%d, want %#v and projection-only replay", got, found, projections, published, want)
	}
	if watcher.files[transcriptPath].Offset != int64(len(line)) {
		t.Fatalf("cursor %#v, want replay committed", watcher.files[transcriptPath])
	}
}

func TestHostedTurnWatcherPollOnceReplaysCommittedAnswerAfterProjectionFailure(t *testing.T) {
	stateDir := t.TempDir()
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{SessionLabel: "solve problem A", WorkerName: "worker", WorkerPort: 11199, TmuxWindowID: "@12"})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	request := `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call-request"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(request), 0600); err != nil {
		t.Fatal(err)
	}
	projectionErr := errors.New("tmux projection failed")
	projections := 0
	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func([]string) (string, error) {
		projections++
		if projections == 2 {
			return "", projectionErr
		}
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}
	answer := `{"type":"response_item","payload":{"type":"function_call_output","call_id":"call-request"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(request+answer), 0600); err != nil {
		t.Fatal(err)
	}
	if err := watcher.pollOnce(); !errors.Is(err, projectionErr) {
		t.Fatalf("got %v, want %v", err, projectionErr)
	}
	committed, found, err := registry.Get(running.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := running
	want.TurnTranscriptOffset = int64(len(request + answer))
	if !found || !reflect.DeepEqual(committed, want) {
		t.Fatalf("got %#v found=%v, want committed %#v", committed, found, want)
	}
	if watcher.files[transcriptPath].Offset != int64(len(request)) {
		t.Fatalf("cursor advanced after projection failure: %#v", watcher.files[transcriptPath])
	}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}
	if projections != 3 || watcher.files[transcriptPath].Offset != int64(len(request+answer)) {
		t.Fatalf("projections=%d cursor=%#v, want committed answer replay", projections, watcher.files[transcriptPath])
	}
}

func TestHostedTurnWatcherPollOnceDoesNotPersistInvalidInputOffset(t *testing.T) {
	stateDir := t.TempDir()
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{SessionLabel: "solve problem A", WorkerName: "worker", WorkerPort: 11199})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	request := `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"first-call"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(request), 0600); err != nil {
		t.Fatal(err)
	}
	committed, applied, err := registry.CommitWatchedTurnInput(running.SessionID, running.TurnGeneration, transcriptPath, running.TurnID, int64(len(request)), "first-call")
	if err != nil || !applied {
		t.Fatalf("commit request applied=%v err=%v", applied, err)
	}
	invalid := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"other-call"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"second-call"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"first-call"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(request+invalid), 0600); err != nil {
		t.Fatal(err)
	}
	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func([]string) (string, error) { return "", nil }))
	watcher.files[transcriptPath] = hostedTurnTranscriptCursor{Offset: int64(len(request)), Size: int64(len(request))}
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}
	got, found, err := registry.Get(running.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(got, committed) {
		t.Fatalf("got %#v found=%v, want unchanged %#v", got, found, committed)
	}
	if watcher.files[transcriptPath].Offset != int64(len(request+invalid)) {
		t.Fatalf("cursor %#v, want invalid lines consumed in memory", watcher.files[transcriptPath])
	}
}

func TestHostedTurnWatcherRetriesTerminalProjectionFromRegistry(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{SessionLabel: "solve problem A", WorkerName: "worker", WorkerPort: 11199, TmuxWindowID: "@12"})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"turn.completed","turn_id":"turn_1","status":"success"}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	projectionErr := errors.New("tmux projection failed")
	projections := 0
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		if reflect.DeepEqual(args, TmuxActiveWindowDetailsCommandForSettings(settings)) {
			return "@99\tother\n", nil
		}
		if reflect.DeepEqual(args, TmuxHasSessionCommandForSettings(settings)) {
			return "", nil
		}
		if reflect.DeepEqual(args, TmuxListWindowDetailsCommandForSettings(settings)) {
			return "@12\tsolve problem A\n", nil
		}
		projections++
		if projections == 1 {
			return "", projectionErr
		}
		return "", nil
	}))
	watcher.startupReconciled = true
	if err := watcher.pollWithStartupReconciliation(); !errors.Is(err, projectionErr) {
		t.Fatalf("got %v, want %v", err, projectionErr)
	}
	committed, found, err := registry.Get(running.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := running
	want.TurnState = HostedTurnStateDone
	want.TurnTranscriptPath = ""
	want.TurnID = ""
	want.TurnWatchKind = ""
	if !found || !reflect.DeepEqual(committed, want) {
		t.Fatalf("got %#v found=%v, want committed %#v", committed, found, want)
	}
	if err := watcher.pollWithStartupReconciliation(); err != nil {
		t.Fatal(err)
	}
	if projections != 2 {
		t.Fatalf("got %d terminal projections, want committed retry", projections)
	}
}

func TestHostedTurnWatcherStartupSkipsStaleWindowAndPollsActiveSession(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	if _, err := registry.Create(HostedSessionRecord{SessionLabel: "stale", WorkerName: "worker", WorkerPort: 11199, TmuxWindowID: "@old"}); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	created, err := registry.Create(HostedSessionRecord{SessionLabel: "active", WorkerName: "worker", WorkerPort: 11199, TmuxWindowID: "@12"})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call-request"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		if reflect.DeepEqual(args, TmuxHasSessionCommandForSettings(settings)) {
			return "", nil
		}
		if reflect.DeepEqual(args, TmuxListWindowDetailsCommandForSettings(settings)) {
			return "@12\tactive\n", nil
		}
		if strings.Contains(strings.Join(args, " "), "@old") {
			return "", errors.New("can't find window")
		}
		return "", nil
	}))
	if err := watcher.pollWithStartupReconciliation(); err != nil {
		t.Fatal(err)
	}
	got, found, err := registry.Get(running.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := running
	want.TurnTranscriptOffset = int64(len(line))
	want.TurnInputRequestID = "call-request"
	if !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v found=%v, want active waiting %#v", got, found, want)
	}
}

func TestHostedTurnWatcherPollOnceKeepsCursorOnRegistryCommitFailure(t *testing.T) {
	stateDir := t.TempDir()
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{SessionLabel: "solve problem A", WorkerName: "worker", WorkerPort: 11199})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call-request"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		t.Fatalf("unexpected projection after registry failure: %#v", args)
		return "", nil
	}))
	if _, err := watcher.watchPlans(); err != nil {
		t.Fatal(err)
	}
	registry.lock = t.TempDir()
	if err := watcher.pollOnce(); err == nil {
		t.Fatal("expected registry commit failure")
	}
	if !reflect.DeepEqual(watcher.files[transcriptPath], hostedTurnTranscriptCursor{}) {
		t.Fatalf("cursor advanced after registry failure: %#v", watcher.files[transcriptPath])
	}
	registry.lock = registry.path + ".lock"
	got, found, err := registry.Get(running.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(got, running) {
		t.Fatalf("got %#v found=%v, want unchanged %#v", got, found, running)
	}
}

func TestHostedTurnWatcherPollOnceRendersCommittedStateOnStartup(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{SessionLabel: "solve problem A", WorkerName: "worker", WorkerPort: 11199, TmuxWindowID: "@12"})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	waiting, applied, err := registry.CommitWatchedTurnInput(running.SessionID, running.TurnGeneration, transcriptPath, running.TurnID, 101, "call-request")
	if err != nil || !applied {
		t.Fatalf("commit waiting applied=%v err=%v", applied, err)
	}
	if err := os.WriteFile(transcriptPath, make([]byte, waiting.TurnTranscriptOffset), 0600); err != nil {
		t.Fatal(err)
	}
	var projected []HostedSessionRecord
	watcher := newHostedTurnWatcher(settings, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		if reflect.DeepEqual(args, TmuxHasSessionCommandForSettings(settings)) {
			return "", nil
		}
		if reflect.DeepEqual(args, TmuxListWindowDetailsCommandForSettings(settings)) {
			return "@12\tsolve problem A\n", nil
		}
		got, found, err := registry.Get(waiting.SessionID)
		if err != nil || !found {
			t.Fatalf("projection read found=%v err=%v", found, err)
		}
		projected = append(projected, got)
		return "", nil
	}))
	if err := watcher.pollWithStartupReconciliation(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projected, []HostedSessionRecord{waiting}) {
		t.Fatalf("projected %#v, want startup snapshot %#v", projected, []HostedSessionRecord{waiting})
	}
}

func TestHostedTurnWatcherLoopStopWaitsForPollExit(t *testing.T) {
	pollStarted := make(chan struct{})
	releasePoll := make(chan struct{})
	stop := startHostedTurnWatcherLoop(time.Millisecond, nil, func() error {
		close(pollStarted)
		<-releasePoll
		return nil
	}, func(error) {})
	select {
	case <-pollStarted:
	case <-time.After(time.Second):
		t.Fatal("poll did not start")
	}
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop returned before poll exited")
	case <-time.After(20 * time.Millisecond):
	}
	close(releasePoll)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not return after poll exited")
	}
}

func TestHostedTurnWatcherLoopChecksOwnershipBeforeEveryPoll(t *testing.T) {
	checks := 0
	polls := 0
	guardStopped := make(chan struct{})
	stop := startHostedTurnWatcherLoop(time.Millisecond, func() bool {
		checks++
		if checks == 2 {
			close(guardStopped)
		}
		return checks < 2
	}, func() error {
		polls++
		return nil
	}, func(error) {})
	select {
	case <-guardStopped:
	case <-time.After(time.Second):
		t.Fatal("guarded watcher did not stop")
	}
	stop()
	if checks != 2 || polls != 1 {
		t.Fatalf("checks=%d polls=%d, want two checks and one poll", checks, polls)
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

	watcher.registryCursor = hostedTurnRegistryCursor{}
	if _, err := watcher.watchPlans(); err != nil {
		t.Fatal(err)
	}
	if globCalls != 1 {
		t.Fatalf("rebuild during miss TTL globCalls=%d, want 1", globCalls)
	}

	now = now.Add(hostedTurnTranscriptMissTTL + time.Second)
	watcher.registryCursor = hostedTurnRegistryCursor{}
	if _, err := watcher.watchPlans(); err != nil {
		t.Fatal(err)
	}
	if globCalls != 2 {
		t.Fatalf("after miss TTL globCalls=%d, want 2", globCalls)
	}

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

func TestHostedTurnWatcherWatchPlansReturnsEmptyWithoutRegistry(t *testing.T) {
	stateDir := t.TempDir()
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		t.Fatalf("unexpected tmux call: %#v", args)
		return "", nil
	}))

	got := make([][]hostedTurnWatchPlan, 0, 2)
	for range 2 {
		plans, err := watcher.watchPlans()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, plans)
	}
	want := [][]hostedTurnWatchPlan{{}, {}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if _, err := os.Stat(registry.path); !os.IsNotExist(err) {
		t.Fatalf("registry stat error = %v, want not exist", err)
	}
}

func TestHostedTurnWatcherWatchPlansDoesNotCacheAcrossRegistryMutation(t *testing.T) {
	stateDir := t.TempDir()
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	if _, err := registry.Create(HostedSessionRecord{
		SessionLabel:      "first",
		WorkerName:        "cli",
		WorkerPort:        33333,
		LauncherSessionID: "launcher-a",
	}); err != nil {
		t.Fatal(err)
	}

	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		t.Fatalf("unexpected tmux call: %#v", args)
		return "", nil
	}))
	now := time.Unix(1_700_000_000, 0).UTC()
	watcher.now = func() time.Time { return now }
	patterns := []string{}
	watcher.globTranscripts = func(pattern string) ([]string, error) {
		patterns = append(patterns, pattern)
		if len(patterns) == 1 {
			if _, err := registry.Create(HostedSessionRecord{
				SessionLabel:      "second",
				WorkerName:        "cli",
				WorkerPort:        33333,
				LauncherSessionID: "launcher-b",
			}); err != nil {
				t.Fatal(err)
			}
		}
		return nil, nil
	}

	if _, err := watcher.watchPlans(); err != nil {
		t.Fatal(err)
	}
	if _, err := watcher.watchPlans(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(expandHomePath(codexTranscriptSessionsDir), "*", "*", "*", "*launcher-a.jsonl"),
		filepath.Join(expandHomePath(codexTranscriptSessionsDir), "*", "*", "*", "*launcher-b.jsonl"),
	}
	if !reflect.DeepEqual(patterns, want) {
		t.Fatalf("got patterns %#v, want %#v", patterns, want)
	}
}
