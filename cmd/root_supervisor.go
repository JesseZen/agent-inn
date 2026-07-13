package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jesse/agent-inn/internal/logging"
)

const (
	rootProcessEnvVar      = "AINN_ROOT_PROCESS"
	rootRunIDEnvVar        = "AINN_RUN_ID"
	rootCrashPathEnvVar    = "AINN_CRASH_PATH"
	rootSupervisorFDEnvVar = "AINN_SUPERVISOR_FD"
	rootSupervisorFD       = 3
	rootStderrBufferBytes  = 32 * 1024
	rootRestartExitCode    = 75
	rootLoginPathCommand   = `printf %s "$PATH"`
)

var (
	rootSupervisorNow                = time.Now
	rootSupervisorExecutable         = os.Executable
	rootSupervisor                   = superviseRoot
	rootSupervisedRunner             = runSupervisedRoot
	rootSupervisorRefreshEnvironment = refreshRootSupervisorEnvironment
)

func superviseRoot(opts RootOptions) error {
	for {
		exit, err := rootSupervisedRunner(opts)
		if exit.ExitCode != rootRestartExitCode {
			return err
		}
		if err := rootSupervisorRefreshEnvironment(); err != nil {
			return err
		}
	}
}

func refreshRootSupervisorEnvironment() error {
	loginShell := exec.Command(os.Getenv("SHELL"), "-lic", rootLoginPathCommand)
	loginShell.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	pathOutput, err := loginShell.Output()
	if err != nil {
		return fmt.Errorf("refresh login shell PATH: %w", err)
	}
	pathValue := string(pathOutput)
	if err := os.Setenv("PATH", pathValue); err != nil {
		return fmt.Errorf("refresh supervisor PATH: %w", err)
	}
	tmuxValue := os.Getenv("TMUX")
	if tmuxValue == "" {
		return nil
	}
	tmuxSocket, _, _ := strings.Cut(tmuxValue, ",")
	if output, err := exec.Command("tmux", "-S", tmuxSocket, "set-environment", "-g", "PATH", pathValue).CombinedOutput(); err != nil {
		return fmt.Errorf("refresh tmux PATH: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func runSupervisedRoot(opts RootOptions) (logging.RootRunExit, error) {
	startedAt := rootSupervisorNow()
	runID, err := logging.NewRunID()
	if err != nil {
		return logging.RootRunExit{}, err
	}
	executable, err := rootSupervisorExecutable()
	if err != nil {
		return logging.RootRunExit{}, fmt.Errorf("locate root executable: %w", err)
	}
	supervisorRead, supervisorWrite, err := os.Pipe()
	if err != nil {
		return logging.RootRunExit{}, fmt.Errorf("create root supervisor pipe: %w", err)
	}
	defer supervisorWrite.Close()
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		_ = supervisorRead.Close()
		return logging.RootRunExit{}, fmt.Errorf("create root stderr pipe: %w", err)
	}
	defer stderrRead.Close()
	defer stderrWrite.Close()

	logDir := expandHome(opts.Config.Settings.LogDir)
	artifact, _, err := logging.OpenCrashArtifact(logDir, logging.RootRunMetadata{
		RunID:         runID,
		SupervisorPID: os.Getpid(),
		StartedAt:     startedAt,
		Version:       version,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		ConfigDir:     opts.ConfigDir,
		ManagerPort:   opts.ManagerPort,
	})
	if err != nil {
		_ = supervisorRead.Close()
		return logging.RootRunExit{}, err
	}

	terminalStderr := opts.Stderr
	if terminalStderr == nil {
		terminalStderr = io.Discard
	}
	cmd := exec.Command(executable, opts.ProcessArgs...)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = stderrWrite
	cmd.ExtraFiles = []*os.File{supervisorRead}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	restoreTerminal, terminalErr := configureRootChildTerminal(opts.Stdin, cmd.SysProcAttr)
	if terminalErr != nil {
		artifact.Logger().Warn(logging.EventRootSupervisorChild, "terminal", "configure", "err", terminalErr.Error())
	}
	cmd.Env = rootProcessEnvironment(os.Environ(), map[string]string{
		rootProcessEnvVar:      "1",
		rootRunIDEnvVar:        runID,
		rootCrashPathEnvVar:    artifact.Path(),
		rootSupervisorFDEnvVar: strconv.Itoa(rootSupervisorFD),
	})
	if err := cmd.Start(); err != nil {
		if restoreErr := restoreTerminal(); restoreErr != nil {
			artifact.Logger().Warn(logging.EventRootSupervisorChild, "terminal", "restore_after_start_error", "err", restoreErr.Error())
		}
		_ = supervisorRead.Close()
		completedAt := rootSupervisorNow()
		exit := logging.RootRunExit{
			ExitCode:             -1,
			Reason:               logging.RootRunExitReasonStartError,
			Error:                err.Error(),
			DurationMilliseconds: completedAt.Sub(startedAt).Milliseconds(),
			CompletedAt:          completedAt,
		}
		if completeErr := artifact.Complete(exit); completeErr != nil {
			_ = artifact.Close()
			return exit, completeErr
		}
		if closeErr := artifact.Close(); closeErr != nil {
			return exit, closeErr
		}
		return exit, err
	}
	_ = supervisorRead.Close()
	_ = stderrWrite.Close()
	artifact.Logger().Info(logging.EventRootSupervisorChild, "child_pid", cmd.Process.Pid)
	stderrResult := make(chan error, 1)
	go func() {
		defer stderrRead.Close()
		buffer := make([]byte, rootStderrBufferBytes)
		for {
			n, readErr := stderrRead.Read(buffer)
			if n > 0 {
				if _, err := artifact.Writer().Write(buffer[:n]); err != nil {
					stderrResult <- err
					return
				}
				_, _ = terminalStderr.Write(buffer[:n])
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					readErr = nil
				}
				stderrResult <- readErr
				return
			}
		}
	}()

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- cmd.Wait()
	}()
	stopSignals := make(chan os.Signal, 4)
	signal.Notify(stopSignals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(stopSignals)
	forwardedSignal := ""
	var waitErr error

waitForChild:
	for {
		select {
		case sig := <-stopSignals:
			signalName := sig.String()
			if forwardedSignal == "" {
				forwardedSignal = signalName
			}
			artifact.Logger().Warn(logging.EventRootSupervisorSignal, "signal", signalName, "child_pid", cmd.Process.Pid)
			signalValue := sig.(syscall.Signal)
			if err := syscall.Kill(-cmd.Process.Pid, signalValue); err != nil && !errors.Is(err, syscall.ESRCH) {
				artifact.Logger().Error(logging.EventRootSupervisorSignal, "signal", signalName, "child_pid", cmd.Process.Pid, "err", err.Error())
			}
		case waitErr = <-waitResult:
			break waitForChild
		}
	}
	if err := restoreTerminal(); err != nil {
		artifact.Logger().Warn(logging.EventRootSupervisorChild, "terminal", "restore", "err", err.Error())
	}
	groupErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(groupErr, syscall.ESRCH) {
		groupErr = nil
	}
	if groupErr != nil {
		_ = stderrRead.Close()
	}
	stderrErr := <-stderrResult
	if errors.Is(stderrErr, os.ErrClosed) && groupErr != nil {
		stderrErr = nil
	}
	runErr := waitErr
	if groupErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("terminate root process group: %w", groupErr))
	}
	if stderrErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("capture root stderr: %w", stderrErr))
	}

	completedAt := rootSupervisorNow()
	exit := logging.RootRunExit{
		ChildPID:             cmd.Process.Pid,
		ExitCode:             0,
		Reason:               logging.RootRunExitReasonClean,
		ForwardedSignal:      forwardedSignal,
		DurationMilliseconds: completedAt.Sub(startedAt).Milliseconds(),
		CompletedAt:          completedAt,
	}
	if runErr != nil {
		exit.ExitCode = -1
		exit.Reason = logging.RootRunExitReasonWaitError
		exit.Error = runErr.Error()
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) && exitError.ProcessState != nil {
			exit.ExitCode = exitError.ProcessState.ExitCode()
			if status, ok := exitError.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				exit.Reason = logging.RootRunExitReasonSignal
				exit.Signal = status.Signal().String()
			} else {
				exit.Reason = logging.RootRunExitReasonExitCode
			}
		}
	}
	if err := artifact.Complete(exit); err != nil {
		_ = artifact.Close()
		return exit, err
	}
	if err := artifact.Close(); err != nil {
		return exit, err
	}
	return exit, runErr
}

func rootProcessEnvironment(current []string, values map[string]string) []string {
	out := make([]string, 0, len(current)+len(values))
	for _, entry := range current {
		name, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replaced := values[name]; replaced {
				continue
			}
		}
		out = append(out, entry)
	}
	for name, value := range values {
		out = append(out, name+"="+value)
	}
	return out
}
