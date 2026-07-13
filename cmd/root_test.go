package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/hostedhooks"
	"github.com/jesse/agent-inn/internal/manager"
)

const tmuxMissingSocketErrorText = "error connecting to /private/tmp/ainn-tmux-repro/tmux-501/ainn-test (No such file or directory)"

func tmuxMainWindowThemeCommand(socketName string, hostSession string) []string {
	return manager.TmuxThemeCommandForSettings(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:    socketName,
				HostSession:   hostSession,
				HostStartMode: config.TmuxHostStartModeMainTUIWindow,
			},
		},
	})
}

func tmuxMainWindowThemeSocketCommand(socketPath string, hostSession string) []string {
	command := tmuxMainWindowThemeCommand(filepath.Base(socketPath), hostSession)
	command[1] = "-S"
	command[2] = socketPath
	return command
}

func TestMain(m *testing.M) {
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")
	previousSidecarStarter := hostedTurnWatcherSidecarStarter
	hostedTurnWatcherSidecarStarter = func(string) error { return nil }
	code := m.Run()
	hostedTurnWatcherSidecarStarter = previousSidecarStarter
	os.Exit(code)
}

func TestRunVersionPrintsVersion(t *testing.T) {
	var stdout bytes.Buffer
	code := Run([]string{"version"}, &stdout, &bytes.Buffer{})

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "ainn") {
		t.Fatalf("expected version output to name ainn, got %q", stdout.String())
	}
}

func TestRunUnknownCommandReturnsUsageError(t *testing.T) {
	var stderr bytes.Buffer
	code := Run([]string{"unknown"}, &bytes.Buffer{}, &stderr)

	if code == 0 {
		t.Fatal("expected non-zero exit code")
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("expected unknown command error, got %q", stderr.String())
	}
}

func TestRunHooksInstallStatusAndUninstall(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	wantInstalled := hostedhooks.StatusReport{
		ScriptInstalled: true,
		CodexInstalled:  true,
		ClaudeInstalled: true,
	}

	var installStdout bytes.Buffer
	var installStderr bytes.Buffer
	installCode := Run([]string{"hooks", "install"}, &installStdout, &installStderr)
	if installCode != 0 {
		t.Fatalf("expected install exit 0, got %d: %s", installCode, installStderr.String())
	}
	if installStdout.String() != "installed\n" {
		t.Fatalf("bad install stdout: %q", installStdout.String())
	}
	installed, err := hostedhooks.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(installed, wantInstalled) {
		t.Fatalf("bad installed status:\n got %#v\nwant %#v", installed, wantInstalled)
	}

	var statusStdout bytes.Buffer
	var statusStderr bytes.Buffer
	statusCode := Run([]string{"hooks", "status"}, &statusStdout, &statusStderr)
	if statusCode != 0 {
		t.Fatalf("expected status exit 0, got %d: %s", statusCode, statusStderr.String())
	}
	wantStatusStdout := "script: installed\ncodex: installed\nclaude: installed\n"
	if statusStdout.String() != wantStatusStdout {
		t.Fatalf("bad status stdout:\n got %q\nwant %q", statusStdout.String(), wantStatusStdout)
	}

	var uninstallStdout bytes.Buffer
	var uninstallStderr bytes.Buffer
	uninstallCode := Run([]string{"hooks", "uninstall"}, &uninstallStdout, &uninstallStderr)
	if uninstallCode != 0 {
		t.Fatalf("expected uninstall exit 0, got %d: %s", uninstallCode, uninstallStderr.String())
	}
	if uninstallStdout.String() != "uninstalled\n" {
		t.Fatalf("bad uninstall stdout: %q", uninstallStdout.String())
	}
	uninstalled, err := hostedhooks.Status()
	if err != nil {
		t.Fatal(err)
	}
	wantUninstalled := hostedhooks.StatusReport{}
	if !reflect.DeepEqual(uninstalled, wantUninstalled) {
		t.Fatalf("bad uninstalled status:\n got %#v\nwant %#v", uninstalled, wantUninstalled)
	}
}

func TestRunHooksRejectsUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer
	code := runHooks([]string{"bogus"}, &bytes.Buffer{}, &stderr)

	if code != 2 {
		t.Fatalf("expected exit code 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown hooks command") {
		t.Fatalf("expected unknown hooks command error, got %q", stderr.String())
	}
}

func TestRunHostedSessionMarkUpdatesRegistryAndTmux(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	path := filepath.Join(configDir, config.ConfigFileName)
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
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
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "mark", "--config-dir", configDir, "--session-id", session.SessionID, "--state", manager.HostedTurnStateRunning}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantSession := session
	wantSession.TurnState = manager.HostedTurnStateRunning
	wantSession.TurnGeneration = 1
	if !ok || !reflect.DeepEqual(updated, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantSession)
	}
	wantCalls := [][]string{manager.TmuxHostedTurnStatusCommandForSettings(cfg.Settings, "@12", manager.HostedTurnStateRunning)}
	if !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", got, wantCalls)
	}
}

func TestRunHostedSessionMarkTerminalStateAcknowledgesCurrentWindow(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	path := filepath.Join(configDir, config.ConfigFileName)
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnState(session.SessionID, manager.HostedTurnStateRunning, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if reflect.DeepEqual(args, manager.TmuxActiveWindowDetailsCommandForSettings(cfg.Settings)) {
					return "@12\tsolve problem A\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "mark", "--config-dir", configDir, "--session-id", session.SessionID, "--state", manager.HostedTurnStateDone}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantSession := running
	wantSession.TurnState = manager.HostedTurnStateDone
	wantSession.TurnAcknowledgedGeneration = running.TurnGeneration
	if !ok || !reflect.DeepEqual(updated, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantSession)
	}
	wantCalls := [][]string{
		manager.TmuxActiveWindowDetailsCommandForSettings(cfg.Settings),
		manager.TmuxHostedTurnStatusCommandForRecord(cfg.Settings, wantSession),
	}
	if !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", got, wantCalls)
	}
}

func TestRunHostedSessionMarkDoesNotWriteTmuxOutputToStdout(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	path := filepath.Join(configDir, config.ConfigFileName)
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.MarkTurnState(session.SessionID, manager.HostedTurnStateRunning, "", ""); err != nil {
		t.Fatal(err)
	}

	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				if reflect.DeepEqual(args, manager.TmuxActiveWindowDetailsCommandForSettings(cfg.Settings)) {
					activeWindow := "@12\tsolve problem A\n"
					if _, err := io.WriteString(stdout, activeWindow); err != nil {
						return "", err
					}
					return activeWindow, nil
				}
				if _, err := io.WriteString(stdout, "tmux status output\n"); err != nil {
					return "", err
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "mark", "--config-dir", configDir, "--session-id", session.SessionID, "--state", manager.HostedTurnStateDone}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("got stdout %q, want empty", stdout.String())
	}
}

func TestRunHostedSessionMarkRecordsCodexTurnWatch(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	path := filepath.Join(configDir, config.ConfigFileName)
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	restoreRunner := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restoreRunner()
	previousInput := hostedSessionMarkInput
	hostedSessionMarkInput = strings.NewReader(`{"session_id":"019f3872-e4d0-7252-b858-3f9284ae8b21","transcript_path":"/tmp/codex.jsonl","turn_id":"turn_1"}`)
	defer func() { hostedSessionMarkInput = previousInput }()

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "mark", "--config-dir", configDir, "--session-id", session.SessionID, "--state", manager.HostedTurnStateRunning, "--capture-launcher-session-id", "--watch-codex-turn"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantSession := session
	wantSession.TurnState = manager.HostedTurnStateRunning
	wantSession.TurnGeneration = 1
	wantSession.LauncherSessionID = "019f3872-e4d0-7252-b858-3f9284ae8b21"
	wantSession.TurnTranscriptPath = "/tmp/codex.jsonl"
	wantSession.TurnID = "turn_1"
	wantSession.TurnWatchKind = manager.HostedTurnWatchKindCodex
	if !ok || !reflect.DeepEqual(updated, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantSession)
	}
	wantCalls := [][]string{manager.TmuxHostedTurnStatusCommandForSettings(cfg.Settings, "@12", manager.HostedTurnStateRunning)}
	if !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", got, wantCalls)
	}
}

func TestRunHostedSessionMarkRecordsLauncherWatchWithoutCodexTurnMetadata(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	path := filepath.Join(configDir, config.ConfigFileName)
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	restoreRunner := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restoreRunner()
	previousInput := hostedSessionMarkInput
	hostedSessionMarkInput = strings.NewReader(`{"session_id":"019f3872-e4d0-7252-b858-3f9284ae8b21"}`)
	defer func() { hostedSessionMarkInput = previousInput }()

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "mark", "--config-dir", configDir, "--session-id", session.SessionID, "--state", manager.HostedTurnStateRunning, "--capture-launcher-session-id", "--watch-codex-turn"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantSession := session
	wantSession.TurnState = manager.HostedTurnStateRunning
	wantSession.TurnGeneration = 1
	wantSession.LauncherSessionID = "019f3872-e4d0-7252-b858-3f9284ae8b21"
	wantSession.TurnWatchKind = manager.HostedTurnWatchKindCodex
	if !ok || !reflect.DeepEqual(updated, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantSession)
	}
	wantCalls := [][]string{manager.TmuxHostedTurnStatusCommandForSettings(cfg.Settings, "@12", manager.HostedTurnStateRunning)}
	if !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", got, wantCalls)
	}
}

func TestRunHostedSessionMarkIgnoresSubagentHookInput(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	path := filepath.Join(configDir, config.ConfigFileName)
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnState(session.SessionID, manager.HostedTurnStateRunning, "", "019f3872-e4d0-7252-b858-3f9284ae8b21")
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	restoreRunner := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restoreRunner()
	previousInput := hostedSessionMarkInput
	hostedSessionMarkInput = strings.NewReader(`{"hook_event_name":"Stop","session_id":"019f3872-e4d0-7252-b858-3f9284ae8b21","agent_id":"agent-abc123","agent_type":"Explore","transcript_path":"/tmp/codex.jsonl","turn_id":"turn_subagent"}`)
	defer func() { hostedSessionMarkInput = previousInput }()

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "mark", "--config-dir", configDir, "--session-id", session.SessionID, "--state", manager.HostedTurnStateDone, "--capture-launcher-session-id"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !reflect.DeepEqual(updated, running) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, running)
	}
	var wantCalls [][]string
	if !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", got, wantCalls)
	}
}

