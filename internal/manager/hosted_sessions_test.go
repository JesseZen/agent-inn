package manager

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const tmuxMissingSocketErrorText = "error connecting to /private/tmp/ainn-tmux-repro/tmux-501/ainn-test (No such file or directory)"

func TestHostedSessionCreateDefaultsWorkerIDFromWorkerName(t *testing.T) {
	registry := NewHostedSessionRegistry(filepath.Join(t.TempDir(), "sessions.json"))
	session, err := registry.Create(HostedSessionRecord{
		SessionLabel: "work",
		WorkerName:   "cli",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := HostedSessionRecord{
		SessionID:    session.SessionID,
		SessionLabel: "work",
		WorkerID:     "cli",
		WorkerName:   "cli",
		WorkerPort:   11199,
		CreatedAt:    session.CreatedAt,
		LastOpenedAt: session.LastOpenedAt,
	}
	if !reflect.DeepEqual(session, want) {
		t.Fatalf("bad session:\nwant %#v\ngot  %#v", want, session)
	}
}

func TestHostedSessionRegistrySummariesUsesTmuxState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	active, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "ainn:worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 2",
		WorkerName:   "worker",
		WorkerPort:   11200,
		TmuxWindowID: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	oldFactory := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			if len(args) == len(TmuxListWindowDetailsCommandForSettings(defaultTmuxSettings())) && args[7] == "#{window_id}\t#{window_name}" {
				return "ainn:worker-1\tworker 1\n", nil
			}
			return "", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldFactory }()

	summaries, err := registry.Summaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("unexpected summaries: %#v", summaries)
	}
	if summaries[0].SessionID != active.SessionID && summaries[1].SessionID != active.SessionID {
		t.Fatalf("missing active session: %#v", summaries)
	}
	if summaries[0].SessionID != stale.SessionID && summaries[1].SessionID != stale.SessionID {
		t.Fatalf("missing stale session: %#v", summaries)
	}
}

func TestHostedSessionRegistrySummariesMatchRealTmuxWindowIDs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	oldFactory := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			switch {
			case reflect.DeepEqual(args, TmuxHasSessionCommand()):
				return "", nil
			case reflect.DeepEqual(args, TmuxListWindowDetailsCommandForSettings(defaultTmuxSettings())):
				return "@12\tsolve problem A\n", nil
			default:
				return "", nil
			}
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldFactory }()

	got, err := registry.Summaries()
	if err != nil {
		t.Fatal(err)
	}
	want := []HostedSessionSummary{{
		HostedSessionRecord: created,
		Status:              hostedSessionStatusActive,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestHostedSessionRegistryMarkTurnStateAdvancesRunningAndPreservesFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	running, err := registry.MarkTurnState(created.SessionID, HostedTurnStateRunning, "", "")
	if err != nil {
		t.Fatal(err)
	}
	wantRunning := created
	wantRunning.TurnState = HostedTurnStateRunning
	wantRunning.TurnGeneration = 1
	if !reflect.DeepEqual(running, wantRunning) {
		t.Fatalf("got %#v, want %#v", running, wantRunning)
	}

	failed, err := registry.MarkTurnState(created.SessionID, HostedTurnStateFailed, "network_error", "")
	if err != nil {
		t.Fatal(err)
	}
	wantFailed := wantRunning
	wantFailed.TurnState = HostedTurnStateFailed
	wantFailed.TurnStateReason = "network_error"
	if !reflect.DeepEqual(failed, wantFailed) {
		t.Fatalf("got %#v, want %#v", failed, wantFailed)
	}

	done, err := registry.MarkTurnState(created.SessionID, HostedTurnStateDone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(done, wantFailed) {
		t.Fatalf("done should not overwrite failed state:\ngot  %#v\nwant %#v", done, wantFailed)
	}
}

func TestHostedSessionRegistryMarkTurnStatePreservesInterrupted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnState(created.SessionID, HostedTurnStateRunning, "", "")
	if err != nil {
		t.Fatal(err)
	}
	interrupted, err := registry.MarkTurnState(created.SessionID, HostedTurnStateInterrupted, "user_interrupt", "")
	if err != nil {
		t.Fatal(err)
	}

	done, err := registry.MarkTurnState(created.SessionID, HostedTurnStateDone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := running
	want.TurnState = HostedTurnStateInterrupted
	want.TurnStateReason = "user_interrupt"
	if !reflect.DeepEqual(interrupted, want) {
		t.Fatalf("got %#v, want %#v", interrupted, want)
	}
	if !reflect.DeepEqual(done, want) {
		t.Fatalf("done should not overwrite interrupted state:\ngot  %#v\nwant %#v", done, want)
	}
}

func TestHostedSessionRegistryMarkTurnStateWithWatchRegistersActiveTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "launcher-1", "/tmp/codex.jsonl", "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	want := created
	want.TurnState = HostedTurnStateRunning
	want.TurnGeneration = 1
	want.LauncherSessionID = "launcher-1"
	want.TurnTranscriptPath = "/tmp/codex.jsonl"
	want.TurnID = "turn_1"
	want.TurnWatchKind = HostedTurnWatchKindCodex
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}

	watched, err := registry.WatchedTurns()
	if err != nil {
		t.Fatal(err)
	}
	wantWatched := []HostedTurnWatch{{
		SessionID:         created.SessionID,
		TurnGeneration:    1,
		TranscriptPath:    "/tmp/codex.jsonl",
		TurnID:            "turn_1",
		LauncherSessionID: "launcher-1",
		TmuxWindowID:      "@12",
		TurnState:         HostedTurnStateRunning,
		SessionSnapshot:   want,
	}}
	if !reflect.DeepEqual(watched, wantWatched) {
		t.Fatalf("got %#v, want %#v", watched, wantWatched)
	}
}

