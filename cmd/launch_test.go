package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
)

func tmuxExtendedKeysCommand(socketName string) []string {
	return []string{"tmux", "-L", socketName, "set-option", "-s", "extended-keys", "always", ";", "set-option", "-s", "extended-keys-format", "csi-u", ";", "set-option", "-s", "terminal-features[3]", "xterm*:extkeys"}
}

func tmuxExtendedKeysSocketCommand(socketPath string) []string {
	return []string{"tmux", "-S", socketPath, "set-option", "-s", "extended-keys", "always", ";", "set-option", "-s", "extended-keys-format", "csi-u", ";", "set-option", "-s", "terminal-features[3]", "xterm*:extkeys"}
}

func tmuxResetMainWindowStatusCommand(socketName string, hostSession string) []string {
	return []string{"tmux", "-L", socketName, "set-window-option", "-t", hostSession + ":0", "-u", "window-status-format", ";", "set-window-option", "-t", hostSession + ":0", "-u", "window-status-current-format"}
}

func tmuxResetMainWindowStatusSocketCommand(socketPath string, hostSession string) []string {
	return []string{"tmux", "-S", socketPath, "set-window-option", "-t", hostSession + ":0", "-u", "window-status-format", ";", "set-window-option", "-t", hostSession + ":0", "-u", "window-status-current-format"}
}

func hostedTestTmuxSettings(socketName string, hostSession string) config.Settings {
	return config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  socketName,
				HostSession: hostSession,
			},
		},
	}
}

func hostedTestTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func hostedTestLaunchCommand(t *testing.T, configDir string, sessionID string, command ...string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := []string{
		"env",
		"AINN_HOSTED_SESSION_ID=" + sessionID,
		"AINN_CONFIG_DIR=" + configDir,
		"AINN_EXECUTABLE=" + exe,
	}
	return append(env, command...)
}

func TestHostedSessionLaunchCommandDisablesClaudeDaemon(t *testing.T) {
	got := hostedSessionLaunchCommand([]string{
		"env",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:11199",
		"ANTHROPIC_AUTH_TOKEN=ainn",
		"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1",
		"claude",
	}, "/tmp/config", "hs_1", true)
	want := []string{
		"env",
		"AINN_HOSTED_SESSION_ID=hs_1",
		"AINN_CONFIG_DIR=/tmp/config",
		"AINN_EXECUTABLE=" + hostedSessionExecutable(),
		"ANTHROPIC_BASE_URL=http://127.0.0.1:11199",
		"ANTHROPIC_AUTH_TOKEN=ainn",
		"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1",
		"CLAUDE_CODE_DISABLE_AGENT_VIEW=1",
		"claude",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected hosted claude command:\ngot  %#v\nwant %#v", got, want)
	}
}

func hostedTestAcknowledgeHookCommand(t *testing.T, settings config.Settings, configDir string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return manager.TmuxAcknowledgeTurnHookCommandForSettings(settings, configDir, exe)
}

func hostedTestPopupMouseBindingCommand(t *testing.T, settings config.Settings, configDir string, managerURL string, mode manager.TmuxHostedPopupMouseMode) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return manager.TmuxHostedPopupMouseBindingCommandForSettings(settings, configDir, managerURL, exe, mode)
}

func hostedTestToggleTodoMouseBindingCommand(t *testing.T, settings config.Settings, configDir string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return manager.TmuxToggleTodoMouseBindingCommandForSettings(settings, configDir, exe)
}

func hostedTestTurnStatusInstallCommands(t *testing.T, settings config.Settings, configDir string) [][]string {
	t.Helper()
	return [][]string{
		manager.TmuxTurnStatusOwnerCommandForSettings(settings),
		manager.TmuxShowHooksCommandForSettings(settings),
		manager.TmuxListAcknowledgeTurnMouseBindingCommandForSettings(settings),
		manager.TmuxListToggleTodoMouseBindingCommandForSettings(settings),
		manager.TmuxSetTurnStatusOwnerCommandForSettings(settings, configDir),
		hostedTestAcknowledgeHookCommand(t, settings, configDir),
		hostedTestToggleTodoMouseBindingCommand(t, settings, configDir),
	}
}

func hostedTestPopupBindingInstallCommands(t *testing.T, settings config.Settings, configDir string, managerURL string, key string) [][]string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		manager.TmuxHostedPopupOwnerCommandForSettings(settings),
		manager.TmuxHostedPopupKeyCommandForSettings(settings),
	}
	if key != "" {
		commands = append(commands,
			manager.TmuxListHostedPopupBindingCommandForSettings(settings, key),
			manager.TmuxSetHostedPopupOwnerCommandForSettings(settings, configDir),
			manager.TmuxSetHostedPopupKeyCommandForSettings(settings, key),
			hostedTestPopupMouseBindingCommand(t, settings, configDir, managerURL, manager.TmuxHostedPopupMouseModeAcknowledge),
			manager.TmuxHostedPopupBindingCommandForSettings(settings, key, configDir, managerURL, exe),
		)
	} else {
		commands = append(commands,
			manager.TmuxSetHostedPopupOwnerCommandForSettings(settings, configDir),
			hostedTestPopupMouseBindingCommand(t, settings, configDir, managerURL, manager.TmuxHostedPopupMouseModeAcknowledge),
		)
	}
	return commands
}

func hostedTestInteractionInstallCommands(t *testing.T, settings config.Settings, configDir string) [][]string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(configDir)
	if err != nil {
		t.Fatal(err)
	}
	return [][]string{
		manager.TmuxHostedInteractionOwnerCommandForSettings(settings),
		manager.TmuxListHostedInteractionBindingCommandForSettings(settings, "root", manager.TmuxHostedInteractionMouseKey),
		manager.TmuxListHostedInteractionBindingCommandForSettings(settings, "prefix", manager.TmuxHostedInteractionRenameKey),
		manager.TmuxSetHostedInteractionOwnerCommandForSettings(settings, resolved),
		manager.TmuxHostedInteractionMouseBindingCommandForSettings(settings, resolved, exe),
		manager.TmuxHostedInteractionRenameBindingCommandForSettings(settings, resolved, exe),
	}
}

func hostedTestHasTmuxSubcommand(commands [][]string, subcommand string) bool {
	for _, command := range commands {
		for _, arg := range command {
			if arg == subcommand {
				return true
			}
		}
	}
	return false
}

func hostedTestHasArg(commands [][]string, arg string) bool {
	for _, command := range commands {
		for _, got := range command {
			if got == arg {
				return true
			}
		}
	}
	return false
}

func hostedTestHasCommand(commands [][]string, want []string) bool {
	for _, command := range commands {
		if reflect.DeepEqual(command, want) {
			return true
		}
	}
	return false
}

func hostedTestAssertNoPopupWritesOrTheme(t *testing.T, commands [][]string, settings config.Settings, configDir string, key string) {
	t.Helper()
	if hostedTestHasCommand(commands, manager.TmuxThemeCommandForSettings(settings)) {
		t.Fatalf("popup conflict should not apply tmux theme/status control: %#v", commands)
	}
	if hostedTestHasCommand(commands, manager.TmuxSetHostedPopupOwnerCommandForSettings(settings, configDir)) {
		t.Fatalf("popup conflict should not write popup owner marker: %#v", commands)
	}
	if key != "" && hostedTestHasCommand(commands, manager.TmuxSetHostedPopupKeyCommandForSettings(settings, key)) {
		t.Fatalf("popup conflict should not write popup key marker: %#v", commands)
	}
	if hostedTestHasCommand(commands, hostedTestPopupMouseBindingCommand(t, settings, configDir, defaultManagerURL, manager.TmuxHostedPopupMouseModeSelect)) ||
		hostedTestHasCommand(commands, hostedTestPopupMouseBindingCommand(t, settings, configDir, defaultManagerURL, manager.TmuxHostedPopupMouseModeAcknowledge)) {
		t.Fatalf("popup conflict should not write popup mouse binding: %#v", commands)
	}
	if key != "" && hostedTestHasCommand(commands, manager.TmuxHostedPopupBindingCommandForSettings(settings, key, configDir, defaultManagerURL, hostedSessionExecutable())) {
		t.Fatalf("popup conflict should not write popup prefix binding: %#v", commands)
	}
}

func hostedTestLegacyAcknowledgeHookOutput(t *testing.T, settings config.Settings, configDir string) string {
	t.Helper()
	command := hostedTestAcknowledgeHookCommand(t, settings, configDir)
	return command[len(command)-2] + " " + command[len(command)-1] + "\n"
}

func hostedTestLegacyToggleTodoBindingOutput(t *testing.T, settings config.Settings, configDir string) string {
	t.Helper()
	command := hostedTestToggleTodoMouseBindingCommand(t, settings, configDir)
	return command[len(command)-2] + " " + command[len(command)-1] + "\n"
}

