package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/logging"
	"github.com/jesse/agent-inn/internal/manager"
)

const rootSupervisorHelperEnv = "AINN_TEST_ROOT_SUPERVISOR_HELPER"
const rootSupervisorDescendantPIDEnv = "AINN_TEST_ROOT_SUPERVISOR_DESCENDANT_PID"
const rootSupervisorReadyPIDEnv = "AINN_TEST_ROOT_SUPERVISOR_READY_PID"

func TestSuperviseRootRestartsAfterRestartExit(t *testing.T) {
	previousRun := rootSupervisedRunner
	previousRefresh := rootSupervisorRefreshEnvironment
	defer func() {
		rootSupervisedRunner = previousRun
		rootSupervisorRefreshEnvironment = previousRefresh
	}()

	runs := 0
	refreshes := 0
	rootSupervisedRunner = func(RootOptions) (logging.RootRunExit, error) {
		runs++
		if runs == 1 {
			return logging.RootRunExit{ExitCode: rootRestartExitCode}, nil
		}
		return logging.RootRunExit{ExitCode: 0}, nil
	}
	rootSupervisorRefreshEnvironment = func() error {
		refreshes++
		return nil
	}

	if err := superviseRoot(RootOptions{}); err != nil {
		t.Fatal(err)
	}
	if runs != 2 || refreshes != 1 {
		t.Fatalf("runs=%d refreshes=%d, want runs=2 refreshes=1", runs, refreshes)
	}
}

func TestSuperviseRootRefreshesTmuxThemeBeforeRestart(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, config.ConfigFileName)
	socketPath := filepath.Join(dir, "tmux", "ainn-test")
	t.Setenv("TMUX", socketPath+",123,0")
	settings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{
		SocketName:      "ainn-test",
		HostSession:     "ainn-test-host",
		HostStartMode:   config.TmuxHostStartModeMainTUIWindow,
		StatusBarHeight: 4,
	}}}
	if err := config.NewStore(configPath, config.Config{Settings: settings}).Save(); err != nil {
		t.Fatal(err)
	}

	previousRun := rootSupervisedRunner
	previousRefresh := rootSupervisorRefreshEnvironment
	previousTmux := rootTmuxRunnerFactory
	defer func() {
		rootSupervisedRunner = previousRun
		rootSupervisorRefreshEnvironment = previousRefresh
		rootTmuxRunnerFactory = previousTmux
	}()

	runs := 0
	sequence := []string{}
	rootSupervisedRunner = func(RootOptions) (logging.RootRunExit, error) {
		runs++
		sequence = append(sequence, fmt.Sprintf("child %d", runs))
		if runs == 1 {
			return logging.RootRunExit{ExitCode: rootRestartExitCode}, nil
		}
		return logging.RootRunExit{ExitCode: 0}, nil
	}
	rootSupervisorRefreshEnvironment = func() error {
		sequence = append(sequence, "environment")
		return nil
	}
	var got [][]string
	rootTmuxRunnerFactory = func(io.Writer, io.Writer) rootTmuxRunner {
		return rootTmuxRunnerFunc(func(args []string) (string, error) {
			sequence = append(sequence, "theme")
			got = append(got, append([]string{}, args...))
			return "", nil
		})
	}

	if err := superviseRoot(RootOptions{ConfigPath: configPath, Config: config.Config{Settings: settings}}); err != nil {
		t.Fatal(err)
	}
	wantTheme := manager.TmuxThemeCommandForSettings(settings)
	wantTheme[1] = "-S"
	wantTheme[2] = socketPath
	if !reflect.DeepEqual(got, [][]string{wantTheme}) {
		t.Fatalf("got theme refresh commands %#v, want %#v", got, [][]string{wantTheme})
	}
	wantSequence := []string{"child 1", "environment", "theme", "child 2"}
	if !reflect.DeepEqual(sequence, wantSequence) {
		t.Fatalf("got restart sequence %#v, want %#v", sequence, wantSequence)
	}
}