func TestHostedSessionRegistryCompleteWatchedTurnUsesGenerationGuard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstTurn, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", "/tmp/codex.jsonl", "turn_1", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	secondTurn, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", "/tmp/codex.jsonl", "turn_2", HostedTurnWatchKindCodex)
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := registry.CompleteWatchedTurn(created.SessionID, firstTurn.TurnGeneration, HostedTurnStateInterrupted, "user_interrupt")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("stale watcher should not update session: %#v", got)
	}
	persisted, found, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(persisted, secondTurn) {
		t.Fatalf("got %#v found=%v, want %#v", persisted, found, secondTurn)
	}

	got, ok, err = registry.CompleteWatchedTurn(created.SessionID, secondTurn.TurnGeneration, HostedTurnStateInterrupted, "user_interrupt")
	if err != nil {
		t.Fatal(err)
	}
	want := secondTurn
	want.TurnState = HostedTurnStateInterrupted
	want.TurnStateReason = "user_interrupt"
	want.TurnTranscriptPath = ""
	want.TurnID = ""
	want.TurnWatchKind = ""
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v ok=%v, want %#v", got, ok, want)
	}
}

func TestHostedSessionRegistryCompleteWatchedTurnMarksCorrectedFailureUnread(t *testing.T) {
	tests := []struct {
		name   string
		state  string
		reason string
	}{
		{name: "failed", state: HostedTurnStateFailed, reason: hostedTurnCodexFailureReason},
		{name: "interrupted", state: HostedTurnStateInterrupted, reason: hostedTurnInterruptedReason},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)

			registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
			created, err := registry.Create(HostedSessionRecord{
				SessionLabel:               "solve problem A",
				WorkerName:                 "worker",
				WorkerPort:                 11199,
				TmuxWindowID:               "@12",
				TurnState:                  HostedTurnStateDone,
				TurnGeneration:             3,
				TurnAcknowledgedGeneration: 3,
				TurnTranscriptPath:         "/tmp/codex.jsonl",
				TurnID:                     "turn_1",
				TurnWatchKind:              HostedTurnWatchKindCodex,
			})
			if err != nil {
				t.Fatal(err)
			}

			got, ok, err := registry.CompleteWatchedTurn(created.SessionID, created.TurnGeneration, tt.state, tt.reason)
			if err != nil {
				t.Fatal(err)
			}
			want := created
			want.TurnState = tt.state
			want.TurnStateReason = tt.reason
			want.TurnAcknowledgedGeneration = 0
			want.TurnTranscriptPath = ""
			want.TurnID = ""
			want.TurnWatchKind = ""
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v ok=%v, want %#v", got, ok, want)
			}
		})
	}
}

