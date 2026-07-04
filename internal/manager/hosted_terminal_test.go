package manager

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
)

func TestTmuxDetectCommand(t *testing.T) {
	got := TmuxDetectCommand()
	want := []string{"tmux", "-V"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxHasSessionCommand(t *testing.T) {
	got := TmuxHasSessionCommand()
	want := []string{"tmux", "-L", "ainn", "has-session", "-t", "ainn-host"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxStartHostCommand(t *testing.T) {
	got := TmuxStartHostCommand()
	want := []string{"tmux", "-L", "ainn", "new-session", "-d", "-s", "ainn-host"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxStartHostWithWindowCommandForSettings(t *testing.T) {
	got := TmuxStartHostWithWindowCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:    "ainn",
				HostSession:   "ainn-host",
				HostStartMode: "reuse-first-window",
			},
		},
	}, "solve problem A", []string{"codex", "--profile", "cli-openai"})
	want := []string{"tmux", "-L", "ainn", "new-session", "-d", "-s", "ainn-host", "-n", "solve problem A", "-P", "-F", "#{window_id}", "codex", "--profile", "cli-openai"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxStartMainWindowHostCommandForSettings(t *testing.T) {
	got := TmuxStartMainWindowHostCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:    "ainn",
				HostSession:   "ainn-host",
				HostStartMode: "main-tui-window",
			},
		},
	}, "ainn", []string{"env", "AINN_TMUX_ROOT_CHILD=1", "/tmp/ainn"})
	want := []string{"tmux", "-L", "ainn", "new-session", "-d", "-s", "ainn-host", "-n", "ainn", "-P", "-F", "#{window_index}", "env", "AINN_TMUX_ROOT_CHILD=1", "/tmp/ainn"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxCreateWindowCommand(t *testing.T) {
	got := TmuxCreateWindowCommand("ainn:cli-openai", []string{"codex", "--profile", "cli-openai", "--cd", "/tmp/work"})
	want := []string{"tmux", "-L", "ainn", "new-window", "-t", "ainn-host", "-n", "ainn:cli-openai", "-P", "-F", "#{window_id}", "codex", "--profile", "cli-openai", "--cd", "/tmp/work"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxSelectWindowCommand(t *testing.T) {
	got := TmuxSelectWindowCommand("ainn:cli-openai")
	want := []string{"tmux", "-L", "ainn", "select-window", "-t", "ainn-host:ainn:cli-openai"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxSelectMainWindowCommandForSettings(t *testing.T) {
	got := TmuxSelectMainWindowCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn",
				HostSession: "ainn-host",
			},
		},
	})
	want := []string{"tmux", "-L", "ainn", "select-window", "-t", "ainn-host:0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxAttachCommand(t *testing.T) {
	got := TmuxAttachCommand()
	want := []string{"tmux", "-L", "ainn", "attach-session", "-t", "ainn-host"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxMainWindowPaneStartCommandForSettings(t *testing.T) {
	got := TmuxMainWindowPaneStartCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn",
				HostSession: "ainn-host",
			},
		},
	})
	want := []string{"tmux", "-L", "ainn", "list-panes", "-t", "ainn-host:0", "-F", "#{pane_start_command}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxCreateMainWindowCommandForSettings(t *testing.T) {
	got := TmuxCreateMainWindowCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn",
				HostSession: "ainn-host",
			},
		},
	}, "ainn", []string{"env", "AINN_TMUX_ROOT_CHILD=1", "/tmp/ainn"})
	want := []string{"tmux", "-L", "ainn", "new-window", "-t", "ainn-host:0", "-n", "ainn", "-P", "-F", "#{window_id}", "env", "AINN_TMUX_ROOT_CHILD=1", "/tmp/ainn"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxMoveWindowToMainWindowCommandForSettings(t *testing.T) {
	got := TmuxMoveWindowToMainWindowCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn",
				HostSession: "ainn-host",
			},
		},
	}, "1")
	want := []string{"tmux", "-L", "ainn", "move-window", "-s", "ainn-host:1", "-t", "ainn-host:0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxRespawnMainWindowCommandForSettings(t *testing.T) {
	got := TmuxRespawnMainWindowCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn",
				HostSession: "ainn-host",
			},
		},
	}, []string{"env", "AINN_TMUX_ROOT_CHILD=1", "/tmp/ainn"})
	want := []string{"tmux", "-L", "ainn", "respawn-pane", "-k", "-t", "ainn-host:0", "env", "AINN_TMUX_ROOT_CHILD=1", "/tmp/ainn"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxListClientPanesCommand(t *testing.T) {
	got := TmuxListClientPanesCommand("/tmp/tmux-501/ainn")
	want := []string{"tmux", "-S", "/tmp/tmux-501/ainn", "list-clients", "-F", "#{client_name}\t#{pane_id}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxSwitchClientToMainWindowCommandForSettings(t *testing.T) {
	got := TmuxSwitchClientToMainWindowCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn",
				HostSession: "ainn-host",
			},
		},
	}, "client-1")
	want := []string{"tmux", "-L", "ainn", "switch-client", "-c", "client-1", "-t", "ainn-host:0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestSafeWindowName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"cli-openai", "ainn:cli-openai"},
		{"cli_openai", "ainn:cli_openai"},
		{"cli openai", "ainn:cli-openai"},
		{"cli/openai", "ainn:cli-openai"},
		{"cli.openai", "ainn:cli-openai"},
		{"", "ainn:"},
	}
	for _, tc := range cases {
		got := SafeWindowName(tc.input)
		if got != tc.want {
			t.Errorf("SafeWindowName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
