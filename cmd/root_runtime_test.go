package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
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
		ConfigDir:   "/tmp/runtime-config",
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