func TestRunLaunchRequiresWorker(t *testing.T) {
	var stderr bytes.Buffer
	code := runLaunch([]string{"--cd", "/tmp/work"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr.String(), "launch requires --worker") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestLaunchRunnerBuffersTmuxControlOutput(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner := launchRunnerFactory(&stdout, &stderr)
	output, err := runner.Run([]string{"/bin/sh", "-c", "printf output; printf error >&2; exit 1"})
	if err == nil {
		t.Fatal("expected command failure")
	}
	if output != "output" {
		t.Fatalf("got output %q", output)
	}
	if !strings.Contains(err.Error(), "error") {
		t.Fatalf("missing captured stderr in %q", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("control output leaked to terminal: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestLaunchRunnerReturnsFailedAttachReason(t *testing.T) {
	installFakeTmuxOnPath(t)
	t.Setenv("FAKE_TMUX_ATTACH_STDOUT", "[server exited unexpectedly]\n")
	t.Setenv("FAKE_TMUX_FAIL_COMMAND", "attach-session")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	output, err := launchRunnerFactory(&stdout, &stderr).Run([]string{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"})
	got := struct {
		Output string
		Stdout string
		Stderr string
		Error  string
	}{Output: output, Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		got.Error = err.Error()
	}
	want := struct {
		Output string
		Stdout string
		Stderr string
		Error  string
	}{
		Output: "[server exited unexpectedly]\n",
		Stdout: "[server exited unexpectedly]\n",
		Error:  "exit status 1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("failed attach reason mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestFinishHostedTerminalLaunchLogsUnexpectedTmuxClientExit(t *testing.T) {
	configDir := t.TempDir()
	settings := config.Settings{
		LogDir: filepath.Join(configDir, "logs"),
		Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{
			SocketName:  "ainn-test",
			HostSession: "ainn-test-host",
		}},
	}
	runner := launchRunnerFunc(func(args []string) (string, error) {
		return "[server exited unexpectedly]\n", errors.New("exit status 1")
	})
	var stderr bytes.Buffer
	code := finishHostedTerminalLaunch(settings, configDir, runner, &stderr, false)
	logPath := filepath.Join(settings.LogDir, "tmux-ainn-test.log")
	data, readErr := os.ReadFile(logPath)
	got := struct {
		Code    int
		ReadErr string
		Event   string
	}{Code: code}
	if readErr != nil {
		got.ReadErr = readErr.Error()
	} else if line := strings.TrimSpace(string(data)); line != "" {
		if index := strings.Index(line, "tmux.supervisor tmux.client.exit "); index >= 0 {
			got.Event = line[index:]
		}
	}
	want := struct {
		Code    int
		ReadErr string
		Event   string
	}{
		Code:  1,
		Event: "tmux.supervisor tmux.client.exit socket=ainn-test host_session=ainn-test-host reason=server_unexpected exit_code=1 error=\"exit status 1\"",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hosted tmux client exit mismatch:\n got %#v\nwant %#v\nstderr %q", got, want, stderr.String())
	}
}

func TestRunLaunchRunsBuiltCommandWithEncodedProfile(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	writeLaunchConfig(t, configDir, filepath.Join(dir, "state"), "ainn", "ainn-host", "new-window")
	replaceLaunchWorkerID(t, configDir, "0.02")

	var got []string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append([]string{}, args...)
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "Codex Main", "--cd", "/tmp/work", "--add-dir", "/tmp/shared", "--model", "gpt-5.5"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}
	if len(got) == 0 || got[0] != "codex" {
		t.Fatalf("unexpected command: %#v", got)
	}
	if strings.Join(got, " ") != strings.Join([]string{"codex", "--profile", "ainn-x-302e3032", "--cd", "/tmp/work", "--add-dir", "/tmp/shared", "--model", "gpt-5.5"}, " ") {
		t.Fatalf("unexpected launch args: %#v", got)
	}
}

func TestRunLaunchRejectsLongCodexProfileBeforeSpawn(t *testing.T) {
	spawned := false
	previous := launchRunnerFactory
	launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
		return launchRunnerFunc(func(args []string) (string, error) {
			spawned = true
			return "", nil
		})
	}
	defer func() { launchRunnerFactory = previous }()

	var stderr bytes.Buffer
	workerID := strings.Repeat("a", 244)
	code := runLaunch([]string{"--worker", "11199", "--profile", workerID}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected long profile to fail")
	}
	if spawned {
		t.Fatal("Codex must not be spawned for an invalid derived profile")
	}
	if !strings.Contains(stderr.String(), "244 bytes; limit is 243 bytes") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunLaunchUsesClaudeCodeWorkerConfig(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`
settings:
  state_dir: ` + stateDir + `
workers:
  claude-main:
    launcher: claudecode
    port: 11199
    upstream: anthropic
upstreams:
  anthropic:
    base_url: https://api.anthropic.com/v1
    api_format: anthropic
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}

	var got []string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append([]string{}, args...)
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}
	want := []string{
		"env",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:11199",
		"ANTHROPIC_AUTH_TOKEN=ainn",
		"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1",
		"claude",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected launch args:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestRunLaunchUsesGrokDefaultModelWhenProbeEmpty(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`
settings:
  state_dir: ` + stateDir + `
workers:
  grok-main:
    launcher: grok
    port: 11199
    upstream: xai
upstreams:
  xai:
    base_url: https://api.x.ai/v1
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}

	// Prefer a deterministic fake grok binary on PATH over any host install.
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	fakeGrok := filepath.Join(binDir, "grok")
	if err := os.WriteFile(fakeGrok, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// Hide ~/.grok/bin/grok by setting HOME away from the real one.
	t.Setenv("HOME", dir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

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
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}
	want := []string{
		"env",
		"HOME=" + filepath.Join(stateDir, "grok-home"),
		"GROK_MODELS_BASE_URL=http://127.0.0.1:11199/v1",
		"XAI_API_KEY=ainn",
		fakeGrok,
		"--model",
		manager.DefaultGrokModel,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected launch args:\ngot  %#v\nwant %#v", got, want)
	}
	for _, arg := range got {
		if arg == "grok-main" {
			t.Fatalf("must not use worker id as model: %#v", got)
		}
	}
}

func TestRunLaunchExplicitExternalWindowMode(t *testing.T) {
	var got []string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append([]string{}, args...)
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--worker", "11199", "--mode", "external-window"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}
	if len(got) == 0 || got[0] != "codex" {
		t.Fatalf("external-window should run codex directly, got %#v", got)
	}
}

func TestRunLaunchExternalWindowUsesDirectExecWithTerminalStreams(t *testing.T) {
	dir := hostedTestTempDir(t)
	codexPath := filepath.Join(dir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	called := false
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			called = true
			return launchRunnerFunc(func(args []string) (string, error) {
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	if code := runLaunch([]string{"--worker", "11199", "--mode", "external-window"}, os.Stdout, os.Stderr); code != 0 {
		t.Fatalf("expected success, got %d", code)
	}
	if called {
		t.Fatal("expected direct exec path, not launchRunnerFactory")
	}
}

func TestRunLaunchRejectsInvalidMode(t *testing.T) {
	var stderr bytes.Buffer
	code := runLaunch([]string{"--worker", "11199", "--mode", "bogus"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected failure for invalid mode")
	}
	if !strings.Contains(stderr.String(), "invalid mode") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunLaunchHostedTerminalRunsTmuxSequence(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")

	var got [][]string
	var startRequest tmuxServerStartRequest
	previousStarter := managedTmuxServerStarter
	managedTmuxServerStarter = func(request tmuxServerStartRequest) (tmuxServerStartResponse, error) {
		startRequest = request
		return tmuxServerStartResponse{}, nil
	}
	defer func() { managedTmuxServerStarter = previousStarter }()
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "off\n", nil
				}
				// Simulate fresh tmux host: has-session and select-window fail.
				// tmux subcommand sits at args[3] after `tmux -L ainn`.
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("no server running")
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--cd", "/tmp/work", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	tmuxSettings := hostedTestTmuxSettings("ainn-test", "ainn-test-host")
	want := [][]string{
		manager.TmuxDetectCommand(),
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "show", "-gv", "mouse"},
		{"tmux", "-L", "ainn-test", "set-option", "-g", "mouse", "on"},
		tmuxExtendedKeysCommand("ainn-test"),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(tmuxSettings))
	want = append(want,
		[]string{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:solve problem A"},
		append([]string{"tmux", "-L", "ainn-test", "new-window", "-t", "ainn-test-host", "-c", "/tmp/work", "-n", "solve problem A", "-P", "-F", "#{window_id}"}, hostedTestLaunchCommand(t, configDir, "hs_1", "codex", "--profile", "cli-openai", "--cd", "/tmp/work")...),
		[]string{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tmux command sequence mismatch:\n got %#v\nwant %#v", got, want)
	}
	wantStartRequest := tmuxServerStartRequest{
		ConfigDir:      configDir,
		LogDir:         filepath.Join(configDir, "logs"),
		SocketName:     "ainn-test",
		HostSession:    "ainn-test-host",
		InitialCommand: manager.TmuxStartHostCommandForSettings(tmuxSettings),
	}
	if !reflect.DeepEqual(startRequest, wantStartRequest) {
		t.Fatalf("tmux server start request mismatch:\n got %#v\nwant %#v", startRequest, wantStartRequest)
	}

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one hosted session, got %#v", records)
	}
	if records[0].SessionLabel != "solve problem A" || records[0].TmuxWindowID != "@12" {
		t.Fatalf("expected label and real window id in registry, got %#v", records[0])
	}
}

func TestRunLaunchHostedTerminalStartsWatcherSidecarForAttachedLaunch(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")

	var gotSidecarConfigDir string
	restoreSidecar := func() func() {
		previous := hostedTurnWatcherSidecarStarter
		hostedTurnWatcherSidecarStarter = func(configDir string) error {
			gotSidecarConfigDir = configDir
			return nil
		}
		return func() { hostedTurnWatcherSidecarStarter = previous }
	}()
	defer restoreSidecar()

	restoreRunner := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				if len(args) > 3 && args[3] == "show" {
					return "off\n", nil
				}
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restoreRunner()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}
	if gotSidecarConfigDir != configDir {
		t.Fatalf("got sidecar config dir %q, want %q", gotSidecarConfigDir, configDir)
	}
}

func TestRunLaunchHostedTerminalNoAttachDoesNotStartWatcherSidecar(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")

	sidecarStarted := false
	restoreSidecar := func() func() {
		previous := hostedTurnWatcherSidecarStarter
		hostedTurnWatcherSidecarStarter = func(configDir string) error {
			sidecarStarted = true
			return nil
		}
		return func() { hostedTurnWatcherSidecarStarter = previous }
	}()
	defer restoreSidecar()

	restoreRunner := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restoreRunner()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A", "--no-attach"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}
	if sidecarStarted {
		t.Fatal("no-attach launch should not start watcher sidecar")
	}
}

func TestRunLaunchHostedTerminalEnablesExtendedKeys(t *testing.T) {
	configDir := hostedTestTempDir(t)
	stateDir := filepath.Join(configDir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	want := tmuxExtendedKeysCommand("ainn-test")
	for _, command := range got {
		if reflect.DeepEqual(command, want) {
			return
		}
	}
	t.Fatalf("missing extended keys command %#v in %#v", want, got)
}

func TestRunLaunchHostedTerminalCreatesFreshHostWhenTmuxSocketMissing(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")

	var got [][]string
	var startRequest tmuxServerStartRequest
	previousStarter := managedTmuxServerStarter
	managedTmuxServerStarter = func(request tmuxServerStartRequest) (tmuxServerStartResponse, error) {
		startRequest = request
		return tmuxServerStartResponse{}, nil
	}
	defer func() { managedTmuxServerStarter = previousStarter }()
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New(tmuxMissingSocketErrorText)
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--cd", "/tmp/work", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	cfg, err := config.LoadFile(filepath.Join(configDir, config.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	codexCmd, err := manager.BuildCodexLaunchCommand(manager.CodexLaunchOptions{
		Profile:    "cli-openai",
		Workspace:  "/tmp/work",
		WorkerPort: 11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	launchCmd := hostedTestLaunchCommand(t, configDir, "hs_1", codexCmd...)
	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommandForSettings(cfg.Settings),
		manager.TmuxHasSessionCommandForSettings(cfg.Settings),
		manager.TmuxShowMouseCommandForSettings(cfg.Settings),
		manager.TmuxEnableExtendedKeysCommandForSettings(cfg.Settings),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, cfg.Settings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, cfg.Settings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, cfg.Settings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(cfg.Settings))
	want = append(want,
		manager.TmuxSelectWindowCommandForSettings(cfg.Settings, "solve problem A"),
		manager.TmuxCreateWindowCommandForSettings(cfg.Settings, "solve problem A", "/tmp/work", launchCmd),
		manager.TmuxAttachCommandForSettings(cfg.Settings),
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	wantStartRequest := tmuxServerStartRequest{
		ConfigDir:      configDir,
		LogDir:         filepath.Join(configDir, "logs"),
		SocketName:     "ainn-test",
		HostSession:    "ainn-test-host",
		InitialCommand: manager.TmuxStartHostCommandForSettings(cfg.Settings),
	}
	if !reflect.DeepEqual(startRequest, wantStartRequest) {
		t.Fatalf("tmux server start request mismatch:\n got %#v\nwant %#v", startRequest, wantStartRequest)
	}
}

func TestRunLaunchHostedTerminalInjectsAbsoluteConfigDir(t *testing.T) {
	workDir := hostedTestTempDir(t)
	configDir := filepath.Join(workDir, "config")
	stateDir := filepath.Join(workDir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	t.Chdir(workDir)

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", "config", "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	want := "AINN_CONFIG_DIR=" + configDir
	for _, command := range got {
		for _, arg := range command {
			if arg == want {
				return
			}
		}
	}
	t.Fatalf("missing %q in commands: %#v", want, got)
}

func TestRunLaunchHostedTerminalTurnStatusHooksDisabledOrOmittedDoesNotInjectSessionID(t *testing.T) {
	cases := []struct {
		name      string
		tmuxExtra string
	}{
		{name: "disabled", tmuxExtra: "      turn_status_hooks: false\n"},
		{name: "omitted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := hostedTestTempDir(t)
			configDir := filepath.Join(dir, "config")
			stateDir := filepath.Join(dir, "state")
			if err := os.MkdirAll(configDir, 0700); err != nil {
				t.Fatal(err)
			}
			data := []byte(`
settings:
  state_dir: ` + stateDir + `
  log_dir: ` + filepath.Join(configDir, "logs") + `
  launch:
    default_mode: hosted-terminal
  terminal:
    host: tmux
    opener: default
    tmux:
      socket_name: ainn-test
      host_session: ainn-test-host
      host_start_mode: new-window
` + tc.tmuxExtra + `workers:
  cli-openai:
    port: 11199
    upstream: openrouter
upstreams:
  openrouter:
    base_url: https://openrouter.ai/api/v1
`)
			if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
				t.Fatal(err)
			}

			var got [][]string
			restore := func() func() {
				previous := launchRunnerFactory
				launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
					return launchRunnerFunc(func(args []string) (string, error) {
						got = append(got, append([]string{}, args...))
						if len(args) > 3 && args[3] == "show" {
							return "on\n", nil
						}
						if len(args) > 3 && args[3] == "has-session" {
							return "", errors.New("can't find session")
						}
						if len(args) > 3 && args[3] == "select-window" {
							return "", errors.New("can't find window")
						}
						if len(args) > 3 && args[3] == "new-window" {
							return "@12\n", nil
						}
						return "", nil
					})
				}
				return func() { launchRunnerFactory = previous }
			}()
			defer restore()

			var stderr bytes.Buffer
			code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
			if code != 0 {
				t.Fatalf("expected success, got %d: %s", code, stderr.String())
			}

			wantConfigDir := "AINN_CONFIG_DIR=" + configDir
			foundConfigDir := false
			foundExecutable := false
			for _, command := range got {
				for _, arg := range command {
					if strings.HasPrefix(arg, "AINN_HOSTED_SESSION_ID=") {
						t.Fatalf("unexpected hosted session env in commands: %#v", got)
					}
					if arg == wantConfigDir {
						foundConfigDir = true
					}
					if strings.HasPrefix(arg, "AINN_EXECUTABLE=") {
						foundExecutable = true
					}
				}
			}
			if !foundConfigDir || !foundExecutable {
				t.Fatalf("missing hosted runtime env: config_dir=%v executable=%v commands=%#v", foundConfigDir, foundExecutable, got)
			}
			if hostedTestHasArg(got, "@ainn_turn_status_owner") {
				t.Fatalf("turn status hooks disabled should not touch owner option: %#v", got)
			}
			if hostedTestHasTmuxSubcommand(got, "set-hook") {
				t.Fatalf("turn status hooks disabled should not install hooks: %#v", got)
			}
			tmuxSettings := hostedTestTmuxSettings("ainn-test", "ainn-test-host")
			if !hostedTestHasCommand(got, hostedTestPopupMouseBindingCommand(t, tmuxSettings, configDir, defaultManagerURL, manager.TmuxHostedPopupMouseModeSelect)) {
				t.Fatalf("turn status hooks disabled should install select-mode popup mouse binding: %#v", got)
			}
		})
	}
}

func TestRunLaunchHostedTerminalKeepsExistingTurnStatusOwner(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	tmuxSettings := hostedTestTmuxSettings("ainn-test", "ainn-test-host")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupKeyCommandForSettings(tmuxSettings)) {
					return "", nil
				}
				if len(args) > 3 && args[3] == "show-option" {
					return configDir + "\n", nil
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}
	if !hostedTestHasCommand(got, manager.TmuxTurnStatusOwnerCommandForSettings(tmuxSettings)) {
		t.Fatalf("missing owner read command: %#v", got)
	}
	if hostedTestHasCommand(got, manager.TmuxSetTurnStatusOwnerCommandForSettings(tmuxSettings, configDir)) {
		t.Fatalf("same owner should not rewrite owner option: %#v", got)
	}
	if !hostedTestHasTmuxSubcommand(got, "set-hook") ||
		!hostedTestHasTmuxSubcommand(got, "bind-key") ||
		!hostedTestHasArg(got, "MouseDown1Status") ||
		!hostedTestHasArg(got, "DoubleClick1Status") {
		t.Fatalf("same owner should install hook and mouse binding: %#v", got)
	}
}

func TestRunLaunchHostedTerminalRejectsDifferentTurnStatusOwner(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	otherConfigDir := filepath.Join(dir, "other-config")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	tmuxSettings := hostedTestTmuxSettings("ainn-test", "ainn-test-host")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if len(args) > 3 && args[3] == "show-option" {
					return otherConfigDir + "\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected owner conflict to fail")
	}
	if !strings.Contains(stderr.String(), otherConfigDir) || !strings.Contains(stderr.String(), configDir) {
		t.Fatalf("expected owner conflict to name both config dirs, got %q", stderr.String())
	}
	if hostedTestHasCommand(got, manager.TmuxSetTurnStatusOwnerCommandForSettings(tmuxSettings, configDir)) ||
		hostedTestHasTmuxSubcommand(got, "set-hook") ||
		hostedTestHasTmuxSubcommand(got, "bind-key") {
		t.Fatalf("owner conflict should not write turn status hooks: %#v", got)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records, []manager.HostedSessionRecord{}) {
		t.Fatalf("owner conflict should clean up new hosted session, got %#v", records)
	}
}

func TestRunLaunchHostedTerminalRejectsLegacyTurnStatusOwnerMismatch(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	otherConfigDir := filepath.Join(dir, "other-config")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	tmuxSettings := hostedTestTmuxSettings("ainn-test", "ainn-test-host")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if len(args) > 3 && args[3] == "show-hooks" {
					return hostedTestLegacyAcknowledgeHookOutput(t, tmuxSettings, otherConfigDir), nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected legacy owner conflict to fail")
	}
	if !strings.Contains(stderr.String(), otherConfigDir) || !strings.Contains(stderr.String(), configDir) {
		t.Fatalf("expected legacy owner conflict to name both config dirs, got %q", stderr.String())
	}
	if hostedTestHasCommand(got, manager.TmuxSetTurnStatusOwnerCommandForSettings(tmuxSettings, configDir)) ||
		hostedTestHasTmuxSubcommand(got, "set-hook") ||
		hostedTestHasTmuxSubcommand(got, "bind-key") {
		t.Fatalf("legacy owner conflict should not write turn status hooks: %#v", got)
	}
}

func TestRunLaunchHostedTerminalRejectsLegacyTodoBindingOwnerMismatch(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	otherConfigDir := filepath.Join(dir, "other-config")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	tmuxSettings := hostedTestTmuxSettings("ainn-test", "ainn-test-host")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxListAcknowledgeTurnMouseBindingCommandForSettings(tmuxSettings)) {
					return "", nil
				}
				if reflect.DeepEqual(args, manager.TmuxListToggleTodoMouseBindingCommandForSettings(tmuxSettings)) {
					return hostedTestLegacyToggleTodoBindingOutput(t, tmuxSettings, otherConfigDir), nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected legacy todo owner conflict to fail")
	}
	if !strings.Contains(stderr.String(), otherConfigDir) || !strings.Contains(stderr.String(), configDir) {
		t.Fatalf("expected legacy todo owner conflict to name both config dirs, got %q", stderr.String())
	}
	if hostedTestHasCommand(got, manager.TmuxSetTurnStatusOwnerCommandForSettings(tmuxSettings, configDir)) ||
		hostedTestHasTmuxSubcommand(got, "set-hook") ||
		hostedTestHasTmuxSubcommand(got, "bind-key") {
		t.Fatalf("legacy todo owner conflict should not write turn status hooks: %#v", got)
	}
}

func TestRunLaunchHostedTerminalRejectsUnparseableLegacyTurnStatusHook(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	tmuxSettings := hostedTestTmuxSettings("ainn-test", "ainn-test-host")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if len(args) > 3 && args[3] == "show-hooks" {
					return "after-select-window[90] run-shell -b \"'/tmp/ainn' hosted-session acknowledge --config-dir --window-id #{window_id}\"\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected unparseable legacy hook to fail")
	}
	if !strings.Contains(stderr.String(), "parse") {
		t.Fatalf("expected parse failure, got %q", stderr.String())
	}
	if hostedTestHasCommand(got, manager.TmuxSetTurnStatusOwnerCommandForSettings(tmuxSettings, configDir)) ||
		hostedTestHasTmuxSubcommand(got, "set-hook") ||
		hostedTestHasTmuxSubcommand(got, "bind-key") {
		t.Fatalf("unparseable legacy hook should not write turn status hooks: %#v", got)
	}
}

func TestRunLaunchHostedTerminalHostedPopupKeyOmittedOrEmptyInstallsSelectMouseBinding(t *testing.T) {
	cases := []struct {
		name     string
		keyLine  string
		settings config.Settings
	}{
		{
			name:     "omitted",
			settings: hostedTestTmuxSettings("ainn-test", "ainn-test-host"),
		},
		{
			name:     "empty",
			keyLine:  "      hosted_popup_key: \"\"\n",
			settings: config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: ""}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := hostedTestTempDir(t)
			configDir := filepath.Join(dir, "config")
			stateDir := filepath.Join(dir, "state")
			writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
			if tc.keyLine != "" {
				appendHostedPopupKeyToLaunchConfig(t, configDir, tc.keyLine)
			}
			path := filepath.Join(configDir, config.ConfigFileName)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.Replace(string(data), "      turn_status_hooks: true\n", "      turn_status_hooks: false\n", 1))
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}

			var got [][]string
			restore := func() func() {
				previous := launchRunnerFactory
				launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
					return launchRunnerFunc(func(args []string) (string, error) {
						got = append(got, append([]string{}, args...))
						if len(args) > 3 && args[3] == "show" {
							return "on\n", nil
						}
						if len(args) > 3 && args[3] == "select-window" {
							return "", errors.New("can't find window")
						}
						if len(args) > 3 && args[3] == "new-window" {
							return "@12\n", nil
						}
						return "", nil
					})
				}
				return func() { launchRunnerFactory = previous }
			}()
			defer restore()

			var stderr bytes.Buffer
			code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
			if code != 0 {
				t.Fatalf("expected success, got %d: %s", code, stderr.String())
			}
			want := [][]string{
				manager.TmuxHostedPopupOwnerCommandForSettings(tc.settings),
				manager.TmuxSetHostedPopupOwnerCommandForSettings(tc.settings, configDir),
				hostedTestPopupMouseBindingCommand(t, tc.settings, configDir, defaultManagerURL, manager.TmuxHostedPopupMouseModeSelect),
			}
			for _, command := range want {
				if !hostedTestHasCommand(got, command) {
					t.Fatalf("hosted popup key %s missing popup mouse install command %#v in %#v", tc.name, command, got)
				}
			}
			for _, command := range [][]string{
				manager.TmuxListHostedPopupBindingCommandForSettings(tc.settings, ""),
				manager.TmuxSetHostedPopupKeyCommandForSettings(tc.settings, ""),
				manager.TmuxHostedPopupBindingCommandForSettings(tc.settings, "", configDir, defaultManagerURL, hostedSessionExecutable()),
			} {
				if hostedTestHasCommand(got, command) {
					t.Fatalf("hosted popup key %s should not run prefix binding command %#v in %#v", tc.name, command, got)
				}
			}
		})
	}
}

