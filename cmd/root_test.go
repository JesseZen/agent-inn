package cmd

import (
	"bytes"
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
)

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
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
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
		{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "ainn", "-P", "-F", "#{window_id}", "env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", dir, "--manager-port", "19090"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if strings.Join(got[i], " ") != strings.Join(want[i], " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], want[i])
		}
	}
}

func TestRunRootMainTUIWindowAttachesExistingHost(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
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
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:ainn"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if strings.Join(got[i], " ") != strings.Join(want[i], " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], want[i])
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
				if len(args) > 3 && args[3] == "select-window" {
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
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:ainn"},
		{"tmux", "-L", "ainn-test", "new-window", "-t", "ainn-test-host", "-n", "ainn", "-P", "-F", "#{window_id}", "env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", dir, "--manager-port", "19090"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if strings.Join(got[i], " ") != strings.Join(want[i], " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], want[i])
		}
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
	t.Setenv("FAKE_TMUX_HAS_SESSION_STDERR", "missing host\n")
	t.Setenv("FAKE_TMUX_NEW_SESSION_STDOUT", "@1\n")
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
		{Argv: []string{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"}, Stdout: "", Stderr: "missing host\n", Err: "exit status 1", HasDuration: true},
		{Argv: []string{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "ainn", "-P", "-F", "#{window_id}", "env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", dir, "--manager-port", "19090"}, Stdout: "@1\n", Stderr: "", Err: "", HasDuration: true},
		{Argv: []string{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"}, Stdout: "attached\n", Stderr: "", Err: "", HasDuration: true},
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
		{Argv: []string{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:ainn"}, Stdout: "", Stderr: "", Err: "", HasDuration: true},
		{Argv: []string{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"}, Stdout: "", Stderr: "attach failed\n", Err: "exit status 1", HasDuration: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
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
	if strings.Contains(stderr.String(), "failed to recreate main tmux window") {
		t.Fatalf("expected direct trace write failure, got %q", stderr.String())
	}

	got, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "-V\n-L ainn-test has-session -t ainn-test-host\n-L ainn-test select-window -t ainn-test-host:ainn\n"
	if string(got) != want {
		t.Fatalf("expected bootstrap to stop after select-window, got %q want %q", string(got), want)
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
		"tmux trace: tmux -L ainn-test select-window -t ainn-test-host:ainn",
		`stdout="attached\n"`,
		"duration_ms=",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected stderr to contain %q, got %q", want, stderr.String())
		}
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
		Config:      config.Config{},
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

	err := rootRunner(RootOptions{ManagerPort: 19090, Config: config.Config{}})
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

func (p *fakeRootProgram) Run() error {
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
    has-session|new-session|select-window|new-window|attach-session)
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
    printf '%s' "${FAKE_TMUX_HAS_SESSION_STDERR:-missing host\n}" >&2
    exit 1
    ;;
  new-session)
    printf '%s' "${FAKE_TMUX_NEW_SESSION_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_NEW_SESSION_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "new-session" ]] && exit 1
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
  attach-session)
    printf '%s' "${FAKE_TMUX_ATTACH_STDOUT:-}"
    printf '%s' "${FAKE_TMUX_ATTACH_STDERR:-}" >&2
    [[ "${FAKE_TMUX_FAIL_COMMAND:-}" == "attach-session" ]] && exit 1
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