func TestRunHostedSessionMarkIgnoresSubagentHookEvent(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	path := filepath.Join(configDir, config.ConfigFileName)
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnState(session.SessionID, manager.HostedTurnStateRunning, "", "019f3872-e4d0-7252-b858-3f9284ae8b21")
	if err != nil {
		t.Fatal(err)
	}

	previousInput := hostedSessionMarkInput
	hostedSessionMarkInput = strings.NewReader(`{"hook_event_name":"SubagentStop","session_id":"019f3872-e4d0-7252-b858-3f9284ae8b21"}`)
	defer func() { hostedSessionMarkInput = previousInput }()

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "mark", "--config-dir", configDir, "--session-id", session.SessionID, "--state", manager.HostedTurnStateDone, "--capture-launcher-session-id"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !reflect.DeepEqual(updated, running) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, running)
	}
}

func TestRunHostedSessionAcknowledgeUpdatesRegistryAndTmux(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	cfg, err := config.LoadFile(filepath.Join(configDir, config.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := registry.MarkTurnState(session.SessionID, manager.HostedTurnStateRunning, "", "")
	if err != nil {
		t.Fatal(err)
	}
	done, err := registry.MarkTurnState(session.SessionID, manager.HostedTurnStateDone, "", "")
	if err != nil {
		t.Fatal(err)
	}

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
	code := Run([]string{"hosted-session", "acknowledge", "--config-dir", configDir, "--window-id", "@12"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantSession := done
	wantSession.TurnAcknowledgedGeneration = running.TurnGeneration
	if !ok || !reflect.DeepEqual(updated, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantSession)
	}
	wantCalls := [][]string{manager.TmuxHostedTurnStatusCommandForRecord(cfg.Settings, wantSession)}
	if !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", got, wantCalls)
	}
}

func TestRunHostedSessionToggleTodoUpdatesRegistryAndTmux(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	cfg, err := config.LoadFile(filepath.Join(configDir, config.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel:               "solve problem A",
		WorkerName:                 "worker",
		WorkerPort:                 11199,
		TmuxWindowID:               "@12",
		TurnState:                  manager.HostedTurnStateDone,
		TurnGeneration:             2,
		TurnAcknowledgedGeneration: 2,
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
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "toggle-todo", "--config-dir", configDir, "--window-id", "@12", "--window-name", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantSession := session
	wantSession.UserMarker = manager.HostedUserMarkerTodo
	if !ok || !reflect.DeepEqual(updated, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantSession)
	}
	wantCalls := [][]string{manager.TmuxHostedTurnStatusCommandForRecord(cfg.Settings, wantSession)}
	if !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", got, wantCalls)
	}
}

func TestRunHostedSessionWatchAllExitsWhenSidecarAlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	holdLockForTest(t)

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "watch-all", "--config-dir", configDir}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if stderr.String() != "" {
		t.Fatalf("got stderr %q, want empty", stderr.String())
	}
}

func TestRunHostedSessionWatchAllExitsWhenTmuxHostMissing(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)

	previousLockerFactory := rootLockerFactory
	rootLockerFactory = func(string) rootLocker { return noopLocker{} }
	defer func() { rootLockerFactory = previousLockerFactory }()

	previousInterval := hostedTurnWatcherSidecarPollInterval
	hostedTurnWatcherSidecarPollInterval = time.Hour
	defer func() { hostedTurnWatcherSidecarPollInterval = previousInterval }()

	previousRunnerFactory := launchRunnerFactory
	launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
		return launchRunnerFunc(func(args []string) (string, error) {
			if len(args) > 3 && args[3] == "has-session" {
				return "", errors.New("can't find session")
			}
			return "", nil
		})
	}
	defer func() { launchRunnerFactory = previousRunnerFactory }()

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "watch-all", "--config-dir", configDir}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if stderr.String() != "" {
		t.Fatalf("got stderr %q, want empty", stderr.String())
	}
}

func TestHostedSessionLatestTurnStatusCommandUsesLatestTodoAfterConcurrentToggle(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	cfg, err := config.LoadFile(filepath.Join(configDir, config.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel:               "solve problem A",
		WorkerName:                 "worker",
		WorkerPort:                 11199,
		TmuxWindowID:               "@12",
		TurnState:                  manager.HostedTurnStateDone,
		TurnGeneration:             2,
		TurnAcknowledgedGeneration: 0,
	})
	if err != nil {
		t.Fatal(err)
	}

	acknowledged, ok, err := registry.AcknowledgeTurnByWindow("@12", "solve problem A")
	if err != nil || !ok {
		t.Fatalf("acknowledge got ok=%v err=%v", ok, err)
	}
	if _, ok, err := registry.ToggleUserMarkerByWindow("@12", "solve problem A"); err != nil || !ok {
		t.Fatalf("toggle todo got ok=%v err=%v", ok, err)
	}
	wantSession := session
	wantSession.TurnAcknowledgedGeneration = session.TurnGeneration
	wantSession.UserMarker = manager.HostedUserMarkerTodo
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !reflect.DeepEqual(updated, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantSession)
	}
	got, ok, err := hostedSessionLatestTurnStatusCommand(cfg.Settings, registry, acknowledged)
	if err != nil || !ok {
		t.Fatalf("latest command got ok=%v err=%v", ok, err)
	}
	want := manager.TmuxHostedTurnStatusCommandForRecord(cfg.Settings, wantSession)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got tmux command %#v, want %#v", got, want)
	}
}

func TestHostedSessionLatestTurnStatusCommandUsesLatestReadAfterConcurrentAcknowledge(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	cfg, err := config.LoadFile(filepath.Join(configDir, config.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel:               "solve problem A",
		WorkerName:                 "worker",
		WorkerPort:                 11199,
		TmuxWindowID:               "@12",
		TurnState:                  manager.HostedTurnStateDone,
		TurnGeneration:             2,
		TurnAcknowledgedGeneration: 0,
	})
	if err != nil {
		t.Fatal(err)
	}

	todo, ok, err := registry.ToggleUserMarkerByWindow("@12", "solve problem A")
	if err != nil || !ok {
		t.Fatalf("toggle todo got ok=%v err=%v", ok, err)
	}
	if _, ok, err := registry.AcknowledgeTurnByWindow("@12", "solve problem A"); err != nil || !ok {
		t.Fatalf("acknowledge got ok=%v err=%v", ok, err)
	}
	wantSession := session
	wantSession.TurnAcknowledgedGeneration = session.TurnGeneration
	wantSession.UserMarker = manager.HostedUserMarkerTodo
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !reflect.DeepEqual(updated, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantSession)
	}
	got, ok, err := hostedSessionLatestTurnStatusCommand(cfg.Settings, registry, todo)
	if err != nil || !ok {
		t.Fatalf("latest command got ok=%v err=%v", ok, err)
	}
	want := manager.TmuxHostedTurnStatusCommandForRecord(cfg.Settings, wantSession)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got tmux command %#v, want %#v", got, want)
	}
}

func TestRunHostedSessionMarkPersistsLauncherSessionID(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	path := filepath.Join(configDir, config.ConfigFileName)
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "mark", "--config-dir", configDir, "--session-id", session.SessionID, "--state", manager.HostedTurnStateRunning, "--launcher-session-id", "019e7c18-0ee7-7ff2-bc82-9c410511ede3"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantSession := session
	wantSession.TurnState = manager.HostedTurnStateRunning
	wantSession.TurnGeneration = 1
	wantSession.LauncherSessionID = "019e7c18-0ee7-7ff2-bc82-9c410511ede3"
	if !ok || !reflect.DeepEqual(updated, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantSession)
	}
}

func TestRunHostedSessionMarkCapturesLauncherSessionIDFromHookInput(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	path := filepath.Join(configDir, config.ConfigFileName)
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	restoreRunner := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restoreRunner()
	previousInput := hostedSessionMarkInput
	hostedSessionMarkInput = strings.NewReader(`{"session_id":"019e7c18-0ee7-7ff2-bc82-9c410511ede3"}`)
	defer func() { hostedSessionMarkInput = previousInput }()

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "mark", "--config-dir", configDir, "--session-id", session.SessionID, "--state", manager.HostedTurnStateRunning, "--capture-launcher-session-id"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantSession := session
	wantSession.TurnState = manager.HostedTurnStateRunning
	wantSession.TurnGeneration = 1
	wantSession.LauncherSessionID = "019e7c18-0ee7-7ff2-bc82-9c410511ede3"
	if !ok || !reflect.DeepEqual(updated, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantSession)
	}
}

func TestRunHostedSessionMarkCaptureKeepsTurnStateWhenHookInputIsEmpty(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	path := filepath.Join(configDir, config.ConfigFileName)
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel:      "solve problem A",
		WorkerName:        "worker",
		WorkerPort:        11199,
		TmuxWindowID:      "@12",
		LauncherSessionID: "019e7c18-0ee7-7ff2-bc82-9c410511ede3",
	})
	if err != nil {
		t.Fatal(err)
	}

	restoreRunner := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restoreRunner()
	previousInput := hostedSessionMarkInput
	hostedSessionMarkInput = strings.NewReader("")
	defer func() { hostedSessionMarkInput = previousInput }()

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "mark", "--config-dir", configDir, "--session-id", session.SessionID, "--state", manager.HostedTurnStateDone, "--capture-launcher-session-id"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	updated, ok, err := registry.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantSession := session
	wantSession.TurnState = manager.HostedTurnStateDone
	if !ok || !reflect.DeepEqual(updated, wantSession) {
		t.Fatalf("got %#v ok=%v, want %#v", updated, ok, wantSession)
	}
}

