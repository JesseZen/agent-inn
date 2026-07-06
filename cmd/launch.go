package cmd

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
)

const (
	modeExternalWindow = "external-window"
	modeHostedTerminal = "hosted-terminal"
)

type launchRunner interface {
	Run(args []string) (string, error)
}

type launchRunnerFunc func([]string) (string, error)

func (f launchRunnerFunc) Run(args []string) (string, error) {
	return f(args)
}

type multiString []string

func (m *multiString) String() string {
	return strings.Join(*m, ",")
}

func (m *multiString) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		*m = append(*m, part)
	}
	return nil
}

var launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
	return launchRunnerFunc(func(args []string) (string, error) {
		cmd := exec.Command(args[0], args[1:]...)
		attachSession := tmuxSubcommand(args) == "attach-session"
		var stdoutBuf bytes.Buffer
		var stderrBuf bytes.Buffer
		if attachSession {
			cmd.Stdout = stdout
			cmd.Stderr = stderr
		} else {
			cmd.Stdout = io.MultiWriter(stdout, &stdoutBuf)
			cmd.Stderr = io.MultiWriter(stderr, &stderrBuf)
		}
		cmd.Stdin = os.Stdin
		err := cmd.Run()
		if err != nil && strings.TrimSpace(stderrBuf.String()) != "" {
			return stdoutBuf.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderrBuf.String()))
		}
		return stdoutBuf.String(), err
	})
}

func runLaunch(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("launch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	worker := flags.String("worker", "", "worker port")
	profile := flags.String("profile", "", "codex profile")
	workspace := flags.String("cd", "", "workspace directory")
	var addDirs multiString
	flags.Var(&addDirs, "add-dir", "extra directories, comma separated")
	model := flags.String("model", "", "model override")
	mode := flags.String("mode", modeExternalWindow, "launch mode: external-window or hosted-terminal")
	noAttach := flags.Bool("no-attach", false, "hosted-terminal: set up window without attaching (for TUI use)")
	sessionID := flags.String("session-id", "", "hosted-terminal: existing AINN session id")
	sessionLabel := flags.String("session-label", "", "hosted-terminal: session label for new AINN sessions")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *worker == "" {
		fmt.Fprintln(stderr, "launch requires --worker")
		return 2
	}
	port, err := strconv.Atoi(*worker)
	if err != nil {
		fmt.Fprintf(stderr, "invalid worker port %q\n", *worker)
		return 2
	}
	if *profile == "" {
		*profile = *worker
	}
	expandedConfigDir := expandHome(*configDir)
	absConfigDir, err := filepath.Abs(expandedConfigDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to resolve config dir: %v\n", err)
		return 1
	}
	resolvedConfigDir := filepath.Clean(absConfigDir)
	switch *mode {
	case modeExternalWindow, modeHostedTerminal:
	default:
		fmt.Fprintf(stderr, "invalid mode %q\n", *mode)
		return 2
	}

	launcher := "codex"
	var cfg config.Config
	configLoaded := false
	var configLoadErr error
	if loaded, err := config.LoadFile(filepath.Join(resolvedConfigDir, config.ConfigFileName)); err == nil {
		cfg = loaded
		configLoaded = true
		if workerCfg, ok := workerConfigByPort(cfg, port); ok {
			launcher = workerCfg.Launcher
		}
	} else {
		configLoadErr = err
	}

	opts := manager.LaunchOptions{
		Launcher:   launcher,
		Profile:    *profile,
		Workspace:  *workspace,
		AddDirs:    addDirs,
		WorkerPort: port,
		Model:      *model,
	}
	cmd := manager.BuildLaunchCommand(opts)

	if *mode == modeHostedTerminal {
		if !configLoaded {
			fmt.Fprintf(stderr, "failed to load config: %v\n", configLoadErr)
			return 1
		}
		return runHostedTerminalLaunch(cfg.Settings, opts, resolvedConfigDir, *profile, *sessionID, *sessionLabel, stdout, stderr, *noAttach)
	}

	if err := runTerminalLaunchCommand(cmd, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "failed to launch: %v\n", err)
		return 1
	}
	return 0
}

func workerConfigByPort(cfg config.Config, port int) (config.WorkerConfig, bool) {
	for _, worker := range cfg.Workers {
		if worker.Port == port {
			return worker, true
		}
	}
	return config.WorkerConfig{}, false
}

