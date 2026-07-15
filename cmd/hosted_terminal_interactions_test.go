package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
)

func hostedCmdTestStatus(settings config.Settings, session manager.HostedSessionRecord) []string {
	snapshot := manager.MapHostedSessionSnapshot(session, manager.HostedSessionStatusActive, manager.HostedSessionWorkerSnapshot{})
	return manager.TmuxHostedTurnStatusCommandForSnapshot(settings, session.TmuxWindowID, snapshot)
}

func hostedCmdTestStatusForState(settings config.Settings, windowID string, state string) []string {
	snapshot := manager.HostedSessionSnapshot{Turn: manager.HostedSessionTurnSnapshot{State: state, Unread: true}}
	return manager.TmuxHostedTurnStatusCommandForSnapshot(settings, windowID, snapshot)
}

func TestInstallTmuxHostedInteractionsRejectsDifferentOwnerWithoutWrites(t *testing.T) {
	currentConfig := t.TempDir()
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-host"}}}
	var calls [][]string
	runner := launchRunnerFunc(func(args []string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		if strings.Contains(strings.Join(args, " "), "@ainn_hosted_interaction_owner") {
			return "/tmp/other-config\n", nil
		}
		return "", nil
	})

	err := installTmuxHostedInteractions(runner, settings, currentConfig, "/tmp/ainn")
	if err == nil {
		t.Fatal("expected owner conflict")
	}
	if len(calls) != 1 {
		t.Fatalf("owner conflict must not write bindings: %#v", calls)
	}
}

func TestInstallTmuxHostedInteractionsReplacesRecognizedBindings(t *testing.T) {
	configDir := t.TempDir()
	resolvedConfigDir, err := canonicalHostedInteractionConfigDir(configDir)
	if err != nil {
		t.Fatal(err)
	}
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-host"}}}
	var calls [][]string
	runner := launchRunnerFunc(func(args []string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "@ainn_hosted_interaction_owner"):
			return "", nil
		case strings.Contains(joined, "MouseDown3Status"):
			return "bind-key -T root MouseDown3Status run-shell hosted-session menu --config-dir '" + resolvedConfigDir + "'\n", nil
		case strings.Contains(joined, "list-keys") && strings.Contains(joined, " -T prefix "):
			return "bind-key -T prefix , run-shell hosted-session rename-or-native --config-dir '" + resolvedConfigDir + "'\n", nil
		default:
			return "", nil
		}
	})

	if err := installTmuxHostedInteractions(runner, settings, configDir, "/tmp/ainn"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 6 {
		t.Fatalf("got %#v", calls)
	}
	if !strings.Contains(strings.Join(calls[4], " "), "MouseDown3Status") || !strings.Contains(strings.Join(calls[5], " "), "prefix ,") {
		t.Fatalf("expected replacement bindings, got %#v", calls)
	}
}

