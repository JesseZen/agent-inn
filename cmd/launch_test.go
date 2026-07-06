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
	return []string{"tmux", "-L", socketName, "set-option", "-s", "extended-keys", "on", ";", "set-option", "-s", "terminal-features[3]", "xterm*:extkeys"}
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

func TestRunLaunchRunsBuiltCommand(t *testing.T) {
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

	code := runLaunch([]string{"--worker", "11199", "--profile", "cli-openai", "--cd", "/tmp/work", "--add-dir", "/tmp/shared", "--model", "gpt-5.5"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}
	if len(got) == 0 || got[0] != "codex" {
		t.Fatalf("unexpected command: %#v", got)
	}
	if strings.Join(got, " ") != strings.Join([]string{"codex", "--profile", "cli-openai", "--cd", "/tmp/work", "--add-dir", "/tmp/shared", "--model", "gpt-5.5"}, " ") {
		t.Fatalf("unexpected launch args: %#v", got)
	}
}

func TestRunLaunchUsesClaudeCodeWorkerConfig(t *testing.T) {
	dir := t.TempDir()
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
	dir := t.TempDir()
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
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")

	var got [][]string
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

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--cd", "/tmp/work", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	want := [][]string{
		manager.TmuxDetectCommand(),
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "show", "-gv", "mouse"},
		{"tmux", "-L", "ainn-test", "set-option", "-g", "mouse", "on"},
		tmuxExtendedKeysCommand("ainn-test"),
		{"tmux", "-L", "ainn-test", "set-option", "-g", "status", "on", ";", "set-option", "-g", "status-left", "", ";", "set-option", "-g", "status-right", "", ";", "set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";", "set-window-option", "-g", "window-status-format", "#[fg=colour244,bg=colour235] #I:#W #[default]", ";", "set-window-option", "-g", "window-status-current-format", "#[fg=colour0,bg=colour45,bold] #I:#W #[default]", ";", "set-window-option", "-g", "automatic-rename", "off"},
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:solve problem A"},
		{"tmux", "-L", "ainn-test", "new-window", "-t", "ainn-test-host", "-n", "solve problem A", "-P", "-F", "#{window_id}", "codex", "--profile", "cli-openai", "--cd", "/tmp/work"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
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

func TestRunLaunchHostedTerminalEnablesExtendedKeys(t *testing.T) {
	configDir := t.TempDir()
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
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")

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
	codexCmd := manager.BuildCodexLaunchCommand(manager.CodexLaunchOptions{
		Profile:    "cli-openai",
		Workspace:  "/tmp/work",
		WorkerPort: 11199,
	})
	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommandForSettings(cfg.Settings),
		manager.TmuxStartHostCommandForSettings(cfg.Settings),
		manager.TmuxShowMouseCommandForSettings(cfg.Settings),
		manager.TmuxEnableExtendedKeysCommandForSettings(cfg.Settings),
		manager.TmuxThemeCommandForSettings(cfg.Settings),
		manager.TmuxSelectWindowCommandForSettings(cfg.Settings, "solve problem A"),
		manager.TmuxCreateWindowCommandForSettings(cfg.Settings, "solve problem A", codexCmd),
		manager.TmuxAttachCommandForSettings(cfg.Settings),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunLaunchHostedTerminalAbortsOnUnexpectedHasSessionError(t *testing.T) {
	dir := t.TempDir()
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
	dir := t.TempDir()
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

	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "ainn", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn"),
		{"tmux", "-L", "ainn", "set-option", "-g", "status", "on", ";", "set-option", "-g", "status-left", "", ";", "set-option", "-g", "status-right", "", ";", "set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";", "set-window-option", "-g", "window-status-format", "#[fg=colour244,bg=colour235] #I:#W #[default]", ";", "set-window-option", "-g", "window-status-current-format", "#[fg=colour0,bg=colour45,bold] #I:#W #[default]", ";", "set-window-option", "-g", "automatic-rename", "off"},
		{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}\t#{window_name}"},
		manager.TmuxSelectWindowCommand("@12"),
		manager.TmuxAttachCommand(),
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
	}
}

func TestRunLaunchHostedTerminalMissingTmux(t *testing.T) {
	dir := t.TempDir()
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
	dir := t.TempDir()
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

	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "ainn", "show", "-gv", "mouse"},
		{"tmux", "-L", "ainn", "set-option", "-g", "mouse", "on"},
		tmuxExtendedKeysCommand("ainn"),
		{"tmux", "-L", "ainn", "set-option", "-g", "status", "on", ";", "set-option", "-g", "status-left", "", ";", "set-option", "-g", "status-right", "", ";", "set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";", "set-window-option", "-g", "window-status-format", "#[fg=colour244,bg=colour235] #I:#W #[default]", ";", "set-window-option", "-g", "window-status-current-format", "#[fg=colour0,bg=colour45,bold] #I:#W #[default]", ";", "set-window-option", "-g", "automatic-rename", "off"},
		{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}\t#{window_name}"},
		manager.TmuxSelectWindowCommand("@12"),
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands (no attach), got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
	}
}

func TestRunLaunchHostedTerminalExistingSessionWithReusedWindowIDIsStale(t *testing.T) {
	dir := t.TempDir()
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
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", created.SessionID}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected stale session failure")
	}
	if !strings.Contains(stderr.String(), "is stale") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}

	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "ainn", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn"),
		{"tmux", "-L", "ainn", "set-option", "-g", "status", "on", ";", "set-option", "-g", "status-left", "", ";", "set-option", "-g", "status-right", "", ";", "set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";", "set-window-option", "-g", "window-status-format", "#[fg=colour244,bg=colour235] #I:#W #[default]", ";", "set-window-option", "-g", "window-status-current-format", "#[fg=colour0,bg=colour45,bold] #I:#W #[default]", ";", "set-window-option", "-g", "automatic-rename", "off"},
		{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}\t#{window_name}"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
	}
}

