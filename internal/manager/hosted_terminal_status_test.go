package manager

import (
	"testing"

	"github.com/jesse/agent-inn/internal/config"
)

func TestTmuxListWindowsCommand(t *testing.T) {
	got := TmuxListWindowsCommand()
	want := []string{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestTmuxListWindowsCommandForSettings(t *testing.T) {
	got := TmuxListWindowsCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	})
	want := []string{"tmux", "-L", "ainn-test", "list-windows", "-t", "ainn-test-host", "-F", "#{window_id}"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestTmuxListWindowDetailsCommandForSettings(t *testing.T) {
	got := TmuxListWindowDetailsCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	})
	want := []string{"tmux", "-L", "ainn-test", "list-windows", "-t", "ainn-test-host", "-F", "#{window_id}\t#{window_name}"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestTmuxKillWindowCommand(t *testing.T) {
	got := TmuxKillWindowCommand("ainn:cli-openai")
	want := []string{"tmux", "-L", "ainn", "kill-window", "-t", "ainn-host:ainn:cli-openai"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestTmuxKillWindowCommandForSettings(t *testing.T) {
	got := TmuxKillWindowCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}, "@12")
	want := []string{"tmux", "-L", "ainn-test", "kill-window", "-t", "ainn-test-host:@12"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestHostedSessionStatusForWindow(t *testing.T) {
	if got := hostedSessionStatusForWindow(hostedWindowDetails("@1\tone\n@2\ttwo\n"), HostedSessionRecord{SessionLabel: "two", TmuxWindowID: "@2"}); got != hostedSessionStatusActive {
		t.Fatalf("got %q, want active", got)
	}
	if got := hostedSessionStatusForWindow(hostedWindowDetails("@1\tone\n"), HostedSessionRecord{SessionLabel: "two", TmuxWindowID: "@2"}); got != hostedSessionStatusStale {
		t.Fatalf("got %q, want stale", got)
	}
	if got := hostedSessionStatusForWindow(hostedWindowDetails("@2\tother\n"), HostedSessionRecord{SessionLabel: "two", TmuxWindowID: "@2"}); got != hostedSessionStatusStale {
		t.Fatalf("got %q, want stale", got)
	}
}

func TestHostedSessionStatusForTmuxWindowID(t *testing.T) {
	if got := hostedSessionStatusForWindow(hostedWindowDetails("@1\tone\n@2\ttwo\n"), HostedSessionRecord{SessionLabel: "one", TmuxWindowID: "@1"}); got != hostedSessionStatusActive {
		t.Fatalf("got %q, want active", got)
	}
}
