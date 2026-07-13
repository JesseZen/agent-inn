package manager

import (
	"os/exec"
	"reflect"
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

func TestTmuxActiveWindowDetailsCommandForSettings(t *testing.T) {
	got := TmuxActiveWindowDetailsCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	})
	want := []string{"tmux", "-L", "ainn-test", "display-message", "-p", "-t", "ainn-test-host", "#{window_id}\t#{window_name}"}
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

func TestTmuxThemeCommandForSettingsPinsMainWindowAndIncludesHostedSessions(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:    "ainn-test",
				HostSession:   "ainn-test-host",
				HostStartMode: config.TmuxHostStartModeMainTUIWindow,
			},
		},
	}
	got := TmuxThemeCommandForSettings(settings)
	want := []string{
		"tmux", "-L", "ainn-test",
		"set-option", "-g", "status", "on", ";",
		"set-option", "-g", "status-left", "#{?#{==:#{window_index},0},#[fg=colour0]#[bg=colour45]#[bold],#[fg=colour244]#[bg=colour235]}#[range=window|0] 0:ainn #[norange]#[default]", ";",
		"set-option", "-g", "status-right", "#[range=user|ainn-sessions]#[fg=colour235,bg=colour45,bold] Sessions #[default]", ";",
		"set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";",
		"set-window-option", "-g", "window-status-format", "#{?#{==:#{window_index},0},,#[fg=colour244]#[bg=colour235] #I:#W #[default]}", ";",
		"set-window-option", "-g", "window-status-current-format", "#{?#{==:#{window_index},0},,#[fg=colour0]#[bg=colour45]#[bold] #I:#W #[default]}", ";",
		"set-window-option", "-g", "automatic-rename", "off",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for _, arg := range got {
		if strings.Contains(arg, "ainn-hosted-sessions") {
			t.Fatalf("tmux status range must fit tmux 3.6b 15-byte user range data limit, got %#v", got)
		}
	}
}