func TestHostedSessionRegistryWatchedTurnsIgnoresLauncherOnlyTurnWithoutCodexWatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.MarkTurnState(created.SessionID, HostedTurnStateRunning, "", "claude-session-1"); err != nil {
		t.Fatal(err)
	}

	got, err := registry.WatchedTurns()
	if err != nil {
		t.Fatal(err)
	}
	var want []HostedTurnWatch
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want no watched turns", got)
	}
}

func TestHostedSessionRegistryAcknowledgeTurnByWindowIDMarksCompletedTurnRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnState(created.SessionID, HostedTurnStateRunning, "", "")
	if err != nil {
		t.Fatal(err)
	}
	done, err := registry.MarkTurnState(created.SessionID, HostedTurnStateDone, "", "")
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := registry.AcknowledgeTurnByWindow("@12", "")
	if err != nil {
		t.Fatal(err)
	}
	want := done
	want.TurnAcknowledgedGeneration = running.TurnGeneration
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v ok=%v, want %#v", got, ok, want)
	}
}

func TestHostedSessionRegistryAcknowledgeTurnByWindowNameMarksLegacyCompletedTurnRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "ainn:worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnState(created.SessionID, HostedTurnStateRunning, "", "")
	if err != nil {
		t.Fatal(err)
	}
	done, err := registry.MarkTurnState(created.SessionID, HostedTurnStateDone, "", "")
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := registry.AcknowledgeTurnByWindow("@12", "ainn:worker-1")
	if err != nil {
		t.Fatal(err)
	}
	want := done
	want.TurnAcknowledgedGeneration = running.TurnGeneration
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v ok=%v, want %#v", got, ok, want)
	}
}

func TestHostedSessionRegistryAcknowledgeTurnByWindowEmptyNameNoopsForUnknownWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.MarkTurnState(created.SessionID, HostedTurnStateRunning, "", ""); err != nil {
		t.Fatal(err)
	}
	done, err := registry.MarkTurnState(created.SessionID, HostedTurnStateDone, "", "")
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := registry.AcknowledgeTurnByWindow("@99", "")
	if err != nil {
		t.Fatal(err)
	}
	if ok || !reflect.DeepEqual(got, HostedSessionRecord{}) {
		t.Fatalf("got %#v ok=%v, want no match", got, ok)
	}
	persisted, found, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := done
	if !found || !reflect.DeepEqual(persisted, want) {
		t.Fatalf("got %#v found=%v, want %#v", persisted, found, want)
	}
}

func TestHostedSessionRegistryToggleUserMarkerByWindowIDPersistsTodo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	toggled, ok, err := registry.ToggleUserMarkerByWindow("@12", "")
	if err != nil {
		t.Fatal(err)
	}
	want := created
	want.UserMarker = HostedUserMarkerTodo
	if !ok || !reflect.DeepEqual(toggled, want) {
		t.Fatalf("got %#v ok=%v, want %#v", toggled, ok, want)
	}

	persisted, found, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(persisted, want) {
		t.Fatalf("got %#v found=%v, want %#v", persisted, found, want)
	}

	cleared, ok, err := registry.ToggleUserMarkerByWindow("@12", "")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !reflect.DeepEqual(cleared, created) {
		t.Fatalf("got %#v ok=%v, want %#v", cleared, ok, created)
	}
}

