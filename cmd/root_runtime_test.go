package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
)

func TestRootRuntimeCancelsTUIOnSignal(t *testing.T) {
	logDir := t.TempDir()
	t.Setenv(rootRunIDEnvVar, "runtime-test")
	program := &blockingRuntimeProgram{started: make(chan struct{})}
	manager := &fakeRootManager{}
	server := &fakeRootServer{listenStarted: make(chan struct{})}
	restoreManager := setRootManagerFactoryForTest(func(RootOptions) rootManager { return manager })
	defer restoreManager()
	restoreServer := setRootServerFactoryForTest(func(addr string, handler http.Handler) rootServer {
		server.addr = addr
		server.handler = handler
		return server
	})
	defer restoreServer()
	restoreProgram := setRootProgramFactoryForTest(func(string) rootProgram { return program })
	defer restoreProgram()

	var stderr bytes.Buffer
	shutdowns := make(chan rootShutdown, 1)
	result := make(chan error, 1)
	go func() {
		result <- runRootRuntime(rootRuntimeTestOptions(logDir, &stderr), shutdowns)
	}()
	select {
	case <-program.started:
	case <-time.After(time.Second):
		t.Fatal("TUI program did not start")
	}
	shutdowns <- rootShutdown{Signal: syscall.SIGTERM, Reason: rootShutdownReasonSignal}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("root runtime returned error after signal: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("root runtime did not stop after signal")
	}

	log := readRootRuntimeLog(t, logDir)
	for _, want := range []string{
		"root.start",
		"run=runtime-test",
		"root.signal",
		"signal=terminated",
		"tui.exit",
		"reason=root_signal",
		"root.stop",
		"reason=signal",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("root log missing %q:\n%s", want, log)
		}
	}
}

func TestRootRuntimeStopsWatcherBeforeReleasingOwnership(t *testing.T) {
	logDir := t.TempDir()
	manager := &fakeRootManager{watcherStopped: make(chan struct{})}
	server := &fakeRootServer{listenStarted: make(chan struct{})}
	program := &blockingRuntimeProgram{started: make(chan struct{})}
	sequence := []string{}
	restoreManager := setRootManagerFactoryForTest(func(RootOptions) rootManager { return manager })
	defer restoreManager()
	restoreServer := setRootServerFactoryForTest(func(addr string, handler http.Handler) rootServer {
		server.addr = addr
		server.handler = handler
		return server
	})
	defer restoreServer()
	restoreProgram := setRootProgramFactoryForTest(func(string) rootProgram { return program })
	defer restoreProgram()
	previousLockerFactory := rootLockerFactory
	rootLockerFactory = func(path string) rootLocker {
		if strings.HasSuffix(path, "-hosted-watcher.lock") {
			return sequenceLocker{sequence: &sequence, acquire: "watcher lock acquire", release: "watcher lock release"}
		}
		return noopLocker{}
	}
	defer func() { rootLockerFactory = previousLockerFactory }()
	manager.stopWatcher = func() {
		sequence = append(sequence, "watcher stop")
		close(manager.watcherStopped)
	}

	shutdowns := make(chan rootShutdown, 1)
	result := make(chan error, 1)
	go func() { result <- runRootRuntime(rootRuntimeTestOptions(logDir, io.Discard), shutdowns) }()
	select {
	case <-program.started:
	case <-time.After(time.Second):
		t.Fatal("TUI program did not start")
	}
	shutdowns <- rootShutdown{Signal: syscall.SIGTERM, Reason: rootShutdownReasonSignal}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("root runtime did not stop")
	}
	want := []string{"watcher lock acquire", "watcher stop", "watcher lock release"}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("got sequence %#v, want %#v", sequence, want)
	}
}