func runTerminalLaunchCommand(cmd []string, stdout io.Writer, stderr io.Writer) error {
	if stdout == os.Stdout && stderr == os.Stderr {
		proc := exec.Command(cmd[0], cmd[1:]...)
		proc.Stdout = os.Stdout
		proc.Stderr = os.Stderr
		proc.Stdin = os.Stdin
		return proc.Run()
	}
	runner := launchRunnerFactory(stdout, stderr)
	_, err := runner.Run(cmd)
	return err
}

// runHostedTerminalLaunch runs the Codex CLI inside a AINN-owned tmux host.
// It ensures the host session exists, creates or switches to a window for the
// session, and attaches to the host. The sessionID determines the tmux window
// name so re-launching the same session switches to the existing window.
// When noAttach is true, the setup runs but the attach step is skipped so the
// caller (TUI) can decide whether to open a new terminal.
func runHostedTerminalLaunch(settings config.Settings, opts manager.LaunchOptions, configDir string, workerName string, sessionID string, sessionLabel string, stdout io.Writer, stderr io.Writer, noAttach bool) int {
	runner := launchRunnerFactory(stdout, stderr)

	if _, err := runner.Run(manager.TmuxDetectCommand()); err != nil {
		fmt.Fprintf(stderr, "tmux is required for hosted-terminal mode: %v\n", err)
		return 1
	}

	hostCreated := false
	if _, err := runner.Run(manager.TmuxHasSessionCommandForSettings(settings)); err != nil {
		if !isTmuxHostMissingError(err) {
			fmt.Fprintf(stderr, "failed to inspect tmux host session: %v\n", err)
			return 1
		}
		hostCreated = true
	}

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(settings.StateDir))
	if sessionID != "" {
		if hostCreated {
			if _, err := runner.Run(manager.TmuxStartHostCommandForSettings(settings)); err != nil {
				fmt.Fprintf(stderr, "failed to start tmux host: %v\n", err)
				return 1
			}
		}
		mouse, err := runner.Run(manager.TmuxShowMouseCommandForSettings(settings))
		if err != nil {
			fmt.Fprintf(stderr, "failed to inspect tmux mouse setting: %v\n", err)
			return 1
		}
		if strings.TrimSpace(mouse) != "on" {
			if _, err := runner.Run(manager.TmuxEnableMouseCommandForSettings(settings)); err != nil {
				fmt.Fprintf(stderr, "failed to enable tmux mouse support: %v\n", err)
				return 1
			}
		}
		if _, err := runner.Run(manager.TmuxEnableExtendedKeysCommandForSettings(settings)); err != nil {
			fmt.Fprintf(stderr, "failed to enable tmux extended keys: %v\n", err)
			return 1
		}
		if _, err := runner.Run(manager.TmuxThemeCommandForSettings(settings)); err != nil {
			fmt.Fprintf(stderr, "failed to apply tmux theme: %v\n", err)
			return 1
		}
		session, ok, err := registry.Get(sessionID)
		if err != nil {
			fmt.Fprintf(stderr, "failed to load hosted session: %v\n", err)
			return 1
		}
		if !ok {
			fmt.Fprintf(stderr, "hosted session %q not found\n", sessionID)
			return 1
		}
		if session.TmuxWindowID == "" {
			fmt.Fprintf(stderr, "hosted session %q is stale\n", sessionID)
			return 1
		}
		windowDetails, err := runner.Run(manager.TmuxListWindowDetailsCommandForSettings(settings))
		if err != nil {
			fmt.Fprintf(stderr, "failed to inspect tmux windows: %v\n", err)
			return 1
		}
		activeWindow := false
		for _, line := range strings.Split(windowDetails, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) != 2 {
				continue
			}
			if parts[0] == session.TmuxWindowID && parts[1] == session.SessionLabel {
				activeWindow = true
				break
			}
		}
		if !activeWindow {
			fmt.Fprintf(stderr, "hosted session %q is stale\n", sessionID)
			return 1
		}
		if _, err := runner.Run(manager.TmuxSelectWindowCommandForSettings(settings, session.TmuxWindowID)); err != nil {
			fmt.Fprintf(stderr, "failed to select tmux window: %v\n", err)
			return 1
		}
		if noAttach {
			return 0
		}
		if _, err := runner.Run(manager.TmuxAttachCommandForSettings(settings)); err != nil {
			fmt.Fprintf(stderr, "failed to attach tmux host: %v\n", err)
			return 1
		}
		return 0
	}

	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: sessionLabel,
		WorkerName:   workerName,
		WorkerPort:   opts.WorkerPort,
		Workspace:    opts.Workspace,
		Model:        opts.Model,
		AddDirs:      append([]string{}, opts.AddDirs...),
	})
	if err != nil {
		fmt.Fprintf(stderr, "failed to create hosted session: %v\n", err)
		return 1
	}
	cleanupIncompleteSession := func() {
		if err := registry.Delete(session.SessionID); err != nil {
			fmt.Fprintf(stderr, "failed to clean up incomplete hosted session: %v\n", err)
		}
	}
	windowName := session.SessionLabel
	launchCmd := hostedSessionLaunchCommand(manager.BuildLaunchCommand(opts), configDir, session.SessionID)
	reuseFirstWindow := hostCreated && settings.Terminal.Tmux.HostStartMode == config.TmuxHostStartModeReuseFirstWindow
	if reuseFirstWindow {
		windowDetails, err := runner.Run(manager.TmuxStartHostWithWindowCommandForSettings(settings, windowName, launchCmd))
		if err != nil {
			cleanupIncompleteSession()
			fmt.Fprintf(stderr, "failed to start tmux host: %v\n", err)
			return 1
		}
		parts := strings.Split(strings.TrimSpace(windowDetails), "\t")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			cleanupIncompleteSession()
			fmt.Fprintf(stderr, "failed to inspect tmux host window: %q\n", strings.TrimSpace(windowDetails))
			return 1
		}
		windowName = strings.TrimSpace(parts[0])
		windowIndex := strings.TrimSpace(parts[1])
		if windowIndex != "0" {
			if _, err := runner.Run(manager.TmuxMoveWindowToMainWindowCommandForSettings(settings, windowIndex)); err != nil {
				cleanupIncompleteSession()
				fmt.Fprintf(stderr, "failed to move tmux window to index 0: %v\n", err)
				return 1
			}
		}
	} else {
		if hostCreated {
			if _, err := runner.Run(manager.TmuxStartHostCommandForSettings(settings)); err != nil {
				cleanupIncompleteSession()
				fmt.Fprintf(stderr, "failed to start tmux host: %v\n", err)
				return 1
			}
		}
	}
	mouse, err := runner.Run(manager.TmuxShowMouseCommandForSettings(settings))
	if err != nil {
		cleanupIncompleteSession()
		fmt.Fprintf(stderr, "failed to inspect tmux mouse setting: %v\n", err)
		return 1
	}
	if strings.TrimSpace(mouse) != "on" {
		if _, err := runner.Run(manager.TmuxEnableMouseCommandForSettings(settings)); err != nil {
			cleanupIncompleteSession()
			fmt.Fprintf(stderr, "failed to enable tmux mouse support: %v\n", err)
			return 1
		}
	}
	if _, err := runner.Run(manager.TmuxEnableExtendedKeysCommandForSettings(settings)); err != nil {
		cleanupIncompleteSession()
		fmt.Fprintf(stderr, "failed to enable tmux extended keys: %v\n", err)
		return 1
	}
	if _, err := runner.Run(manager.TmuxThemeCommandForSettings(settings)); err != nil {
		cleanupIncompleteSession()
		fmt.Fprintf(stderr, "failed to apply tmux theme: %v\n", err)
		return 1
	}
	if !reuseFirstWindow {
		if _, err := runner.Run(manager.TmuxSelectWindowCommandForSettings(settings, windowName)); err != nil {
			windowID, err := runner.Run(manager.TmuxCreateWindowCommandForSettings(settings, windowName, launchCmd))
			if err != nil {
				cleanupIncompleteSession()
				fmt.Fprintf(stderr, "failed to create tmux window: %v\n", err)
				return 1
			}
			windowName = strings.TrimSpace(windowID)
		}
	}
	if err := registry.UpdateWindowID(session.SessionID, windowName); err != nil {
		cleanupIncompleteSession()
		fmt.Fprintf(stderr, "failed to persist hosted session: %v\n", err)
		return 1
	}
	if noAttach {
		return 0
	}
	if _, err := runner.Run(manager.TmuxAttachCommandForSettings(settings)); err != nil {
		fmt.Fprintf(stderr, "failed to attach tmux host: %v\n", err)
		return 1
	}
	return 0
}

func hostedSessionLaunchCommand(command []string, configDir string, sessionID string) []string {
	executable, err := os.Executable()
	if err != nil {
		executable = os.Args[0]
	}
	env := []string{
		"env",
		"AINN_HOSTED_SESSION_ID=" + sessionID,
		"AINN_CONFIG_DIR=" + configDir,
		"AINN_EXECUTABLE=" + executable,
	}
	if len(command) > 0 && command[0] == "env" {
		return append(env, command[1:]...)
	}
	return append(env, command...)
}
