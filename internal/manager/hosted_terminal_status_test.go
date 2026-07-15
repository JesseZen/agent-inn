package manager

import (
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
)

const wantTmuxStatusSpacerFormat = "#[align=left range=left]#[range=window|0 fg=colour231,bg=colour96,bold]#{R: ,#{w:#{E:status-left}}}#[norange default]#[list=on align=#{status-justify}]#{W:#[range=window|#{window_index} #{E:window-status-style}]#{R: ,#{w:#{E:window-status-format}}}#[norange default]#{?loop_last_flag,,#{window-status-separator}},#[range=window|#{window_index} list=focus #{E:window-status-current-style}]#{R: ,#{w:#{E:window-status-current-format}}}#[norange default list=on]#{?loop_last_flag,,#{window-status-separator}}}#[nolist align=right range=right]#[range=user|ainn-sessions fg=colour231,bg=colour96,bold]#{R: ,#{w:#{E:status-right}}}#[norange default]"

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

func hostedTestStatusCommand(settings config.Settings, session HostedSessionRecord) []string {
	snapshot := MapHostedSessionSnapshot(session, HostedSessionStatusActive, HostedSessionWorkerSnapshot{})
	return TmuxHostedTurnStatusCommandForSnapshot(settings, session.TmuxWindowID, snapshot)
}

func hostedTestStatusCommandForState(settings config.Settings, windowID string, state string) []string {
	return TmuxHostedTurnStatusCommandForSnapshot(settings, windowID, HostedSessionSnapshot{Turn: HostedSessionTurnSnapshot{State: state, Unread: true}})
}

func TestTmuxHostedTurnStatusCommandForSnapshot(t *testing.T) {
	settings := config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	got := hostedTestStatusCommandForState(settings, "@12", HostedTurnStateFailed)
	want := []string{
		"tmux", "-L", "ainn-test",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-format", " #I:! #W ",
		";",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-current-format", " #I:! #W ",
		";",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-style", "fg=colour196,bg=colour235,bold",
		";",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-current-style", "fg=colour231,bg=colour196,bold",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxHostedTurnStatusCommandForSnapshotRendersPriorityMatrix(t *testing.T) {
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host"}}}
	base := HostedSessionSnapshot{
		SessionID: "hs_1", SessionLabel: "work", Worker: HostedSessionWorkerSnapshot{}, AddDirs: []string{},
		Status: HostedSessionStatusActive, Turn: HostedSessionTurnSnapshot{State: HostedTurnStateIdle},
	}
	cases := []struct {
		name          string
		marker        string
		snapshot      HostedSessionSnapshot
		inactiveLabel string
		currentLabel  string
		inactiveStyle string
		currentStyle  string
	}{
		{"waiting todo", "?", snapshotWithTurn(base, HostedSessionTurnSnapshot{State: HostedTurnStateRunning, NeedsInput: true}, HostedUserMarkerTodo), " #I:? #W ", " #I:? #W ", "fg=colour208,bg=colour235,bold", "fg=colour0,bg=colour208,bold"},
		{"running todo", "*", snapshotWithTurn(base, HostedSessionTurnSnapshot{State: HostedTurnStateRunning}, HostedUserMarkerTodo), " #I:* #W ", " #I:* #W ", "fg=colour45,bg=colour235,bold", "fg=colour0,bg=colour45,bold"},
		{"done unread todo", "+", snapshotWithTurn(base, HostedSessionTurnSnapshot{State: HostedTurnStateDone, Unread: true}, HostedUserMarkerTodo), " #I:+ #W ", " #I:+ #W ", "fg=colour46,bg=colour235,bold", "fg=colour0,bg=colour46,bold"},
		{"failed unread todo", "!", snapshotWithTurn(base, HostedSessionTurnSnapshot{State: HostedTurnStateFailed, Unread: true}, HostedUserMarkerTodo), " #I:! #W ", " #I:! #W ", "fg=colour196,bg=colour235,bold", "fg=colour231,bg=colour196,bold"},
		{"acknowledged todo", "~", snapshotWithTurn(base, HostedSessionTurnSnapshot{State: HostedTurnStateDone}, HostedUserMarkerTodo), " #I:~ #W ", " #I:~ #W ", "fg=colour226,bg=colour235,bold", "fg=colour0,bg=colour226,bold"},
		{"done read", "+", snapshotWithTurn(base, HostedSessionTurnSnapshot{State: HostedTurnStateDone}, ""), " #I:+ #W ", " #I:+ #W ", "fg=colour244,bg=colour235", "fg=colour0,bg=colour45,bold"},
		{"interrupted read", "!", snapshotWithTurn(base, HostedSessionTurnSnapshot{State: HostedTurnStateInterrupted}, ""), " #I:! #W ", " #I:! #W ", "fg=colour244,bg=colour235", "fg=colour0,bg=colour45,bold"},
		{"idle", ":", snapshotWithTurn(base, HostedSessionTurnSnapshot{State: HostedTurnStateIdle}, ""), " #I:#W ", " #I:#W ", "fg=colour244,bg=colour235", "fg=colour0,bg=colour45,bold"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TmuxHostedTurnStatusCommandForSnapshot(settings, "@12", tc.snapshot)
			want := []string{
				"tmux", "-L", "ainn-test", "set-window-option", "-t", "ainn-test-host:@12", "window-status-format", tc.inactiveLabel, ";",
				"set-window-option", "-t", "ainn-test-host:@12", "window-status-current-format", tc.currentLabel, ";",
				"set-window-option", "-t", "ainn-test-host:@12", "window-status-style", tc.inactiveStyle, ";",
				"set-window-option", "-t", "ainn-test-host:@12", "window-status-current-style", tc.currentStyle,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, want %#v", got, want)
			}
			if strings.Contains(strings.Join(got, " "), "colour208") != (tc.marker == "?") {
				t.Fatalf("colour208 must be waiting-only: %#v", got)
			}
		})
	}
}