func TestHostedSessionRegistryToggleUserMarkerByWindowNameMatchesLegacyRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "ainn:worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := registry.ToggleUserMarkerByWindow("@12", "ainn:worker-1")
	if err != nil {
		t.Fatal(err)
	}
	want := created
	want.UserMarker = HostedUserMarkerTodo
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v ok=%v, want %#v", got, ok, want)
	}
}

func TestHostedSessionRegistryToggleUserMarkerByWindowEmptyNameNoopsForUnknownWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := registry.ToggleUserMarkerByWindow("@99", "")
	if err != nil {
		t.Fatal(err)
	}
	if ok || !reflect.DeepEqual(got, HostedSessionRecord{}) {
		t.Fatalf("got %#v ok=%v, want no match", got, ok)
	}
	persisted, found, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(persisted, created) {
		t.Fatalf("got %#v found=%v, want %#v", persisted, found, created)
	}
}

func TestHostedSessionRegistryToggleUserMarkerByWindowNoopsForNonHostedWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := registry.ToggleUserMarkerByWindow("@99", "other")
	if err != nil {
		t.Fatal(err)
	}
	if ok || !reflect.DeepEqual(got, HostedSessionRecord{}) {
		t.Fatalf("got %#v ok=%v, want no match", got, ok)
	}
	persisted, found, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(persisted, created) {
		t.Fatalf("got %#v found=%v, want %#v", persisted, found, created)
	}
}

func TestHostedSessionRegistryMarkTurnUnreadMarksCompletedTurnUnread(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:               "solve problem A",
		WorkerName:                 "worker",
		WorkerPort:                 11199,
		TmuxWindowID:               "@12",
		TurnState:                  HostedTurnStateDone,
		TurnGeneration:             3,
		TurnAcknowledgedGeneration: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := registry.MarkTurnUnread(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := created
	want.TurnAcknowledgedGeneration = 0
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestHostedSessionRegistryMarkTurnStatePreservesLauncherSessionIDWhenEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:      "solve problem A",
		WorkerName:        "worker",
		WorkerPort:        11199,
		TmuxWindowID:      "@12",
		LauncherSessionID: "019e7c18-0ee7-7ff2-bc82-9c410511ede3",
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := registry.MarkTurnState(created.SessionID, HostedTurnStateDone, "", "")
	if err != nil {
		t.Fatal(err)
	}

	want := created
	want.TurnState = HostedTurnStateDone
	if !reflect.DeepEqual(updated, want) {
		t.Fatalf("got %#v, want %#v", updated, want)
	}
}

func TestHostedSessionRegistryDuplicateCreatesFreshSessionFromWorkspaceAndWorker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:               "solve problem A",
		WorkerName:                 "worker",
		WorkerPort:                 11199,
		Workspace:                  "/tmp/work",
		Model:                      "gpt-5.5",
		AddDirs:                    []string{"/tmp/shared"},
		TmuxWindowID:               "@12",
		LauncherSessionID:          "019e7c18-0ee7-7ff2-bc82-9c410511ede3",
		TurnState:                  HostedTurnStateDone,
		TurnGeneration:             3,
		TurnAcknowledgedGeneration: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	duplicated, err := registry.Duplicate(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}

	want := HostedSessionRecord{
		SessionID:    duplicated.SessionID,
		SessionLabel: "solve problem A 2",
		WorkerID:     "worker",
		WorkerName:   "worker",
		WorkerPort:   11199,
		Workspace:    "/tmp/work",
		Model:        "gpt-5.5",
		AddDirs:      []string{"/tmp/shared"},
		CreatedAt:    duplicated.CreatedAt,
		LastOpenedAt: duplicated.LastOpenedAt,
	}
	if !reflect.DeepEqual(duplicated, want) {
		t.Fatalf("got %#v, want %#v", duplicated, want)
	}
}

