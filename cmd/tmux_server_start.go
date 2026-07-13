package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/logging"
)

const (
	tmuxLifecycleLogPrefix = "tmux-"
	tmuxLifecycleLogSuffix = ".log"
)

type tmuxClientExitReason string

const (
	tmuxClientExitReasonDetached         tmuxClientExitReason = "detached"
	tmuxClientExitReasonEmpty            tmuxClientExitReason = "empty"
	tmuxClientExitReasonServerTerminated tmuxClientExitReason = "server_terminated"
	tmuxClientExitReasonServerUnexpected tmuxClientExitReason = "server_unexpected"
	tmuxClientExitReasonClientError      tmuxClientExitReason = "client_error"
)

type tmuxClientExit struct {
	Reason   tmuxClientExitReason
	ExitCode int
	Error    string
}

type tmuxServerStartRequest struct {
	ConfigDir      string
	LogDir         string
	SocketName     string
	HostSession    string
	InitialCommand []string
}

type tmuxServerStartResponse struct {
	Stdout        string `json:"stdout"`
	Error         string `json:"error"`
	SupervisorPID int    `json:"supervisor_pid"`
	ServerPID     int    `json:"server_pid"`
}

func classifyTmuxClientExit(output string, err error) tmuxClientExit {
	exit := tmuxClientExit{}
	if err != nil {
		exit.ExitCode = 1
		exit.Error = err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exit.ExitCode = exitErr.ExitCode()
		} else if fields := strings.Fields(err.Error()); len(fields) > 0 {
			if code, parseErr := strconv.Atoi(fields[len(fields)-1]); parseErr == nil {
				exit.ExitCode = code
			}
		}
	}

	switch {
	case strings.Contains(output, "server exited unexpectedly"):
		exit.Reason = tmuxClientExitReasonServerUnexpected
	case strings.Contains(output, "server exited"):
		exit.Reason = tmuxClientExitReasonServerTerminated
	case strings.Contains(output, "detached"):
		exit.Reason = tmuxClientExitReasonDetached
	case strings.Contains(output, "exited"):
		exit.Reason = tmuxClientExitReasonEmpty
	default:
		exit.Reason = tmuxClientExitReasonClientError
	}
	return exit
}

func openTmuxLifecycleLogger(settings config.Settings) (*os.File, *slog.Logger, error) {
	logDir := expandHome(settings.LogDir)
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return nil, nil, fmt.Errorf("create tmux log directory %s: %w", logDir, err)
	}
	logPath := filepath.Join(logDir, tmuxLifecycleLogPrefix+settings.Terminal.Tmux.SocketName+tmuxLifecycleLogSuffix)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, nil, fmt.Errorf("open tmux lifecycle log %s: %w", logPath, err)
	}
	return file, logging.New(file, "detail", logging.ComponentTmuxSupervisor), nil
}

func writeTmuxClientExit(settings config.Settings, output string, err error) error {
	file, logger, openErr := openTmuxLifecycleLogger(settings)
	if openErr != nil {
		return openErr
	}
	exit := classifyTmuxClientExit(output, err)
	logger.Info(logging.EventTmuxClientExit,
		"socket", settings.Terminal.Tmux.SocketName,
		"host_session", settings.Terminal.Tmux.HostSession,
		"reason", string(exit.Reason),
		"exit_code", exit.ExitCode,
		"error", exit.Error,
	)
	if closeErr := file.Close(); closeErr != nil {
		return fmt.Errorf("close tmux lifecycle log: %w", closeErr)
	}
	return nil
}