func TestRunLaunchHostedTerminalKeepsMouseWhenEnabled(t *testing.T) {
	dir := t.TempDir()
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

	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "ainn", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn"),
		{"tmux", "-L", "ainn", "set-option", "-g", "status", "on", ";", "set-option", "-g", "status-left", "", ";", "set-option", "-g", "status-right", "", ";", "set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";", "set-window-option", "-g", "window-status-format", "#[fg=colour244,bg=colour235] #I:#W #[default]", ";", "set-window-option", "-g", "window-status-current-format", "#[fg=colour0,bg=colour45,bold] #I:#W #[default]", ";", "set-window-option", "-g", "automatic-rename", "off"},
		{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}\t#{window_name}"},
		manager.TmuxSelectWindowCommand("@12"),
		manager.TmuxAttachCommand(),
	}
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
	dir := t.TempDir()
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
					return "@1\t0\n", nil
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

	want := [][]string{
		manager.TmuxDetectCommand(),
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "solve problem A", "-P", "-F", "#{window_id}\t#{window_index}", "codex", "--profile", "cli-openai"},
		{"tmux", "-L", "ainn-test", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn-test"),
		{"tmux", "-L", "ainn-test", "set-option", "-g", "status", "on", ";", "set-option", "-g", "status-left", "", ";", "set-option", "-g", "status-right", "", ";", "set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";", "set-window-option", "-g", "window-status-format", "#[fg=colour244,bg=colour235] #I:#W #[default]", ";", "set-window-option", "-g", "window-status-current-format", "#[fg=colour0,bg=colour45,bold] #I:#W #[default]", ";", "set-window-option", "-g", "automatic-rename", "off"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
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
	dir := t.TempDir()
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

	want := [][]string{
		manager.TmuxDetectCommand(),
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "solve problem A", "-P", "-F", "#{window_id}\t#{window_index}", "codex", "--profile", "cli-openai"},
		{"tmux", "-L", "ainn-test", "move-window", "-s", "ainn-test-host:1", "-t", "ainn-test-host:0"},
		{"tmux", "-L", "ainn-test", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn-test"),
		{"tmux", "-L", "ainn-test", "set-option", "-g", "status", "on", ";", "set-option", "-g", "status-left", "", ";", "set-option", "-g", "status-right", "", ";", "set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";", "set-window-option", "-g", "window-status-format", "#[fg=colour244,bg=colour235] #I:#W #[default]", ";", "set-window-option", "-g", "window-status-current-format", "#[fg=colour0,bg=colour45,bold] #I:#W #[default]", ";", "set-window-option", "-g", "automatic-rename", "off"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
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
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "reuse-first-window")
	installFakeTmuxOnPath(t)
	t.Setenv("FAKE_TMUX_NEW_SESSION_STDOUT", "@1\t0\n")

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
	dir := t.TempDir()
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

	want := [][]string{
		manager.TmuxDetectCommand(),
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn-test"),
		{"tmux", "-L", "ainn-test", "set-option", "-g", "status", "on", ";", "set-option", "-g", "status-left", "", ";", "set-option", "-g", "status-right", "", ";", "set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";", "set-window-option", "-g", "window-status-format", "#[fg=colour244,bg=colour235] #I:#W #[default]", ";", "set-window-option", "-g", "window-status-current-format", "#[fg=colour0,bg=colour45,bold] #I:#W #[default]", ";", "set-window-option", "-g", "automatic-rename", "off"},
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:solve problem A"},
		{"tmux", "-L", "ainn-test", "new-window", "-t", "ainn-test-host", "-n", "solve problem A", "-P", "-F", "#{window_id}", "codex", "--profile", "cli-openai"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
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
	dir := t.TempDir()
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
	dir := t.TempDir()
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

	want := [][]string{
		manager.TmuxDetectCommand(),
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "show", "-gv", "mouse"},
		tmuxExtendedKeysCommand("ainn-test"),
		{"tmux", "-L", "ainn-test", "set-option", "-g", "status", "on", ";", "set-option", "-g", "status-left", "", ";", "set-option", "-g", "status-right", "", ";", "set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";", "set-window-option", "-g", "window-status-format", "#[fg=colour244,bg=colour235] #I:#W #[default]", ";", "set-window-option", "-g", "window-status-current-format", "#[fg=colour0,bg=colour45,bold] #I:#W #[default]", ";", "set-window-option", "-g", "automatic-rename", "off"},
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:solve problem A"},
		{"tmux", "-L", "ainn-test", "new-window", "-t", "ainn-test-host", "-n", "solve problem A", "-P", "-F", "#{window_id}", "codex", "--profile", "cli-openai"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
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
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "ainn-test", "ainn-test-host", "new-window")

	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
				}
				if len(args) > 3 && args[3] == "new-session" {
					return "", errors.New("new-session failed")
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
workers:
  cli-openrouter:
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

func TestRenderCodexLaunchCommand(t *testing.T) {
	got := manager.BuildCodexLaunchCommand(manager.CodexLaunchOptions{Profile: "11199", WorkerPort: 11199})
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
