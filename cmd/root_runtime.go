package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/logging"
)

const (
	rootShutdownReasonSignal         = "signal"
	rootShutdownReasonSupervisorLost = "supervisor_lost"
	rootShutdownReasonServerError    = "server_error"
	rootShutdownReasonTUIExit        = "tui_exit"
	rootTUIExitReasonRootSignal      = "root_signal"
	tuiStopWaitDelay                 = 5 * time.Second
	rootStackInitialBytes            = 64 * 1024
	rootStackMaximumBytes            = 8 * 1024 * 1024
)

type rootShutdown struct {
	Signal os.Signal
	Reason string
}

var rootStackDump = dumpAllRootStacks

func setRootStackDumpForTest(dump func(io.Writer)) func() {
	previous := rootStackDump
	rootStackDump = dump
	return func() { rootStackDump = previous }
}

func runRootProcess(opts RootOptions) error {
	shutdowns, stopSignals := rootShutdownSignals()
	defer stopSignals()
	return runRootRuntime(opts, shutdowns)
}

func rootShutdownSignals() (<-chan rootShutdown, func()) {
	shutdowns := make(chan rootShutdown, 1)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	var once sync.Once
	go func() {
		select {
		case sig := <-signals:
			shutdowns <- rootShutdown{Signal: sig, Reason: rootShutdownReasonSignal}
		case <-done:
		}
	}()
	var supervisorFile *os.File
	if value := os.Getenv(rootSupervisorFDEnvVar); value != "" {
		fd, err := strconv.Atoi(value)
		if err == nil {
			supervisorFile = os.NewFile(uintptr(fd), "ainn-root-supervisor")
			go func() {
				_, _ = supervisorFile.Read(make([]byte, 1))
				select {
				case shutdowns <- rootShutdown{Reason: rootShutdownReasonSupervisorLost}:
				case <-done:
				}
			}()
		}
	}
	return shutdowns, func() {
		once.Do(func() {
			signal.Stop(signals)
			close(done)
			if supervisorFile != nil {
				_ = supervisorFile.Close()
			}
		})
	}
}

