package manager

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestHostedSessionRegistrySummariesUsesTmuxState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	active, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "codex:worker-1",
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
			if len(args) == len(TmuxListWindowsCommand()) {
				return "codex:worker-1\n", nil
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
			case len(args) == 8 && args[7] == "#{window_name}":
				return "solve problem A\n", nil
			case len(args) == 8 && args[7] == "#{window_id}":
				return "@12\n", nil
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

func TestHostedSessionRegistryRemoveKillsActiveWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "codex:worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	oldFactory := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			got = append(got, append([]string{}, args...))
			if len(args) == len(TmuxListWindowsCommand()) {
				return "codex:worker-1\n", nil
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
		TmuxListWindowsCommand(),
		TmuxKillWindowCommand("codex:worker-1"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected tmux calls: %#v", got)
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
		TmuxWindowID: "codex:worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	runner := hostedTMuxRunnerFunc(func(args []string) (string, error) {
		got = append(got, append([]string{}, args...))
		if strings.Join(args, " ") == strings.Join(TmuxHasSessionCommand(), " ") {
			return "", errors.New("no server running")
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