func TestHostedSessionRegistryDuplicateIncrementsExistingLabelSuffix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	_, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A 2",
		WorkerName:   "worker",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}

	duplicated, err := registry.Duplicate(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}

	want := HostedSessionRecord{
		SessionID:    duplicated.SessionID,
		SessionLabel: "solve problem A 3",
		WorkerID:     "worker",
		WorkerName:   "worker",
		WorkerPort:   11199,
		AddDirs:      []string{},
		CreatedAt:    duplicated.CreatedAt,
		LastOpenedAt: duplicated.LastOpenedAt,
	}
	if !reflect.DeepEqual(duplicated, want) {
		t.Fatalf("got %#v, want %#v", duplicated, want)
	}
}

func TestHostedSessionRegistryDuplicateIncrementsInitialLabelSuffix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}

	duplicated, err := registry.Duplicate(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}

	want := HostedSessionRecord{
		SessionID:    duplicated.SessionID,
		SessionLabel: "worker 2",
		WorkerID:     "worker",
		WorkerName:   "worker",
		WorkerPort:   11199,
		AddDirs:      []string{},
		CreatedAt:    duplicated.CreatedAt,
		LastOpenedAt: duplicated.LastOpenedAt,
	}
	if !reflect.DeepEqual(duplicated, want) {
		t.Fatalf("got %#v, want %#v", duplicated, want)
	}
}

func TestHostedSessionRegistrySummariesTreatsMissingTmuxSocketAsStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotCalls [][]string
	oldFactory := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			gotCalls = append(gotCalls, append([]string{}, args...))
			if reflect.DeepEqual(args, TmuxHasSessionCommand()) {
				return "", errors.New(tmuxMissingSocketErrorText)
			}
			return "@12\tworker 1\n", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldFactory }()

	got, err := registry.Summaries()
	if err != nil {
		t.Fatal(err)
	}
	want := []HostedSessionSummary{{
		HostedSessionRecord: created,
		Status:              hostedSessionStatusStale,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	wantCalls := [][]string{TmuxHasSessionCommand()}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestHostedSessionRegistrySummariesReturnsUnexpectedHasSessionError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	_, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	hasSessionErr := errors.New("tmux socket permission denied")
	oldFactory := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			if reflect.DeepEqual(args, TmuxHasSessionCommand()) {
				return "", hasSessionErr
			}
			return "@12\tworker 1\n", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldFactory }()

	summaries, err := registry.Summaries()
	if !errors.Is(err, hasSessionErr) {
		t.Fatalf("got summaries %#v and error %v, want error %v", summaries, err, hasSessionErr)
	}
}

func TestHostedSessionRegistryRemoveKillsActiveWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "ainn:worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	oldFactory := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			got = append(got, append([]string{}, args...))
			if reflect.DeepEqual(args, TmuxListWindowDetailsCommandForSettings(defaultTmuxSettings())) {
				return "ainn:worker-1\tworker 1\n", nil
			}
			return "", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldFactory }()

	if err := registry.Remove(created.SessionID, hostedTMuxRunnerFactory()); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		TmuxHasSessionCommand(),
		TmuxListWindowDetailsCommandForSettings(defaultTmuxSettings()),
		TmuxKillWindowCommand("ainn:worker-1"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected tmux calls: %#v", got)
	}
}

func TestHostedSessionRegistryRemoveKillsLegacyNamedWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "ainn:worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	runner := hostedTMuxRunnerFunc(func(args []string) (string, error) {
		got = append(got, append([]string{}, args...))
		if reflect.DeepEqual(args, TmuxListWindowDetailsCommandForSettings(defaultTmuxSettings())) {
			return "@12\tainn:worker-1\n", nil
		}
		return "", nil
	})

	if err := registry.Remove(created.SessionID, runner); err != nil {
		t.Fatal(err)
	}
	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records, []HostedSessionRecord{}) {
		t.Fatalf("got records %#v, want none", records)
	}
	want := [][]string{
		TmuxHasSessionCommand(),
		TmuxListWindowDetailsCommandForSettings(defaultTmuxSettings()),
		TmuxKillWindowCommand("@12"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got tmux calls %#v, want %#v", got, want)
	}
}

