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
	modeExternalWindow              = "external-window"
	modeHostedTerminal              = "hosted-terminal"
	hostedSessionAcknowledgeCommand = "hosted-session acknowledge"
	hostedSessionToggleTodoCommand  = "hosted-session toggle-todo"
	defaultManagerURL               = "http://127.0.0.1:9090"
	legacyHostedPopupWidth          = "80%"
	legacyHostedPopupHeight         = "70%"
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
		var attachOutputTail tmuxServerOutputTail
		if attachSession {
			cmd.Stdout = io.MultiWriter(stdout, &attachOutputTail)
			cmd.Stderr = io.MultiWriter(stderr, &stderrBuf)
		} else {
			cmd.Stdout = &stdoutBuf
			cmd.Stderr = &stderrBuf
		}
		cmd.Stdin = os.Stdin
		err := cmd.Run()
		if attachSession {
			return attachOutputTail.RedactedString() + stderrBuf.String(), err
		}
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
	if *mode == modeHostedTerminal {
		resolvedConfigDir, err = filepath.EvalSymlinks(resolvedConfigDir)
		if err != nil {
			fmt.Fprintf(stderr, "failed to resolve config dir: %v\n", err)
			return 1
		}
	}

	launcher := "codex"
	apiFormat := ""
	grokHome := ""
	grokExecutable := ""
	piAgentDir := ""
	var cfg config.Config
	configLoaded := false
	var configLoadErr error
	if loaded, err := config.LoadFile(filepath.Join(resolvedConfigDir, config.ConfigFileName)); err == nil {
		cfg = loaded
		configLoaded = true
		if workerID, workerCfg, ok := workerConfigByPort(cfg, port); ok {
			launcher = workerCfg.Launcher
			*profile = workerID
			apiFormat = cfg.Upstreams[workerCfg.Upstream].APIFormat
			if launcher == "grok" && *model == "" {
				*model = manager.DefaultGrokModel
			}
			if launcher == "grok" {
				stateDir := cfg.Settings.StateDir
				if stateDir == "" {
					stateDir = config.DefaultConfigDir
				}
				grokHome = filepath.Join(expandHome(stateDir), "grok-home")
				candidate := expandHome("~/.grok/bin/grok")
				if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() && info.Mode()&0111 != 0 {
					grokExecutable = candidate
				} else if resolved, lookErr := exec.LookPath("grok"); lookErr == nil {
					grokExecutable = resolved
				}
			}
			if launcher == "pi" {
				stateDir := cfg.Settings.StateDir
				if stateDir == "" {
					stateDir = config.DefaultConfigDir
				}
				piAgentDir = filepath.Join(expandHome(stateDir), "pi-agent")
			}
		}
	} else {
		configLoadErr = err
	}
	if launcher == "grok" && grokExecutable == "" {
		fmt.Fprintln(stderr, "grok launcher is not installed or not executable (expected ~/.grok/bin/grok or grok in PATH)")
		return 1
	}

	opts := manager.LaunchOptions{
		Launcher:       launcher,
		Profile:        *profile,
		Workspace:      *workspace,
		AddDirs:        addDirs,
		WorkerPort:     port,
		GrokHome:       grokHome,
		GrokExecutable: grokExecutable,
		Model:          *model,
		APIFormat:      apiFormat,
		PiAgentDir:     piAgentDir,
	}
	if *mode == modeHostedTerminal {
		if !configLoaded {
			fmt.Fprintf(stderr, "failed to load config: %v\n", configLoadErr)
			return 1
		}
		return runHostedTerminalLaunch(cfg, opts, resolvedConfigDir, *profile, *sessionID, *sessionLabel, stdout, stderr, *noAttach)
	}

	cmd, err := manager.BuildLaunchCommand(opts)
	if err != nil {
		fmt.Fprintf(stderr, "failed to build launch command: %v\n", err)
		return 1
	}
	if err := runTerminalLaunchCommand(cmd, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "failed to launch: %v\n", err)
		return 1
	}
	return 0
}