func TestRunHostedSessionPopupOpenDisplaysHostedPopup(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "real-config")
	stateDir := filepath.Join(configDir, "state")
	linkDir := filepath.Join(dir, "linked-config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	registryPath := manager.HostedSessionRegistryPath(stateDir)
	if err := os.WriteFile(registryPath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(configDir, linkDir); err != nil {
		t.Fatal(err)
	}
	resolvedConfigDir, err := filepath.EvalSymlinks(configDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(filepath.Join(resolvedConfigDir, config.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}

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
	code := Run([]string{"hosted-session", "popup-open", "--config-dir", linkDir, "--manager-url", "http://127.0.0.1:19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxDisplayHostedPopupCommandForSettings(cfg.Settings, resolvedConfigDir, "http://127.0.0.1:19090", hostedSessionExecutable()),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got tmux calls %#v, want %#v", got, want)
	}
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{" {
		t.Fatalf("popup-open should not write hosted session registry, got %q", string(data))
	}
}

func TestRunHostedSessionPopupOpenUsesEnvManagerURLThenDefault(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{name: "env", env: "http://127.0.0.1:19091", want: "http://127.0.0.1:19091"},
		{name: "default", want: defaultManagerURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			configDir := filepath.Join(dir, "config")
			writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
			t.Setenv("AINN_URL", tc.env)
			resolvedConfigDir, err := filepath.EvalSymlinks(configDir)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadFile(filepath.Join(configDir, config.ConfigFileName))
			if err != nil {
				t.Fatal(err)
			}

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
			code := Run([]string{"hosted-session", "popup-open", "--config-dir", configDir}, &bytes.Buffer{}, &stderr)
			if code != 0 {
				t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
			}
			want := [][]string{
				manager.TmuxDetectCommand(),
				manager.TmuxDisplayHostedPopupCommandForSettings(cfg.Settings, resolvedConfigDir, tc.want, hostedSessionExecutable()),
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got tmux calls %#v, want %#v", got, want)
			}
		})
	}
}

func TestRunHostedSessionPopupStartsTUIProgramWithPopupEnv(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	resolvedConfigDir, err := filepath.EvalSymlinks(configDir)
	if err != nil {
		t.Fatal(err)
	}
	capturePath := filepath.Join(dir, "popup-env.txt")
	bunPath := filepath.Join(dir, "bun")
	script := `#!/bin/sh
{
printf 'cwd=%s\n' "$(pwd)"
printf 'argv=%s\n' "$*"
printf 'AINN_URL=%s\n' "$AINN_URL"
printf 'AINN_CONFIG_DIR=%s\n' "$AINN_CONFIG_DIR"
printf 'AINN_EXECUTABLE=%s\n' "$AINN_EXECUTABLE"
printf 'AINN_PROJECT_DIR=%s\n' "$AINN_PROJECT_DIR"
printf 'AINN_FAST_BOOT=%s\n' "$AINN_FAST_BOOT"
printf 'AINN_HOSTED_TERMINAL_POPUP=%s\n' "$AINN_HOSTED_TERMINAL_POPUP"
} > "$AINN_POPUP_CAPTURE"
`
	if err := os.WriteFile(bunPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUN_PATH", bunPath)
	t.Setenv("AINN_POPUP_CAPTURE", capturePath)
	t.Chdir("..")

	var stderr bytes.Buffer
	code := Run([]string{"hosted-session", "popup", "--config-dir", configDir, "--manager-url", "http://127.0.0.1:19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	exe := hostedSessionExecutable()
	if exe == "" {
		t.Fatal("expected hosted popup executable env value")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want := "cwd=" + filepath.Join(wd, "tui") + "\n" +
		"argv=run src/cli.ts\n" +
		"AINN_URL=http://127.0.0.1:19090\n" +
		"AINN_CONFIG_DIR=" + resolvedConfigDir + "\n" +
		"AINN_EXECUTABLE=" + exe + "\n" +
		"AINN_PROJECT_DIR=" + wd + "\n" +
		"AINN_FAST_BOOT=1\n" +
		"AINN_HOSTED_TERMINAL_POPUP=1\n"
	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("got popup env:\n%s\nwant:\n%s", string(data), want)
	}
}

func TestRunDefaultStartsRootRunnerWithConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
workers:
  app:
    port: 6767
    provider: openai
providers:
  openai:
    base_url: https://api.openai.com/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	var called bool
	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		called = true
		if opts.ConfigPath != configPath {
			t.Fatalf("unexpected config path %s", opts.ConfigPath)
		}
		if opts.ConfigDir != dir {
			t.Fatalf("unexpected config dir %s", opts.ConfigDir)
		}
		if len(opts.Config.Workers) != 1 {
			t.Fatalf("config was not loaded: %#v", opts.Config)
		}
		return nil
	})
	defer restore()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if !called {
		t.Fatal("root runner was not called")
	}
}

func TestRunDefaultLogsRootStartupFailure(t *testing.T) {
	configDir := t.TempDir()
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	startErr := errors.New("preload not found \"@opentui/solid/preload\"")

	restoreRunner := SetRootRunnerForTest(func(opts RootOptions) error {
		return startErr
	})
	defer restoreRunner()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", configDir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("expected startup failure")
	}
	if !strings.Contains(stderr.String(), "failed to start: "+startErr.Error()) {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}

	logPath := filepath.Join(configDir, "logs", "ainn.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "failed to start: " + startErr.Error() + "\n"
	if string(data) != want {
		t.Fatalf("got log %q, want %q", string(data), want)
	}
}

func TestRunDefaultNormalizesConfigDirForRootRunner(t *testing.T) {
	workDir := t.TempDir()
	configDir := filepath.Join(workDir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	t.Chdir(workDir)

	type observedRootOptions struct {
		ConfigDir   string
		ConfigPath  string
		ManagerPort int
	}
	var got observedRootOptions
	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		got = observedRootOptions{
			ConfigDir:   opts.ConfigDir,
			ConfigPath:  opts.ConfigPath,
			ManagerPort: opts.ManagerPort,
		}
		return nil
	})
	defer restore()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", "./config/..//config", "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	want := observedRootOptions{
		ConfigDir:   configDir,
		ConfigPath:  filepath.Join(configDir, "config.yaml"),
		ManagerPort: 19090,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected root options: got %#v want %#v", got, want)
	}
}

func TestRunDefaultExpandsHomeConfigDirForRootRunner(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	configDir := filepath.Join(homeDir, "ainn-config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)

	type observedRootOptions struct {
		ConfigDir  string
		ConfigPath string
	}
	var got observedRootOptions
	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		got = observedRootOptions{ConfigDir: opts.ConfigDir, ConfigPath: opts.ConfigPath}
		return nil
	})
	defer restore()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", "~/ainn-config", "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	want := observedRootOptions{
		ConfigDir:  configDir,
		ConfigPath: filepath.Join(configDir, "config.yaml"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected root options: got %#v want %#v", got, want)
	}
}

func TestRunDefaultNormalizesSymlinkConfigDirForRootRunnerAndLock(t *testing.T) {
	workDir := t.TempDir()
	targetDir := filepath.Join(workDir, "target")
	linkDir := filepath.Join(workDir, "link")
	writeRootConfig(t, targetDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)
	if err := os.Symlink(targetDir, linkDir); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workDir)

	type observedRootOptions struct {
		ConfigDir  string
		ConfigPath string
	}
	var got observedRootOptions
	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		got = observedRootOptions{ConfigDir: opts.ConfigDir, ConfigPath: opts.ConfigPath}
		return nil
	})
	defer restore()
	var gotLockPath string
	previousLockerFactory := rootLockerFactory
	rootLockerFactory = func(lockPath string) rootLocker {
		gotLockPath = lockPath
		return noopLocker{}
	}
	defer func() { rootLockerFactory = previousLockerFactory }()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", "./link", "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	want := observedRootOptions{
		ConfigDir:  linkDir,
		ConfigPath: filepath.Join(linkDir, "config.yaml"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected root options: got %#v want %#v", got, want)
	}
	wantLockPath, err := rootLockPath(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	if gotLockPath != wantLockPath {
		t.Fatalf("unexpected lock path: got %q want %q", gotLockPath, wantLockPath)
	}
}

func TestRunDefaultRejectsLegacyConfigFlag(t *testing.T) {
	var stderr bytes.Buffer
	code := Run([]string{"--config", "config.yaml"}, &bytes.Buffer{}, &stderr)

	if code == 0 {
		t.Fatal("expected legacy --config to fail")
	}
	if !strings.Contains(stderr.String(), "--config") {
		t.Fatalf("expected --config flag error, got %q", stderr.String())
	}
}

func TestRunDefaultContinuesWhenConfiguredWorkerWillFailToStart(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
workers:
  bad:
    port: 6767
    provider: missing
providers:
  openai:
    base_url: https://api.openai.com/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	var called bool
	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		called = true
		if len(opts.Config.Workers) != 1 {
			t.Fatalf("config was not loaded: %#v", opts.Config)
		}
		return nil
	})
	defer restore()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0 despite failed worker config, got %d: %s", code, stderr.String())
	}
	if !called {
		t.Fatal("root runner was not called")
	}
}

func TestRunRootMainTUIWindowCreatesHostAndAttaches(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
				}
				if len(args) > 3 && args[3] == "new-session" {
					return "0\n", nil
				}
				if len(args) > 3 && args[3] == "list-panes" {
					return "env " + tmuxRootChildEnvVar + "=1 " + exe + " --config-dir " + dir + " --manager-port 19090\n", nil
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "ainn", "-P", "-F", "#{window_index}", "env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", dir, "--manager-port", "19090"},
		tmuxResetMainWindowStatusCommand("ainn-test", "ainn-test-host"),
		tmuxMainWindowThemeCommand("ainn-test", "ainn-test-host"),
		tmuxExtendedKeysCommand("ainn-test"),
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:0"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunRootMainTUIWindowResetsInheritedTurnStatus(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "has-session" {
					return "ok\n", nil
				}
				if len(args) > 3 && args[3] == "list-panes" {
					return "env " + tmuxRootChildEnvVar + "=1 " + exe + " --config-dir " + dir + " --manager-port 19090\n", nil
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	wantReset := tmuxResetMainWindowStatusCommand("ainn-test", "ainn-test-host")
	found := false
	for _, call := range got {
		if reflect.DeepEqual(call, wantReset) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing main window status reset command in %#v", got)
	}
}

