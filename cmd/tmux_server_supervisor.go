package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/logging"
)

const (
	tmuxServerStartupTimeout              = 5 * time.Second
	tmuxServerStartupPollInterval         = 20 * time.Millisecond
	tmuxServerOutputTailBytes             = 32 * 1024
	tmuxServerOutputRedactionOverlapBytes = 4 * 1024
	tmuxServerResponseFD                  = 3
	tmuxServerStartupControlFD            = 4
	tmuxServerStartupControlAck           = byte(1)
	tmuxServerDefaultTmpDir               = "/tmp"
)

var (
	tmuxServerCommandTimeout = 5 * time.Second
	tmuxServerForwardSignal  = func(process *os.Process, signal os.Signal) error { return process.Signal(signal) }
)

type tmuxServerExitReason string
type tmuxServerInitiator string

const (
	tmuxServerExitReasonClean      tmuxServerExitReason = "clean"
	tmuxServerExitReasonExitCode   tmuxServerExitReason = "exit_code"
	tmuxServerExitReasonSignal     tmuxServerExitReason = "signal"
	tmuxServerExitReasonStartError tmuxServerExitReason = "start_error"
	tmuxServerExitReasonWaitError  tmuxServerExitReason = "wait_error"

	tmuxServerInitiatorAINN     tmuxServerInitiator = "ainn"
	tmuxServerInitiatorExternal tmuxServerInitiator = "external_or_unknown"
)

type tmuxServerOutputTail struct {
	mu   sync.Mutex
	data []byte
}

func (w *tmuxServerOutputTail) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data = append(w.data, p...)
	storageLimit := tmuxServerOutputTailBytes + tmuxServerOutputRedactionOverlapBytes
	if len(w.data) > storageLimit {
		w.data = append([]byte(nil), w.data[len(w.data)-storageLimit:]...)
	}
	return len(p), nil
}

func (w *tmuxServerOutputTail) RedactedString() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	output := logging.Redact(string(w.data))
	if len(output) > tmuxServerOutputTailBytes {
		output = output[len(output)-tmuxServerOutputTailBytes:]
	}
	return output
}

func parseTmuxServerStartRequest(args []string, stderr io.Writer) (tmuxServerStartRequest, error) {
	flags := flag.NewFlagSet("tmux-server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", "", "config directory")
	logDir := flags.String("log-dir", "", "tmux lifecycle log directory")
	socketName := flags.String("socket", "", "tmux socket name")
	hostSession := flags.String("host-session", "", "tmux host session")
	if err := flags.Parse(args); err != nil {
		return tmuxServerStartRequest{}, err
	}
	initialCommand := flags.Args()
	if *configDir == "" || *logDir == "" || *socketName == "" || *hostSession == "" || len(initialCommand) == 0 {
		return tmuxServerStartRequest{}, errors.New("tmux-server requires --config-dir, --log-dir, --socket, --host-session, and an initial command after --")
	}
	return tmuxServerStartRequest{
		ConfigDir:      *configDir,
		LogDir:         *logDir,
		SocketName:     *socketName,
		HostSession:    *hostSession,
		InitialCommand: append([]string(nil), initialCommand...),
	}, nil
}