func TestTmuxThemeCommandForSettingsKeepsFirstHostedWindowInWindowList(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:    "ainn-test",
				HostSession:   "ainn-test-host",
				HostStartMode: config.TmuxHostStartModeNewWindow,
			},
		},
	}
	got := TmuxThemeCommandForSettings(settings)
	want := []string{
		"tmux", "-L", "ainn-test",
		"set-option", "-g", "status", "on", ";",
		"set-option", "-g", "status-left", "", ";",
		"set-option", "-g", "status-right", "#[range=user|ainn-sessions]#[fg=colour235,bg=colour45,bold] Sessions #[default]", ";",
		"set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";",
		"set-window-option", "-g", "window-status-format", "#[fg=colour244,bg=colour235] #I:#W #[default]", ";",
		"set-window-option", "-g", "window-status-current-format", "#[fg=colour0,bg=colour45,bold] #I:#W #[default]", ";",
		"set-window-option", "-g", "automatic-rename", "off",
	}
	if !reflect.DeepEqual(got, want) {
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

func TestTmuxHostedTurnStatusCommandForRecordRendersTodoBelowUnreadStates(t *testing.T) {
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host"}}}
	cases := []struct {
		name    string
		session HostedSessionRecord
		want    string
	}{
		{
			name:    "idle todo",
			session: HostedSessionRecord{TmuxWindowID: "@12", UserMarker: HostedUserMarkerTodo},
			want:    "#[fg=colour226,bg=colour235,bold] #I:~ #W #[default]",
		},
		{
			name:    "read done todo",
			session: HostedSessionRecord{TmuxWindowID: "@12", TurnState: HostedTurnStateDone, TurnGeneration: 2, TurnAcknowledgedGeneration: 2, UserMarker: HostedUserMarkerTodo},
			want:    "#[fg=colour226,bg=colour235,bold] #I:~ #W #[default]",
		},
		{
			name:    "unread done wins",
			session: HostedSessionRecord{TmuxWindowID: "@12", TurnState: HostedTurnStateDone, TurnGeneration: 2, UserMarker: HostedUserMarkerTodo},
			want:    "#[fg=colour46,bg=colour235,bold] #I:+ #W #[default]",
		},
		{
			name:    "running wins",
			session: HostedSessionRecord{TmuxWindowID: "@12", TurnState: HostedTurnStateRunning, UserMarker: HostedUserMarkerTodo},
			want:    "#[fg=colour45,bg=colour235,bold] #I:* #W #[default]",
		},
		{
			name:    "failed unread wins",
			session: HostedSessionRecord{TmuxWindowID: "@12", TurnState: HostedTurnStateFailed, TurnGeneration: 2, UserMarker: HostedUserMarkerTodo},
			want:    "#[fg=colour196,bg=colour235,bold] #I:! #W #[default]",
		},
		{
			name:    "interrupted unread wins",
			session: HostedSessionRecord{TmuxWindowID: "@12", TurnState: HostedTurnStateInterrupted, TurnGeneration: 2, UserMarker: HostedUserMarkerTodo},
			want:    "#[fg=colour196,bg=colour235,bold] #I:! #W #[default]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TmuxHostedTurnStatusCommandForRecord(settings, tc.session)
			if got[7] != tc.want {
				t.Fatalf("got %#v, want format %q", got, tc.want)
			}
		})
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
		"run-shell -b \"'/tmp/ainn bin' hosted-session acknowledge --config-dir '/tmp/ainn config' --window-id #{window_id} --window-name #{q:window_name}\"",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxAcknowledgeTurnMouseBindingCommandForSettings(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	got := TmuxAcknowledgeTurnMouseBindingCommandForSettings(settings, "/tmp/ainn config", "/tmp/ainn bin")
	want := []string{
		"tmux", "-L", "ainn-test",
		"bind-key", "-T", "root", "MouseDown1Status",
		"switch-client -t = ; run-shell -b -t = \"'/tmp/ainn bin' hosted-session acknowledge --config-dir '/tmp/ainn config' --window-id #{window_id} --window-name #{q:window_name}\"",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxToggleTodoMouseBindingCommandForSettings(t *testing.T) {
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host"}}}
	got := TmuxToggleTodoMouseBindingCommandForSettings(settings, "/tmp/ainn config", "/tmp/ainn bin")
	want := []string{
		"tmux", "-L", "ainn-test",
		"bind-key", "-T", "root", "DoubleClick1Status",
		"run-shell -b -t = \"'/tmp/ainn bin' hosted-session toggle-todo --config-dir '/tmp/ainn config' --window-id #{window_id} --window-name #{q:window_name}\"",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxTurnStatusOwnerCommandForSettings(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	got := TmuxTurnStatusOwnerCommandForSettings(settings)
	want := []string{
		"tmux", "-L", "ainn-test",
		"show-option", "-qv", "-t", "ainn-test-host",
		"@ainn_turn_status_owner",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxSetTurnStatusOwnerCommandForSettings(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	got := TmuxSetTurnStatusOwnerCommandForSettings(settings, "/tmp/ainn config")
	want := []string{
		"tmux", "-L", "ainn-test",
		"set-option", "-t", "ainn-test-host",
		"@ainn_turn_status_owner", "/tmp/ainn config",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxHostedPopupOwnerCommandsForSettings(t *testing.T) {
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host"}}}

	gotOwner := TmuxHostedPopupOwnerCommandForSettings(settings)
	wantOwner := []string{"tmux", "-L", "ainn-test", "show-option", "-qv", "-t", "ainn-test-host", "@ainn_hosted_popup_owner"}
	if !reflect.DeepEqual(gotOwner, wantOwner) {
		t.Fatalf("got %#v, want %#v", gotOwner, wantOwner)
	}

	gotSetOwner := TmuxSetHostedPopupOwnerCommandForSettings(settings, "/tmp/ainn config")
	wantSetOwner := []string{"tmux", "-L", "ainn-test", "set-option", "-t", "ainn-test-host", "@ainn_hosted_popup_owner", "/tmp/ainn config"}
	if !reflect.DeepEqual(gotSetOwner, wantSetOwner) {
		t.Fatalf("got %#v, want %#v", gotSetOwner, wantSetOwner)
	}

	gotKey := TmuxHostedPopupKeyCommandForSettings(settings)
	wantKey := []string{"tmux", "-L", "ainn-test", "show-option", "-qv", "-t", "ainn-test-host", "@ainn_hosted_popup_key"}
	if !reflect.DeepEqual(gotKey, wantKey) {
		t.Fatalf("got %#v, want %#v", gotKey, wantKey)
	}

	gotSetKey := TmuxSetHostedPopupKeyCommandForSettings(settings, "H")
	wantSetKey := []string{"tmux", "-L", "ainn-test", "set-option", "-t", "ainn-test-host", "@ainn_hosted_popup_key", "H"}
	if !reflect.DeepEqual(gotSetKey, wantSetKey) {
		t.Fatalf("got %#v, want %#v", gotSetKey, wantSetKey)
	}

	gotList := TmuxListHostedPopupBindingCommandForSettings(settings, "H")
	wantList := []string{"tmux", "-L", "ainn-test", "list-keys", "-T", "prefix", "H"}
	if !reflect.DeepEqual(gotList, wantList) {
		t.Fatalf("got %#v, want %#v", gotList, wantList)
	}

	gotUnbind := TmuxUnbindHostedPopupBindingCommandForSettings(settings, "H")
	wantUnbind := []string{"tmux", "-L", "ainn-test", "unbind-key", "-T", "prefix", "H"}
	if !reflect.DeepEqual(gotUnbind, wantUnbind) {
		t.Fatalf("got %#v, want %#v", gotUnbind, wantUnbind)
	}
}

func TestTmuxHostedPopupCommandsForSettings(t *testing.T) {
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host"}}}
	gotDisplay := TmuxDisplayHostedPopupCommandForSettings(settings, "/tmp/ainn config", "http://127.0.0.1:19090", "/tmp/ainn bin")
	wantDisplay := []string{"tmux", "-L", "ainn-test", "display-popup", "-E", "-x", "R", "-y", "0", "-w", "40%", "-h", "100%", "-T", "Hosted Terminal", "'/tmp/ainn bin' hosted-session popup --config-dir '/tmp/ainn config' --manager-url 'http://127.0.0.1:19090'"}
	if !reflect.DeepEqual(gotDisplay, wantDisplay) {
		t.Fatalf("got %#v, want %#v", gotDisplay, wantDisplay)
	}

	gotBinding := TmuxHostedPopupBindingCommandForSettings(settings, "H", "/tmp/ainn config", "http://127.0.0.1:19090", "/tmp/ainn bin")
	wantBinding := []string{"tmux", "-L", "ainn-test", "bind-key", "-T", "prefix", "H", "display-popup -E -x R -y 0 -w 40% -h 100% -T 'Hosted Terminal' '/tmp/ainn bin' hosted-session popup --config-dir '/tmp/ainn config' --manager-url 'http://127.0.0.1:19090'"}
	if !reflect.DeepEqual(gotBinding, wantBinding) {
		t.Fatalf("got %#v, want %#v", gotBinding, wantBinding)
	}
}

func TestTmuxHostedPopupMouseBindingCommandForSettingsSelectMode(t *testing.T) {
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host"}}}
	got := TmuxHostedPopupMouseBindingCommandForSettings(settings, "/tmp/ainn config", "http://127.0.0.1:19090", "/tmp/ainn bin", TmuxHostedPopupMouseModeSelect)
	want := []string{
		"tmux", "-L", "ainn-test",
		"bind-key", "-T", "root", "MouseDown1Status",
		"if -F \"#{==:#{mouse_status_range},ainn-sessions}\" \"display-popup -E -x R -y 0 -w 40% -h 100% -T 'Hosted Terminal' '/tmp/ainn bin' hosted-session popup --config-dir '/tmp/ainn config' --manager-url 'http://127.0.0.1:19090'\" \"switch-client -t =\"",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if strings.Contains(strings.Join(got, "\n"), "ainn-hosted-sessions") {
		t.Fatalf("tmux popup mouse range must use <=15-byte user range data, got %#v", got)
	}
}

func TestTmuxHostedPopupMouseBindingCommandForSettingsAcknowledgeMode(t *testing.T) {
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host"}}}
	got := TmuxHostedPopupMouseBindingCommandForSettings(settings, "/tmp/ainn config", "http://127.0.0.1:19090", "/tmp/ainn bin", TmuxHostedPopupMouseModeAcknowledge)
	want := []string{
		"tmux", "-L", "ainn-test",
		"bind-key", "-T", "root", "MouseDown1Status",
		"if -F \"#{==:#{mouse_status_range},ainn-sessions}\" \"display-popup -E -x R -y 0 -w 40% -h 100% -T 'Hosted Terminal' '/tmp/ainn bin' hosted-session popup --config-dir '/tmp/ainn config' --manager-url 'http://127.0.0.1:19090'\" \"switch-client -t = ; run-shell -b -t = \\\"'/tmp/ainn bin' hosted-session acknowledge --config-dir '/tmp/ainn config' --window-id #{window_id} --window-name #{q:window_name}\\\"\"",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if strings.Contains(strings.Join(got, "\n"), "ainn-hosted-sessions") {
		t.Fatalf("tmux popup mouse range must use <=15-byte user range data, got %#v", got)
	}
}

func TestTmuxShowHooksCommandForSettings(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	got := TmuxShowHooksCommandForSettings(settings)
	want := []string{
		"tmux", "-L", "ainn-test",
		"show-hooks", "-t", "ainn-test-host",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxListAcknowledgeTurnMouseBindingCommandForSettings(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	got := TmuxListAcknowledgeTurnMouseBindingCommandForSettings(settings)
	want := []string{
		"tmux", "-L", "ainn-test",
		"list-keys", "-T", "root", "MouseDown1Status",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxListToggleTodoMouseBindingCommandForSettings(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	got := TmuxListToggleTodoMouseBindingCommandForSettings(settings)
	want := []string{
		"tmux", "-L", "ainn-test",
		"list-keys", "-T", "root",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxAcknowledgeTurnCommandsShellQuoteExpandedWindowName(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	commands := [][]string{
		TmuxAcknowledgeTurnHookCommandForSettings(settings, "/tmp/ainn config", "/tmp/ainn bin"),
		TmuxAcknowledgeTurnMouseBindingCommandForSettings(settings, "/tmp/ainn config", "/tmp/ainn bin"),
	}
	for _, got := range commands {
		command := got[len(got)-1]
		start := strings.Index(command, "\"")
		end := strings.LastIndex(command, "\"")
		if start < 0 || end <= start {
			t.Fatalf("got command %q, want quoted shell command", command)
		}
		shellCommand := command[start+1 : end]
		shellCommand = strings.ReplaceAll(shellCommand, `\\`, `\`)
		shellCommand = strings.ReplaceAll(shellCommand, `\"`, `"`)
		shellCommand = strings.ReplaceAll(shellCommand, "#{window_id}", "@12")
		shellCommand = strings.ReplaceAll(shellCommand, "#{q:window_name}", `O\'Brien`)
		shellCommand = strings.ReplaceAll(shellCommand, "#{window_name}", "O'Brien")
		if out, err := exec.Command("sh", "-n", "-c", shellCommand).CombinedOutput(); err != nil {
			t.Fatalf("expanded shell command %q did not parse: %v: %s", shellCommand, err, string(out))
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
