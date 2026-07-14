package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
)

const (
	tmuxRootChildEnvVar = "AINN_TMUX_ROOT_CHILD"
	tmuxMainWindowName  = "ainn"
	tmuxMainWindowIndex = "0"
	tmuxDebugEnvVar     = "AINN_TMUX_DEBUG"
	tmuxDebugLogEnvVar  = "AINN_TMUX_DEBUG_LOG"
	tmuxTraceWriteError = "write tmux trace "
	tmuxNoServerRunning = "no server running"
	tmuxCantFindSession = "can't find session"
	tmuxErrorConnecting = "error connecting to "
	tmuxNoSuchFile      = "No such file or directory"
)

type rootTmuxRunner interface {
	Run(args []string) (string, error)
}

type rootTmuxRunnerFunc func([]string) (string, error)

func (f rootTmuxRunnerFunc) Run(args []string) (string, error) {
	return f(args)
}

var rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
	debugToStderr := os.Getenv(tmuxDebugEnvVar) == "1"
	debugLogPath := os.Getenv(tmuxDebugLogEnvVar)
	return rootTmuxRunnerFunc(func(args []string) (string, error) {
		cmd := exec.Command(args[0], args[1:]...)
		attachSession := tmuxSubcommand(args) == "attach-session"
		var stdoutBuf bytes.Buffer
		var stderrBuf bytes.Buffer
		var attachOutputTail tmuxServerOutputTail
		if attachSession {
			cmd.Stdout = io.MultiWriter(stdout, &attachOutputTail)
			cmd.Stderr = io.MultiWriter(stderr, &stderrBuf)
		} else {
			cmd.Stdout = &stdoutBuf
			cmd.Stderr = &stderrBuf
		}
		cmd.Stdin = os.Stdin
		startedAt := time.Now()
		err := cmd.Run()
		duration := time.Since(startedAt)
		stdoutText := stdoutBuf.String()
		stderrText := stderrBuf.String()
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		if debugToStderr {
			fmt.Fprintf(stderr, "tmux trace: %s duration_ms=%d stdout=%q stderr=%q err=%q\n", strings.Join(args, " "), duration.Milliseconds(), stdoutText, stderrText, errText)
		}
		if debugLogPath != "" {
			record := struct {
				Argv       []string `json:"argv"`
				Stdout     string   `json:"stdout"`
				Stderr     string   `json:"stderr"`
				Err        string   `json:"err"`
				DurationMS int64    `json:"duration_ms"`
			}{
				Argv:       append([]string{}, args...),
				Stdout:     stdoutText,
				Stderr:     stderrText,
				Err:        errText,
				DurationMS: duration.Milliseconds(),
			}
			f, openErr := os.OpenFile(debugLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if openErr != nil {
				return stdoutText, fmt.Errorf("write tmux trace %s: %w", debugLogPath, openErr)
			}
			encodeErr := json.NewEncoder(f).Encode(record)
			closeErr := f.Close()
			if encodeErr != nil {
				return stdoutText, fmt.Errorf("write tmux trace %s: %w", debugLogPath, encodeErr)
			}
			if closeErr != nil {
				return stdoutText, fmt.Errorf("write tmux trace %s: %w", debugLogPath, closeErr)
			}
		}
		if attachSession {
			return attachOutputTail.RedactedString() + stderrText, err
		}
		if err != nil && strings.TrimSpace(stderrText) != "" {
			return stdoutText, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderrText))
		}
		return stdoutText, err
	})
}

func isTmuxHostMissingError(err error) bool {
	errText := err.Error()
	return strings.Contains(errText, tmuxNoServerRunning) ||
		strings.Contains(errText, tmuxCantFindSession) ||
		(strings.Contains(errText, tmuxErrorConnecting) && strings.Contains(errText, tmuxNoSuchFile))
}

func isTmuxServerMissingError(err error) bool {
	errText := err.Error()
	return strings.Contains(errText, tmuxNoServerRunning) ||
		(strings.Contains(errText, tmuxErrorConnecting) && strings.Contains(errText, tmuxNoSuchFile))
}

func printTmuxTraceWriteError(stderr io.Writer, err error) bool {
	if strings.HasPrefix(err.Error(), tmuxTraceWriteError) {
		fmt.Fprintln(stderr, err)
		return true
	}
	return false
}