func TestRunRootMainTUIWindowChildCommandUsesNormalizedConfigDir(t *testing.T) {
	workDir := t.TempDir()
	dir := filepath.Join(workDir, "config")
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")
	t.Chdir(workDir)

	var got [][]string
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
				}
				if len(args) > 3 && args[3] == "new-session" {
					return "0\n", nil
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", "./config/..//config", "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "ainn", "-P", "-F", "#{window_index}", "env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", dir, "--manager-port", "19090"},
		tmuxResetMainWindowStatusCommand("ainn-test", "ainn-test-host"),
		tmuxMainWindowThemeCommand("ainn-test", "ainn-test-host"),
		tmuxExtendedKeysCommand("ainn-test"),
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:0"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunRootMainTUIWindowCreatesHostWhenTmuxSocketMissing(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New(tmuxMissingSocketErrorText)
				}
				if len(args) > 3 && args[3] == "new-session" {
					return "0\n", nil
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "ainn", "-P", "-F", "#{window_index}", "env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", dir, "--manager-port", "19090"},
		tmuxResetMainWindowStatusCommand("ainn-test", "ainn-test-host"),
		tmuxMainWindowThemeCommand("ainn-test", "ainn-test-host"),
		tmuxExtendedKeysCommand("ainn-test"),
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:0"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunRootMainTUIWindowMovesFreshHostWindowToZeroWhenBaseIndexDiffers(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
				}
				if len(args) > 3 && args[3] == "new-session" {
					return "1\n", nil
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "ainn", "-P", "-F", "#{window_index}", "env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", dir, "--manager-port", "19090"},
		{"tmux", "-L", "ainn-test", "move-window", "-s", "ainn-test-host:1", "-t", "ainn-test-host:0"},
		tmuxResetMainWindowStatusCommand("ainn-test", "ainn-test-host"),
		tmuxMainWindowThemeCommand("ainn-test", "ainn-test-host"),
		tmuxExtendedKeysCommand("ainn-test"),
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:0"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunRootMainTUIWindowDoesNotUseNamePrefixAsWindowZero(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "has-session" {
					return "ok\n", nil
				}
				if len(args) > 3 && args[3] == "list-panes" {
					return "5\tenv " + tmuxRootChildEnvVar + "=1 " + exe + " --config-dir " + dir + " --manager-port 19090\n", nil
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	for _, call := range got {
		if len(call) > 3 && call[3] == "respawn-pane" {
			t.Fatalf("respawned a name-prefix match instead of creating window 0: %#v", got)
		}
	}
	created := false
	for _, call := range got {
		if len(call) > 3 && call[3] == "new-window" {
			created = true
			break
		}
	}
	if !created {
		t.Fatalf("expected a new main window after resolving actual index 5: %#v", got)
	}
}

