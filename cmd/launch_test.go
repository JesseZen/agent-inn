package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jesse/codex-app-proxy/internal/manager"
)

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
			return launchRunnerFunc(func(args []string) error {
				got = append([]string{}, args...)
				return nil
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

func TestRunLaunchExplicitExternalWindowMode(t *testing.T) {
	var got []string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) error {
				got = append([]string{}, args...)
				return nil
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
	t.Setenv("HOME", t.TempDir())

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) error {
				got = append(got, append([]string{}, args...))
				// Simulate fresh tmux host: has-session and select-window fail.
				// tmux subcommand sits at args[3] after `tmux -L cap`.
				if len(args) > 3 && args[3] == "has-session" {
					return errors.New("can't find session")
				}
				if len(args) > 3 && args[3] == "select-window" {
					return errors.New("can't find window")
				}
				return nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--worker", "11199", "--profile", "cli-openai", "--cd", "/tmp/work", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	windowName := manager.SafeWindowName("solve problem A")
	codexCmd := []string{"codex", "--profile", "cli-openai", "--cd", "/tmp/work"}
	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		manager.TmuxStartHostCommand(),
		manager.TmuxSelectWindowCommand(windowName),
		manager.TmuxCreateWindowCommand(windowName, codexCmd),
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

func TestRunLaunchHostedTerminalSwitchesExistingWindow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) error {
				got = append(got, append([]string{}, args...))
				// Simulate existing host and window: has-session and select-window succeed.
				return nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(""))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, manager.SafeWindowName("solve problem A")); err != nil {
		t.Fatal(err)
	}

	code := runLaunch([]string{"--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", created.SessionID}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		manager.TmuxSelectWindowCommand(manager.SafeWindowName("solve problem A")),
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
	t.Setenv("HOME", t.TempDir())

	var stderr bytes.Buffer
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) error {
				return errors.New("exec: \"tmux\": executable file not found")
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--worker", "11199", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected failure when tmux is missing")
	}
	if !strings.Contains(stderr.String(), "tmux is required for hosted-terminal mode") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunLaunchHostedTerminalNoAttachSkipsAttach(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) error {
				got = append(got, append([]string{}, args...))
				return nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(""))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, manager.SafeWindowName("solve problem A")); err != nil {
		t.Fatal(err)
	}

	code := runLaunch([]string{"--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", created.SessionID, "--no-attach"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	windowName := manager.SafeWindowName("solve problem A")
	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		manager.TmuxSelectWindowCommand(windowName),
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
