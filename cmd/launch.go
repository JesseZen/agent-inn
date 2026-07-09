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
	hostedPopupTitle                = "Hosted Terminal"
	hostedPopupWidth                = "80%"
	hostedPopupHeight               = "70%"
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

var hostedTurnWatcherSidecarStarter = startHostedTurnWatcherSidecar

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
		return runHostedTerminalLaunch(cfg, opts, resolvedConfigDir, *profile, *sessionID, *sessionLabel, stdout, stderr, *noAttach)
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
func runHostedTerminalLaunch(cfg config.Config, opts manager.LaunchOptions, configDir string, workerName string, sessionID string, sessionLabel string, stdout io.Writer, stderr io.Writer, noAttach bool) int {
	runner := launchRunnerFactory(stdout, stderr)
	settings := cfg.Settings

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
			if session.TurnGeneration > 0 {
				fmt.Fprintf(stderr, "hosted session %q is stale and has no launcher session id\n", sessionID)
				return 1
			}
		} else {
			reopenOpts.LauncherSessionID = session.LauncherSessionID
			reopenOpts.LauncherSessionMode = manager.LauncherSessionModeResume
		}
		launchCmd := hostedSessionLaunchCommand(manager.BuildLaunchCommand(reopenOpts), configDir, session.SessionID, settings.Terminal.Tmux.TurnStatusHooks)
		windowID, err := runner.Run(manager.TmuxCreateWindowCommandForSettings(settings, session.SessionLabel, launchCmd))
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
	launchCmd := hostedSessionLaunchCommand(manager.BuildLaunchCommand(opts), configDir, session.SessionID, settings.Terminal.Tmux.TurnStatusHooks)
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
	return finishHostedTerminalLaunch(settings, configDir, runner, stderr, noAttach)
}