func TestRunRootMainTUIWindowOutsideTmuxSelectsWindowZeroThenAttaches(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "list-panes" {
					return "env " + tmuxRootChildEnvVar + "=1 " + exe + " --config-dir " + dir + " --manager-port 19090\n", nil
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run when tmux host exists: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "list-panes", "-t", "ainn-test-host:0", "-F", "#{window_index}\t#{pane_start_command}"},
		tmuxResetMainWindowStatusCommand("ainn-test", "ainn-test-host"),
		tmuxMainWindowThemeCommand("ainn-test", "ainn-test-host"),
		tmuxExtendedKeysCommand("ainn-test"),
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:0"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunRootMainTUIWindowSwitchesClientInsideTmux(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")
	t.Setenv("TMUX", "/tmp/tmux-1000/ainn-test,123,0")
	t.Setenv("TMUX_PANE", "%2")

	var got [][]string
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "list-panes" {
					return "env " + tmuxRootChildEnvVar + "=1 " + exe + " --config-dir " + dir + " --manager-port 19090\n", nil
				}
				if len(args) > 3 && args[3] == "list-clients" {
					return "client-1\t%1\nclient-2\t%2\n", nil
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run when tmux host exists: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-S", "/tmp/tmux-1000/ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-S", "/tmp/tmux-1000/ainn-test", "list-panes", "-t", "ainn-test-host:0", "-F", "#{window_index}\t#{pane_start_command}"},
		tmuxResetMainWindowStatusSocketCommand("/tmp/tmux-1000/ainn-test", "ainn-test-host"),
		tmuxMainWindowThemeSocketCommand("/tmp/tmux-1000/ainn-test", "ainn-test-host"),
		tmuxExtendedKeysSocketCommand("/tmp/tmux-1000/ainn-test"),
		{"tmux", "-S", "/tmp/tmux-1000/ainn-test", "list-clients", "-F", "#{client_name}\t#{pane_id}"},
		{"tmux", "-S", "/tmp/tmux-1000/ainn-test", "switch-client", "-c", "client-2", "-t", "ainn-test-host:0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunRootMainTUIWindowUsesCurrentSocketPathInsideTmux(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")
	t.Setenv("TMUX", "/tmp/custom/ainn-test,123,0")
	t.Setenv("TMUX_PANE", "%2")

	var got [][]string
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "list-panes" {
					return "env " + tmuxRootChildEnvVar + "=1 " + exe + " --config-dir " + dir + " --manager-port 9090\n", nil
				}
				if len(args) > 3 && args[3] == "list-clients" {
					return "client-1\t%1\nclient-2\t%2\n", nil
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run when tmux host exists: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-S", "/tmp/custom/ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-S", "/tmp/custom/ainn-test", "list-panes", "-t", "ainn-test-host:0", "-F", "#{window_index}\t#{pane_start_command}"},
		tmuxResetMainWindowStatusSocketCommand("/tmp/custom/ainn-test", "ainn-test-host"),
		tmuxMainWindowThemeSocketCommand("/tmp/custom/ainn-test", "ainn-test-host"),
		tmuxExtendedKeysSocketCommand("/tmp/custom/ainn-test"),
		{"tmux", "-S", "/tmp/custom/ainn-test", "list-clients", "-F", "#{client_name}\t#{pane_id}"},
		{"tmux", "-S", "/tmp/custom/ainn-test", "switch-client", "-c", "client-2", "-t", "ainn-test-host:0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunRootMainTUIWindowFailsWhenInsideTmuxClientPaneIsMissing(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")
	t.Setenv("TMUX", "/tmp/tmux-1000/ainn-test,123,0")
	t.Setenv("TMUX_PANE", "%3")

	var got [][]string
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "list-panes" {
					return "env " + tmuxRootChildEnvVar + "=1 " + exe + " --config-dir " + dir + " --manager-port 19090\n", nil
				}
				if len(args) > 3 && args[3] == "list-clients" {
					return "client-1\t%1\nclient-2\t%2\n", nil
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run when tmux host exists: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit when tmux client pane is missing")
	}
	if !strings.Contains(stderr.String(), "failed to identify tmux client") {
		t.Fatalf("expected client lookup failure in stderr, got %q", stderr.String())
	}

	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-S", "/tmp/tmux-1000/ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-S", "/tmp/tmux-1000/ainn-test", "list-panes", "-t", "ainn-test-host:0", "-F", "#{window_index}\t#{pane_start_command}"},
		tmuxResetMainWindowStatusSocketCommand("/tmp/tmux-1000/ainn-test", "ainn-test-host"),
		tmuxMainWindowThemeSocketCommand("/tmp/tmux-1000/ainn-test", "ainn-test-host"),
		tmuxExtendedKeysSocketCommand("/tmp/tmux-1000/ainn-test"),
		{"tmux", "-S", "/tmp/tmux-1000/ainn-test", "list-clients", "-F", "#{client_name}\t#{pane_id}"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunRootMainTUIWindowRejectsInsideTmuxDifferentSocket(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")
	t.Setenv("TMUX", "/tmp/tmux-1000/user-default,123,0")
	t.Setenv("TMUX_PANE", "%1")

	var got [][]string
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "list-panes" {
					return "env " + tmuxRootChildEnvVar + "=1 " + exe + " --config-dir " + dir + " --manager-port 9090\n", nil
				}
				if len(args) > 3 && args[3] == "switch-client" {
					t.Fatalf("switch-client should not run for a client on a different tmux socket: %#v", args)
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run when tmux host exists: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for mismatched tmux socket, got 0")
	}
	for _, want := range []string{"unsupported tmux startup state", "user-default", "ainn-test"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected stderr to contain %q, got %q", want, stderr.String())
		}
	}

	want := [][]string{
		{"tmux", "-V"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunRootMainTUIWindowRejectsInsideTmuxDifferentSocketBeforeCreatingMissingHost(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")
	t.Setenv("TMUX", "/tmp/tmux-1000/user-default,123,0")
	t.Setenv("TMUX_PANE", "%1")

	var got [][]string
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
				}
				if len(args) > 3 && args[3] == "new-session" {
					return "0\n", nil
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for mismatched tmux socket, got 0")
	}
	for _, want := range []string{"unsupported tmux startup state", "user-default", "ainn-test"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected stderr to contain %q, got %q", want, stderr.String())
		}
	}
	mutatingCommands := map[string]bool{
		"new-session":  true,
		"new-window":   true,
		"move-window":  true,
		"respawn-pane": true,
	}
	for _, call := range got {
		if len(call) > 3 && mutatingCommands[call[3]] {
			t.Fatalf("cross-socket validation should run before mutating tmux commands, got %#v", got)
		}
	}
}

func TestRunRootMainTUIWindowRecreatesMissingMainWindowOnExistingHost(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "list-panes" {
					return "", errors.New("can't find window")
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "list-panes", "-t", "ainn-test-host:0", "-F", "#{window_index}\t#{pane_start_command}"},
		{"tmux", "-L", "ainn-test", "new-window", "-t", "ainn-test-host:0", "-n", "ainn", "-P", "-F", "#{window_id}", "env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", dir, "--manager-port", "19090"},
		tmuxResetMainWindowStatusCommand("ainn-test", "ainn-test-host"),
		tmuxMainWindowThemeCommand("ainn-test", "ainn-test-host"),
		tmuxExtendedKeysCommand("ainn-test"),
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:0"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunRootMainTUIWindowRespawnsWindowZeroWhenPaneCommandDiffers(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "list-panes" {
					return "/usr/bin/old-worker\n", nil
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run when tmux host exists: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "list-panes", "-t", "ainn-test-host:0", "-F", "#{window_index}\t#{pane_start_command}"},
		{"tmux", "-L", "ainn-test", "respawn-pane", "-k", "-t", "ainn-test-host:0", "env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", dir, "--manager-port", "19090"},
		tmuxResetMainWindowStatusCommand("ainn-test", "ainn-test-host"),
		tmuxMainWindowThemeCommand("ainn-test", "ainn-test-host"),
		tmuxExtendedKeysCommand("ainn-test"),
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:0"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunRootMainTUIWindowAbortsWhenMainWindowInspectFailsWithoutMissingWindow(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	callsPath := filepath.Join(t.TempDir(), "tmux-calls.log")
	installFakeTmuxOnPath(t)
	t.Setenv("FAKE_TMUX_CALLS_FILE", callsPath)
	t.Setenv("FAKE_TMUX_HAS_SESSION", "1")
	t.Setenv("FAKE_TMUX_FAIL_COMMAND", "list-panes")
	t.Setenv("FAKE_TMUX_LIST_PANES_STDERR", "permission denied\n")

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "failed to inspect main tmux window") {
		t.Fatalf("expected inspect failure in stderr, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "permission denied") {
		t.Fatalf("expected list-panes stderr in failure output, got %q", stderr.String())
	}

	got, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "-V\n-L ainn-test has-session -t ainn-test-host\n-L ainn-test list-panes -t ainn-test-host:0 -F #{window_index}\t#{pane_start_command}\n"
	if string(got) != want {
		t.Fatalf("expected bootstrap to stop after list-panes, got %q want %q", string(got), want)
	}
}

func TestRunRootMainTUIWindowAbortsWhenHasSessionFailsUnexpectedly(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	callsPath := filepath.Join(t.TempDir(), "tmux-calls.log")
	installFakeTmuxOnPath(t)
	t.Setenv("FAKE_TMUX_CALLS_FILE", callsPath)
	t.Setenv("FAKE_TMUX_HAS_SESSION_STDERR", "permission denied\n")

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Errorf("expected non-zero exit, got 0")
	}
	if !strings.Contains(stderr.String(), "failed to inspect tmux host session") {
		t.Errorf("expected has-session step failure in stderr, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "permission denied") {
		t.Errorf("expected has-session stderr in failure output, got %q", stderr.String())
	}

	got, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "-V\n-L ainn-test has-session -t ainn-test-host\n"
	if string(got) != want {
		t.Fatalf("expected bootstrap to stop after has-session, got %q want %q", string(got), want)
	}
}

func TestRunRootMainTUIWindowChildRunsRootRunnerDirectly(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")
	t.Setenv(tmuxRootChildEnvVar, "1")

	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				t.Fatalf("tmux runner should not be used in child root: %#v", args)
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	var called bool
	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		called = true
		if opts.ConfigDir != dir {
			t.Fatalf("unexpected config dir %s", opts.ConfigDir)
		}
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if !called {
		t.Fatal("expected root runner to run in tmux child process")
	}
}

func TestRunRootMainTUIWindowWritesJSONLTracePerTmuxCommand(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	tracePath := filepath.Join(t.TempDir(), "tmux-trace.jsonl")
	installFakeTmuxOnPath(t)
	t.Setenv("AINN_TMUX_DEBUG_LOG", tracePath)
	t.Setenv("FAKE_TMUX_HAS_SESSION", "0")
	t.Setenv("FAKE_TMUX_HAS_SESSION_STDERR", "can't find session\n")
	t.Setenv("FAKE_TMUX_NEW_SESSION_STDOUT", "0\n")
	t.Setenv("FAKE_TMUX_ATTACH_STDOUT", "attached\n")

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got := readTmuxTraceViews(t, tracePath)
	want := []tmuxTraceView{
		{Argv: []string{"tmux", "-V"}, Stdout: "tmux 3.6b\n", Stderr: "", Err: "", HasDuration: true},
		{Argv: []string{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"}, Stdout: "", Stderr: "can't find session\n", Err: "exit status 1", HasDuration: true},
		{Argv: []string{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "ainn", "-P", "-F", "#{window_index}", "env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", dir, "--manager-port", "19090"}, Stdout: "0\n", Stderr: "", Err: "", HasDuration: true},
		{Argv: tmuxResetMainWindowStatusCommand("ainn-test", "ainn-test-host"), Stdout: "", Stderr: "", Err: "", HasDuration: true},
		{Argv: tmuxMainWindowThemeCommand("ainn-test", "ainn-test-host"), Stdout: "", Stderr: "", Err: "", HasDuration: true},
		{Argv: tmuxExtendedKeysCommand("ainn-test"), Stdout: "", Stderr: "", Err: "", HasDuration: true},
		{Argv: []string{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:0"}, Stdout: "", Stderr: "", Err: "", HasDuration: true},
		{Argv: []string{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"}, Stdout: "", Stderr: "", Err: "", HasDuration: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRunRootMainTUIWindowLogsFailedCommandBeforeReturningError(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	tracePath := filepath.Join(t.TempDir(), "tmux-trace.jsonl")
	installFakeTmuxOnPath(t)
	t.Setenv("AINN_TMUX_DEBUG_LOG", tracePath)
	t.Setenv("FAKE_TMUX_HAS_SESSION", "1")
	t.Setenv("FAKE_TMUX_ATTACH_STDERR", "attach failed\n")
	t.Setenv("FAKE_TMUX_FAIL_COMMAND", "attach-session")

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0: %s", stderr.String())
	}

	got := readTmuxTraceViews(t, tracePath)
	want := []tmuxTraceView{
		{Argv: []string{"tmux", "-V"}, Stdout: "tmux 3.6b\n", Stderr: "", Err: "", HasDuration: true},
		{Argv: []string{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"}, Stdout: "", Stderr: "", Err: "", HasDuration: true},
		{Argv: []string{"tmux", "-L", "ainn-test", "list-panes", "-t", "ainn-test-host:0", "-F", "#{window_index}\t#{pane_start_command}"}, Stdout: "env AINN_TMUX_ROOT_CHILD=1 " + fakeTmuxCurrentExecutable(t) + "\n", Stderr: "", Err: "", HasDuration: true},
		{Argv: tmuxResetMainWindowStatusCommand("ainn-test", "ainn-test-host"), Stdout: "", Stderr: "", Err: "", HasDuration: true},
		{Argv: tmuxMainWindowThemeCommand("ainn-test", "ainn-test-host"), Stdout: "", Stderr: "", Err: "", HasDuration: true},
		{Argv: tmuxExtendedKeysCommand("ainn-test"), Stdout: "", Stderr: "", Err: "", HasDuration: true},
		{Argv: []string{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:0"}, Stdout: "", Stderr: "", Err: "", HasDuration: true},
		{Argv: []string{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"}, Stdout: "", Stderr: "", Err: "exit status 1", HasDuration: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRootTmuxRunnerStreamsAttachSessionToTerminalWriters(t *testing.T) {
	installFakeTmuxOnPath(t)
	t.Setenv(tmuxDebugEnvVar, "")
	t.Setenv(tmuxDebugLogEnvVar, "")
	t.Setenv("FAKE_TMUX_ATTACH_STDOUT", "attached\n")
	t.Setenv("FAKE_TMUX_ATTACH_STDERR", "attach stderr\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	got, err := rootTmuxRunnerFactory(&stdout, &stderr).Run([]string{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"})
	if err != nil {
		t.Fatalf("expected attach-session success, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected attach-session output not to be buffered, got %q", got)
	}
	if stdout.String() != "attached\n" {
		t.Fatalf("expected attach-session stdout to stream to terminal writer, got %q", stdout.String())
	}
	if stderr.String() != "attach stderr\n" {
		t.Fatalf("expected attach-session stderr to stream to terminal writer, got %q", stderr.String())
	}
}

func TestRootTmuxRunnerBuffersNewSessionWhenWindowNameIsAttachSession(t *testing.T) {
	installFakeTmuxOnPath(t)
	t.Setenv(tmuxDebugEnvVar, "")
	t.Setenv(tmuxDebugLogEnvVar, "")
	t.Setenv("FAKE_TMUX_NEW_SESSION_STDOUT", "0\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	got, err := rootTmuxRunnerFactory(&stdout, &stderr).Run([]string{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "attach-session", "-P", "-F", "#{window_index}"})
	if err != nil {
		t.Fatalf("expected new-session success, got %v", err)
	}
	if got != "0\n" {
		t.Fatalf("expected new-session output to be buffered, got %q", got)
	}
	if stdout.String() != "" {
		t.Fatalf("expected new-session stdout not to stream to terminal writer, got %q", stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("expected new-session stderr not to stream to terminal writer, got %q", stderr.String())
	}
}

func TestRunRootMainTUIWindowFailsWhenTraceWriteFails(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	callsPath := filepath.Join(t.TempDir(), "tmux-calls.log")
	tracePath := filepath.Join(t.TempDir(), "missing", "tmux-trace.jsonl")
	installFakeTmuxOnPath(t)
	t.Setenv("FAKE_TMUX_CALLS_FILE", callsPath)
	t.Setenv("FAKE_TMUX_HAS_SESSION_STDERR", "missing host\n")
	t.Setenv("AINN_TMUX_DEBUG_LOG", tracePath)

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "write tmux trace "+tracePath) {
		t.Fatalf("expected trace write failure in stderr, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "tmux is required for main-tui-window mode") {
		t.Fatalf("expected stderr to avoid misleading tmux-required message, got %q", stderr.String())
	}

	got, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "-V\n" {
		t.Fatalf("expected trace write failure to abort after first tmux command, got %q", string(got))
	}
}

func TestRunRootMainTUIWindowAbortsWhenHasSessionTraceWriteFails(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	callsPath := filepath.Join(t.TempDir(), "tmux-calls.log")
	tracePath := filepath.Join(t.TempDir(), "tmux-trace.jsonl")
	installFakeTmuxOnPath(t)
	t.Setenv("AINN_TMUX_DEBUG_LOG", tracePath)
	t.Setenv("FAKE_TMUX_CALLS_FILE", callsPath)
	t.Setenv("FAKE_TMUX_HAS_SESSION", "1")
	t.Setenv("FAKE_TMUX_CHMOD_TRACE_PATH", tracePath)
	t.Setenv("FAKE_TMUX_CHMOD_TRACE_ON_COMMAND", "has-session")

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "write tmux trace "+tracePath) {
		t.Fatalf("expected trace write failure in stderr, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "failed to start tmux host") {
		t.Fatalf("expected direct trace write failure, got %q", stderr.String())
	}

	got, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "-V\n-L ainn-test has-session -t ainn-test-host\n"
	if string(got) != want {
		t.Fatalf("expected bootstrap to stop after has-session, got %q want %q", string(got), want)
	}
}

func TestRunRootMainTUIWindowAbortsWhenNewSessionTraceWriteFails(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	callsPath := filepath.Join(t.TempDir(), "tmux-calls.log")
	tracePath := filepath.Join(t.TempDir(), "tmux-trace.jsonl")
	installFakeTmuxOnPath(t)
	t.Setenv("AINN_TMUX_DEBUG_LOG", tracePath)
	t.Setenv("FAKE_TMUX_CALLS_FILE", callsPath)
	t.Setenv("FAKE_TMUX_HAS_SESSION_STDERR", "can't find session\n")
	t.Setenv("FAKE_TMUX_NEW_SESSION_STDOUT", "0\n")
	t.Setenv("FAKE_TMUX_CHMOD_TRACE_PATH", tracePath)
	t.Setenv("FAKE_TMUX_CHMOD_TRACE_ON_COMMAND", "new-session")

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "write tmux trace "+tracePath) {
		t.Fatalf("expected trace write failure in stderr, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "failed to start tmux host") {
		t.Fatalf("expected direct trace write failure, got %q", stderr.String())
	}

	got, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "-V\n-L ainn-test has-session -t ainn-test-host\n-L ainn-test new-session -d -s ainn-test-host -n ainn -P -F #{window_index} env AINN_TMUX_ROOT_CHILD=1 " + fakeTmuxCurrentExecutable(t) + " --config-dir " + dir + " --manager-port 19090\n"
	if string(got) != want {
		t.Fatalf("expected bootstrap to stop after new-session, got %q want %q", string(got), want)
	}
}

func TestRunRootMainTUIWindowAbortsWhenSelectWindowTraceWriteFails(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	callsPath := filepath.Join(t.TempDir(), "tmux-calls.log")
	tracePath := filepath.Join(t.TempDir(), "tmux-trace.jsonl")
	installFakeTmuxOnPath(t)
	t.Setenv("AINN_TMUX_DEBUG_LOG", tracePath)
	t.Setenv("FAKE_TMUX_CALLS_FILE", callsPath)
	t.Setenv("FAKE_TMUX_HAS_SESSION", "1")
	t.Setenv("FAKE_TMUX_CHMOD_TRACE_PATH", tracePath)
	t.Setenv("FAKE_TMUX_CHMOD_TRACE_ON_COMMAND", "select-window")

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "write tmux trace "+tracePath) {
		t.Fatalf("expected trace write failure in stderr, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "failed to select main tmux window") {
		t.Fatalf("expected direct trace write failure, got %q", stderr.String())
	}

	got, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	theme := strings.Join(tmuxMainWindowThemeCommand("ainn-test", "ainn-test-host")[1:], " ")
	want := "-V\n-L ainn-test has-session -t ainn-test-host\n-L ainn-test list-panes -t ainn-test-host:0 -F #{window_index}\t#{pane_start_command}\n-L ainn-test set-window-option -t ainn-test-host:0 -u window-status-format ; set-window-option -t ainn-test-host:0 -u window-status-current-format\n" + theme + "\n-L ainn-test set-option -s extended-keys always ; set-option -s extended-keys-format csi-u ; set-option -s terminal-features[3] xterm*:extkeys\n-L ainn-test select-window -t ainn-test-host:0\n"
	if string(got) != want {
		t.Fatalf("expected bootstrap to stop after select-window, got %q want %q", string(got), want)
	}
}

func TestRunRootMainTUIWindowWithoutDebugDoesNotMirrorControlCommandOutput(t *testing.T) {
	for name, hasSession := range map[string]string{
		"new host":      "0",
		"existing host": "1",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

			installFakeTmuxOnPath(t)
			t.Setenv(tmuxDebugEnvVar, "")
			t.Setenv(tmuxDebugLogEnvVar, "")
			t.Setenv("FAKE_TMUX_HAS_SESSION", hasSession)
			t.Setenv("FAKE_TMUX_NEW_SESSION_STDOUT", "0\n")
			t.Setenv("FAKE_TMUX_SELECT_WINDOW_STDOUT", "selected window\n")
			t.Setenv("FAKE_TMUX_ATTACH_STDOUT", "attached session\n")

			restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
				t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
				return nil
			})
			defer restoreRoot()
			restoreLocker := setRootLockerFactoryForTest(noopLocker{})
			defer restoreLocker()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
			}
			if stdout.String() != "attached session\n" {
				t.Fatalf("expected only attach-session stdout to stream without tmux debug, got %q", stdout.String())
			}
			if stderr.String() != "" {
				t.Fatalf("expected stderr to stay quiet without tmux debug, got %q", stderr.String())
			}
		})
	}
}

func TestRunRootMainTUIWindowWithoutDebugDoesNotCreateTraceFiles(t *testing.T) {
	sandboxDir := t.TempDir()
	configDir := filepath.Join(sandboxDir, "config")
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", "main-tui-window")

	installFakeTmuxOnPath(t)
	inheritedTracePath := os.Getenv(tmuxDebugLogEnvVar)
	var inheritedTrace []byte
	inheritedTraceMissing := false
	if inheritedTracePath != "" {
		trace, err := os.ReadFile(inheritedTracePath)
		if err == nil {
			inheritedTrace = trace
		} else if errors.Is(err, os.ErrNotExist) {
			inheritedTraceMissing = true
		} else {
			t.Fatal(err)
		}
	}
	homeDir := filepath.Join(sandboxDir, "home")
	runtimeDir := filepath.Join(sandboxDir, "runtime")
	tmpDir := filepath.Join(sandboxDir, "tmp")
	for _, dir := range []string{homeDir, runtimeDir, tmpDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("TMPDIR", tmpDir)
	t.Setenv(tmuxDebugEnvVar, "")
	t.Setenv(tmuxDebugLogEnvVar, "")
	t.Setenv("FAKE_TMUX_HAS_SESSION", "1")

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", configDir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if inheritedTracePath != "" {
		got, err := os.ReadFile(inheritedTracePath)
		if inheritedTraceMissing {
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected inherited trace path %q to stay absent, got err=%v", inheritedTracePath, err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, inheritedTrace) {
				t.Fatalf("expected inherited trace path %q to stay unchanged", inheritedTracePath)
			}
		}
	}

	var files []string
	err := filepath.Walk(sandboxDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(sandboxDir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"config/config.yaml"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("expected sandbox files %#v, got %#v", want, files)
	}
}

func TestRunRootMainTUIWindowWritesHumanTraceToStderr(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	installFakeTmuxOnPath(t)
	t.Setenv("AINN_TMUX_DEBUG", "1")
	t.Setenv("FAKE_TMUX_HAS_SESSION", "1")
	t.Setenv("FAKE_TMUX_ATTACH_STDOUT", "attached\n")

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	for _, want := range []string{
		"tmux trace: tmux -V",
		`stdout="tmux 3.6b\n"`,
		"tmux trace: tmux -L ainn-test list-panes -t ainn-test-host:0 -F #{window_index}\t#{pane_start_command}",
		"tmux trace: tmux -L ainn-test select-window -t ainn-test-host:0",
		"tmux trace: tmux -L ainn-test attach-session -t ainn-test-host",
		"duration_ms=",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected stderr to contain %q, got %q", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), `stdout="attached\n"`) {
		t.Fatalf("expected attach-session trace not to buffer interactive stdout, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), `{"argv":`) {
		t.Fatalf("expected human-readable trace, got %q", stderr.String())
	}
}

func TestRunRootMainTUIWindowChildDoesNotCreateTraceLog(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	tracePath := filepath.Join(t.TempDir(), "tmux-trace.jsonl")
	t.Setenv(tmuxRootChildEnvVar, "1")
	t.Setenv("AINN_TMUX_DEBUG_LOG", tracePath)

	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				t.Fatalf("tmux runner should not be used in child root: %#v", args)
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(tracePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no child trace log, got err=%v", err)
	}
}

func TestRunRootRejectsSecondInstanceWhenLockHeld(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
workers:
  app:
    port: 6767
    provider: openai
providers:
  openai:
    base_url: https://api.openai.com/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	holdLockForTest(t)

	var called bool
	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		called = true
		return nil
	})
	defer restore()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19091"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit when lock held, got 0: %s", stderr.String())
	}
	if called {
		t.Fatal("root runner should not be called when lock is held")
	}
	if !strings.Contains(stderr.String(), "another instance") {
		t.Fatalf("expected 'another instance' error, got: %s", stderr.String())
	}
}

func TestRunRootAllowsDifferentCanonicalConfigDirs(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	firstConfigDir := t.TempDir()
	secondConfigDir := t.TempDir()
	writeRootConfig(t, firstConfigDir, "ainn-first", "ainn-first-host", config.TmuxHostStartModeNewWindow)
	writeRootConfig(t, secondConfigDir, "ainn-second", "ainn-second-host", config.TmuxHostStartModeNewWindow)

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var once sync.Once

	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		if opts.ConfigDir == firstConfigDir {
			once.Do(func() { close(firstStarted) })
			<-releaseFirst
		}
		return nil
	})
	defer restore()

	firstDone := make(chan struct{})
	var firstCode int
	var firstStderr bytes.Buffer
	go func() {
		firstCode = Run([]string{"--config-dir", firstConfigDir, "--manager-port", "19090"}, &bytes.Buffer{}, &firstStderr)
		close(firstDone)
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first root run to acquire lock")
	}

	var secondStderr bytes.Buffer
	secondCode := Run([]string{"--config-dir", secondConfigDir, "--manager-port", "19091"}, &bytes.Buffer{}, &secondStderr)
	close(releaseFirst)

	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first root run to finish")
	}

	if firstCode != 0 {
		t.Fatalf("expected first root run to succeed, got %d: %s", firstCode, firstStderr.String())
	}
	if secondCode != 0 {
		t.Fatalf("expected second root run with different config dir to succeed, got %d: %s", secondCode, secondStderr.String())
	}
}

func TestRunRootRejectsSecondInstanceForSameCanonicalConfigDir(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	configDir := t.TempDir()
	writeRootConfig(t, configDir, "ainn-test", "ainn-test-host", config.TmuxHostStartModeNewWindow)

	aliasDir := filepath.Join(t.TempDir(), "config-link")
	if err := os.Symlink(configDir, aliasDir); err != nil {
		t.Fatal(err)
	}

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var once sync.Once

	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		if opts.ConfigDir == aliasDir {
			t.Fatalf("root runner should not run for alias path while canonical lock is held: %#v", opts)
		}
		if opts.ConfigDir == configDir {
			once.Do(func() { close(firstStarted) })
			<-releaseFirst
		}
		return nil
	})
	defer restore()

	firstDone := make(chan struct{})
	var firstCode int
	var firstStderr bytes.Buffer
	go func() {
		firstCode = Run([]string{"--config-dir", configDir, "--manager-port", "19090"}, &bytes.Buffer{}, &firstStderr)
		close(firstDone)
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first root run to acquire lock")
	}

	var secondStderr bytes.Buffer
	secondCode := Run([]string{"--config-dir", aliasDir, "--manager-port", "19091"}, &bytes.Buffer{}, &secondStderr)
	close(releaseFirst)

	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first root run to finish")
	}

	if firstCode != 0 {
		t.Fatalf("expected first root run to succeed, got %d: %s", firstCode, firstStderr.String())
	}
	if secondCode == 0 {
		t.Fatalf("expected second root run with same canonical config dir to fail, got 0: %s", secondStderr.String())
	}
	if !strings.Contains(secondStderr.String(), "another instance") {
		t.Fatalf("expected 'another instance' error, got: %s", secondStderr.String())
	}
}

func TestRootLockPathUsesCanonicalConfigDir(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	configDir := t.TempDir()
	linkPath := filepath.Join(t.TempDir(), "config-link")
	if err := os.Symlink(configDir, linkPath); err != nil {
		t.Fatal(err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	homeConfigDir, err := os.MkdirTemp(homeDir, "ainn-root-lock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(homeConfigDir) })

	homeAlias := "~/" + strings.TrimPrefix(homeConfigDir, homeDir+"/")
	canonicalConfigDir, err := filepath.EvalSymlinks(configDir)
	if err != nil {
		t.Fatal(err)
	}
	canonicalHomeConfigDir, err := filepath.EvalSymlinks(homeConfigDir)
	if err != nil {
		t.Fatal(err)
	}

	gotCanonical, err := rootLockPath(configDir)
	if err != nil {
		t.Fatal(err)
	}
	gotSymlink, err := rootLockPath(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	gotHomeAlias, err := rootLockPath(homeAlias)
	if err != nil {
		t.Fatal(err)
	}
	gotHomeAbsolute, err := rootLockPath(homeConfigDir)
	if err != nil {
		t.Fatal(err)
	}

	wantCanonical := filepath.Join(runtimeDir, fmt.Sprintf("ainn-%x.lock", sha256.Sum256([]byte(canonicalConfigDir))))
	wantHome := filepath.Join(runtimeDir, fmt.Sprintf("ainn-%x.lock", sha256.Sum256([]byte(canonicalHomeConfigDir))))

	if gotCanonical != wantCanonical {
		t.Fatalf("expected canonical lock path %q, got %q", wantCanonical, gotCanonical)
	}
	if gotSymlink != gotCanonical {
		t.Fatalf("expected symlink lock path %q, got %q", gotCanonical, gotSymlink)
	}
	if gotHomeAlias != wantHome {
		t.Fatalf("expected home alias lock path %q, got %q", wantHome, gotHomeAlias)
	}
	if gotHomeAbsolute != wantHome {
		t.Fatalf("expected absolute home lock path %q, got %q", wantHome, gotHomeAbsolute)
	}
}

func TestRootLockPathFormatsFilesystemSafeName(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	firstConfigDir := t.TempDir()
	secondConfigDir := t.TempDir()

	firstLockPath, err := rootLockPath(firstConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	firstLockPathAgain, err := rootLockPath(firstConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	secondLockPath, err := rootLockPath(secondConfigDir)
	if err != nil {
		t.Fatal(err)
	}

	lockNamePattern := regexp.MustCompile(`^ainn-[0-9a-f]+\.lock$`)
	if !lockNamePattern.MatchString(filepath.Base(firstLockPath)) {
		t.Fatalf("expected lock filename to match %q, got %q", lockNamePattern.String(), filepath.Base(firstLockPath))
	}
	if strings.Contains(filepath.Base(firstLockPath), string(os.PathSeparator)) {
		t.Fatalf("expected filesystem-safe lock filename, got %q", filepath.Base(firstLockPath))
	}
	if firstLockPath != firstLockPathAgain {
		t.Fatalf("expected stable lock path %q, got %q", firstLockPath, firstLockPathAgain)
	}
	if firstLockPath == secondLockPath {
		t.Fatalf("expected different config dirs to produce different lock paths, got %q", firstLockPath)
	}
}

func TestRootRunnerContinuesAfterConfiguredWorkerStartupFailure(t *testing.T) {
	startErr := errors.New("app: config_patch recovery state unresolved must be resolved before enabling")
	mgr := &fakeRootManager{startErr: startErr}
	server := &fakeRootServer{listenStarted: make(chan struct{})}
	program := &fakeRootProgram{waitForListen: server.listenStarted}

	restoreManager := setRootManagerFactoryForTest(func(opts RootOptions) rootManager {
		return mgr
	})
	defer restoreManager()
	restoreServer := setRootServerFactoryForTest(func(addr string, handler http.Handler) rootServer {
		server.addr = addr
		server.handler = handler
		return server
	})
	defer restoreServer()
	restoreProgram := func() func() {
		previous := rootProgramFactory
		rootProgramFactory = func(addr string, startupStatus string, configDir string) rootProgram {
			program.addr = addr
			program.startupStatus = startupStatus
			program.configDir = configDir
			return program
		}
		return func() { rootProgramFactory = previous }
	}()
	defer restoreProgram()

	err := rootRunner(RootOptions{
		ManagerPort: 19090,
		Config: config.Config{
			Settings: config.Settings{LogDir: t.TempDir()},
		},
	})
	if err != nil {
		t.Fatalf("expected root runner to keep manager running despite worker startup failure, got %v", err)
	}
	if !mgr.startConfiguredWorkersCalled {
		t.Fatal("expected root runner to attempt configured worker startup")
	}
	if !mgr.startHealthMonitorCalled {
		t.Fatal("expected health monitor to start after worker startup failure")
	}
	if !mgr.startUpstreamProberCalled {
		t.Fatal("expected upstream prober to start after worker startup failure")
	}
	if !mgr.startHostedTurnWatcherCalled {
		t.Fatal("expected hosted turn watcher to start after worker startup failure")
	}
	if !server.listenCalled {
		t.Fatal("expected manager API server to start after worker startup failure")
	}
	if !program.runCalled {
		t.Fatal("expected TUI program to run after worker startup failure")
	}
	if !server.closeCalled {
		t.Fatal("expected server to be closed when program exits")
	}
	if program.startupStatus != startErr.Error() {
		t.Fatalf("expected startup status %q, got %q", startErr.Error(), program.startupStatus)
	}
}

func TestRootRunnerPassesManagerLoggerToFactory(t *testing.T) {
	mgr := &fakeRootManager{}
	server := &fakeRootServer{listenStarted: make(chan struct{})}
	program := &fakeRootProgram{waitForListen: server.listenStarted}
	managerLoggerSet := false
	healthLoggerSet := false

	restoreManager := setRootManagerFactoryForTest(func(opts RootOptions) rootManager {
		managerLoggerSet = opts.ManagerLogger != nil
		healthLoggerSet = opts.ManagerHealthLogger != nil
		return mgr
	})
	defer restoreManager()
	restoreServer := setRootServerFactoryForTest(func(addr string, handler http.Handler) rootServer {
		server.addr = addr
		server.handler = handler
		return server
	})
	defer restoreServer()
	restoreProgram := func() func() {
		previous := rootProgramFactory
		rootProgramFactory = func(addr string, startupStatus string, configDir string) rootProgram {
			program.addr = addr
			program.startupStatus = startupStatus
			program.configDir = configDir
			return program
		}
		return func() { rootProgramFactory = previous }
	}()
	defer restoreProgram()

	err := rootRunner(RootOptions{
		ManagerPort: 19090,
		Config: config.Config{
			Settings: config.Settings{LogDir: t.TempDir()},
		},
	})
	if err != nil {
		t.Fatalf("expected root runner to finish, got %v", err)
	}
	if !managerLoggerSet {
		t.Fatal("expected root runner to pass manager logger to manager factory")
	}
	if !healthLoggerSet {
		t.Fatal("expected root runner to pass health logger to manager factory")
	}
}

func TestRootProgramFactoryBuildsTypeScriptTUICommand(t *testing.T) {
	program := rootProgramFactory("127.0.0.1:8787", "", "/tmp/ainn-config")
	cmd := program.CommandLine()
	if cmd[len(cmd)-2] != "run" || cmd[len(cmd)-1] != "src/cli.ts" {
		t.Fatalf("expected bun run src/cli.ts command, got %#v", cmd)
	}
	if program.WorkingDir() != "tui" {
		t.Fatalf("expected tui working dir, got %q", program.WorkingDir())
	}
	if program.Env()["AINN_URL"] != "http://127.0.0.1:8787" {
		t.Fatalf("expected AINN_URL for manager API, got %#v", program.Env())
	}
	if program.Env()["AINN_PROJECT_DIR"] == "" {
		t.Fatalf("expected AINN_PROJECT_DIR to be set, got %#v", program.Env())
	}
	if program.Env()["AINN_CONFIG_DIR"] != "/tmp/ainn-config" {
		t.Fatalf("expected AINN_CONFIG_DIR for TUI, got %#v", program.Env())
	}
}

func TestRootProgramEnvIncludesConfigDir(t *testing.T) {
	program := newTUIProgram("127.0.0.1:8787", "", "/tmp/ainn-config")

	if program.Env()["AINN_CONFIG_DIR"] != "/tmp/ainn-config" {
		t.Fatalf("expected AINN_CONFIG_DIR, got %#v", program.Env())
	}
}

func TestRootRunnerDoesNotWriteConfiguredWorkerStartupFailureToTerminal(t *testing.T) {
	startErr := errors.New("cli-groq: missing API key")
	mgr := &fakeRootManager{startErr: startErr}
	server := &fakeRootServer{listenStarted: make(chan struct{})}
	program := &fakeRootProgram{waitForListen: server.listenStarted}

	var logOutput bytes.Buffer
	restoreLogWriter := setRootLogWriterForTest(&logOutput)
	defer restoreLogWriter()

	restoreManager := setRootManagerFactoryForTest(func(opts RootOptions) rootManager {
		return mgr
	})
	defer restoreManager()
	restoreServer := setRootServerFactoryForTest(func(addr string, handler http.Handler) rootServer {
		server.addr = addr
		server.handler = handler
		return server
	})
	defer restoreServer()
	restoreProgram := func() func() {
		previous := rootProgramFactory
		rootProgramFactory = func(addr string, startupStatus string, configDir string) rootProgram {
			program.addr = addr
			program.startupStatus = startupStatus
			program.configDir = configDir
			return program
		}
		return func() { rootProgramFactory = previous }
	}()
	defer restoreProgram()

	err := rootRunner(RootOptions{
		ManagerPort: 19090,
		Config: config.Config{
			Settings: config.Settings{LogDir: t.TempDir()},
		},
	})
	if err != nil {
		t.Fatalf("expected root runner to keep running, got %v", err)
	}
	if strings.Contains(logOutput.String(), startErr.Error()) {
		t.Fatalf("startup error should not be written to terminal log output: %q", logOutput.String())
	}
}

// holdLockForTest 替换 rootLockerFactory 让 Run 抢锁失败，模拟同一实例的第二次启动。
func holdLockForTest(t *testing.T) {
	t.Helper()
	previous := rootLockerFactory
	rootLockerFactory = func(string) rootLocker {
		return lockedLocker{}
	}
	t.Cleanup(func() { rootLockerFactory = previous })
}

type lockedLocker struct{}

func (lockedLocker) Acquire() (func(), error) {
	return nil, errAlreadyLocked
}

// noopLocker 总是成功抢锁，用于走 runRoot 的测试避免依赖真实文件锁。
type noopLocker struct{}

func (noopLocker) Acquire() (func(), error) {
	return func() {}, nil
}

func TestFlockLockerRejectsSecondAcquireOnSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	first := flockLocker{path: path}
	release, err := first.Acquire()
	if err != nil {
		t.Fatalf("first acquire should succeed: %v", err)
	}
	defer release()

	second := flockLocker{path: path}
	if _, err := second.Acquire(); err == nil {
		t.Fatal("second acquire on same path should fail while first is held")
	}
}

func writeRootConfig(t *testing.T, configDir string, socketName string, hostSession string, hostStartMode string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`
settings:
  state_dir: ` + filepath.Join(configDir, "state") + `
  log_dir: ` + filepath.Join(configDir, "logs") + `
  terminal:
    host: tmux
    opener: default
    tmux:
      socket_name: ` + socketName + `
      host_session: ` + hostSession + `
      host_start_mode: ` + hostStartMode + `
workers: {}
upstreams: {}
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

type fakeRootManager struct {
	startErr                     error
	startConfiguredWorkersCalled bool
	startHealthMonitorCalled     bool
	startUpstreamProberCalled    bool
	startHostedTurnWatcherCalled bool
	closeCalled                  bool
}

func (m *fakeRootManager) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (m *fakeRootManager) Close() {
	m.closeCalled = true
}

func (m *fakeRootManager) StartConfiguredWorkers() error {
	m.startConfiguredWorkersCalled = true
	return m.startErr
}

func (m *fakeRootManager) StartHealthMonitor(_ time.Duration) func() {
	m.startHealthMonitorCalled = true
	return func() {}
}

func (m *fakeRootManager) StartUpstreamProber(_ time.Duration) func() {
	m.startUpstreamProberCalled = true
	return func() {}
}

func (m *fakeRootManager) StartHostedTurnWatcher(_ time.Duration) func() {
	m.startHostedTurnWatcherCalled = true
	return func() {}
}

type fakeRootServer struct {
	addr          string
	handler       http.Handler
	listenStarted chan struct{}
	listenCalled  bool
	closeCalled   bool
}

func (s *fakeRootServer) ensureListenStarted() {
	if s.listenStarted == nil {
		s.listenStarted = make(chan struct{})
	}
}

func (s *fakeRootServer) ListenAndServe() error {
	s.ensureListenStarted()
	s.listenCalled = true
	close(s.listenStarted)
	return http.ErrServerClosed
}

func (s *fakeRootServer) Close() error {
	s.closeCalled = true
	return nil
}

type fakeRootProgram struct {
	addr          string
	waitForListen <-chan struct{}
	runCalled     bool
	startupStatus string
	configDir     string
}

func (p *fakeRootProgram) Run(context.Context) error {
	p.runCalled = true
	if p.waitForListen != nil {
		select {
		case <-p.waitForListen:
		case <-time.After(time.Second):
			return errors.New("timed out waiting for server startup")
		}
	}
	return nil
}

func (p *fakeRootProgram) CommandLine() []string {
	return []string{"fake"}
}

func (p *fakeRootProgram) WorkingDir() string {
	return ""
}

func (p *fakeRootProgram) Env() map[string]string {
	return map[string]string{}
}

type tmuxTraceRecord struct {
	Argv       []string `json:"argv"`
	Stdout     string   `json:"stdout"`
	Stderr     string   `json:"stderr"`
	Err        string   `json:"err"`
	DurationMS int64    `json:"duration_ms"`
}

type tmuxTraceView struct {
	Argv        []string
	Stdout      string
	Stderr      string
	Err         string
	HasDuration bool
}

func installFakeTmuxOnPath(t *testing.T) {
	t.Helper()
	currentExe := fakeTmuxCurrentExecutable(t)
	fakeBinDir := t.TempDir()
	fakeTmuxPath := filepath.Join(fakeBinDir, "tmux")
	script := `#!/usr/bin/env bash
set -eu
if [[ -n "${FAKE_TMUX_CALLS_FILE:-}" ]]; then
  printf '%s\n' "$*" >> "$FAKE_TMUX_CALLS_FILE"
fi
if [[ "${1:-}" == "-V" ]]; then
  printf 'tmux 3.6b\n'
  exit 0
fi
cmd=""
for arg in "$@"; do
  case "$arg" in
    has-session|new-session|list-panes|list-clients|select-window|new-window|respawn-pane|move-window|switch-client|attach-session|show|show-option|show-hooks|list-keys|set-option|set-window-option|set-hook|bind-key)
      cmd="$arg"
      break
      ;;
  esac
done
case "$cmd" in
  has-session)
    if [[ "${FAKE_TMUX_HAS_SESSION:-0}" == "1" ]]; then
      printf '%s' "${FAKE_TMUX_HAS_SESSION_STDOUT:-}"
      printf '%s' "${FAKE_TMUX_HAS_SESSION_STDERR:-}" >&2
      if [[ "${FAKE_TMUX_CHMOD_TRACE_ON_COMMAND:-}" == "has-session" && -n "${FAKE_TMUX_CHMOD_TRACE_PATH:-}" ]]; then
        chmod 0400 "$FAKE_TMUX_CHMOD_TRACE_PATH"
      fi
      exit 0
    fi
    printf '%s' "${FAKE_TMUX_HAS_SESSION_STDERR:-no server running\n}" >&2
    exit 1
    ;;
  new-session)
    printf '%s' "${FAKE_TMUX_NEW_SESSION_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_NEW_SESSION_STDERR:-}" >&2
    if [[ "${FAKE_TMUX_CHMOD_TRACE_ON_COMMAND:-}" == "new-session" && -n "${FAKE_TMUX_CHMOD_TRACE_PATH:-}" ]]; then
      chmod 0400 "$FAKE_TMUX_CHMOD_TRACE_PATH"
    fi
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "new-session" ]] && exit 1
    exit 0
    ;;
  list-panes)
    if [[ -n "${FAKE_TMUX_LIST_PANES_STDOUT:-}" ]]; then
      printf '%s' "${FAKE_TMUX_LIST_PANES_STDOUT}"
    else
      printf 'env AINN_TMUX_ROOT_CHILD=1 %s\n' "${FAKE_TMUX_CURRENT_EXE}"
    fi
    printf '%s' "${FAKE_TMUX_LIST_PANES_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "list-panes" ]] && exit 1
    exit 0
    ;;
  select-window)
    printf '%s' "${FAKE_TMUX_SELECT_WINDOW_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_SELECT_WINDOW_STDERR:-}" >&2
    if [[ "${FAKE_TMUX_CHMOD_TRACE_ON_COMMAND:-}" == "select-window" && -n "${FAKE_TMUX_CHMOD_TRACE_PATH:-}" ]]; then
      chmod 0400 "$FAKE_TMUX_CHMOD_TRACE_PATH"
    fi
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "select-window" ]] && exit 1
    exit 0
    ;;
  new-window)
    printf '%s' "${FAKE_TMUX_NEW_WINDOW_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_NEW_WINDOW_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "new-window" ]] && exit 1
    exit 0
    ;;
  respawn-pane)
    printf '%s' "${FAKE_TMUX_RESPAWN_PANE_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_RESPAWN_PANE_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "respawn-pane" ]] && exit 1
    exit 0
    ;;
  move-window)
    printf '%s' "${FAKE_TMUX_MOVE_WINDOW_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_MOVE_WINDOW_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "move-window" ]] && exit 1
    exit 0
    ;;
  list-clients)
    printf '%s' "${FAKE_TMUX_LIST_CLIENTS_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_LIST_CLIENTS_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "list-clients" ]] && exit 1
    exit 0
    ;;
  switch-client)
    printf '%s' "${FAKE_TMUX_SWITCH_CLIENT_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_SWITCH_CLIENT_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "switch-client" ]] && exit 1
    exit 0
    ;;
  attach-session)
    printf '%s' "${FAKE_TMUX_ATTACH_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_ATTACH_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "attach-session" ]] && exit 1
    exit 0
    ;;
  show)
    printf '%s' "${FAKE_TMUX_SHOW_STDOUT:-on\n}"
    printf '%s' "${FAKE_TMUX_SHOW_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "show" ]] && exit 1
    exit 0
    ;;
  show-option)
    printf '%s' "${FAKE_TMUX_SHOW_OPTION_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_SHOW_OPTION_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "show-option" ]] && exit 1
    exit 0
    ;;
  show-hooks)
    printf '%s' "${FAKE_TMUX_SHOW_HOOKS_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_SHOW_HOOKS_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "show-hooks" ]] && exit 1
    exit 0
    ;;
  list-keys)
    printf '%s' "${FAKE_TMUX_LIST_KEYS_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_LIST_KEYS_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "list-keys" ]] && exit 1
    exit 0
    ;;
  set-option)
    printf '%s' "${FAKE_TMUX_SET_OPTION_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_SET_OPTION_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "set-option" ]] && exit 1
    exit 0
    ;;
  set-window-option)
    printf '%s' "${FAKE_TMUX_SET_WINDOW_OPTION_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_SET_WINDOW_OPTION_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "set-window-option" ]] && exit 1
    exit 0
    ;;
  set-hook)
    printf '%s' "${FAKE_TMUX_SET_HOOK_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_SET_HOOK_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "set-hook" ]] && exit 1
    exit 0
    ;;
  bind-key)
    printf '%s' "${FAKE_TMUX_BIND_KEY_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_BIND_KEY_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "bind-key" ]] && exit 1
    exit 0
    ;;
  *)
    printf 'unexpected tmux args: %s\n' "$*" >&2
    exit 64
    ;;
esac
`
	if err := os.WriteFile(fakeTmuxPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_TMUX_CURRENT_EXE", currentExe)
}

func fakeTmuxCurrentExecutable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func readTmuxTraceViews(t *testing.T, tracePath string) []tmuxTraceView {
	t.Helper()
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	views := make([]tmuxTraceView, 0, len(lines))
	for _, line := range lines {
		var record tmuxTraceRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("failed to parse trace line %q: %v", line, err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("failed to inspect trace line %q: %v", line, err)
		}
		views = append(views, tmuxTraceView{
			Argv:        record.Argv,
			Stdout:      record.Stdout,
			Stderr:      record.Stderr,
			Err:         record.Err,
			HasDuration: raw["duration_ms"] != nil,
		})
	}
	return views
}