func TestRunLaunchHostedTerminalHostedPopupKeyInstallsBinding(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	appendHostedPopupKeyToLaunchConfig(t, configDir, "      hosted_popup_key: H\n")
	t.Setenv("AINN_URL", "http://127.0.0.1:19090")
	tmuxSettings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "H"}}}

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxTurnStatusOwnerCommandForSettings(tmuxSettings)) {
					return configDir + "\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(tmuxSettings)) {
					return "", nil
				}
				if reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(tmuxSettings, "H")) {
					return "", errors.New("exit status 1: unknown key: H")
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}
	want := hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, "http://127.0.0.1:19090", "H")
	for _, command := range want {
		if !hostedTestHasCommand(got, command) {
			t.Fatalf("missing popup install command %#v in %#v", command, got)
		}
	}
	lastIndex := -1
	for _, command := range want {
		found := false
		for j, gotCommand := range got {
			if reflect.DeepEqual(gotCommand, command) {
				if j <= lastIndex {
					t.Fatalf("popup install command order got %#v, want %#v in %#v", gotCommand, want, got)
				}
				lastIndex = j
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing popup install command %#v in %#v", command, got)
		}
	}
}

func TestRunLaunchHostedTerminalHostedPopupExistingAinnBindingAllowsTmuxNormalizedListKeys(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	appendHostedPopupKeyToLaunchConfig(t, configDir, "      hosted_popup_key: H\n")
	tmuxSettings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "H"}}}

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxTurnStatusOwnerCommandForSettings(tmuxSettings)) {
					return configDir + "\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(tmuxSettings)) {
					return configDir + "\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupKeyCommandForSettings(tmuxSettings)) {
					return "H\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(tmuxSettings, "H")) {
					return "bind-key -T prefix H display-popup -E -T \"Hosted Terminal\" -h \"100%\" -w \"40%\" -x R -y 0 '/tmp/ainn' hosted-session popup --config-dir '" + configDir + "' --manager-url 'http://127.0.0.1:9090'\n", nil
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A", "--no-attach"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}
	if hostedTestHasCommand(got, manager.TmuxSetHostedPopupOwnerCommandForSettings(tmuxSettings, configDir)) ||
		hostedTestHasCommand(got, manager.TmuxSetHostedPopupKeyCommandForSettings(tmuxSettings, "H")) {
		t.Fatalf("existing AINN popup binding should not rewrite owner markers: %#v", got)
	}
	if !hostedTestHasCommand(got, manager.TmuxHostedPopupBindingCommandForSettings(tmuxSettings, "H", configDir, defaultManagerURL, hostedSessionExecutable())) {
		t.Fatalf("missing refreshed popup binding command in %#v", got)
	}
}