func runTmuxServer(args []string, stderr io.Writer) int {
	request, err := parseTmuxServerStartRequest(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	responseWriter := os.NewFile(tmuxServerResponseFD, "tmux-server-response")
	if responseWriter == nil {
		fmt.Fprintln(stderr, "tmux-server response fd is unavailable")
		return 1
	}
	defer responseWriter.Close()
	var startupControl io.ReadCloser
	controlCandidate := os.NewFile(tmuxServerStartupControlFD, "tmux-server-control")
	if controlCandidate != nil {
		if _, statErr := controlCandidate.Stat(); statErr == nil {
			startupControl = controlCandidate
		} else {
			_ = controlCandidate.Close()
		}
	}
	if err := superviseTmuxServerWithControl(request, responseWriter, startupControl); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func superviseTmuxServer(request tmuxServerStartRequest, responseWriter io.Writer) error {
	stopSignals := make(chan os.Signal, 4)
	signal.Notify(stopSignals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(stopSignals)
	return superviseTmuxServerWithSignalsAndControl(request, responseWriter, stopSignals, nil)
}

func superviseTmuxServerWithSignals(request tmuxServerStartRequest, responseWriter io.Writer, stopSignals <-chan os.Signal) error {
	return superviseTmuxServerWithSignalsAndControl(request, responseWriter, stopSignals, nil)
}

func superviseTmuxServerWithControl(request tmuxServerStartRequest, responseWriter io.Writer, startupControl io.ReadCloser) error {
	stopSignals := make(chan os.Signal, 4)
	signal.Notify(stopSignals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(stopSignals)
	return superviseTmuxServerWithSignalsAndControl(request, responseWriter, stopSignals, startupControl)
}

func superviseTmuxServerWithSignalsAndControl(request tmuxServerStartRequest, responseWriter io.Writer, stopSignals <-chan os.Signal, startupControl io.ReadCloser) error {
	settings := config.Settings{
		LogDir: request.LogDir,
		Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{
			SocketName:  request.SocketName,
			HostSession: request.HostSession,
		}},
	}
	logFile, logger, err := openTmuxLifecycleLogger(settings)
	if err != nil {
		_ = json.NewEncoder(responseWriter).Encode(tmuxServerStartResponse{Error: err.Error()})
		return err
	}
	defer logFile.Close()

	startedAt := time.Now()
	tmuxCmd := exec.Command("tmux", "-D", "-L", request.SocketName)
	ptyFile, err := pty.Start(tmuxCmd)
	if err != nil {
		startErr := fmt.Errorf("start foreground tmux server: %w", err)
		logger.Error(logging.EventTmuxServerExit,
			"pid", 0,
			"exit_code", -1,
			"reason", string(tmuxServerExitReasonStartError),
			"signal", "",
			"initiator", string(tmuxServerInitiatorExternal),
			"duration_ms", time.Since(startedAt).Milliseconds(),
			"completed_at", time.Now().UTC().Format(time.RFC3339Nano),
			"error", startErr.Error(),
		)
		_ = json.NewEncoder(responseWriter).Encode(tmuxServerStartResponse{Error: startErr.Error()})
		return startErr
	}
	defer ptyFile.Close()

	var outputTail tmuxServerOutputTail
	outputDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&outputTail, ptyFile)
		close(outputDone)
	}()
	var startupControlResult <-chan bool
	if startupControl != nil {
		controlResult := make(chan bool, 1)
		startupControlResult = controlResult
		defer startupControl.Close()
		go func() {
			var ack [1]byte
			readCount, readErr := io.ReadFull(startupControl, ack[:])
			controlResult <- readErr == nil && readCount == len(ack) && ack[0] == tmuxServerStartupControlAck
		}()
	}
	startupControlReceived := false
	startupControlAcknowledged := false
	collectTmuxServerExit := func(waitErr error, initiator tmuxServerInitiator, extraErr error) ([]any, error) {
		_ = ptyFile.Close()
		<-outputDone
		completedAt := time.Now()
		exitCode := 0
		reason := tmuxServerExitReasonClean
		signalName := ""
		if waitErr != nil {
			exitCode = -1
			reason = tmuxServerExitReasonWaitError
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) && exitErr.ProcessState != nil {
				exitCode = exitErr.ProcessState.ExitCode()
				if status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
					reason = tmuxServerExitReasonSignal
					signalName = status.Signal().String()
				} else {
					reason = tmuxServerExitReasonExitCode
				}
			}
		}
		logArgs := []any{
			"pid", tmuxCmd.Process.Pid,
			"exit_code", exitCode,
			"reason", string(reason),
			"signal", signalName,
			"initiator", string(initiator),
			"duration_ms", completedAt.Sub(startedAt).Milliseconds(),
			"completed_at", completedAt.UTC().Format(time.RFC3339Nano),
		}
		if extraErr != nil {
			logArgs = append(logArgs, "error", extraErr.Error())
		} else if waitErr != nil {
			logArgs = append(logArgs, "error", waitErr.Error())
		}
		if output := strings.TrimSpace(outputTail.RedactedString()); output != "" {
			logArgs = append(logArgs, "output_tail", output)
		}
		return logArgs, errors.Join(extraErr, waitErr)
	}

	logger.Info(logging.EventTmuxServerStart,
		"pid", tmuxCmd.Process.Pid,
		"supervisor_pid", os.Getpid(),
		"socket", request.SocketName,
		"host_session", request.HostSession,
		"config_dir", request.ConfigDir,
		"started_at", startedAt.UTC().Format(time.RFC3339Nano),
	)

	tmuxTmpDir := os.Getenv("TMUX_TMPDIR")
	if tmuxTmpDir == "" {
		tmuxTmpDir = tmuxServerDefaultTmpDir
	}
	socketPath := filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()), request.SocketName)
	deadline := time.Now().Add(tmuxServerStartupTimeout)
	for {
		if startupControlResult != nil && !startupControlReceived {
			select {
			case startupControlAcknowledged = <-startupControlResult:
				startupControlReceived = true
				if !startupControlAcknowledged {
					err = errors.New("tmux supervisor startup client disconnected")
				}
			default:
			}
		}
		if err != nil {
			break
		}
		if connection, dialErr := net.DialTimeout("unix", socketPath, tmuxServerStartupPollInterval); dialErr == nil {
			_ = connection.Close()
			break
		}
		if time.Now().After(deadline) {
			err = fmt.Errorf("wait for tmux server socket %s: timeout", socketPath)
			break
		}
		time.Sleep(tmuxServerStartupPollInterval)
	}

	var initialStdout bytes.Buffer
	var initialStderr bytes.Buffer
	if err == nil {
		commandContext, cancelCommand := context.WithTimeout(context.Background(), tmuxServerCommandTimeout)
		initialCmd := exec.CommandContext(commandContext, request.InitialCommand[0], request.InitialCommand[1:]...)
		initialCmd.Stdout = &initialStdout
		initialCmd.Stderr = &initialStderr
		err = initialCmd.Run()
		if commandContext.Err() != nil {
			err = fmt.Errorf("run initial tmux command: %w", commandContext.Err())
		}
		cancelCommand()
		if err != nil && strings.TrimSpace(initialStderr.String()) != "" {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(initialStderr.String()))
		}
	}
	if err == nil {
		commandContext, cancelCommand := context.WithTimeout(context.Background(), tmuxServerCommandTimeout)
		var exitEmptyStderr bytes.Buffer
		exitEmptyCmd := exec.CommandContext(commandContext, "tmux", "-L", request.SocketName, "set-option", "-g", "exit-empty", "on")
		exitEmptyCmd.Stderr = &exitEmptyStderr
		err = exitEmptyCmd.Run()
		if commandContext.Err() != nil {
			err = fmt.Errorf("set tmux exit-empty: %w", commandContext.Err())
		}
		cancelCommand()
		if err != nil && strings.TrimSpace(exitEmptyStderr.String()) != "" {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(exitEmptyStderr.String()))
		}
	}
	if err != nil {
		logger.Warn(logging.EventTmuxServerSignal,
			"pid", tmuxCmd.Process.Pid,
			"signal", syscall.SIGTERM.String(),
			"initiator", string(tmuxServerInitiatorAINN),
		)
		if signalErr := tmuxCmd.Process.Signal(syscall.SIGTERM); signalErr != nil && !errors.Is(signalErr, os.ErrProcessDone) {
			_ = tmuxCmd.Process.Kill()
		}
		waitErr := tmuxCmd.Wait()
		logArgs, returnErr := collectTmuxServerExit(waitErr, tmuxServerInitiatorAINN, err)
		logger.Info(logging.EventTmuxServerExit, logArgs...)
		_ = json.NewEncoder(responseWriter).Encode(tmuxServerStartResponse{
			Error:         err.Error(),
			SupervisorPID: os.Getpid(),
			ServerPID:     tmuxCmd.Process.Pid,
		})
		return returnErr
	}

	response := tmuxServerStartResponse{
		Stdout:        initialStdout.String(),
		SupervisorPID: os.Getpid(),
		ServerPID:     tmuxCmd.Process.Pid,
	}
	if err := json.NewEncoder(responseWriter).Encode(response); err != nil {
		responseErr := fmt.Errorf("write tmux server startup response: %w", err)
		logger.Warn(logging.EventTmuxServerSignal,
			"pid", tmuxCmd.Process.Pid,
			"signal", syscall.SIGTERM.String(),
			"initiator", string(tmuxServerInitiatorAINN),
		)
		if signalErr := tmuxCmd.Process.Signal(syscall.SIGTERM); signalErr != nil && !errors.Is(signalErr, os.ErrProcessDone) {
			_ = tmuxCmd.Process.Kill()
		}
		waitErr := tmuxCmd.Wait()
		logArgs, returnErr := collectTmuxServerExit(waitErr, tmuxServerInitiatorAINN, responseErr)
		logger.Info(logging.EventTmuxServerExit, logArgs...)
		return returnErr
	}
	if startupControlResult != nil && !startupControlReceived {
		startupControlAcknowledged = <-startupControlResult
		startupControlReceived = true
	}
	if startupControlResult != nil && !startupControlAcknowledged {
		startupErr := errors.New("tmux supervisor startup client disconnected")
		logger.Warn(logging.EventTmuxServerSignal,
			"pid", tmuxCmd.Process.Pid,
			"signal", syscall.SIGTERM.String(),
			"initiator", string(tmuxServerInitiatorAINN),
		)
		if signalErr := tmuxCmd.Process.Signal(syscall.SIGTERM); signalErr != nil && !errors.Is(signalErr, os.ErrProcessDone) {
			_ = tmuxCmd.Process.Kill()
		}
		waitErr := tmuxCmd.Wait()
		logArgs, returnErr := collectTmuxServerExit(waitErr, tmuxServerInitiatorAINN, startupErr)
		logger.Info(logging.EventTmuxServerExit, logArgs...)
		return returnErr
	}

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- tmuxCmd.Wait()
	}()
	initiator := tmuxServerInitiatorExternal
	var waitErr error