// runHostedTerminalLaunch runs the Codex CLI inside a AINN-owned tmux host.
// It ensures the host session exists, creates or switches to a window for the
// session, and attaches to the host. The sessionID determines the tmux window
// name so re-launching the same session switches to the existing window.
// When noAttach is true, the setup runs but the attach step is skipped so the
// caller (TUI) can decide whether to open a new terminal.
func runHostedTerminalLaunch(cfg config.Config, opts manager.LaunchOptions, configDir string, workerName string, sessionID string, sessionLabel string, stdout io.Writer, stderr io.Writer, noAttach bool) int {
	var freshCommand []string
	if sessionID == "" {
		var err error
		freshCommand, err = manager.BuildLaunchCommand(opts)
		if err != nil {
			fmt.Fprintf(stderr, "failed to build launch command: %v\n", err)
			return 1
		}
	}
	runner := launchRunnerFactory(stdout, stderr)
	settings := cfg.Settings

	if _, err := runner.Run(manager.TmuxDetectCommand()); err != nil {
		fmt.Fprintf(stderr, "tmux is required for hosted-terminal mode: %v\n", err)
		return 1
	}

	hostCreated := false
	serverMissing := false
	releaseTmuxStartupLock := func() {}
	if _, err := runner.Run(manager.TmuxHasSessionCommandForSettings(settings)); err != nil {
		if !isTmuxHostMissingError(err) {
			fmt.Fprintf(stderr, "failed to inspect tmux host session: %v\n", err)
			return 1
		}
		serverMissing = isTmuxServerMissingError(err)
		if serverMissing {
			releaseTmuxStartupLock, err = acquireTmuxServerStartupLock(settings.Terminal.Tmux.SocketName)
			if err != nil {
				fmt.Fprintf(stderr, "failed to lock tmux host startup: %v\n", err)
				return 1
			}
			defer releaseTmuxStartupLock()
			if _, err := runner.Run(manager.TmuxHasSessionCommandForSettings(settings)); err != nil {
				if !isTmuxHostMissingError(err) {
					fmt.Fprintf(stderr, "failed to inspect tmux host session: %v\n", err)
					return 1
				}
				hostCreated = true
				serverMissing = isTmuxServerMissingError(err)
			} else {
				releaseTmuxStartupLock()
			}
		} else {
			hostCreated = true
		}
	}

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(settings.StateDir))
	if sessionID != "" {
		session, ok, err := registry.Get(sessionID)
		if err != nil {
			fmt.Fprintf(stderr, "failed to load hosted session: %v\n", err)
			return 1
		}
		if !ok {
			fmt.Fprintf(stderr, "hosted session %q not found\n", sessionID)
			return 1
		}
		if hostCreated {
			var err error
			if serverMissing {
				_, err = startHostedTmuxServer(settings, configDir, manager.TmuxStartHostCommandForSettings(settings))
			} else {
				_, err = runner.Run(manager.TmuxStartHostCommandForSettings(settings))
			}
			if err != nil {
				fmt.Fprintf(stderr, "failed to start tmux host: %v\n", err)
				return 1
			}
			releaseTmuxStartupLock()
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
		if settings.Terminal.Tmux.TurnStatusHooks {
			if err := installTmuxTurnStatusHooks(runner, settings, configDir, hostedSessionExecutable()); err != nil {
				fmt.Fprintf(stderr, "failed to install tmux turn acknowledgement hooks: %v\n", err)
				return 1
			}
		}
		if err := installTmuxHostedPopupBinding(runner, settings, configDir, hostedSessionExecutable()); err != nil {
			fmt.Fprintf(stderr, "failed to install tmux hosted popup binding: %v\n", err)
			return 1
		}
		if err := installTmuxHostedInteractions(runner, settings, configDir, hostedSessionExecutable()); err != nil {
			fmt.Fprintf(stderr, "failed to install tmux hosted interactions: %v\n", err)
			return 1
		}
		if _, err := runner.Run(manager.TmuxThemeCommandForSettings(settings)); err != nil {
			fmt.Fprintf(stderr, "failed to apply tmux theme: %v\n", err)
			return 1
		}
		activeWindowID := ""
		if session.TmuxWindowID != "" {
			windowDetails, err := runner.Run(manager.TmuxListWindowDetailsCommandForSettings(settings))
			if err != nil {
				fmt.Fprintf(stderr, "failed to inspect tmux windows: %v\n", err)
				return 1
			}
			windows := map[string]string{}
			for _, line := range strings.Split(windowDetails, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.SplitN(line, "\t", 2)
				if len(parts) != 2 {
					continue
				}
				windows[parts[0]] = parts[1]
			}
			if windowID, active := manager.HostedSessionActiveWindowID(windows, session); active {
				activeWindowID = windowID
			}
		}
		if activeWindowID != "" {
			if _, err := runner.Run(manager.TmuxSelectWindowCommandForSettings(settings, activeWindowID)); err != nil {
				fmt.Fprintf(stderr, "failed to select tmux window: %v\n", err)
				return 1
			}
			return finishHostedTerminalLaunch(settings, configDir, runner, stderr, noAttach)
		}
		reopenOpts := opts
		workerID := session.WorkerID
		if workerID == "" {
			workerID = session.WorkerName
		}
		workerCfg, ok := cfg.Workers[workerID]
		if !ok {
			fmt.Fprintf(stderr, "worker %q not found for hosted session %q\n", workerID, sessionID)
			return 1
		}
		reopenOpts.Launcher = workerCfg.Launcher
		reopenOpts.Profile = workerID
		reopenOpts.WorkerPort = workerCfg.Port
		reopenOpts.Workspace = session.Workspace
		reopenOpts.AddDirs = append([]string{}, session.AddDirs...)
		reopenOpts.Model = session.Model
		if session.LauncherSessionID == "" {
			if session.TurnGeneration > 0 && workerCfg.Launcher == "codex" {
				fmt.Fprintf(stderr, "hosted session %q is stale and has no launcher session id\n", sessionID)
				return 1
			}
		} else {
			reopenOpts.LauncherSessionID = session.LauncherSessionID
			reopenOpts.LauncherSessionMode = manager.LauncherSessionModeResume
		}
		command, err := manager.BuildLaunchCommand(reopenOpts)
		if err != nil {
			fmt.Fprintf(stderr, "failed to build launch command: %v\n", err)
			return 1
		}
		launchCmd := hostedSessionLaunchCommand(command, configDir, session.SessionID, settings.Terminal.Tmux.TurnStatusHooks)
		windowID, err := runner.Run(manager.TmuxCreateWindowCommandForSettings(settings, session.SessionLabel, session.Workspace, launchCmd))
		if err != nil {
			fmt.Fprintf(stderr, "failed to reopen tmux window: %v\n", err)
			return 1
		}
		if err := registry.UpdateWindowID(session.SessionID, strings.TrimSpace(windowID)); err != nil {
			fmt.Fprintf(stderr, "failed to persist hosted session: %v\n", err)
			return 1
		}
		return finishHostedTerminalLaunch(settings, configDir, runner, stderr, noAttach)
	}

	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: sessionLabel,
		WorkerID:     workerName,
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
	launchCmd := hostedSessionLaunchCommand(freshCommand, configDir, session.SessionID, settings.Terminal.Tmux.TurnStatusHooks)
	reuseFirstWindow := hostCreated && settings.Terminal.Tmux.HostStartMode == config.TmuxHostStartModeReuseFirstWindow
	if reuseFirstWindow {
		initialCommand := manager.TmuxStartHostWithWindowCommandForSettings(settings, windowName, opts.Workspace, launchCmd)
		windowDetails := ""
		if serverMissing {
			windowDetails, err = startHostedTmuxServer(settings, configDir, initialCommand)
		} else {
			windowDetails, err = runner.Run(initialCommand)
		}
		if err != nil {
			cleanupIncompleteSession()
			fmt.Fprintf(stderr, "failed to start tmux host: %v\n", err)
			return 1
		}
		releaseTmuxStartupLock()
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
			if serverMissing {
				_, err = startHostedTmuxServer(settings, configDir, manager.TmuxStartHostCommandForSettings(settings))
			} else {
				_, err = runner.Run(manager.TmuxStartHostCommandForSettings(settings))
			}
			if err != nil {
				cleanupIncompleteSession()
				fmt.Fprintf(stderr, "failed to start tmux host: %v\n", err)
				return 1
			}
			releaseTmuxStartupLock()
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
	if settings.Terminal.Tmux.TurnStatusHooks {
		if err := installTmuxTurnStatusHooks(runner, settings, configDir, hostedSessionExecutable()); err != nil {
			cleanupIncompleteSession()
			fmt.Fprintf(stderr, "failed to install tmux turn acknowledgement hooks: %v\n", err)
			return 1
		}
	}
	if err := installTmuxHostedPopupBinding(runner, settings, configDir, hostedSessionExecutable()); err != nil {
		cleanupIncompleteSession()
		fmt.Fprintf(stderr, "failed to install tmux hosted popup binding: %v\n", err)
		return 1
	}
	if err := installTmuxHostedInteractions(runner, settings, configDir, hostedSessionExecutable()); err != nil {
		cleanupIncompleteSession()
		fmt.Fprintf(stderr, "failed to install tmux hosted interactions: %v\n", err)
		return 1
	}
	if _, err := runner.Run(manager.TmuxThemeCommandForSettings(settings)); err != nil {
		cleanupIncompleteSession()
		fmt.Fprintf(stderr, "failed to apply tmux theme: %v\n", err)
		return 1
	}
	if !reuseFirstWindow {
		if _, err := runner.Run(manager.TmuxSelectWindowCommandForSettings(settings, windowName)); err != nil {
			windowID, err := runner.Run(manager.TmuxCreateWindowCommandForSettings(settings, windowName, opts.Workspace, launchCmd))
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
	return finishHostedTerminalLaunch(settings, configDir, runner, stderr, noAttach)
}