func TestRunLaunchHostedTerminalHostedPopupExistingAinnBindingWithOldGeometryRefreshesBinding(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	appendHostedPopupKeyToLaunchConfig(t, configDir, "      hosted_popup_key: H\n")
	tmuxSettings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "H"}}}

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxTurnStatusOwnerCommandForSettings(tmuxSettings)) {
					return configDir + "\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(tmuxSettings)) {
					return configDir + "\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupKeyCommandForSettings(tmuxSettings)) {
					return "H\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(tmuxSettings, "H")) {
					return "bind-key -T prefix H display-popup -E -T \"Hosted Terminal\" -h \"70%\" -w \"80%\" -x R -y 0 '/tmp/ainn' hosted-session popup --config-dir '" + configDir + "' --manager-url 'http://127.0.0.1:9090'\n", nil
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A", "--no-attach"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}
	if hostedTestHasCommand(got, manager.TmuxSetHostedPopupOwnerCommandForSettings(tmuxSettings, configDir)) ||
		hostedTestHasCommand(got, manager.TmuxSetHostedPopupKeyCommandForSettings(tmuxSettings, "H")) {
		t.Fatalf("existing AINN popup binding should not rewrite owner markers: %#v", got)
	}
	if !hostedTestHasCommand(got, manager.TmuxHostedPopupBindingCommandForSettings(tmuxSettings, "H", configDir, defaultManagerURL, hostedSessionExecutable())) {
		t.Fatalf("missing refreshed popup binding command in %#v", got)
	}
}