func finishHostedTerminalLaunch(settings config.Settings, configDir string, runner launchRunner, stderr io.Writer, noAttach bool) int {
	if settings.Terminal.Tmux.TurnStatusHooks && !noAttach {
		if err := hostedTurnWatcherSidecarStarter(configDir); err != nil {
			fmt.Fprintf(stderr, "failed to start hosted turn watcher: %v\n", err)
			return 1
		}
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

func startHostedTurnWatcherSidecar(configDir string) error {
	rootLock, err := rootLockPath(configDir)
	if err != nil {
		return err
	}
	release, err := rootLockerFactory(rootLock).Acquire()
	if err == nil {
		release()
	} else if err == errAlreadyLocked {
		return nil
	} else {
		return err
	}

	cmd := exec.Command(hostedSessionExecutable(), "hosted-session", "watch-all", "--config-dir", configDir)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func installTmuxTurnStatusHooks(runner launchRunner, settings config.Settings, configDir string, executable string) error {
	ownerOut, err := runner.Run(manager.TmuxTurnStatusOwnerCommandForSettings(settings))
	if err != nil {
		return fmt.Errorf("inspect tmux turn status owner: %w", err)
	}
	owner := strings.TrimSpace(ownerOut)
	if owner != "" {
		if owner != configDir {
			return fmt.Errorf("tmux turn status hooks are owned by config dir %q, current config dir is %q; use a unique tmux socket/session for test instances", owner, configDir)
		}
	} else {
		hooksOut, err := runner.Run(manager.TmuxShowHooksCommandForSettings(settings))
		if err != nil {
			return fmt.Errorf("inspect tmux hooks: %w", err)
		}
		mouseBindingOut, err := runner.Run(manager.TmuxListAcknowledgeTurnMouseBindingCommandForSettings(settings))
		if err != nil {
			return fmt.Errorf("inspect tmux mouse binding: %w", err)
		}
		todoBindingOut, err := runner.Run(manager.TmuxListToggleTodoMouseBindingCommandForSettings(settings))
		if err != nil {
			return fmt.Errorf("inspect tmux todo mouse binding: %w", err)
		}
		legacyOwner, found, err := managedTurnStatusConfigDir(hooksOut, mouseBindingOut, todoBindingOut)
		if err != nil {
			return err
		}
		if found && legacyOwner != configDir {
			return fmt.Errorf("tmux turn status hooks are owned by legacy config dir %q, current config dir is %q; use a unique tmux socket/session for test instances", legacyOwner, configDir)
		}
		if _, err := runner.Run(manager.TmuxSetTurnStatusOwnerCommandForSettings(settings, configDir)); err != nil {
			return fmt.Errorf("set tmux turn status owner: %w", err)
		}
	}
	if _, err := runner.Run(manager.TmuxAcknowledgeTurnHookCommandForSettings(settings, configDir, executable)); err != nil {
		return fmt.Errorf("install tmux turn acknowledgement hook: %w", err)
	}
	if _, err := runner.Run(manager.TmuxToggleTodoMouseBindingCommandForSettings(settings, configDir, executable)); err != nil {
		return fmt.Errorf("install tmux hosted todo mouse binding: %w", err)
	}
	return nil
}

func installTmuxHostedPopupBinding(runner launchRunner, settings config.Settings, configDir string, executable string) error {
	key := strings.TrimSpace(settings.Terminal.Tmux.HostedPopupKey)
	ownerOut, err := runner.Run(manager.TmuxHostedPopupOwnerCommandForSettings(settings))
	if err != nil {
		return fmt.Errorf("inspect tmux hosted popup owner: %w", err)
	}
	owner := strings.TrimSpace(ownerOut)
	if owner != "" && owner != configDir {
		return fmt.Errorf("tmux hosted popup binding is owned by config dir %q, current config dir is %q; use a unique tmux socket/session for test instances", owner, configDir)
	}

	ownedKey := ""
	if key != "" {
		keyOut, err := runner.Run(manager.TmuxHostedPopupKeyCommandForSettings(settings))
		if err != nil {
			return fmt.Errorf("inspect tmux hosted popup key owner: %w", err)
		}
		ownedKey = strings.TrimSpace(keyOut)

		bindingOut, err := runner.Run(manager.TmuxListHostedPopupBindingCommandForSettings(settings, key))
		if err != nil {
			if !strings.HasSuffix(strings.TrimSpace(err.Error()), "unknown key: "+key) {
				return fmt.Errorf("inspect tmux hosted popup binding: %w", err)
			}
			bindingOut = ""
		}
		binding := strings.TrimSpace(bindingOut)
		if binding != "" && owner == "" {
			return fmt.Errorf("tmux hosted popup key %q already has a binding and no AINN owner; choose a different hosted_popup_key or use a unique tmux socket/session", key)
		}
		if binding != "" && ownedKey != key {
			return fmt.Errorf("tmux hosted popup key %q already has a non-AINN binding; choose a different hosted_popup_key or use a unique tmux socket/session", key)
		}
		if binding != "" {
			ownedBinding := strings.Contains(binding, "bind-key -T prefix "+key+" ") &&
				strings.Contains(binding, "display-popup") &&
				strings.Contains(binding, "-E") &&
				strings.Contains(binding, "-x R") &&
				strings.Contains(binding, "-y 0") &&
				strings.Contains(binding, hostedPopupWidth) &&
				strings.Contains(binding, hostedPopupHeight) &&
				strings.Contains(binding, hostedPopupTitle) &&
				strings.Contains(binding, "hosted-session popup") &&
				strings.Contains(binding, "--config-dir") &&
				strings.Contains(binding, configDir)
			if !ownedBinding {
				return fmt.Errorf("tmux hosted popup key %q already has a non-AINN binding; choose a different hosted_popup_key or use a unique tmux socket/session", key)
			}
		}
	}

	if owner == "" {
		if _, err := runner.Run(manager.TmuxSetHostedPopupOwnerCommandForSettings(settings, configDir)); err != nil {
			return fmt.Errorf("set tmux hosted popup owner: %w", err)
		}
	}

	managerURL := strings.TrimSpace(os.Getenv("AINN_URL"))
	if managerURL == "" {
		managerURL = defaultManagerURL
	}
	mode := manager.TmuxHostedPopupMouseModeSelect
	if settings.Terminal.Tmux.TurnStatusHooks {
		mode = manager.TmuxHostedPopupMouseModeAcknowledge
	}
	if _, err := runner.Run(manager.TmuxHostedPopupMouseBindingCommandForSettings(settings, configDir, managerURL, executable, mode)); err != nil {
		return fmt.Errorf("install tmux hosted popup mouse binding: %w", err)
	}
	if key == "" {
		return nil
	}
	if ownedKey != key {
		if _, err := runner.Run(manager.TmuxSetHostedPopupKeyCommandForSettings(settings, key)); err != nil {
			return fmt.Errorf("set tmux hosted popup key owner: %w", err)
		}
	}

	if _, err := runner.Run(manager.TmuxHostedPopupBindingCommandForSettings(settings, key, configDir, managerURL, executable)); err != nil {
		return fmt.Errorf("install tmux hosted popup binding: %w", err)
	}
	return nil
}

func managedTurnStatusConfigDir(hooksOutput string, acknowledgeBindingOutput string, todoBindingOutput string) (string, bool, error) {
	owner := ""
	found := false
	outputs := []struct {
		text    string
		matches func(string) bool
	}{
		{
			text: hooksOutput,
			matches: func(line string) bool {
				return strings.Contains(line, manager.TmuxAcknowledgeTurnHook) && strings.Contains(line, hostedSessionAcknowledgeCommand)
			},
		},
		{
			text: acknowledgeBindingOutput,
			matches: func(line string) bool {
				return strings.Contains(line, manager.TmuxAcknowledgeMouseKey) && strings.Contains(line, hostedSessionAcknowledgeCommand)
			},
		},
		{
			text: todoBindingOutput,
			matches: func(line string) bool {
				return strings.Contains(line, manager.TmuxToggleTodoMouseKey) && strings.Contains(line, hostedSessionToggleTodoCommand)
			},
		},
	}
	for _, output := range outputs {
		for _, line := range strings.Split(output.text, "\n") {
			if !output.matches(line) {
				continue
			}
			marker := "--config-dir "
			index := strings.Index(line, marker)
			if index < 0 {
				return "", false, fmt.Errorf("failed to parse managed tmux turn status hook config dir from %q", line)
			}
			configDir, ok := parseTmuxSingleQuotedToken(line[index+len(marker):])
			if !ok {
				return "", false, fmt.Errorf("failed to parse managed tmux turn status hook config dir from %q", line)
			}
			if found && owner != configDir {
				return "", false, fmt.Errorf("tmux turn status hooks contain multiple legacy config dirs %q and %q; use a unique tmux socket/session for test instances", owner, configDir)
			}
			owner = configDir
			found = true
		}
	}
	return owner, found, nil
}

func parseTmuxSingleQuotedToken(value string) (string, bool) {
	if value == "" || value[0] != '\'' {
		return "", false
	}
	var parsed strings.Builder
	for i := 1; i < len(value); {
		if strings.HasPrefix(value[i:], "'\\''") {
			parsed.WriteByte('\'')
			i += len("'\\''")
			continue
		}
		if value[i] == '\'' {
			return parsed.String(), true
		}
		parsed.WriteByte(value[i])
		i++
	}
	return "", false
}

func hostedSessionLaunchCommand(command []string, configDir string, sessionID string, turnStatusHooks bool) []string {
	executable := hostedSessionExecutable()
	env := []string{"env"}
	if turnStatusHooks {
		env = append(env, "AINN_HOSTED_SESSION_ID="+sessionID)
	}
	env = append(env, "AINN_CONFIG_DIR="+configDir, "AINN_EXECUTABLE="+executable)
	if len(command) > 0 && command[0] == "env" {
		return append(env, command[1:]...)
	}
	return append(env, command...)
}

func hostedSessionExecutable() string {
	executable, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return executable
}