func TestSuperviseRootDoesNotRefreshTmuxThemeWhenCurrentLifecycleIsNotMainTUIWindow(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, config.ConfigFileName)
	latestSettings := config.Settings{Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{
		SocketName:      "ainn-test",
		HostSession:     "ainn-test-host",
		HostStartMode:   config.TmuxHostStartModeMainTUIWindow,
		StatusBarHeight: 4,
	}}}
	if err := config.NewStore(configPath, config.Config{Settings: latestSettings}).Save(); err != nil {
		t.Fatal(err)
	}

	previousRun := rootSupervisedRunner
	previousRefresh := rootSupervisorRefreshEnvironment
	previousTmux := rootTmuxRunnerFactory
	defer func() {
		rootSupervisedRunner = previousRun
		rootSupervisorRefreshEnvironment = previousRefresh
		rootTmuxRunnerFactory = previousTmux
	}()

	runs := 0
	rootSupervisedRunner = func(RootOptions) (logging.RootRunExit, error) {
		runs++
		if runs == 1 {
			return logging.RootRunExit{ExitCode: rootRestartExitCode}, nil
		}
		return logging.RootRunExit{ExitCode: 0}, nil
	}
	rootSupervisorRefreshEnvironment = func() error { return nil }
	refreshes := 0
	rootTmuxRunnerFactory = func(io.Writer, io.Writer) rootTmuxRunner {
		return rootTmuxRunnerFunc(func([]string) (string, error) {
			refreshes++
			return "", nil
		})
	}

	currentSettings := latestSettings
	currentSettings.Terminal.Tmux.HostStartMode = config.TmuxHostStartModeNewWindow
	if err := superviseRoot(RootOptions{ConfigPath: configPath, Config: config.Config{Settings: currentSettings}}); err != nil {
		t.Fatal(err)
	}
	if refreshes != 0 {
		t.Fatalf("got %d tmux refreshes outside the current main-tui-window lifecycle, want 0", refreshes)
	}
}

func TestSuperviseRootPreservesExclusiveWatcherHandoffAcrossRestart(t *testing.T) {
	previousRun := rootSupervisedRunner
	previousRefresh := rootSupervisorRefreshEnvironment
	defer func() {
		rootSupervisedRunner = previousRun
		rootSupervisorRefreshEnvironment = previousRefresh
	}()

	rootLockHeld := true
	watcherOwned := false
	runs := 0
	sequence := []string{}
	rootSupervisedRunner = func(RootOptions) (logging.RootRunExit, error) {
		runs++
		if !rootLockHeld || watcherOwned {
			t.Fatalf("run %d started rootLockHeld=%v watcherOwned=%v", runs, rootLockHeld, watcherOwned)
		}
		watcherOwned = true
		sequence = append(sequence, fmt.Sprintf("child %d watcher acquire", runs), fmt.Sprintf("child %d startup reconcile", runs))
		sequence = append(sequence, fmt.Sprintf("child %d watcher stop", runs))
		watcherOwned = false
		sequence = append(sequence, fmt.Sprintf("child %d watcher release", runs))
		if runs == 1 {
			return logging.RootRunExit{ExitCode: rootRestartExitCode}, nil
		}
		return logging.RootRunExit{ExitCode: 0}, nil
	}
	rootSupervisorRefreshEnvironment = func() error {
		if !rootLockHeld || watcherOwned {
			t.Fatalf("refresh rootLockHeld=%v watcherOwned=%v", rootLockHeld, watcherOwned)
		}
		sequence = append(sequence, "sidecar observes root lock", "supervisor refresh")
		return nil
	}

	if err := superviseRoot(RootOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"child 1 watcher acquire",
		"child 1 startup reconcile",
		"child 1 watcher stop",
		"child 1 watcher release",
		"sidecar observes root lock",
		"supervisor refresh",
		"child 2 watcher acquire",
		"child 2 startup reconcile",
		"child 2 watcher stop",
		"child 2 watcher release",
	}
	if !rootLockHeld || watcherOwned || !reflect.DeepEqual(sequence, want) {
		t.Fatalf("rootLockHeld=%v watcherOwned=%v sequence=%#v, want %#v", rootLockHeld, watcherOwned, sequence, want)
	}
}

