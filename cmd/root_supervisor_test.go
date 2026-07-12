package cmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/logging"
)

const rootSupervisorHelperEnv = "AINN_TEST_ROOT_SUPERVISOR_HELPER"

func TestRootSupervisorHelperProcess(t *testing.T) {
	mode := os.Getenv(rootSupervisorHelperEnv)
	if mode == "" {
		return
	}
	fmtPID := strconv.Itoa(os.Getpid()) + "\n"
	_, _ = os.Stdout.WriteString(fmtPID)
	_, _ = os.Stderr.WriteString("Authorization: Bearer supervisor-secret\n")
	switch mode {
	case "clean":
		os.Exit(0)
	case "exit23":
		os.Exit(23)
	case "sigkill":
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {}
	default:
		os.Exit(97)
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