func TestRootRuntimeFailsWhenWatcherHandoffTimesOut(t *testing.T) {
	logDir := t.TempDir()
	manager := &fakeRootManager{}
	restoreManager := setRootManagerFactoryForTest(func(RootOptions) rootManager { return manager })
	defer restoreManager()
	previousLockerFactory := rootLockerFactory
	rootLockerFactory = func(path string) rootLocker {
		if strings.HasSuffix(path, "-hosted-watcher.lock") {
			return lockedLocker{}
		}
		return noopLocker{}
	}
	defer func() { rootLockerFactory = previousLockerFactory }()
	previousTimeout := hostedWatcherHandoffTimeout
	hostedWatcherHandoffTimeout = 20 * time.Millisecond
	defer func() { hostedWatcherHandoffTimeout = previousTimeout }()

	err := runRootRuntime(rootRuntimeTestOptions(logDir, io.Discard), make(chan rootShutdown))
	if err == nil || !strings.Contains(err.Error(), "acquire hosted watcher lock") {
		t.Fatalf("got %v, want explicit watcher handoff timeout", err)
	}
	if manager.startHostedTurnWatcherCalled {
		t.Fatal("watcher started without ownership")
	}
	log := readRootRuntimeLog(t, logDir)
	if !strings.Contains(log, "hosted_turn.ownership") || !strings.Contains(log, "category=handoff_timeout") {
		t.Fatalf("handoff ownership event missing: %s", log)
	}
	if strings.Contains(log, "another instance is already running") {
		t.Fatalf("raw lock error leaked into ownership log: %s", log)
	}
}

func TestRootRuntimeWaitsForSidecarWatcherHandoff(t *testing.T) {
	logDir := t.TempDir()
	sequence := []string{}
	manager := &fakeRootManager{stopWatcher: func() { sequence = append(sequence, "root watcher stop") }}
	server := &fakeRootServer{listenStarted: make(chan struct{})}
	program := &fakeRootProgram{waitForListen: server.listenStarted}
	restoreManager := setRootManagerFactoryForTest(func(RootOptions) rootManager { return manager })
	defer restoreManager()
	restoreServer := setRootServerFactoryForTest(func(addr string, handler http.Handler) rootServer {
		server.addr = addr
		server.handler = handler
		return server
	})
	defer restoreServer()
	restoreProgram := setRootProgramFactoryForTest(func(string) rootProgram { return program })
	defer restoreProgram()
	watcherAttempts := 0
	previousLockerFactory := rootLockerFactory
	rootLockerFactory = func(path string) rootLocker {
		if strings.HasSuffix(path, "-hosted-watcher.lock") {
			watcherAttempts++
			if watcherAttempts == 1 {
				sequence = append(sequence, "sidecar owns watcher")
				return lockedLocker{}
			}
			return sequenceLocker{sequence: &sequence, acquire: "root watcher acquire", release: "root watcher release"}
		}
		return noopLocker{}
	}
	defer func() { rootLockerFactory = previousLockerFactory }()

	if err := runRootRuntime(rootRuntimeTestOptions(logDir, io.Discard), make(chan rootShutdown)); err != nil {
		t.Fatal(err)
	}
	want := []string{"sidecar owns watcher", "root watcher acquire", "root watcher stop", "root watcher release"}
	if watcherAttempts != 2 || !reflect.DeepEqual(sequence, want) {
		t.Fatalf("attempts=%d sequence=%#v, want 2 and %#v", watcherAttempts, sequence, want)
	}
}

func TestRootRuntimeRecordsTUIExitCode(t *testing.T) {
	logDir := t.TempDir()
	t.Setenv(rootRunIDEnvVar, "runtime-test")
	program := &exitRuntimeProgram{err: commandExitError(t, 7)}
	manager := &fakeRootManager{}
	server := &fakeRootServer{listenStarted: make(chan struct{})}
	restoreManager := setRootManagerFactoryForTest(func(RootOptions) rootManager { return manager })
	defer restoreManager()
	restoreServer := setRootServerFactoryForTest(func(addr string, handler http.Handler) rootServer {
		server.addr = addr
		server.handler = handler
		return server
	})
	defer restoreServer()
	restoreProgram := setRootProgramFactoryForTest(func(string) rootProgram { return program })
	defer restoreProgram()

	err := runRootRuntime(rootRuntimeTestOptions(logDir, io.Discard), make(chan rootShutdown))
	if err == nil {
		t.Fatal("expected TUI exit error")
	}
	log := readRootRuntimeLog(t, logDir)
	for _, want := range []string{
		"tui.exit",
		"reason=tui_exit",
		"exit_code=7",
		"root.stop",
		"reason=tui_exit",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("root log missing %q:\n%s", want, log)
		}
	}
}