func TestRefreshRootSupervisorEnvironmentIsolatesLoginShellSession(t *testing.T) {
	dir := t.TempDir()
	shellPath := filepath.Join(dir, "login-shell")
	sessionPath := filepath.Join(dir, "session")
	script := "#!/bin/sh\nprintf '%s %s\\n' \"$$\" \"$(ps -o pgid= -p $$)\" > \"$AINN_TEST_LOGIN_SESSION\"\nprintf /tmp/ainn-test-path\n"
	if err := os.WriteFile(shellPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shellPath)
	t.Setenv("TMUX", "")
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv("AINN_TEST_LOGIN_SESSION", sessionPath)

	if err := refreshRootSupervisorEnvironment(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[0] != fields[1] {
		t.Fatalf("login shell pid/process group = %q, want a new session led by the shell", strings.TrimSpace(string(data)))
	}
}

func TestRootSupervisorHelperProcess(t *testing.T) {
	mode := os.Getenv(rootSupervisorHelperEnv)
	if mode == "" {
		return
	}
	if mode == "hold-stderr" {
		for {
			time.Sleep(time.Hour)
		}
	}
	fmtPID := strconv.Itoa(os.Getpid()) + "\n"
	_, _ = os.Stdout.WriteString(fmtPID)
	_, _ = os.Stderr.WriteString("Authorization: Bearer supervisor-secret\n")
	if pidPath := os.Getenv(rootSupervisorReadyPIDEnv); pidPath != "" {
		_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0600)
	}
	switch mode {
	case "clean":
		os.Exit(0)
	case "exit23":
		os.Exit(23)
	case "sigkill":
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {}
	case "sigkill-with-descendant":
		descendant := exec.Command(os.Args[0], "-test.run=TestRootSupervisorHelperProcess", "--")
		descendant.Env = rootProcessEnvironment(os.Environ(), map[string]string{rootSupervisorHelperEnv: "hold-stderr"})
		descendant.Stderr = os.Stderr
		if err := descendant.Start(); err != nil {
			os.Exit(96)
		}
		pidPath := os.Getenv(rootSupervisorDescendantPIDEnv)
		if err := os.WriteFile(pidPath, []byte(strconv.Itoa(descendant.Process.Pid)), 0600); err != nil {
			os.Exit(95)
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {}
	case "hold":
		for {
			time.Sleep(time.Hour)
		}
	case "catch-quit":
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGQUIT)
		<-quit
		signal.Stop(quit)
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {}
	default:
		os.Exit(97)
	}
}