func TestInstallTmuxHostedPopupBindingReplacesOwnedPrefixKey(t *testing.T) {
	configDir := filepath.Join(hostedTestTempDir(t), "config")
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "J"}}}
	oldBinding := "bind-key -T prefix H display-popup -E -x R -y 0 -w \"40%\" -h \"100%\" -T \"Hosted Terminal\" '/tmp/ainn' hosted-session popup --config-dir '" + configDir + "' --manager-url 'http://127.0.0.1:9090'\n"
	var got [][]string
	runner := launchRunnerFunc(func(args []string) (string, error) {
		got = append(got, append([]string{}, args...))
		switch {
		case reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(settings)):
			return configDir + "\n", nil
		case reflect.DeepEqual(args, manager.TmuxHostedPopupKeyCommandForSettings(settings)):
			return "H\n", nil
		case reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(settings, "J")):
			return "", errors.New("exit status 1: unknown key: J")
		case reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(settings, "H")):
			return oldBinding, nil
		default:
			return "", nil
		}
	})

	err := installTmuxHostedPopupBinding(runner, settings, configDir, "/tmp/ainn")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		manager.TmuxHostedPopupOwnerCommandForSettings(settings),
		manager.TmuxHostedPopupKeyCommandForSettings(settings),
		manager.TmuxListHostedPopupBindingCommandForSettings(settings, "H"),
		manager.TmuxListHostedPopupBindingCommandForSettings(settings, "J"),
		manager.TmuxUnbindHostedPopupBindingCommandForSettings(settings, "H"),
		manager.TmuxSetHostedPopupKeyCommandForSettings(settings, "J"),
		manager.TmuxHostedPopupMouseBindingCommandForSettings(settings, configDir, defaultManagerURL, "/tmp/ainn", manager.TmuxHostedPopupMouseModeSelect),
		manager.TmuxHostedPopupBindingCommandForSettings(settings, "J", configDir, defaultManagerURL, "/tmp/ainn"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestInstallTmuxHostedPopupBindingRemovesOwnedPrefixKeyWhenDisabled(t *testing.T) {
	configDir := filepath.Join(hostedTestTempDir(t), "config")
	settings := hostedTestTmuxSettings("ainn-test", "ainn-test-host")
	oldBinding := "bind-key -T prefix H display-popup -E -x R -y 0 -w \"80%\" -h \"70%\" -T \"Hosted Terminal\" '/tmp/ainn' hosted-session popup --config-dir '" + configDir + "' --manager-url 'http://127.0.0.1:9090'\n"
	var got [][]string
	runner := launchRunnerFunc(func(args []string) (string, error) {
		got = append(got, append([]string{}, args...))
		switch {
		case reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(settings)):
			return configDir + "\n", nil
		case reflect.DeepEqual(args, manager.TmuxHostedPopupKeyCommandForSettings(settings)):
			return "H\n", nil
		case reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(settings, "H")):
			return oldBinding, nil
		default:
			return "", nil
		}
	})

	err := installTmuxHostedPopupBinding(runner, settings, configDir, "/tmp/ainn")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		manager.TmuxHostedPopupOwnerCommandForSettings(settings),
		manager.TmuxHostedPopupKeyCommandForSettings(settings),
		manager.TmuxListHostedPopupBindingCommandForSettings(settings, "H"),
		manager.TmuxUnbindHostedPopupBindingCommandForSettings(settings, "H"),
		manager.TmuxSetHostedPopupKeyCommandForSettings(settings, ""),
		manager.TmuxHostedPopupMouseBindingCommandForSettings(settings, configDir, defaultManagerURL, "/tmp/ainn", manager.TmuxHostedPopupMouseModeSelect),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestInstallTmuxHostedPopupBindingReplacementConflictDoesNotModifyPriorOwnedKey(t *testing.T) {
	configDir := filepath.Join(hostedTestTempDir(t), "config")
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "J"}}}
	oldBinding := "bind-key -T prefix H display-popup -E -x R -y 0 -w \"40%\" -h \"100%\" -T \"Hosted Terminal\" '/tmp/ainn' hosted-session popup --config-dir '" + configDir + "' --manager-url 'http://127.0.0.1:9090'\n"
	var got [][]string
	runner := launchRunnerFunc(func(args []string) (string, error) {
		got = append(got, append([]string{}, args...))
		switch {
		case reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(settings)):
			return configDir + "\n", nil
		case reflect.DeepEqual(args, manager.TmuxHostedPopupKeyCommandForSettings(settings)):
			return "H\n", nil
		case reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(settings, "J")):
			return "bind-key -T prefix J display-message user-binding\n", nil
		case reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(settings, "H")):
			return oldBinding, nil
		default:
			return "", nil
		}
	})

	err := installTmuxHostedPopupBinding(runner, settings, configDir, "/tmp/ainn")
	if err == nil || !strings.Contains(err.Error(), "J") {
		t.Fatalf("got error %v, want replacement binding conflict", err)
	}
	want := [][]string{
		manager.TmuxHostedPopupOwnerCommandForSettings(settings),
		manager.TmuxHostedPopupKeyCommandForSettings(settings),
		manager.TmuxListHostedPopupBindingCommandForSettings(settings, "H"),
		manager.TmuxListHostedPopupBindingCommandForSettings(settings, "J"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestInstallTmuxHostedPopupBindingDoesNotRemoveNonAinnPriorBinding(t *testing.T) {
	configDir := filepath.Join(hostedTestTempDir(t), "config")
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "J"}}}
	var got [][]string
	runner := launchRunnerFunc(func(args []string) (string, error) {
		got = append(got, append([]string{}, args...))
		switch {
		case reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(settings)):
			return configDir + "\n", nil
		case reflect.DeepEqual(args, manager.TmuxHostedPopupKeyCommandForSettings(settings)):
			return "H\n", nil
		case reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(settings, "J")):
			return "", errors.New("exit status 1: unknown key: J")
		case reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(settings, "H")):
			return "bind-key -T prefix H display-message user-binding\n", nil
		default:
			return "", nil
		}
	})

	err := installTmuxHostedPopupBinding(runner, settings, configDir, "/tmp/ainn")
	if err == nil || !strings.Contains(err.Error(), "H") {
		t.Fatalf("got error %v, want prior binding conflict", err)
	}
	want := [][]string{
		manager.TmuxHostedPopupOwnerCommandForSettings(settings),
		manager.TmuxHostedPopupKeyCommandForSettings(settings),
		manager.TmuxListHostedPopupBindingCommandForSettings(settings, "H"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunLaunchHostedTerminalHostedPopupQuotedGeometryInShellPathDoesNotAllowRewrite(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config -w 40% -h 100%")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	appendHostedPopupKeyToLaunchConfig(t, configDir, "      hosted_popup_key: H\n")
	tmuxSettings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "H"}}}

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxTurnStatusOwnerCommandForSettings(tmuxSettings)) {
					return configDir + "\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(tmuxSettings)) {
					return configDir + "\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupKeyCommandForSettings(tmuxSettings)) {
					return "H\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(tmuxSettings, "H")) {
					return "bind-key -T prefix H display-popup -E -T \"Hosted Terminal\" -h \"100%\" -w \"60%\" -x R -y 0 '/tmp/ainn -w 40% -h 100%' hosted-session popup --config-dir '" + configDir + "' --manager-url 'http://127.0.0.1:9090'\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A", "--no-attach"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected custom popup geometry to fail")
	}
	if !strings.Contains(stderr.String(), "non-AINN binding") || !strings.Contains(stderr.String(), "H") {
		t.Fatalf("expected popup binding conflict, got %q", stderr.String())
	}
	hostedTestAssertNoPopupWritesOrTheme(t, got, tmuxSettings, configDir, "H")
}

func TestRunLaunchHostedTerminalHostedPopupSameOwnerSameKeyCustomGeometryFails(t *testing.T) {
	cases := []struct {
		name   string
		width  string
		height string
	}{
		{name: "custom width", width: "60%", height: "100%"},
		{name: "current width legacy height", width: "40%", height: "70%"},
		{name: "legacy width current height", width: "80%", height: "100%"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := hostedTestTempDir(t)
			configDir := filepath.Join(dir, "config")
			stateDir := filepath.Join(dir, "state")
			writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
			appendHostedPopupKeyToLaunchConfig(t, configDir, "      hosted_popup_key: H\n")
			tmuxSettings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "H"}}}

			var got [][]string
			restore := func() func() {
				previous := launchRunnerFactory
				launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
					return launchRunnerFunc(func(args []string) (string, error) {
						got = append(got, append([]string{}, args...))
						if len(args) > 3 && args[3] == "show" {
							return "on\n", nil
						}
						if reflect.DeepEqual(args, manager.TmuxTurnStatusOwnerCommandForSettings(tmuxSettings)) {
							return configDir + "\n", nil
						}
						if reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(tmuxSettings)) {
							return configDir + "\n", nil
						}
						if reflect.DeepEqual(args, manager.TmuxHostedPopupKeyCommandForSettings(tmuxSettings)) {
							return "H\n", nil
						}
						if reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(tmuxSettings, "H")) {
							return "bind-key -T prefix H display-popup -E -T \"Hosted Terminal\" -h \"" + tc.height + "\" -w \"" + tc.width + "\" -x R -y 0 '/tmp/ainn' hosted-session popup --config-dir '" + configDir + "' --manager-url 'http://127.0.0.1:9090'\n", nil
						}
						return "", nil
					})
				}
				return func() { launchRunnerFactory = previous }
			}()
			defer restore()

			var stderr bytes.Buffer
			code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A", "--no-attach"}, &bytes.Buffer{}, &stderr)
			if code == 0 {
				t.Fatal("expected custom popup geometry to fail")
			}
			if !strings.Contains(stderr.String(), "non-AINN binding") || !strings.Contains(stderr.String(), "H") {
				t.Fatalf("expected popup binding conflict, got %q", stderr.String())
			}
			hostedTestAssertNoPopupWritesOrTheme(t, got, tmuxSettings, configDir, "H")
		})
	}
}

func TestRunLaunchHostedTerminalHostedPopupUsesResolvedConfigDir(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "real-config")
	stateDir := filepath.Join(dir, "state")
	symlinkConfigDir := filepath.Join(dir, "linked-config")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	appendHostedPopupKeyToLaunchConfig(t, configDir, "      hosted_popup_key: H\n")
	if err := os.Symlink(configDir, symlinkConfigDir); err != nil {
		t.Fatal(err)
	}
	resolvedConfigDir, err := filepath.EvalSymlinks(configDir)
	if err != nil {
		t.Fatal(err)
	}
	tmuxSettings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "H"}}}

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", symlinkConfigDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A", "--no-attach"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}
	want := [][]string{
		manager.TmuxSetHostedPopupOwnerCommandForSettings(tmuxSettings, resolvedConfigDir),
		manager.TmuxSetHostedPopupKeyCommandForSettings(tmuxSettings, "H"),
		manager.TmuxHostedPopupBindingCommandForSettings(tmuxSettings, "H", resolvedConfigDir, defaultManagerURL, hostedSessionExecutable()),
	}
	for _, command := range want {
		if !hostedTestHasCommand(got, command) {
			t.Fatalf("missing popup install command %#v in %#v", command, got)
		}
	}
	if hostedTestHasCommand(got, manager.TmuxSetHostedPopupOwnerCommandForSettings(tmuxSettings, symlinkConfigDir)) ||
		hostedTestHasCommand(got, manager.TmuxHostedPopupBindingCommandForSettings(tmuxSettings, "H", symlinkConfigDir, defaultManagerURL, hostedSessionExecutable())) {
		t.Fatalf("popup install commands should use resolved config dir, got %#v", got)
	}
}

func TestRunLaunchHostedTerminalHostedPopupExistingBindingWithoutOwnerFails(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	appendHostedPopupKeyToLaunchConfig(t, configDir, "      hosted_popup_key: H\n")
	tmuxSettings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "H"}}}

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxTurnStatusOwnerCommandForSettings(tmuxSettings)) {
					return configDir + "\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(tmuxSettings)) {
					return "", nil
				}
				if reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(tmuxSettings, "H")) {
					return "bind-key -T prefix H display-message taken\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected existing popup binding without owner to fail")
	}
	if !strings.Contains(stderr.String(), "hosted popup") || !strings.Contains(stderr.String(), "H") {
		t.Fatalf("expected popup binding conflict, got %q", stderr.String())
	}
	hostedTestAssertNoPopupWritesOrTheme(t, got, tmuxSettings, configDir, "H")
	if hostedTestHasCommand(got, manager.TmuxHostedPopupBindingCommandForSettings(tmuxSettings, "H", configDir, defaultManagerURL, hostedSessionExecutable())) {
		t.Fatalf("existing binding conflict should not install popup binding: %#v", got)
	}
}