func runRootTmuxBootstrap(cfg config.Config, configDir string, managerPort int, stdout io.Writer, stderr io.Writer) int {
	runner := rootTmuxRunnerFactory(stdout, stderr)
	if _, err := runner.Run(manager.TmuxDetectCommand()); err != nil {
		if printTmuxTraceWriteError(stderr, err) {
			return 1
		}
		fmt.Fprintf(stderr, "tmux is required for main-tui-window mode: %v\n", err)
		return 1
	}
	insideTmux := os.Getenv("TMUX") != "" && os.Getenv("TMUX_PANE") != ""
	currentSocketPath, _, _ := strings.Cut(os.Getenv("TMUX"), ",")
	if insideTmux {
		currentSocketName := filepath.Base(currentSocketPath)
		configuredSocketName := cfg.Settings.Terminal.Tmux.SocketName
		if currentSocketName != configuredSocketName {
			fmt.Fprintf(stderr, "unsupported tmux startup state: current tmux socket %q differs from configured tmux socket %q\n", currentSocketName, configuredSocketName)
			return 1
		}
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "failed to locate executable: %v\n", err)
		return 1
	}
	tmuxHostCommand := func(args []string) []string { return args }
	if insideTmux {
		tmuxHostCommand = func(args []string) []string {
			args[1] = "-S"
			args[2] = currentSocketPath
			return args
		}
	}
	rootCmd := []string{"env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", configDir, "--manager-port", strconv.Itoa(managerPort)}
	serverMissing := false
	if _, err := runner.Run(tmuxHostCommand(manager.TmuxHasSessionCommandForSettings(cfg.Settings))); err != nil {
		if printTmuxTraceWriteError(stderr, err) {
			return 1
		}
		if !isTmuxHostMissingError(err) {
			fmt.Fprintf(stderr, "failed to inspect tmux host session: %v\n", err)
			return 1
		}
		serverMissing = isTmuxServerMissingError(err)
		initialCommand := tmuxHostCommand(manager.TmuxStartMainWindowHostCommandForSettings(cfg.Settings, tmuxMainWindowName, rootCmd))
		createdWindowIndex := ""
		if serverMissing {
			response, startErr := managedTmuxServerStarter(tmuxServerStartRequest{
				ConfigDir:      configDir,
				LogDir:         expandHome(cfg.Settings.LogDir),
				SocketName:     cfg.Settings.Terminal.Tmux.SocketName,
				HostSession:    cfg.Settings.Terminal.Tmux.HostSession,
				InitialCommand: initialCommand,
			})
			err = startErr
			createdWindowIndex = response.Stdout
		} else {
			createdWindowIndex, err = runner.Run(initialCommand)
		}
		if err != nil {
			if printTmuxTraceWriteError(stderr, err) {
				return 1
			}
			fmt.Fprintf(stderr, "failed to start tmux host: %v\n", err)
			return 1
		}
		createdWindowIndex = strings.TrimSpace(createdWindowIndex)
		if createdWindowIndex != tmuxMainWindowIndex {
			if _, err := runner.Run(tmuxHostCommand(manager.TmuxMoveWindowToMainWindowCommandForSettings(cfg.Settings, createdWindowIndex))); err != nil {
				if printTmuxTraceWriteError(stderr, err) {
					return 1
				}
				fmt.Fprintf(stderr, "failed to move tmux main window: %v\n", err)
				return 1
			}
		}
	} else if paneStartOutput, err := runner.Run(tmuxHostCommand(manager.TmuxMainWindowPaneStartCommandForSettings(cfg.Settings))); err != nil {
		if printTmuxTraceWriteError(stderr, err) {
			return 1
		}
		if !strings.Contains(err.Error(), "can't find window") {
			fmt.Fprintf(stderr, "failed to inspect main tmux window: %v\n", err)
			return 1
		}
		if _, err := runner.Run(tmuxHostCommand(manager.TmuxCreateMainWindowCommandForSettings(cfg.Settings, tmuxMainWindowName, rootCmd))); err != nil {
			if printTmuxTraceWriteError(stderr, err) {
				return 1
			}
			fmt.Fprintf(stderr, "failed to create main tmux window: %v\n", err)
			return 1
		}
	} else {
		paneStartOutput = strings.TrimSpace(paneStartOutput)
		windowIndex := tmuxMainWindowIndex
		paneStartCommand := paneStartOutput
		if index, command, found := strings.Cut(paneStartOutput, "\t"); found {
			windowIndex = strings.TrimSpace(index)
			paneStartCommand = command
		}
		if windowIndex != tmuxMainWindowIndex {
			if _, err := runner.Run(tmuxHostCommand(manager.TmuxCreateMainWindowCommandForSettings(cfg.Settings, tmuxMainWindowName, rootCmd))); err != nil {
				if printTmuxTraceWriteError(stderr, err) {
					return 1
				}
				fmt.Fprintf(stderr, "failed to create main tmux window: %v\n", err)
				return 1
			}
		} else if !strings.Contains(paneStartCommand, exe) {
			if _, err := runner.Run(tmuxHostCommand(manager.TmuxRespawnMainWindowCommandForSettings(cfg.Settings, rootCmd))); err != nil {
				if printTmuxTraceWriteError(stderr, err) {
					return 1
				}
				fmt.Fprintf(stderr, "failed to respawn main tmux window: %v\n", err)
				return 1
			}
		}
	}
	if _, err := runner.Run(tmuxHostCommand(manager.TmuxResetMainWindowStatusCommandForSettings(cfg.Settings))); err != nil {
		if printTmuxTraceWriteError(stderr, err) {
			return 1
		}
		fmt.Fprintf(stderr, "failed to reset main tmux window status: %v\n", err)
		return 1
	}
	if _, err := runner.Run(tmuxHostCommand(manager.TmuxThemeCommandForSettings(cfg.Settings))); err != nil {
		if printTmuxTraceWriteError(stderr, err) {
			return 1
		}
		fmt.Fprintf(stderr, "failed to apply tmux theme: %v\n", err)
		return 1
	}
	if _, err := runner.Run(tmuxHostCommand(manager.TmuxEnableExtendedKeysCommandForSettings(cfg.Settings))); err != nil {
		if printTmuxTraceWriteError(stderr, err) {
			return 1
		}
		fmt.Fprintf(stderr, "failed to enable tmux extended keys: %v\n", err)
		return 1
	}
	if insideTmux {
		clientRows, err := runner.Run(manager.TmuxListClientPanesCommand(currentSocketPath))
		if err != nil {
			if printTmuxTraceWriteError(stderr, err) {
				return 1
			}
			fmt.Fprintf(stderr, "failed to identify tmux client: %v\n", err)
			return 1
		}
		currentPaneID := os.Getenv("TMUX_PANE")
		clientName := ""
		for _, row := range strings.Split(strings.TrimSpace(clientRows), "\n") {
			rowClientName, rowPaneID, _ := strings.Cut(row, "\t")
			if rowPaneID == currentPaneID {
				clientName = rowClientName
				break
			}
		}
		if clientName == "" {
			fmt.Fprintf(stderr, "failed to identify tmux client: no client found for pane %s\n", currentPaneID)
			return 1
		}
		if _, err := runner.Run(tmuxHostCommand(manager.TmuxSwitchClientToMainWindowCommandForSettings(cfg.Settings, clientName))); err != nil {
			if printTmuxTraceWriteError(stderr, err) {
				return 1
			}
			fmt.Fprintf(stderr, "failed to switch tmux client: %v\n", err)
			return 1
		}
		return 0
	}
	if _, err := runner.Run(tmuxHostCommand(manager.TmuxSelectMainWindowCommandForSettings(cfg.Settings))); err != nil {
		if printTmuxTraceWriteError(stderr, err) {
			return 1
		}
		fmt.Fprintf(stderr, "failed to select main tmux window: %v\n", err)
		return 1
	}
	attachOutput, attachErr := runner.Run(tmuxHostCommand(manager.TmuxAttachCommandForSettings(cfg.Settings)))
	if err := writeTmuxClientExit(cfg.Settings, attachOutput, attachErr); err != nil {
		fmt.Fprintf(stderr, "failed to log tmux client exit: %v\n", err)
		return 1
	}
	if attachErr != nil {
		if printTmuxTraceWriteError(stderr, attachErr) {
			return 1
		}
		fmt.Fprintf(stderr, "failed to attach tmux host: %v\n", attachErr)
		return 1
	}
	return 0
}