func TestConfigureRootChildTerminalIgnoresNonFileInput(t *testing.T) {
	attr := &syscall.SysProcAttr{Setpgid: true}
	restore, err := configureRootChildTerminal(strings.NewReader(""), attr)
	if err != nil {
		t.Fatal(err)
	}
	if attr.Foreground {
		t.Fatal("non-terminal input configured a foreground process group")
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
}

func TestRootSupervisorRecordsCleanChild(t *testing.T) {
	startedAt := time.Date(2026, 7, 12, 13, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(1500 * time.Millisecond)
	restoreClock := setRootSupervisorClockForTest(startedAt, completedAt)
	defer restoreClock()
	t.Setenv(rootSupervisorHelperEnv, "clean")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exit, err := runSupervisedRoot(rootSupervisorTestOptions(t, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err != nil {
		t.Fatalf("parse helper pid %q: %v", stdout.String(), err)
	}
	want := logging.RootRunExit{
		ChildPID:             childPID,
		ExitCode:             0,
		Reason:               logging.RootRunExitReasonClean,
		DurationMilliseconds: 1500,
		CompletedAt:          completedAt,
	}
	if !reflect.DeepEqual(exit, want) {
		t.Fatalf("root exit mismatch:\n got %#v\nwant %#v", exit, want)
	}
	assertSupervisorEvidence(t, rootSupervisorLogDir(t), stderr.String(), "reason=clean", "exit_code=0")
}

func TestRootSupervisorRecordsNonzeroChild(t *testing.T) {
	startedAt := time.Date(2026, 7, 12, 14, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(2 * time.Second)
	restoreClock := setRootSupervisorClockForTest(startedAt, completedAt)
	defer restoreClock()
	t.Setenv(rootSupervisorHelperEnv, "exit23")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exit, err := runSupervisedRoot(rootSupervisorTestOptions(t, &stdout, &stderr))
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("expected exec exit error, got %T %v", err, err)
	}
	childPID, parseErr := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if parseErr != nil {
		t.Fatalf("parse helper pid %q: %v", stdout.String(), parseErr)
	}
	want := logging.RootRunExit{
		ChildPID:             childPID,
		ExitCode:             23,
		Reason:               logging.RootRunExitReasonExitCode,
		Error:                "exit status 23",
		DurationMilliseconds: 2000,
		CompletedAt:          completedAt,
	}
	if !reflect.DeepEqual(exit, want) {
		t.Fatalf("root exit mismatch:\n got %#v\nwant %#v", exit, want)
	}
	assertSupervisorEvidence(t, rootSupervisorLogDir(t), stderr.String(), "reason=exit_code", "exit_code=23", `error="exit status 23"`)
}

func TestRootSupervisorRecordsSignaledChild(t *testing.T) {
	startedAt := time.Date(2026, 7, 12, 15, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(2500 * time.Millisecond)
	restoreClock := setRootSupervisorClockForTest(startedAt, completedAt)
	defer restoreClock()
	t.Setenv(rootSupervisorHelperEnv, "sigkill")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exit, err := runSupervisedRoot(rootSupervisorTestOptions(t, &stdout, &stderr))
	if err == nil {
		t.Fatal("expected signaled child error")
	}
	childPID, parseErr := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if parseErr != nil {
		t.Fatalf("parse helper pid %q: %v", stdout.String(), parseErr)
	}
	want := logging.RootRunExit{
		ChildPID:             childPID,
		ExitCode:             -1,
		Reason:               logging.RootRunExitReasonSignal,
		Error:                "signal: killed",
		Signal:               "killed",
		DurationMilliseconds: 2500,
		CompletedAt:          completedAt,
	}
	if !reflect.DeepEqual(exit, want) {
		t.Fatalf("root exit mismatch:\n got %#v\nwant %#v", exit, want)
	}
	assertSupervisorEvidence(t, rootSupervisorLogDir(t), stderr.String(), "reason=signal", "signal=killed", "exit_code=-1")
}

func TestRootSupervisorDoesNotWaitForDescendantStderr(t *testing.T) {
	startedAt := time.Date(2026, 7, 12, 16, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(3 * time.Second)
	restoreClock := setRootSupervisorClockForTest(startedAt, completedAt)
	defer restoreClock()
	t.Setenv(rootSupervisorHelperEnv, "sigkill-with-descendant")
	descendantPIDPath := filepath.Join(t.TempDir(), "descendant.pid")
	t.Setenv(rootSupervisorDescendantPIDEnv, descendantPIDPath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	opts := rootSupervisorTestOptions(t, &stdout, &stderr)
	type result struct {
		exit logging.RootRunExit
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		exit, err := runSupervisedRoot(opts)
		resultCh <- result{exit: exit, err: err}
	}()

	var descendantPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(descendantPIDPath)
		if err == nil {
			descendantPID, err = strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if descendantPID == 0 {
		t.Fatal("helper did not record descendant pid")
	}

	select {
	case got := <-resultCh:
		if got.err == nil {
			t.Fatal("expected signaled child error")
		}
		childPID, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
		if err != nil {
			t.Fatalf("parse helper pid %q: %v", stdout.String(), err)
		}
		want := logging.RootRunExit{
			ChildPID:             childPID,
			ExitCode:             -1,
			Reason:               logging.RootRunExitReasonSignal,
			Error:                "signal: killed",
			Signal:               "killed",
			DurationMilliseconds: 3000,
			CompletedAt:          completedAt,
		}
		if !reflect.DeepEqual(got.exit, want) {
			t.Fatalf("root exit mismatch:\n got %#v\nwant %#v", got.exit, want)
		}
	case <-time.After(time.Second):
		_ = syscall.Kill(descendantPID, syscall.SIGKILL)
		<-resultCh
		t.Fatal("supervisor waited for stderr held by a descendant")
	}

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(descendantPID, 0); errors.Is(err, syscall.ESRCH) {
			assertSupervisorEvidence(t, rootSupervisorLogDir(t), stderr.String(), "reason=signal", "signal=killed", "exit_code=-1")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = syscall.Kill(descendantPID, syscall.SIGKILL)
	t.Fatal("supervisor left the root descendant running")
}

func TestRootSupervisorForwardsSIGQUIT(t *testing.T) {
	startedAt := time.Date(2026, 7, 12, 16, 30, 0, 0, time.UTC)
	completedAt := startedAt.Add(3 * time.Second)
	restoreClock := setRootSupervisorClockForTest(startedAt, completedAt)
	defer restoreClock()
	t.Setenv(rootSupervisorHelperEnv, "catch-quit")
	readyPIDPath := filepath.Join(t.TempDir(), "ready.pid")
	t.Setenv(rootSupervisorReadyPIDEnv, readyPIDPath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	opts := rootSupervisorTestOptions(t, &stdout, &stderr)
	type result struct {
		exit logging.RootRunExit
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		exit, err := runSupervisedRoot(opts)
		resultCh <- result{exit: exit, err: err}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(readyPIDPath)
		if err == nil && len(data) > 0 {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGQUIT); err != nil {
		t.Fatal(err)
	}

	got := <-resultCh
	if got.err == nil {
		t.Fatal("expected SIGQUIT child error")
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err != nil {
		t.Fatalf("parse helper pid %q: %v", stdout.String(), err)
	}
	want := logging.RootRunExit{
		ChildPID:             childPID,
		ExitCode:             -1,
		Reason:               logging.RootRunExitReasonSignal,
		Error:                "signal: killed",
		Signal:               "killed",
		ForwardedSignal:      "quit",
		DurationMilliseconds: 3000,
		CompletedAt:          completedAt,
	}
	if !reflect.DeepEqual(got.exit, want) {
		t.Fatalf("root exit mismatch:\n got %#v\nwant %#v", got.exit, want)
	}
	assertSupervisorEvidence(t, rootSupervisorLogDir(t), stderr.String(), "reason=signal", "signal=quit", "forwarded_signal=quit")
}

func rootSupervisorTestOptions(t *testing.T, stdout *bytes.Buffer, stderr *bytes.Buffer) RootOptions {
	t.Helper()
	logDir := t.TempDir()
	t.Setenv("AINN_TEST_ROOT_SUPERVISOR_LOG_DIR", logDir)
	return RootOptions{
		ConfigDir:   "/tmp/ainn-config",
		ManagerPort: 19090,
		Config:      config.Config{Settings: config.Settings{LogDir: logDir}},
		ProcessArgs: []string{"-test.run=TestRootSupervisorHelperProcess", "--"},
		Stdin:       strings.NewReader(""),
		Stdout:      stdout,
		Stderr:      stderr,
	}
}

func rootSupervisorLogDir(t *testing.T) string {
	t.Helper()
	return os.Getenv("AINN_TEST_ROOT_SUPERVISOR_LOG_DIR")
}

func setRootSupervisorClockForTest(times ...time.Time) func() {
	previous := rootSupervisorNow
	index := 0
	rootSupervisorNow = func() time.Time {
		value := times[index]
		index++
		return value
	}
	return func() { rootSupervisorNow = previous }
}

func assertSupervisorEvidence(t *testing.T, logDir string, terminal string, wants ...string) {
	t.Helper()
	if !strings.Contains(terminal, "supervisor-secret") {
		t.Fatalf("terminal did not receive child stderr: %q", terminal)
	}
	entries, err := os.ReadDir(filepath.Join(logDir, "crashes"))
	if err != nil {
		t.Fatal(err)
	}
	var artifactPath string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "root-") && strings.Contains(entry.Name(), ".log") {
			artifactPath = filepath.Join(logDir, "crashes", entry.Name())
			break
		}
	}
	if artifactPath == "" {
		t.Fatalf("no crash artifact in %s", logDir)
	}
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "supervisor-secret") {
		t.Fatalf("crash artifact leaked child secret:\n%s", got)
	}
	for _, want := range append([]string{"root.supervisor.start", "root.supervisor.child", "root.supervisor.exit", "Authorization: Bearer ***REDACTED***"}, wants...) {
		if !strings.Contains(got, want) {
			t.Fatalf("crash artifact missing %q:\n%s", want, got)
		}
	}
	if _, err := os.Stat(filepath.Join(logDir, "crashes", "active-root.json")); !os.IsNotExist(err) {
		t.Fatalf("active marker remains after child exit: %v", err)
	}
}