waitForServer:
	for {
		select {
		case waitErr = <-waitResult:
			break waitForServer
		case receivedSignal := <-stopSignals:
			signalValue := receivedSignal.(syscall.Signal)
			logger.Warn(logging.EventTmuxServerSignal,
				"pid", tmuxCmd.Process.Pid,
				"signal", signalValue.String(),
				"initiator", string(initiator),
			)
			if signalErr := tmuxServerForwardSignal(tmuxCmd.Process, signalValue); signalErr != nil && !errors.Is(signalErr, os.ErrProcessDone) {
				forwardErr := fmt.Errorf("forward tmux server signal %s: %w", signalValue, signalErr)
				initiator = tmuxServerInitiatorAINN
				logger.Warn(logging.EventTmuxServerSignal,
					"pid", tmuxCmd.Process.Pid,
					"signal", syscall.SIGKILL.String(),
					"initiator", string(initiator),
				)
				killErr := tmuxCmd.Process.Kill()
				if errors.Is(killErr, os.ErrProcessDone) {
					killErr = nil
				}
				waitErr = errors.Join(forwardErr, killErr, <-waitResult)
				break waitForServer
			}
		}
	}
	logArgs, returnErr := collectTmuxServerExit(waitErr, initiator, nil)
	if waitErr == nil {
		logger.Info(logging.EventTmuxServerExit, logArgs...)
	} else {
		logger.Error(logging.EventTmuxServerExit, logArgs...)
	}
	return returnErr
}
