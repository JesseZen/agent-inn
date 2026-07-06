package manager

import (
	"strings"
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

func TestTmuxHostedTurnStatusCommandForSettings(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	got := TmuxHostedTurnStatusCommandForSettings(settings, "@12", HostedTurnStateFailed)
	want := []string{
		"tmux", "-L", "ainn-test",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-format", "#[fg=colour196,bg=colour235,bold] #I:! #W #[default]",
		";",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-current-format", "#[fg=colour231,bg=colour196,bold] #I:! #W #[default]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxHostedTurnStatusCommandForRecordDistinguishesUnreadAndReadDone(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	unread := HostedSessionRecord{
		TmuxWindowID:   "@12",
		TurnState:      HostedTurnStateDone,
		TurnGeneration: 2,
	}
	read := unread
	read.TurnAcknowledgedGeneration = 2

	gotUnread := TmuxHostedTurnStatusCommandForRecord(settings, unread)
	wantUnread := []string{
		"tmux", "-L", "ainn-test",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-format", "#[fg=colour46,bg=colour235,bold] #I:+ #W #[default]",
		";",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-current-format", "#[fg=colour0,bg=colour46,bold] #I:+ #W #[default]",
	}
	if strings.Join(gotUnread, "\n") != strings.Join(wantUnread, "\n") {
		t.Fatalf("unread got %#v, want %#v", gotUnread, wantUnread)
	}

	gotRead := TmuxHostedTurnStatusCommandForRecord(settings, read)
	wantRead := []string{
		"tmux", "-L", "ainn-test",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-format", "#[fg=colour244,bg=colour235] #I:+ #W #[default]",
		";",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-current-format", "#[fg=colour0,bg=colour45,bold] #I:+ #W #[default]",
	}
	if strings.Join(gotRead, "\n") != strings.Join(wantRead, "\n") {
		t.Fatalf("read got %#v, want %#v", gotRead, wantRead)
	}
}

func TestTmuxAcknowledgeTurnHookCommandForSettings(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	got := TmuxAcknowledgeTurnHookCommandForSettings(settings, "/tmp/ainn config", "/tmp/ainn bin")
	want := []string{
		"tmux", "-L", "ainn-test",
		"set-hook", "-t", "ainn-test-host",
		"after-select-window[90]",
		"run-shell -b '/tmp/ainn bin' hosted-session acknowledge --config-dir '/tmp/ainn config' --window-id #{window_id}",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %#v, want %#v", got, want)
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

func TestHostedTMuxRunnerIncludesStderrOnError(t *testing.T) {
	runner := hostedTMuxRunnerFactory()

	_, err := runner.Run([]string{"sh", "-c", "printf 'missing host session' >&2; exit 1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "missing host session") {
		t.Fatalf("got error %q, want stderr included", err.Error())
	}
}