func TestInstallTmuxHostedPopupBindingEmptyOwnerUserBindingFailsBeforeWrites(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	tmuxSettings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "H"}}}

	var got [][]string
	runner := launchRunnerFunc(func(args []string) (string, error) {
		got = append(got, append([]string{}, args...))
		if reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(tmuxSettings)) {
			return "", nil
		}
		if reflect.DeepEqual(args, manager.TmuxHostedPopupKeyCommandForSettings(tmuxSettings)) {
			return "", nil
		}
		if reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(tmuxSettings, "H")) {
			return "bind-key -T prefix H display-message user-binding\n", nil
		}
		return "", nil
	})

	err := installTmuxHostedPopupBinding(runner, tmuxSettings, configDir, hostedSessionExecutable())
	if err == nil {
		t.Fatal("expected existing popup binding without owner to fail")
	}
	if !strings.Contains(err.Error(), "hosted popup") || !strings.Contains(err.Error(), "H") {
		t.Fatalf("expected popup binding conflict, got %q", err.Error())
	}
	want := [][]string{
		manager.TmuxHostedPopupOwnerCommandForSettings(tmuxSettings),
		manager.TmuxHostedPopupKeyCommandForSettings(tmuxSettings),
		manager.TmuxListHostedPopupBindingCommandForSettings(tmuxSettings, "H"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got commands %#v, want %#v", got, want)
	}
}

func TestRunLaunchHostedTerminalHostedPopupDifferentOwnerFails(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	otherConfigDir := filepath.Join(dir, "other-config")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	appendHostedPopupKeyToLaunchConfig(t, configDir, "      hosted_popup_key: H\n")
	tmuxSettings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "H"}}}

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxTurnStatusOwnerCommandForSettings(tmuxSettings)) {
					return configDir + "\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(tmuxSettings)) {
					return otherConfigDir + "\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected different popup owner to fail")
	}
	if !strings.Contains(stderr.String(), otherConfigDir) || !strings.Contains(stderr.String(), configDir) {
		t.Fatalf("expected popup owner conflict to name both config dirs, got %q", stderr.String())
	}
	hostedTestAssertNoPopupWritesOrTheme(t, got, tmuxSettings, configDir, "H")
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records, []manager.HostedSessionRecord{}) {
		t.Fatalf("owner conflict should clean up new hosted session, got %#v", records)
	}
}

func TestRunLaunchHostedTerminalExistingSessionHostedPopupDifferentOwnerFailsBeforeTheme(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	otherConfigDir := filepath.Join(dir, "other-config")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	appendHostedPopupKeyToLaunchConfig(t, configDir, "      hosted_popup_key: H\n")
	tmuxSettings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "H"}}}

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "popup conflict",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxTurnStatusOwnerCommandForSettings(tmuxSettings)) {
					return configDir + "\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(tmuxSettings)) {
					return otherConfigDir + "\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", created.SessionID, "--no-attach"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected different popup owner to fail")
	}
	if !strings.Contains(stderr.String(), otherConfigDir) || !strings.Contains(stderr.String(), configDir) {
		t.Fatalf("expected popup owner conflict to name both config dirs, got %q", stderr.String())
	}
	hostedTestAssertNoPopupWritesOrTheme(t, got, tmuxSettings, configDir, "H")
	if hostedTestHasTmuxSubcommand(got, "list-windows") ||
		hostedTestHasTmuxSubcommand(got, "select-window") ||
		hostedTestHasTmuxSubcommand(got, "new-window") {
		t.Fatalf("popup owner conflict should stop before window selection/setup: %#v", got)
	}
}

func TestRunLaunchHostedTerminalHostedPopupSameOwnerSameKeyNonAinnBindingFails(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	appendHostedPopupKeyToLaunchConfig(t, configDir, "      hosted_popup_key: H\n")
	tmuxSettings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{SocketName: "ainn-test", HostSession: "ainn-test-host", HostedPopupKey: "H"}}}

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxTurnStatusOwnerCommandForSettings(tmuxSettings)) {
					return configDir + "\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupOwnerCommandForSettings(tmuxSettings)) {
					return configDir + "\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxHostedPopupKeyCommandForSettings(tmuxSettings)) {
					return "H\n", nil
				}
				if reflect.DeepEqual(args, manager.TmuxListHostedPopupBindingCommandForSettings(tmuxSettings, "H")) {
					return "bind-key -T prefix H display-message user-binding\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected same-owner same-key non-AINN popup binding to fail")
	}
	if !strings.Contains(stderr.String(), "hosted popup") || !strings.Contains(stderr.String(), "H") {
		t.Fatalf("expected popup binding conflict, got %q", stderr.String())
	}
	hostedTestAssertNoPopupWritesOrTheme(t, got, tmuxSettings, configDir, "H")
	if hostedTestHasCommand(got, manager.TmuxHostedPopupBindingCommandForSettings(tmuxSettings, "H", configDir, defaultManagerURL, hostedSessionExecutable())) {
		t.Fatalf("same-owner same-key non-AINN binding should not install popup binding: %#v", got)
	}
}

func TestManagedTurnStatusConfigDirIgnoresUnrelatedTodoBindings(t *testing.T) {
	owner, found, err := managedTurnStatusConfigDir("", "", "bind-key -T root C-t run-shell -b \"'/tmp/ainn' hosted-session toggle-todo --config-dir '/tmp/other' --window-id #{window_id}\"\n")
	got := struct {
		owner string
		found bool
		err   string
	}{owner: owner, found: found}
	if err != nil {
		got.err = err.Error()
	}
	want := struct {
		owner string
		found bool
		err   string
	}{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestManagedTurnStatusConfigDirIgnoresUnrelatedAcknowledgeRootBindings(t *testing.T) {
	owner, found, err := managedTurnStatusConfigDir("", "", "bind-key -T root C-a run-shell -b \"'/tmp/ainn' hosted-session acknowledge --config-dir '/tmp/other' --window-id #{window_id}\"\n")
	got := struct {
		owner string
		found bool
		err   string
	}{owner: owner, found: found}
	if err != nil {
		got.err = err.Error()
	}
	want := struct {
		owner string
		found bool
		err   string
	}{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunLaunchHostedTerminalMissingSessionDoesNotTouchTmuxHostSettings(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", "hs_missing"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected missing hosted session to fail")
	}
	if !strings.Contains(stderr.String(), "hosted session \"hs_missing\" not found") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	for _, subcommand := range []string{"show", "show-option", "show-hooks", "list-keys", "set-option", "set-hook", "bind-key"} {
		if hostedTestHasTmuxSubcommand(got, subcommand) {
			t.Fatalf("missing hosted session should not touch tmux host settings, ran %s in %#v", subcommand, got)
		}
	}
}

func TestRunLaunchHostedTerminalAbortsOnUnexpectedHasSessionError(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("permission denied")
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Errorf("expected unexpected has-session error to fail launch")
	}
	if !strings.Contains(stderr.String(), "failed to inspect tmux host session") || !strings.Contains(stderr.String(), "permission denied") {
		t.Errorf("expected clear has-session failure, got %q", stderr.String())
	}
	for _, call := range got {
		if len(call) > 3 && call[3] == "new-session" {
			t.Errorf("unexpected new-session call after has-session failure: %#v", got)
		}
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("expected no hosted session registry records, got %#v", records)
	}
}

func TestRunLaunchHostedTerminalSwitchesExistingWindow(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn", "ainn-host", "new-window")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if strings.Join(args, " ") == strings.Join(manager.TmuxListWindowDetailsCommandForSettings(config.Settings{}), " ") {
					return "@12\tsolve problem A\n", nil
				}
				// Simulate existing host and window: has-session and select-window succeed.
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", created.SessionID}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	tmuxSettings := hostedTestTmuxSettings("ainn", "ainn-host")
	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "ainn", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn"),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(tmuxSettings))
	want = append(want,
		[]string{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}\t#{window_name}"},
		manager.TmuxSelectWindowCommand("@12"),
		manager.TmuxAttachCommand(),
	)
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
	}
}

func TestRunLaunchHostedTerminalSwitchesExistingLegacyNamedWindow(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn", "ainn-host", "new-window")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if strings.Join(args, " ") == strings.Join(manager.TmuxListWindowDetailsCommandForSettings(config.Settings{}), " ") {
					return "@12\tsolve problem A\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
		TmuxWindowID: "solve problem A",
	})
	if err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", created.SessionID}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}

	tmuxSettings := hostedTestTmuxSettings("ainn", "ainn-host")
	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "ainn", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn"),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(tmuxSettings))
	want = append(want,
		[]string{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}\t#{window_name}"},
		manager.TmuxSelectWindowCommand("@12"),
		manager.TmuxAttachCommand(),
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got commands %#v, want %#v", got, want)
	}
}

func TestRunLaunchHostedTerminalMissingTmux(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	writeLaunchConfig(t, configDir, filepath.Join(dir, "state"), "ainn", "ainn-host", "new-window")

	var stderr bytes.Buffer
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				return "", errors.New("exec: \"tmux\": executable file not found")
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected failure when tmux is missing")
	}
	if !strings.Contains(stderr.String(), "tmux is required for hosted-terminal mode") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunLaunchHostedTerminalNoAttachSkipsAttach(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn", "ainn-host", "new-window")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "off\n", nil
				}
				if strings.Join(args, " ") == strings.Join(manager.TmuxListWindowDetailsCommandForSettings(config.Settings{}), " ") {
					return "@12\tsolve problem A\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", created.SessionID, "--no-attach"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	tmuxSettings := hostedTestTmuxSettings("ainn", "ainn-host")
	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "ainn", "show", "-gv", "mouse"},
		{"tmux", "-L", "ainn", "set-option", "-g", "mouse", "on"},
		tmuxExtendedKeysCommand("ainn"),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(tmuxSettings))
	want = append(want,
		[]string{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}\t#{window_name}"},
		manager.TmuxSelectWindowCommand("@12"),
	)
	if len(got) != len(want) {
		t.Fatalf("expected %d commands (no attach), got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
	}
}