func TestRootRuntimeLogsAndRepanics(t *testing.T) {
	logDir := t.TempDir()
	t.Setenv(rootRunIDEnvVar, "runtime-test")
	managerPanic := errors.New("manager construction panic")
	restoreManager := setRootManagerFactoryForTest(func(RootOptions) rootManager { panic(managerPanic) })
	defer restoreManager()
	var stderr bytes.Buffer
	func() {
		defer func() {
			if recovered := recover(); recovered != managerPanic {
				t.Fatalf("panic = %v, want %v", recovered, managerPanic)
			}
		}()
		_ = runRootRuntime(rootRuntimeTestOptions(logDir, &stderr), make(chan rootShutdown))
	}()
	log := readRootRuntimeLog(t, logDir)
	if !strings.Contains(log, "root.panic") || !strings.Contains(log, `panic="manager construction panic"`) {
		t.Fatalf("root panic event missing:\n%s", log)
	}
	if !strings.Contains(stderr.String(), "manager construction panic") || !strings.Contains(stderr.String(), "root_runtime_test.go") {
		t.Fatalf("panic stack missing from stderr: %q", stderr.String())
	}
}

func TestRootRuntimeDumpsAllStacksForHangup(t *testing.T) {
	logDir := t.TempDir()
	t.Setenv(rootRunIDEnvVar, "runtime-test")
	program := &blockingRuntimeProgram{started: make(chan struct{})}
	manager := &fakeRootManager{}
	server := &fakeRootServer{listenStarted: make(chan struct{})}
	restoreManager := setRootManagerFactoryForTest(func(RootOptions) rootManager { return manager })
	defer restoreManager()
	restoreServer := setRootServerFactoryForTest(func(addr string, handler http.Handler) rootServer {
		server.addr = addr
		server.handler = handler
		return server
	})
	defer restoreServer()
	restoreProgram := setRootProgramFactoryForTest(func(string) rootProgram { return program })
	defer restoreProgram()
	stackCalled := false
	restoreStack := setRootStackDumpForTest(func(io.Writer) { stackCalled = true })
	defer restoreStack()

	result := make(chan error, 1)
	shutdowns := make(chan rootShutdown, 1)
	go func() { result <- runRootRuntime(rootRuntimeTestOptions(logDir, io.Discard), shutdowns) }()
	select {
	case <-program.started:
	case <-time.After(time.Second):
		t.Fatal("TUI program did not start")
	}
	shutdowns <- rootShutdown{Signal: syscall.SIGHUP, Reason: rootShutdownReasonSignal}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("root runtime did not stop after SIGHUP")
	}
	if !stackCalled {
		t.Fatal("expected all-goroutine stack dump on SIGHUP")
	}
}

func TestRootRuntimeRejectsUnavailableLogPath(t *testing.T) {
	logPath := t.TempDir() + "/not-a-directory"
	if err := os.WriteFile(logPath, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	err := runRootRuntime(RootOptions{
		ConfigDir:   "/tmp/runtime-config",
		ManagerPort: 19090,
		Config:      config.Config{Settings: config.Settings{LogDir: logPath}},
		Stdout:      io.Discard,
		Stderr:      io.Discard,
	}, make(chan rootShutdown))
	if err == nil || !strings.Contains(err.Error(), logPath) {
		t.Fatalf("expected configured log path error, got %v", err)
	}
}

type blockingRuntimeProgram struct {
	started chan struct{}
}

func (p *blockingRuntimeProgram) Run(ctx context.Context) error {
	close(p.started)
	<-ctx.Done()
	return nil
}

func (p *blockingRuntimeProgram) CommandLine() []string  { return []string{"blocking"} }
func (p *blockingRuntimeProgram) WorkingDir() string     { return "" }
func (p *blockingRuntimeProgram) Env() map[string]string { return nil }

type exitRuntimeProgram struct {
	err error
}

func (p *exitRuntimeProgram) Run(context.Context) error { return p.err }
func (p *exitRuntimeProgram) CommandLine() []string     { return []string{"exit"} }
func (p *exitRuntimeProgram) WorkingDir() string        { return "" }
func (p *exitRuntimeProgram) Env() map[string]string    { return nil }

func commandExitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	if err == nil {
		t.Fatalf("expected helper exit error")
	}
	return err
}

func rootRuntimeTestOptions(logDir string, stderr io.Writer) RootOptions {
	return RootOptions{
		ConfigDir:   logDir,
		ManagerPort: 19090,
		Config:      config.Config{Settings: config.Settings{LogDir: logDir}},
		Stdin:       strings.NewReader(""),
		Stdout:      io.Discard,
		Stderr:      stderr,
	}
}

func readRootRuntimeLog(t *testing.T, logDir string) string {
	t.Helper()
	data, err := os.ReadFile(logDir + "/ainn.log")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