func snapshotWithTurn(base HostedSessionSnapshot, turn HostedSessionTurnSnapshot, marker string) HostedSessionSnapshot {
	base.Turn = turn
	base.UserMarker = marker
	return base
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
		"set-option", "-g", "status", "2", ";",
		"set-option", "-g", "status-format[1]", wantTmuxStatusSpacerFormat, ";",
		"set-option", "-g", "status-left", "#[range=window|0]#[fg=colour231,bg=colour96,bold] 0:ainn #[norange]#[default]", ";",
		"set-option", "-g", "status-right", "#[range=user|ainn-sessions]#[fg=colour231,bg=colour96,bold] Sessions #[default]", ";",
		"set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";",
		"set-window-option", "-g", "window-status-format", "#{?#{==:#{window_index},0},, #I:#W }", ";",
		"set-window-option", "-g", "window-status-current-format", "#{?#{==:#{window_index},0},, #I:#W }", ";",
		"set-window-option", "-g", "window-status-style", "fg=colour244,bg=colour235", ";",
		"set-window-option", "-g", "window-status-current-style", "fg=colour0,bg=colour45,bold", ";",
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

func TestTmuxThemeCommandForSettingsUsesConfiguredStatusBarHeight(t *testing.T) {
	cases := []struct {
		name   string
		height int
		status string
	}{
		{name: "one row", height: 1, status: "on"},
		{name: "two rows", height: 2, status: "2"},
		{name: "five rows", height: 5, status: "5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{
				SocketName:      "ainn-test",
				HostSession:     "ainn-test-host",
				HostStartMode:   config.TmuxHostStartModeNewWindow,
				StatusBarHeight: tc.height,
			}}}
			got := TmuxThemeCommandForSettings(settings)
			want := []string{
				"tmux", "-L", "ainn-test",
				"set-option", "-g", "status", tc.status, ";",
			}
			for row := 1; row < tc.height; row++ {
				want = append(want, "set-option", "-g", fmt.Sprintf("status-format[%d]", row), wantTmuxStatusSpacerFormat, ";")
			}
			want = append(want,
				"set-option", "-g", "status-left", "", ";",
				"set-option", "-g", "status-right", "#[range=user|ainn-sessions]#[fg=colour231,bg=colour96,bold] Sessions #[default]", ";",
				"set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";",
				"set-window-option", "-g", "window-status-format", " #I:#W ", ";",
				"set-window-option", "-g", "window-status-current-format", " #I:#W ", ";",
				"set-window-option", "-g", "window-status-style", "fg=colour244,bg=colour235", ";",
				"set-window-option", "-g", "window-status-current-style", "fg=colour0,bg=colour45,bold", ";",
				"set-window-option", "-g", "automatic-rename", "off",
			)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, want %#v", got, want)
			}
		})
	}
}