func TestRunLaunchHostedTerminalReopensStaleCodexSessionWithEncodedProfile(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn", "ainn-host", "new-window")
	replaceLaunchWorkerID(t, configDir, "0.02")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if strings.Join(args, " ") == strings.Join(manager.TmuxListWindowDetailsCommandForSettings(config.Settings{}), " ") {
					return "@12\tother session\n", nil
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@77\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel:      "solve problem A",
		WorkerID:          "0.02",
		WorkerName:        "Codex Main",
		WorkerPort:        9999,
		Workspace:         "/tmp/work",
		AddDirs:           []string{"/tmp/shared"},
		Model:             "gpt-5.5",
		LauncherSessionID: "019e7c18-0ee7-7ff2-bc82-9c410511ede3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "0.02", "--mode", "hosted-terminal", "--session-id", created.SessionID}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	type sessionView struct {
		SessionID         string
		TmuxWindowID      string
		LauncherSessionID string
	}
	gotSession := sessionView{
		SessionID:         updated.SessionID,
		TmuxWindowID:      updated.TmuxWindowID,
		LauncherSessionID: updated.LauncherSessionID,
	}
	wantSession := sessionView{
		SessionID:         created.SessionID,
		TmuxWindowID:      "@77",
		LauncherSessionID: "019e7c18-0ee7-7ff2-bc82-9c410511ede3",
	}
	if !ok || !reflect.DeepEqual(gotSession, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", gotSession, ok, wantSession)
	}

	tmuxSettings := hostedTestTmuxSettings("ainn", "ainn-host")
	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "ainn", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn"),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(tmuxSettings))
	want = append(want,
		[]string{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}\t#{window_name}"},
		append([]string{"tmux", "-L", "ainn", "new-window", "-t", "ainn-host", "-c", "/tmp/work", "-n", "solve problem A", "-P", "-F", "#{window_id}"}, hostedTestLaunchCommand(t, configDir, created.SessionID, "codex", "resume", "--profile", "ainn-x-302e3032", "--cd", "/tmp/work", "--add-dir", "/tmp/shared", "--model", "gpt-5.5", "019e7c18-0ee7-7ff2-bc82-9c410511ede3")...),
		manager.TmuxAttachCommand(),
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got commands %#v, want %#v", got, want)
	}
}

func TestRunLaunchHostedTerminalReopensSessionThroughManagedMissingServer(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerID:     "cli-openai",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
		Workspace:    "/tmp/work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	var startRequest tmuxServerStartRequest
	previousStarter := managedTmuxServerStarter
	managedTmuxServerStarter = func(request tmuxServerStartRequest) (tmuxServerStartResponse, error) {
		startRequest = request
		return tmuxServerStartResponse{}, nil
	}
	defer func() { managedTmuxServerStarter = previousStarter }()
	previousRunner := launchRunnerFactory
	launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
		return launchRunnerFunc(func(args []string) (string, error) {
			switch tmuxSubcommand(args) {
			case "has-session":
				return "", errors.New(tmuxMissingSocketErrorText)
			case "show":
				return "on\n", nil
			case "new-window":
				return "@77\n", nil
			default:
				return "", nil
			}
		})
	}
	defer func() { launchRunnerFactory = previousRunner }()

	var stderr bytes.Buffer
	code := runLaunch([]string{
		"--config-dir", configDir,
		"--worker", "11199",
		"--profile", "cli-openai",
		"--mode", "hosted-terminal",
		"--session-id", created.SessionID,
		"--no-attach",
	}, &bytes.Buffer{}, &stderr)
	updated, ok, getErr := registry.Get(created.SessionID)
	got := struct {
		Code          int
		StartRequest  tmuxServerStartRequest
		WindowID      string
		RecordPresent bool
		GetError      string
	}{
		Code:          code,
		StartRequest:  startRequest,
		WindowID:      updated.TmuxWindowID,
		RecordPresent: ok,
	}
	if getErr != nil {
		got.GetError = getErr.Error()
	}
	want := struct {
		Code          int
		StartRequest  tmuxServerStartRequest
		WindowID      string
		RecordPresent bool
		GetError      string
	}{
		StartRequest: tmuxServerStartRequest{
			ConfigDir:      configDir,
			LogDir:         filepath.Join(configDir, "logs"),
			SocketName:     "ainn-test",
			HostSession:    "ainn-test-host",
			InitialCommand: manager.TmuxStartHostCommandForSettings(hostedTestTmuxSettings("ainn-test", "ainn-test-host")),
		},
		WindowID:      "@77",
		RecordPresent: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("managed reopen mismatch:\n got %#v\nwant %#v\nstderr %q", got, want, stderr.String())
	}
}

func TestRunLaunchHostedTerminalReopensUnstartedStaleCodexSessionWithEncodedProfile(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn", "ainn-host", "new-window")
	replaceLaunchWorkerID(t, configDir, "0.02")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if strings.Join(args, " ") == strings.Join(manager.TmuxListWindowDetailsCommandForSettings(config.Settings{}), " ") {
					return "@12\tother session\n", nil
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@77\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerID:     "0.02",
		WorkerName:   "Codex Main",
		WorkerPort:   11199,
		Workspace:    "/tmp/work",
		AddDirs:      []string{"/tmp/shared"},
		Model:        "gpt-5.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "0.02", "--mode", "hosted-terminal", "--session-id", created.SessionID}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	type unstartedSessionView struct {
		SessionID         string
		TmuxWindowID      string
		LauncherSessionID string
		TurnGeneration    int
	}
	gotSession := unstartedSessionView{
		SessionID:         updated.SessionID,
		TmuxWindowID:      updated.TmuxWindowID,
		LauncherSessionID: updated.LauncherSessionID,
		TurnGeneration:    updated.TurnGeneration,
	}
	wantSession := unstartedSessionView{
		SessionID:         created.SessionID,
		TmuxWindowID:      "@77",
		LauncherSessionID: "",
		TurnGeneration:    0,
	}
	if !ok || !reflect.DeepEqual(gotSession, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", gotSession, ok, wantSession)
	}

	tmuxSettings := hostedTestTmuxSettings("ainn", "ainn-host")
	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "ainn", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn"),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(tmuxSettings))
	want = append(want,
		[]string{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}\t#{window_name}"},
		append([]string{"tmux", "-L", "ainn", "new-window", "-t", "ainn-host", "-c", "/tmp/work", "-n", "solve problem A", "-P", "-F", "#{window_id}"}, hostedTestLaunchCommand(t, configDir, created.SessionID, "codex", "--profile", "ainn-x-302e3032", "--cd", "/tmp/work", "--add-dir", "/tmp/shared", "--model", "gpt-5.5")...),
		manager.TmuxAttachCommand(),
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got commands %#v, want %#v", got, want)
	}
}

func TestRunLaunchHostedTerminalRejectsStartedStaleSessionWithoutLauncherID(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn", "ainn-host", "new-window")

	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if strings.Join(args, " ") == strings.Join(manager.TmuxListWindowDetailsCommandForSettings(config.Settings{}), " ") {
					return "@12\tother session\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.MarkTurnState(created.SessionID, manager.HostedTurnStateRunning, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", created.SessionID}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected started stale session without launcher id to fail")
	}
	if !strings.Contains(stderr.String(), "is stale and has no launcher session id") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunLaunchHostedTerminalReopensStaleClaudeSessionWithoutLauncherID(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn", "ainn-host", "new-window")
	configPath := filepath.Join(configDir, config.ConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "    port: 11199\n", "    port: 11199\n    launcher: claudecode\n", 1))
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if strings.Join(args, " ") == strings.Join(manager.TmuxListWindowDetailsCommandForSettings(config.Settings{}), " ") {
					return "@12\tother session\n", nil
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@77\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.MarkTurnState(created.SessionID, manager.HostedTurnStateRunning, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--mode", "hosted-terminal", "--session-id", created.SessionID}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected Claude stale session to start a fresh pane, got %d: %s", code, stderr.String())
	}
	var claudeLaunch []string
	for _, command := range got {
		if len(command) > 0 && command[0] == "tmux" && len(command) > 3 && command[3] == "new-window" {
			claudeLaunch = command
		}
	}
	if len(claudeLaunch) == 0 || strings.Contains(strings.Join(claudeLaunch, " "), "--resume") {
		t.Fatalf("expected fresh Claude launch, got %#v", claudeLaunch)
	}
}