func runRootRuntime(opts RootOptions, shutdowns <-chan rootShutdown) error {
	logDir := opts.Config.Settings.LogDir
	if logDir == "" {
		logDir = "~/.ainn/logs"
	}
	logDir = expandHome(logDir)
	logPath := filepath.Join(logDir, "ainn.log")
	logWriter, err := logging.NewRotatingWriter(logPath, logging.DefaultRotateMaxBytes, logging.DefaultRotateKeep)
	if err != nil {
		return fmt.Errorf("open root log %s: %w", logPath, err)
	}
	defer logWriter.Close()
	level := opts.Config.Settings.LogLevel
	if level == "" {
		level = "simple"
	}
	rootLogger := logging.New(logWriter, level, logging.ComponentRoot)
	managerLogger := logging.New(logWriter, level, logging.ComponentManagerSuper)
	healthLogger := logging.New(logWriter, level, logging.ComponentManagerHealth)
	if runID := os.Getenv(rootRunIDEnvVar); runID != "" {
		rootLogger = rootLogger.With("run", runID)
		managerLogger = managerLogger.With("run", runID)
		healthLogger = healthLogger.With("run", runID)
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			rootLogger.Error(logging.EventRootPanic, "panic", fmt.Sprint(recovered), "pid", os.Getpid())
			_, _ = fmt.Fprintf(stderr, "AINN root panic: %v\n", recovered)
			rootStackDump(stderr)
			panic(recovered)
		}
	}()

	opts.ManagerLogger = managerLogger
	opts.ManagerHealthLogger = healthLogger
	rootLogger.Info(logging.EventRootStart,
		"pid", os.Getpid(),
		"ppid", os.Getppid(),
		"version", version,
		"go", runtime.Version(),
		"os", runtime.GOOS,
		"arch", runtime.GOARCH,
		"config_dir", opts.ConfigDir,
		"port", opts.ManagerPort,
		"crash_path", os.Getenv(rootCrashPathEnvVar),
	)

	mgr := rootManagerFactory(opts)
	defer mgr.Close()
	startupStatus := ""
	if err := mgr.StartConfiguredWorkers(); err != nil {
		startupStatus = err.Error()
	}
	stopHealthMonitor := mgr.StartHealthMonitor(0)
	defer stopHealthMonitor()
	stopUpstreamProber := mgr.StartUpstreamProber(0)
	defer stopUpstreamProber()
	stopHostedTurnWatcher := mgr.StartHostedTurnWatcher(0)
	defer stopHostedTurnWatcher()
	addr := constants.LocalhostAddr + ":" + strconv.Itoa(opts.ManagerPort)
	server := rootServerFactory(addr, mgr)
	defer server.Close()
	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	program := rootProgramFactory(addr, startupStatus, opts.ConfigDir)
	rootLogger.Info(logging.EventTUIStart, "command", strings.Join(program.CommandLine(), " "))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	programDone := make(chan error, 1)
	go func() { programDone <- program.Run(ctx) }()

	for {
		select {
		case cause := <-shutdowns:
			rootLogger.Warn(logging.EventRootSignal, "reason", cause.Reason, "signal", signalName(cause.Signal))
			if cause.Signal == syscall.SIGHUP {
				rootLogger.Info(logging.EventRootStack, "reason", "hangup")
				rootStackDump(stderr)
			}
			cancel()
			tuiErr := <-programDone
			logTUIExit(rootLogger, tuiErr, rootTUIExitReasonRootSignal)
			rootLogger.Info(logging.EventRootStop, "reason", cause.Reason, "signal", signalName(cause.Signal))
			return nil
		case tuiErr := <-programDone:
			logTUIExit(rootLogger, tuiErr, rootShutdownReasonTUIExit)
			select {
			case serverErr := <-errCh:
				rootLogger.Error(logging.EventRootServerError, "err", serverErr.Error())
				rootLogger.Error(logging.EventRootStop, "reason", rootShutdownReasonServerError, "err", serverErr.Error())
				return serverErr
			default:
				if tuiErr != nil {
					rootLogger.Error(logging.EventRootStop, "reason", rootShutdownReasonTUIExit, "err", tuiErr.Error())
					return tuiErr
				}
				rootLogger.Info(logging.EventRootStop, "reason", rootShutdownReasonTUIExit)
				return nil
			}
		case serverErr := <-errCh:
			rootLogger.Error(logging.EventRootServerError, "err", serverErr.Error())
			cancel()
			tuiErr := <-programDone
			logTUIExit(rootLogger, tuiErr, rootShutdownReasonServerError)
			rootLogger.Error(logging.EventRootStop, "reason", rootShutdownReasonServerError, "err", serverErr.Error())
			return serverErr
		}
	}
}

func logTUIExit(logger *slog.Logger, err error, reason string) {
	args := []any{"reason", reason, "exit_code", 0, "signal", ""}
	if err != nil {
		args = []any{"reason", reason, "exit_code", -1, "signal", "", "err", err.Error()}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
			args[3] = exitErr.ProcessState.ExitCode()
			if status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				args[5] = status.Signal().String()
			}
		}
		logger.Error(logging.EventTUIExit, args...)
		return
	}
	logger.Info(logging.EventTUIExit, args...)
}

func signalName(signal os.Signal) string {
	if signal == nil {
		return ""
	}
	return signal.String()
}

func dumpAllRootStacks(w io.Writer) {
	bufferSize := rootStackInitialBytes
	for {
		buffer := make([]byte, bufferSize)
		n := runtime.Stack(buffer, true)
		if n < len(buffer) || bufferSize >= rootStackMaximumBytes {
			_, _ = w.Write(buffer[:n])
			return
		}
		bufferSize *= 2
	}
}

func (p *tuiProgram) Run(ctx context.Context) error {
	line := p.CommandLine()
	cmd := exec.CommandContext(ctx, line[0], line[1:]...)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = tuiStopWaitDelay
	cmd.Dir = p.WorkingDir()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = rootProcessEnvironment(os.Environ(), p.Env())
	return cmd.Run()
}