func TestTmuxThemeCommandForSettingsKeepsFirstHostedWindowInNonMainModes(t *testing.T) {
	want := []string{
		"tmux", "-L", "ainn-test",
		"set-option", "-g", "status", "2", ";",
		"set-option", "-g", "status-format[1]", wantTmuxStatusSpacerFormat, ";",
		"set-option", "-g", "status-left", "", ";",
		"set-option", "-g", "status-right", "#[range=user|ainn-sessions]#[fg=colour231,bg=colour96,bold] Sessions #[default]", ";",
		"set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";",
		"set-window-option", "-g", "window-status-format", " #I:#W ", ";",
		"set-window-option", "-g", "window-status-current-format", " #I:#W ", ";",
		"set-window-option", "-g", "window-status-style", "fg=colour244,bg=colour235", ";",
		"set-window-option", "-g", "window-status-current-style", "fg=colour0,bg=colour45,bold", ";",
		"set-window-option", "-g", "automatic-rename", "off",
	}
	for _, mode := range []string{config.TmuxHostStartModeNewWindow, config.TmuxHostStartModeReuseFirstWindow} {
		t.Run(mode, func(t *testing.T) {
			settings := config.Settings{
				Terminal: config.TerminalSettings{
					Tmux: config.TmuxSettings{
						SocketName:    "ainn-test",
						HostSession:   "ainn-test-host",
						HostStartMode: mode,
					},
				},
			}
			got := TmuxThemeCommandForSettings(settings)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, want %#v", got, want)
			}
		})
	}
}

func TestTmuxHostedTurnStatusCommandForSnapshotDistinguishesUnreadAndReadDone(t *testing.T) {
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

	gotUnread := hostedTestStatusCommand(settings, unread)
	wantUnread := []string{
		"tmux", "-L", "ainn-test",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-format", " #I:+ #W ",
		";",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-current-format", " #I:+ #W ",
		";",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-style", "fg=colour46,bg=colour235,bold",
		";",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-current-style", "fg=colour0,bg=colour46,bold",
	}
	if strings.Join(gotUnread, "\n") != strings.Join(wantUnread, "\n") {
		t.Fatalf("unread got %#v, want %#v", gotUnread, wantUnread)
	}

	gotRead := hostedTestStatusCommand(settings, read)
	wantRead := []string{
		"tmux", "-L", "ainn-test",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-format", " #I:+ #W ",
		";",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-current-format", " #I:+ #W ",
		";",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-style", "fg=colour244,bg=colour235",
		";",
		"set-window-option", "-t", "ainn-test-host:@12",
		"window-status-current-style", "fg=colour0,bg=colour45,bold",
	}
	if strings.Join(gotRead, "\n") != strings.Join(wantRead, "\n") {
		t.Fatalf("read got %#v, want %#v", gotRead, wantRead)
	}
}