func TestHostedSessionRegistryRemoveReturnsUnexpectedHasSessionError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	hasSessionErr := errors.New("tmux socket permission denied")
	runner := hostedTMuxRunnerFunc(func(args []string) (string, error) {
		if reflect.DeepEqual(args, TmuxHasSessionCommand()) {
			return "", hasSessionErr
		}
		return "@12\tworker 1\n", nil
	})

	err = registry.Remove(created.SessionID, runner)
	if !errors.Is(err, hasSessionErr) {
		t.Errorf("got error %v, want %v", err, hasSessionErr)
	}

	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []HostedSessionRecord{created}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("got records %#v, want %#v", records, want)
	}
}

func TestHostedSessionRegistryRemoveSkipsStaleKill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	oldFactory := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			got = append(got, append([]string{}, args...))
			return "", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldFactory }()

	if err := registry.Remove(created.SessionID, nil); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no tmux calls, got %#v", got)
	}
}

func TestHostedSessionRegistryRemoveDeletesStaleWhenTmuxHostMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "ainn:worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	runner := hostedTMuxRunnerFunc(func(args []string) (string, error) {
		got = append(got, append([]string{}, args...))
		if strings.Join(args, " ") == strings.Join(TmuxHasSessionCommand(), " ") {
			return "", errors.New(tmuxNoServerRunningError)
		}
		return "", nil
	})

	if err := registry.Remove(created.SessionID, runner); err != nil {
		t.Fatal(err)
	}

	if records, err := registry.List(); err != nil {
		t.Fatal(err)
	} else if len(records) != 0 {
		t.Fatalf("expected hosted session removed, got %#v", records)
	}

	want := [][]string{TmuxHasSessionCommand()}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected tmux calls: %#v", got)
	}
}

func TestHostedSessionRegistryRemoveDeletesStaleWhenTmuxSocketMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "ainn:worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	runner := hostedTMuxRunnerFunc(func(args []string) (string, error) {
		got = append(got, append([]string{}, args...))
		if reflect.DeepEqual(args, TmuxHasSessionCommand()) {
			return "", errors.New(tmuxMissingSocketErrorText)
		}
		return "", nil
	})

	if err := registry.Remove(created.SessionID, runner); err != nil {
		t.Fatal(err)
	}

	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records, []HostedSessionRecord{}) {
		t.Fatalf("got records %#v, want none", records)
	}
	want := [][]string{TmuxHasSessionCommand()}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got tmux calls %#v, want %#v", got, want)
	}
}

func TestHostedSessionRegistryRemoveDeletesStaleWhenTmuxHostDisappearsDuringWindowList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "ainn:worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	runner := hostedTMuxRunnerFunc(func(args []string) (string, error) {
		got = append(got, append([]string{}, args...))
		if reflect.DeepEqual(args, TmuxListWindowDetailsCommandForSettings(defaultTmuxSettings())) {
			return "", errors.New(tmuxCantFindSessionError)
		}
		return "", nil
	})

	if err := registry.Remove(created.SessionID, runner); err != nil {
		t.Fatal(err)
	}

	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records, []HostedSessionRecord{}) {
		t.Fatalf("got records %#v, want none", records)
	}
	want := [][]string{
		TmuxHasSessionCommand(),
		TmuxListWindowDetailsCommandForSettings(defaultTmuxSettings()),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got tmux calls %#v, want %#v", got, want)
	}
}

func TestHostedSessionRegistrySummariesTreatsReusedWindowIDAsStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "cc",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@2",
	})
	if err != nil {
		t.Fatal(err)
	}

	oldFactory := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			switch {
			case reflect.DeepEqual(args, TmuxHasSessionCommand()):
				return "", nil
			case reflect.DeepEqual(args, TmuxListWindowDetailsCommandForSettings(defaultTmuxSettings())):
				return "@2\tcoleet\n", nil
			default:
				return "", nil
			}
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldFactory }()

	got, err := registry.Summaries()
	if err != nil {
		t.Fatal(err)
	}
	want := []HostedSessionSummary{{
		HostedSessionRecord: created,
		Status:              hostedSessionStatusStale,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