func TestRunLaunchHostedTerminalReopensStaleClaudeCodeSession(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`
settings:
  state_dir: ` + stateDir + `
  log_dir: ` + filepath.Join(configDir, "logs") + `
  launch:
    default_mode: hosted-terminal
  terminal:
    host: tmux
    opener: default
    tmux:
      socket_name: ainn
      host_session: ainn-host
      host_start_mode: new-window
      turn_status_hooks: true
workers:
  claude-main:
    port: 11200
    upstream: anthropic
    launcher: claudecode
upstreams:
  anthropic:
    base_url: https://api.anthropic.com
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if strings.Join(args, " ") == strings.Join(manager.TmuxListWindowDetailsCommandForSettings(config.Settings{}), " ") {
					return "@12\tother session\n", nil
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@77\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel:      "solve problem A",
		WorkerName:        "claude-main",
		WorkerPort:        11199,
		Workspace:         "/tmp/work",
		LauncherSessionID: "9e98a56c-7224-4bf2-9263-b4e470e9673d",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "claude-main", "--mode", "hosted-terminal", "--session-id", created.SessionID}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}

	tmuxSettings := hostedTestTmuxSettings("ainn", "ainn-host")
	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "ainn", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn"),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(tmuxSettings))
	want = append(want,
		[]string{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}\t#{window_name}"},
		append([]string{"tmux", "-L", "ainn", "new-window", "-t", "ainn-host", "-c", "/tmp/work", "-n", "solve problem A", "-P", "-F", "#{window_id}"}, hostedTestLaunchCommand(t, configDir, created.SessionID, "ANTHROPIC_BASE_URL=http://127.0.0.1:11200", "ANTHROPIC_AUTH_TOKEN=ainn", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1", "CLAUDE_CODE_DISABLE_AGENT_VIEW=1", "claude", "--resume", "9e98a56c-7224-4bf2-9263-b4e470e9673d")...),
		manager.TmuxAttachCommand(),
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got commands %#v, want %#v", got, want)
	}
}

func TestRunLaunchHostedTerminalKeepsMouseWhenEnabled(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn", "ainn-host", "new-window")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if strings.Join(args, " ") == strings.Join(manager.TmuxListWindowDetailsCommandForSettings(config.Settings{}), " ") {
					return "@12\tsolve problem A\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", created.SessionID}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	tmuxSettings := hostedTestTmuxSettings("ainn", "ainn-host")
	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "ainn", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn"),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(tmuxSettings))
	want = append(want,
		[]string{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}\t#{window_name}"},
		manager.TmuxSelectWindowCommand("@12"),
		manager.TmuxAttachCommand(),
	)
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
	}
}

func TestRunLaunchHostedTerminalReuseFirstWindowOnFreshHost(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "reuse-first-window")

	var got [][]string
	var startRequest tmuxServerStartRequest
	previousStarter := managedTmuxServerStarter
	managedTmuxServerStarter = func(request tmuxServerStartRequest) (tmuxServerStartResponse, error) {
		startRequest = request
		return tmuxServerStartResponse{Stdout: "@1\t0\n"}, nil
	}
	defer func() { managedTmuxServerStarter = previousStarter }()
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("no server running")
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	tmuxSettings := hostedTestTmuxSettings("ainn-test", "ainn-test-host")
	want := [][]string{
		manager.TmuxDetectCommand(),
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn-test"),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(tmuxSettings))
	want = append(want,
		[]string{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tmux command sequence mismatch:\n got %#v\nwant %#v", got, want)
	}
	wantStartRequest := tmuxServerStartRequest{
		ConfigDir:   configDir,
		LogDir:      filepath.Join(configDir, "logs"),
		SocketName:  "ainn-test",
		HostSession: "ainn-test-host",
		InitialCommand: append(
			[]string{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "solve problem A", "-P", "-F", "#{window_id}\t#{window_index}"},
			hostedTestLaunchCommand(t, configDir, "hs_1", "codex", "--profile", "cli-openai")...,
		),
	}
	if !reflect.DeepEqual(startRequest, wantStartRequest) {
		t.Fatalf("tmux server start request mismatch:\n got %#v\nwant %#v", startRequest, wantStartRequest)
	}

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one hosted session, got %#v", records)
	}
	if records[0].SessionLabel != "solve problem A" || records[0].TmuxWindowID != "@1" {
		t.Fatalf("expected label and real window id in registry, got %#v", records[0])
	}
}

func TestRunLaunchHostedTerminalReuseFirstWindowMovesNonZeroFirstWindowToIndex0(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "reuse-first-window")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
				}
				if len(args) > 3 && args[3] == "new-session" {
					return "@1\t1\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	tmuxSettings := hostedTestTmuxSettings("ainn-test", "ainn-test-host")
	want := [][]string{
		manager.TmuxDetectCommand(),
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		append([]string{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "solve problem A", "-P", "-F", "#{window_id}\t#{window_index}"}, hostedTestLaunchCommand(t, configDir, "hs_1", "codex", "--profile", "cli-openai")...),
		{"tmux", "-L", "ainn-test", "move-window", "-s", "ainn-test-host:1", "-t", "ainn-test-host:0"},
		{"tmux", "-L", "ainn-test", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn-test"),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(tmuxSettings))
	want = append(want,
		[]string{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	gotRecords := make([]struct {
		SessionLabel string
		TmuxWindowID string
	}, 0, len(records))
	for _, record := range records {
		gotRecords = append(gotRecords, struct {
			SessionLabel string
			TmuxWindowID string
		}{
			SessionLabel: record.SessionLabel,
			TmuxWindowID: record.TmuxWindowID,
		})
	}
	wantRecords := []struct {
		SessionLabel string
		TmuxWindowID string
	}{
		{SessionLabel: "solve problem A", TmuxWindowID: "@1"},
	}
	if !reflect.DeepEqual(gotRecords, wantRecords) {
		t.Fatalf("got %#v, want %#v", gotRecords, wantRecords)
	}
}

func TestRunLaunchHostedTerminalReuseFirstWindowCapturesNewSessionIDWhenLabelIsAttachSession(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "reuse-first-window")
	installFakeTmuxOnPath(t)
	t.Setenv("FAKE_TMUX_NEW_SESSION_STDOUT", "@1\t0\n")
	previousStarter := managedTmuxServerStarter
	managedTmuxServerStarter = func(request tmuxServerStartRequest) (tmuxServerStartResponse, error) {
		return tmuxServerStartResponse{Stdout: "@1\t0\n"}, nil
	}
	defer func() { managedTmuxServerStarter = previousStarter }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "attach-session"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one hosted session, got %#v", records)
	}
	got := []struct {
		SessionLabel string
		TmuxWindowID string
	}{
		{SessionLabel: records[0].SessionLabel, TmuxWindowID: records[0].TmuxWindowID},
	}
	want := []struct {
		SessionLabel string
		TmuxWindowID string
	}{
		{SessionLabel: "attach-session", TmuxWindowID: "@1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunLaunchHostedTerminalReuseFirstWindowStillUsesNewWindowOnExistingHost(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "reuse-first-window")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	tmuxSettings := hostedTestTmuxSettings("ainn-test", "ainn-test-host")
	want := [][]string{
		manager.TmuxDetectCommand(),
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn-test"),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(tmuxSettings))
	want = append(want,
		[]string{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:solve problem A"},
		append([]string{"tmux", "-L", "ainn-test", "new-window", "-t", "ainn-test-host", "-n", "solve problem A", "-P", "-F", "#{window_id}"}, hostedTestLaunchCommand(t, configDir, "hs_1", "codex", "--profile", "cli-openai")...),
		[]string{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	)
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
	}
}

func TestRunLaunchHostedTerminalCapturesNewWindowIDWhenLabelIsAttachSession(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")
	installFakeTmuxOnPath(t)
	t.Setenv("FAKE_TMUX_HAS_SESSION", "1")
	t.Setenv("FAKE_TMUX_FAIL_COMMAND", "select-window")
	t.Setenv("FAKE_TMUX_NEW_WINDOW_STDOUT", "@12\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "attach-session"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d: %s", code, stderr.String())
	}

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one hosted session, got %#v", records)
	}
	got := []struct {
		SessionLabel string
		TmuxWindowID string
	}{
		{SessionLabel: records[0].SessionLabel, TmuxWindowID: records[0].TmuxWindowID},
	}
	want := []struct {
		SessionLabel string
		TmuxWindowID string
	}{
		{SessionLabel: "attach-session", TmuxWindowID: "@12"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunLaunchHostedTerminalMainTUIWindowStillUsesNewWindow(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	tmuxSettings := hostedTestTmuxSettings("ainn-test", "ainn-test-host")
	tmuxSettings.Terminal.Tmux.HostStartMode = config.TmuxHostStartModeMainTUIWindow
	want := [][]string{
		manager.TmuxDetectCommand(),
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn-test"),
	}
	want = append(want, hostedTestTurnStatusInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, hostedTestPopupBindingInstallCommands(t, tmuxSettings, configDir, defaultManagerURL, "")...)
	want = append(want, hostedTestInteractionInstallCommands(t, tmuxSettings, configDir)...)
	want = append(want, manager.TmuxThemeCommandForSettings(tmuxSettings))
	want = append(want,
		[]string{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:solve problem A"},
		append([]string{"tmux", "-L", "ainn-test", "new-window", "-t", "ainn-test-host", "-n", "solve problem A", "-P", "-F", "#{window_id}"}, hostedTestLaunchCommand(t, configDir, "hs_1", "codex", "--profile", "cli-openai")...),
		[]string{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	)
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
	}
}

func TestRunLaunchHostedTerminalDeletesNewRecordWhenFreshHostSetupFails(t *testing.T) {
	dir := hostedTestTempDir(t)
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")

	previousStarter := managedTmuxServerStarter
	managedTmuxServerStarter = func(request tmuxServerStartRequest) (tmuxServerStartResponse, error) {
		return tmuxServerStartResponse{}, errors.New("managed server failed")
	}
	defer func() { managedTmuxServerStarter = previousStarter }()
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("no server running")
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr.String(), "failed to start tmux host") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records, []manager.HostedSessionRecord{}) {
		t.Fatalf("got %#v, want no hosted session records", records)
	}
}

func writeLaunchConfig(t *testing.T, configDir string, stateDir string, socketName string, hostSession string, hostStartMode string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`
settings:
  state_dir: ` + stateDir + `
  log_dir: ` + filepath.Join(configDir, "logs") + `
  launch:
    default_mode: hosted-terminal
  terminal:
    host: tmux
    opener: default
    tmux:
      socket_name: ` + socketName + `
      host_session: ` + hostSession + `
      host_start_mode: ` + hostStartMode + `
      turn_status_hooks: true
workers:
  cli-openai:
    port: 11199
    upstream: openrouter
upstreams:
  openrouter:
    base_url: https://openrouter.ai/api/v1
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func replaceLaunchWorkerID(t *testing.T, configDir string, workerID string) {
	t.Helper()
	path := filepath.Join(configDir, config.ConfigFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	next := strings.Replace(string(data), "  cli-openai:\n", "  \""+workerID+"\":\n", 1)
	if err := os.WriteFile(path, []byte(next), 0600); err != nil {
		t.Fatal(err)
	}
}

func appendHostedPopupKeyToLaunchConfig(t *testing.T, configDir string, keyLine string) {
	t.Helper()
	path := filepath.Join(configDir, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	next := strings.Replace(string(data), "      turn_status_hooks: true\n", "      turn_status_hooks: true\n"+keyLine, 1)
	if err := os.WriteFile(path, []byte(next), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRenderCodexLaunchCommand(t *testing.T) {
	got, err := manager.BuildCodexLaunchCommand(manager.CodexLaunchOptions{Profile: "11199", WorkerPort: 11199})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("unexpected launch command: %#v", got)
	}
}

func TestRunLaunchRejectsBadWorker(t *testing.T) {
	var stderr bytes.Buffer
	code := runLaunch([]string{"--worker", "abc"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr.String(), "invalid worker port") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}