func TestInstallTmuxHostedInteractionsRejectsConfigPathPrefixBindings(t *testing.T) {
	parent := t.TempDir()
	configDir := filepath.Join(parent, "config")
	otherConfigDir := configDir + "-other"
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	resolvedOtherConfigDir, err := filepath.EvalSymlinks(otherConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-host"}}}
	for _, conflictingKey := range []string{manager.TmuxHostedInteractionMouseKey, manager.TmuxHostedInteractionRenameKey} {
		t.Run(conflictingKey, func(t *testing.T) {
			var calls [][]string
			runner := launchRunnerFunc(func(args []string) (string, error) {
				calls = append(calls, append([]string{}, args...))
				joined := strings.Join(args, " ")
				switch {
				case strings.Contains(joined, manager.TmuxHostedInteractionOwnerOption):
					return "", nil
				case strings.Contains(joined, manager.TmuxHostedInteractionMouseKey):
					if conflictingKey == manager.TmuxHostedInteractionMouseKey {
						return "bind-key -T root MouseDown3Status run-shell hosted-session menu --config-dir '" + resolvedOtherConfigDir + "'\n", nil
					}
					return tmuxNativeMouseDown3StatusBinding + "\n", nil
				case strings.Contains(joined, " -T prefix "):
					return "bind-key -T prefix , run-shell hosted-session rename-or-native --config-dir '" + resolvedOtherConfigDir + "'\n", nil
				default:
					return "", nil
				}
			})

			if err := installTmuxHostedInteractions(runner, settings, configDir, "/tmp/ainn"); err == nil {
				t.Fatal("expected config path prefix conflict")
			}
			wantCalls := 2
			if conflictingKey == manager.TmuxHostedInteractionRenameKey {
				wantCalls = 3
			}
			if len(calls) != wantCalls {
				t.Fatalf("conflict wrote interaction ownership or bindings: %#v", calls)
			}
		})
	}
}

func TestInstallTmuxHostedInteractionsRecognizesNativeTmuxBindings(t *testing.T) {
	if !isNativeHostedInteractionBinding(`bind-key -T root MouseDown3Status display-menu -T "#[align=centre]#{window_index}:#{window_name}" -t = -x W -y W "#{?#{>:#{session_windows},1},,-}Swap Left" l { swap-window -t :-1 } "#{?#{>:#{session_windows},1},,-}Swap Right" r { swap-window -t :+1 } "#{?pane_marked_set,,-}Swap Marked" s { swap-window } '' Kill X { kill-window } Respawn R { respawn-window -k } "#{?pane_marked,Unmark,Mark}" m { select-pane -m } Rename n { command-prompt -F -I "#W" { rename-window -t "#{window_id}" "%%" } } '' "New After" w { new-window -a } "New At End" W { new-window }`, manager.TmuxHostedInteractionMouseKey) {
		t.Fatal("expected tmux native status menu binding")
	}
	if !isNativeHostedInteractionBinding(`bind-key -T prefix , command-prompt -I "#W" { rename-window "%%" }`, manager.TmuxHostedInteractionRenameKey) {
		t.Fatal("expected tmux native rename binding")
	}
	if isNativeHostedInteractionBinding("bind-key -T root MouseDown3Status run-shell user-script", manager.TmuxHostedInteractionMouseKey) {
		t.Fatal("user binding must remain a conflict")
	}
	if isNativeHostedInteractionBinding(`bind-key -T root MouseDown3Status display-menu -T custom "Custom" c { run-shell user-script }`, manager.TmuxHostedInteractionMouseKey) {
		t.Fatal("custom display menu must remain a conflict")
	}
	if isNativeHostedInteractionBinding(`bind-key -T prefix , command-prompt -p Custom { rename-window "custom-%%" }`, manager.TmuxHostedInteractionRenameKey) {
		t.Fatal("custom rename prompt must remain a conflict")
	}
}

func TestRunHostedSessionMenuOrdersReadTurnActions(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-host", "new-window")
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	_, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "review", WorkerName: "cli-openai", WorkerPort: 11199, TmuxWindowID: "@12", TurnState: manager.HostedTurnStateDone,
		TurnGeneration: 1, TurnAcknowledgedGeneration: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	previous := launchRunnerFactory
	launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
		return launchRunnerFunc(func(args []string) (string, error) {
			got = append([]string{}, args...)
			return "", nil
		})
	}
	defer func() { launchRunnerFactory = previous }()

	var stderr bytes.Buffer
	code := runHostedSessionMenu([]string{"--config-dir", configDir, "--window-id", "@12", "--window-name", "review", "--client-name", "client-1"}, io.Discard, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	wantOrder := []string{"Open", "Mark todo", "Rename", "Mark unread", "-", "Hosted Terminal"}
	last := -1
	for _, label := range wantOrder {
		index := -1
		for i, arg := range got {
			if arg == label {
				index = i
				break
			}
		}
		if index <= last {
			t.Fatalf("menu order got %#v", got)
		}
		last = index
	}
	if !reflect.DeepEqual(got[:14], []string{
		"tmux", "-L", "ainn-test", "display-menu", "-M", "-O", "-c", "client-1",
		"-t", "ainn-host:@12", "-x", "W", "-y", "S",
	}) {
		t.Fatalf("menu placement got %#v", got)
	}
	renameIndex := -1
	for i, arg := range got {
		if arg == "Rename" {
			renameIndex = i
			break
		}
	}
	if renameIndex < 0 {
		t.Fatalf("missing Rename in %#v", got)
	}
	renameCommand := got[renameIndex+2]
	if !strings.Contains(renameCommand, "hosted-session rename-or-native") || strings.Contains(renameCommand, "command-prompt") {
		t.Fatalf("rename command got %q", renameCommand)
	}
}

func TestRunHostedSessionMenuIgnoresMissingRecord(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	writeLaunchConfig(t, configDir, filepath.Join(dir, "state"), "ainn-test", "ainn-host", "new-window")
	called := false
	previous := launchRunnerFactory
	launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
		return launchRunnerFunc(func(args []string) (string, error) {
			called = true
			return "", nil
		})
	}
	defer func() { launchRunnerFactory = previous }()

	if code := runHostedSessionMenu([]string{"--config-dir", configDir, "--window-id", "@99"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if called {
		t.Fatal("missing record must not open a menu")
	}
}

func TestRunHostedSessionRenameOrNativeFallsBackForNonHostedWindow(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	writeLaunchConfig(t, configDir, filepath.Join(dir, "state"), "ainn-test", "ainn-host", "new-window")
	var got []string
	previous := launchRunnerFactory
	launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
		return launchRunnerFunc(func(args []string) (string, error) {
			got = append([]string{}, args...)
			return "", nil
		})
	}
	defer func() { launchRunnerFactory = previous }()

	if code := runHostedSessionRenameOrNative([]string{"--config-dir", configDir, "--window-id", "@99", "--window-name", "shell"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("code=%d", code)
	}
	cfg, err := config.LoadFile(filepath.Join(configDir, config.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	want := manager.TmuxNativeRenameWindowPromptCommandForSettings(cfg.Settings, "shell")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestResetTmuxHostedInteractionsRemovesMatchingOwnerOnly(t *testing.T) {
	configDir := hostedTestTempDir(t)
	settings := hostedTestTmuxSettings("ainn-test", "ainn-host")
	var calls [][]string
	runner := launchRunnerFunc(func(args []string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		if strings.Contains(strings.Join(args, " "), manager.TmuxHostedInteractionOwnerOption) {
			return configDir + "\n", nil
		}
		return "", nil
	})
	if err := resetTmuxHostedInteractions(runner, settings, configDir); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		manager.TmuxHostedInteractionOwnerCommandForSettings(settings),
		manager.TmuxUnbindHostedInteractionBindingCommandForSettings(settings, "root", manager.TmuxHostedInteractionMouseKey),
		manager.TmuxUnbindHostedInteractionBindingCommandForSettings(settings, "prefix", manager.TmuxHostedInteractionRenameKey),
		manager.TmuxUnsetHostedInteractionOwnerCommandForSettings(settings),
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("got %#v want %#v", calls, want)
	}

	calls = nil
	runner = launchRunnerFunc(func(args []string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		return filepath.Join(configDir, "other") + "\n", nil
	})
	if err := resetTmuxHostedInteractions(runner, settings, configDir); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, [][]string{manager.TmuxHostedInteractionOwnerCommandForSettings(settings)}) {
		t.Fatalf("foreign owner must remain untouched: %#v", calls)
	}
}

func TestInstallTmuxHostedInteractionsUsesResolvedSymlinkOwner(t *testing.T) {
	dir := hostedTestTempDir(t)
	realConfig := filepath.Join(dir, "real")
	if err := os.Mkdir(realConfig, 0700); err != nil {
		t.Fatal(err)
	}
	linkConfig := filepath.Join(dir, "link")
	if err := os.Symlink(realConfig, linkConfig); err != nil {
		t.Fatal(err)
	}
	settings := hostedTestTmuxSettings("ainn-test", "ainn-host")
	var calls [][]string
	runner := launchRunnerFunc(func(args []string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		if strings.Contains(strings.Join(args, " "), manager.TmuxHostedInteractionOwnerOption) {
			return realConfig + "\n", nil
		}
		return "", nil
	})
	if err := installTmuxHostedInteractions(runner, settings, linkConfig, "/tmp/ainn"); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if strings.Contains(strings.Join(call, " "), linkConfig) {
			t.Fatalf("symlink path leaked into owner commands: %#v", calls)
		}
	}
}