func TestTmuxHostedTurnStatusCommandForSnapshotRendersTodoBelowUnreadStates(t *testing.T) {
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host"}}}
	cases := []struct {
		name          string
		session       HostedSessionRecord
		label         string
		inactiveStyle string
		currentStyle  string
	}{
		{
			name:          "idle todo",
			session:       HostedSessionRecord{TmuxWindowID: "@12", UserMarker: HostedUserMarkerTodo},
			label:         " #I:~ #W ",
			inactiveStyle: "fg=colour226,bg=colour235,bold",
			currentStyle:  "fg=colour0,bg=colour226,bold",
		},
		{
			name:          "read done todo",
			session:       HostedSessionRecord{TmuxWindowID: "@12", TurnState: HostedTurnStateDone, TurnGeneration: 2, TurnAcknowledgedGeneration: 2, UserMarker: HostedUserMarkerTodo},
			label:         " #I:~ #W ",
			inactiveStyle: "fg=colour226,bg=colour235,bold",
			currentStyle:  "fg=colour0,bg=colour226,bold",
		},
		{
			name:          "unread done wins",
			session:       HostedSessionRecord{TmuxWindowID: "@12", TurnState: HostedTurnStateDone, TurnGeneration: 2, UserMarker: HostedUserMarkerTodo},
			label:         " #I:+ #W ",
			inactiveStyle: "fg=colour46,bg=colour235,bold",
			currentStyle:  "fg=colour0,bg=colour46,bold",
		},
		{
			name:          "running wins",
			session:       HostedSessionRecord{TmuxWindowID: "@12", TurnState: HostedTurnStateRunning, UserMarker: HostedUserMarkerTodo},
			label:         " #I:* #W ",
			inactiveStyle: "fg=colour45,bg=colour235,bold",
			currentStyle:  "fg=colour0,bg=colour45,bold",
		},
		{
			name:          "failed unread wins",
			session:       HostedSessionRecord{TmuxWindowID: "@12", TurnState: HostedTurnStateFailed, TurnGeneration: 2, UserMarker: HostedUserMarkerTodo},
			label:         " #I:! #W ",
			inactiveStyle: "fg=colour196,bg=colour235,bold",
			currentStyle:  "fg=colour231,bg=colour196,bold",
		},
		{
			name:          "interrupted unread wins",
			session:       HostedSessionRecord{TmuxWindowID: "@12", TurnState: HostedTurnStateInterrupted, TurnGeneration: 2, UserMarker: HostedUserMarkerTodo},
			label:         " #I:! #W ",
			inactiveStyle: "fg=colour196,bg=colour235,bold",
			currentStyle:  "fg=colour231,bg=colour196,bold",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hostedTestStatusCommand(settings, tc.session)
			want := []string{
				"tmux", "-L", "ainn-test", "set-window-option", "-t", "ainn-test-host:@12", "window-status-format", tc.label, ";",
				"set-window-option", "-t", "ainn-test-host:@12", "window-status-current-format", tc.label, ";",
				"set-window-option", "-t", "ainn-test-host:@12", "window-status-style", tc.inactiveStyle, ";",
				"set-window-option", "-t", "ainn-test-host:@12", "window-status-current-style", tc.currentStyle,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, want %#v", got, want)
			}
		})
	}
}

func TestTmuxHostedInteractionBindingsCarryRegistryTargetData(t *testing.T) {
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-host"}}}
	mouse := TmuxHostedInteractionMouseBindingCommandForSettings(settings, "/tmp/ainn config", "/tmp/ainn bin")
	if !strings.Contains(strings.Join(mouse, " "), "MouseDown3Status") || !strings.Contains(strings.Join(mouse, " "), "--client-name #{q:client_name}") {
		t.Fatalf("unexpected mouse binding: %#v", mouse)
	}
	rename := TmuxHostedInteractionRenameBindingCommandForSettings(settings, "/tmp/ainn config", "/tmp/ainn bin")
	if !strings.Contains(strings.Join(rename, " "), "prefix ,") || !strings.Contains(strings.Join(rename, " "), "rename-or-native") {
		t.Fatalf("unexpected rename binding: %#v", rename)
	}
	prompt := TmuxHostedSessionRenamePromptCommandForSettings(settings, "/tmp/ainn config", "/tmp/ainn bin", "@12", `$(touch /tmp/pwned);"quoted"`)
	if !strings.Contains(strings.Join(prompt, " "), "%%%") {
		t.Fatalf("rename prompt must use tmux response substitution: %#v", prompt)
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
