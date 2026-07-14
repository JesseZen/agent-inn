package cmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/manager"
)

const tmuxNativeMouseDown3StatusBinding = `bind-key -T root MouseDown3Status display-menu -T "#[align=centre]#{window_index}:#{window_name}" -t = -x W -y W "#{?#{>:#{session_windows},1},,-}Swap Left" l { swap-window -t :-1 } "#{?#{>:#{session_windows},1},,-}Swap Right" r { swap-window -t :+1 } "#{?pane_marked_set,,-}Swap Marked" s { swap-window } '' Kill X { kill-window } Respawn R { respawn-window -k } "#{?pane_marked,Unmark,Mark}" m { select-pane -m } Rename n { command-prompt -F -I "#W" { rename-window -t "#{window_id}" "%%" } } '' "New After" w { new-window -a } "New At End" W { new-window }`
const tmuxNativeRenameWindowBinding = `bind-key -T prefix , command-prompt -I "#W" { rename-window "%%" }`

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

func hostedSessionLaunchCommand(command []string, configDir string, sessionID string, turnStatusHooks bool) []string {
	env := []string{"env"}
	if turnStatusHooks {
		env = append(env, "AINN_HOSTED_SESSION_ID="+sessionID)
	}
	env = append(env, "AINN_CONFIG_DIR="+configDir, "AINN_EXECUTABLE="+hostedSessionExecutable())
	if len(command) > 0 && command[0] == "env" {
		for _, arg := range command[1:] {
			env = append(env, arg)
			if arg == constants.ClaudeCodeProviderManagedEnv {
				env = append(env, constants.ClaudeCodeDisableAgentViewEnv)
			}
		}
		return env
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

func workerConfigByPort(cfg config.Config, port int) (string, config.WorkerConfig, bool) {
	for workerID, worker := range cfg.Workers {
		if worker.Port == port {
			return workerID, worker, true
		}
	}
	return "", config.WorkerConfig{}, false
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

func installTmuxHostedInteractions(runner launchRunner, settings config.Settings, configDir string, executable string) error {
	configDir, err := canonicalHostedInteractionConfigDir(configDir)
	if err != nil {
		return err
	}
	ownerOut, err := runner.Run(manager.TmuxHostedInteractionOwnerCommandForSettings(settings))
	if err != nil {
		return fmt.Errorf("inspect tmux hosted interaction owner: %w", err)
	}
	owner := strings.TrimSpace(ownerOut)
	if owner != "" && owner != configDir {
		return fmt.Errorf("tmux hosted interactions are owned by config dir %q, current config dir is %q; use a unique tmux socket/session for test instances", owner, configDir)
	}
	mouseBinding, err := runner.Run(manager.TmuxListHostedInteractionBindingCommandForSettings(settings, "root", manager.TmuxHostedInteractionMouseKey))
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "unknown key") {
		return fmt.Errorf("inspect tmux hosted interaction mouse binding: %w", err)
	}
	if strings.TrimSpace(mouseBinding) != "" && !isAINNHostedInteractionBinding(mouseBinding, manager.TmuxHostedInteractionMouseKey, configDir) {
		return fmt.Errorf("tmux hosted interaction mouse binding is not owned by AINN; choose a unique tmux socket/session")
	}
	renameBinding, err := runner.Run(manager.TmuxListHostedInteractionBindingCommandForSettings(settings, "prefix", manager.TmuxHostedInteractionRenameKey))
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "unknown key") {
		return fmt.Errorf("inspect tmux hosted interaction rename binding: %w", err)
	}
	if strings.TrimSpace(renameBinding) != "" && !isAINNHostedInteractionBinding(renameBinding, manager.TmuxHostedInteractionRenameKey, configDir) {
		return fmt.Errorf("tmux hosted interaction rename binding is not owned by AINN; choose a unique tmux socket/session")
	}
	if owner == "" {
		if _, err := runner.Run(manager.TmuxSetHostedInteractionOwnerCommandForSettings(settings, configDir)); err != nil {
			return fmt.Errorf("set tmux hosted interaction owner: %w", err)
		}
	}
	if _, err := runner.Run(manager.TmuxHostedInteractionMouseBindingCommandForSettings(settings, configDir, executable)); err != nil {
		return fmt.Errorf("install tmux hosted interaction mouse binding: %w", err)
	}
	if _, err := runner.Run(manager.TmuxHostedInteractionRenameBindingCommandForSettings(settings, configDir, executable)); err != nil {
		return fmt.Errorf("install tmux hosted interaction rename binding: %w", err)
	}
	return nil
}

func resetTmuxHostedInteractions(runner launchRunner, settings config.Settings, configDir string) error {
	configDir, err := canonicalHostedInteractionConfigDir(configDir)
	if err != nil {
		return err
	}
	ownerOut, err := runner.Run(manager.TmuxHostedInteractionOwnerCommandForSettings(settings))
	if err != nil {
		return fmt.Errorf("inspect tmux hosted interaction owner: %w", err)
	}
	if strings.TrimSpace(ownerOut) != configDir {
		return nil
	}
	if _, err := runner.Run(manager.TmuxUnbindHostedInteractionBindingCommandForSettings(settings, "root", manager.TmuxHostedInteractionMouseKey)); err != nil {
		return fmt.Errorf("remove tmux hosted interaction mouse binding: %w", err)
	}
	if _, err := runner.Run(manager.TmuxUnbindHostedInteractionBindingCommandForSettings(settings, "prefix", manager.TmuxHostedInteractionRenameKey)); err != nil {
		return fmt.Errorf("remove tmux hosted interaction rename binding: %w", err)
	}
	if _, err := runner.Run(manager.TmuxUnsetHostedInteractionOwnerCommandForSettings(settings)); err != nil {
		return fmt.Errorf("remove tmux hosted interaction owner: %w", err)
	}
	return nil
}

func isAINNHostedInteractionBinding(binding string, key string, configDir string) bool {
	if isNativeHostedInteractionBinding(binding, key) {
		return true
	}
	configToken := "--config-dir " + hostedInteractionShellQuote(configDir)
	return strings.Contains(binding, key) &&
		strings.Contains(binding, configToken) &&
		(strings.Contains(binding, "hosted-session menu") || strings.Contains(binding, "hosted-session rename-or-native"))
}

func isNativeHostedInteractionBinding(binding string, key string) bool {
	binding = strings.TrimSpace(binding)
	if key == manager.TmuxHostedInteractionMouseKey {
		return binding == tmuxNativeMouseDown3StatusBinding
	}
	return key == manager.TmuxHostedInteractionRenameKey && binding == tmuxNativeRenameWindowBinding
}

func canonicalHostedInteractionConfigDir(configDir string) (string, error) {
	absConfigDir, err := filepath.Abs(expandHome(configDir))
	if err != nil {
		return "", fmt.Errorf("failed to resolve config dir: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absConfigDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve config dir: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func loadHostedInteractionConfig(configDir string) (config.Config, string, error) {
	configDir, err := canonicalHostedInteractionConfigDir(configDir)
	if err != nil {
		return config.Config{}, "", err
	}
	cfg, err := config.LoadFile(filepath.Join(configDir, config.ConfigFileName))
	return cfg, configDir, err
}

func hostedInteractionShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func hostedInteractionCommandQuote(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}

func hostedSessionRenamePromptCommand(configDir string, executable string, windowID string, windowName string) string {
	shellCommand := hostedInteractionShellQuote(executable) +
		" hosted-session rename-current --config-dir " + hostedInteractionShellQuote(configDir) +
		" --window-id " + hostedInteractionShellQuote(windowID) +
		" --window-name #{q:window_name}"
	return "command-prompt -p \"Rename hosted session\" -I " + hostedInteractionCommandQuote(windowName) +
		" \"rename-window \\\"%%%\\\" ; run-shell -b " + hostedInteractionCommandQuote(shellCommand) + "\""
}

func hostedSessionNativeRenamePromptCommand(windowName string) string {
	return "command-prompt -p \"Rename window\" -I " + hostedInteractionCommandQuote(windowName) + " \"rename-window \\\"%%%\\\"\""
}

func hostedInteractionDisplayPopupCommand(configDir string, managerURL string, executable string) string {
	shellCommand := hostedInteractionShellQuote(executable) +
		" hosted-session popup --config-dir " + hostedInteractionShellQuote(configDir) +
		" --manager-url " + hostedInteractionShellQuote(managerURL)
	return "display-popup -E -x R -y 0 -w " + manager.TmuxHostedPopupWidth +
		" -h " + manager.TmuxHostedPopupHeight +
		" -T " + hostedInteractionShellQuote(manager.TmuxHostedPopupTitle) +
		" " + shellCommand
}

func hostedSessionStatusRender(cfg config.Config, session manager.HostedSessionRecord, runner launchRunner) error {
	snapshot := manager.MapHostedSessionSnapshot(session, manager.HostedSessionStatusActive, manager.HostedSessionWorkerSnapshot{})
	_, err := runner.Run(manager.TmuxHostedTurnStatusCommandForSnapshot(cfg.Settings, session.TmuxWindowID, snapshot))
	return err
}

func runHostedSessionSetMarker(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session set-marker", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	windowID := flags.String("window-id", "", "tmux window id")
	marker := flags.String("marker", "", "hosted session marker")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*windowID) == "" || (*marker != "" && *marker != manager.HostedUserMarkerTodo) {
		fmt.Fprintln(stderr, "hosted-session set-marker requires --window-id and marker todo or empty")
		return 2
	}
	cfg, _, err := loadHostedInteractionConfig(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, found, err := registry.FindByWindow(strings.TrimSpace(*windowID))
	if err != nil {
		fmt.Fprintf(stderr, "failed to find hosted session: %v\n", err)
		return 1
	}
	if !found {
		return 0
	}
	session, err = registry.SetUserMarker(session.SessionID, *marker)
	if err != nil {
		fmt.Fprintf(stderr, "failed to set hosted session marker: %v\n", err)
		return 1
	}
	runner := launchRunnerFactory(io.Discard, stderr)
	if err := hostedSessionStatusRender(cfg, session, runner); err != nil {
		fmt.Fprintf(stderr, "failed to update tmux turn status: %v\n", err)
		return 1
	}
	return 0
}

func runHostedSessionMarkUnread(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session mark-unread", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	windowID := flags.String("window-id", "", "tmux window id")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := loadHostedInteractionConfig(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, found, err := registry.FindByWindow(strings.TrimSpace(*windowID))
	if err != nil {
		fmt.Fprintf(stderr, "failed to find hosted session: %v\n", err)
		return 1
	}
	if !found {
		return 0
	}
	session, err = registry.MarkTurnUnread(session.SessionID)
	if err != nil {
		fmt.Fprintf(stderr, "failed to mark hosted session unread: %v\n", err)
		return 1
	}
	runner := launchRunnerFactory(io.Discard, stderr)
	if err := hostedSessionStatusRender(cfg, session, runner); err != nil {
		fmt.Fprintf(stderr, "failed to update tmux turn status: %v\n", err)
		return 1
	}
	return 0
}

func runHostedSessionRenameCurrent(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session rename-current", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	windowID := flags.String("window-id", "", "tmux window id")
	windowName := flags.String("window-name", "", "tmux window name")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := loadHostedInteractionConfig(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	if _, found, err := registry.RenameByWindow(strings.TrimSpace(*windowID), *windowName); err != nil {
		fmt.Fprintf(stderr, "failed to rename hosted session: %v\n", err)
		return 1
	} else if !found {
		return 0
	}
	return 0
}

func runHostedSessionRenameOrNative(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session rename-or-native", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	windowID := flags.String("window-id", "", "tmux window id")
	windowName := flags.String("window-name", "", "tmux window name")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	cfg, resolvedConfigDir, err := loadHostedInteractionConfig(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	_, found, err := registry.FindByWindow(strings.TrimSpace(*windowID))
	if err != nil {
		fmt.Fprintf(stderr, "failed to find hosted session: %v\n", err)
		return 1
	}
	runner := launchRunnerFactory(io.Discard, stderr)
	var command []string
	if found {
		command = manager.TmuxHostedSessionRenamePromptCommandForSettings(cfg.Settings, resolvedConfigDir, hostedSessionExecutable(), strings.TrimSpace(*windowID), *windowName)
	} else {
		command = manager.TmuxNativeRenameWindowPromptCommandForSettings(cfg.Settings, *windowName)
	}
	if _, err := runner.Run(command); err != nil {
		fmt.Fprintf(stderr, "failed to open rename prompt: %v\n", err)
		return 1
	}
	return 0
}

func runHostedSessionMenu(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session menu", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	windowID := flags.String("window-id", "", "tmux window id")
	_ = flags.String("window-name", "", "tmux window name")
	clientName := flags.String("client-name", "", "tmux client name")
	x := flags.String("x", "0", "menu x position")
	y := flags.String("y", "0", "menu y position")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	cfg, resolvedConfigDir, err := loadHostedInteractionConfig(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, found, err := registry.FindByWindow(strings.TrimSpace(*windowID))
	if err != nil {
		fmt.Fprintf(stderr, "failed to find hosted session: %v\n", err)
		return 1
	}
	if !found {
		return 0
	}
	snapshot := manager.MapHostedSessionSnapshot(session, manager.HostedSessionStatusActive, manager.HostedSessionWorkerSnapshot{})
	target := cfg.Settings.Terminal.Tmux.HostSession + ":" + session.TmuxWindowID
	markerLabel := "Mark todo"
	markerValue := manager.HostedUserMarkerTodo
	if snapshot.UserMarker == manager.HostedUserMarkerTodo {
		markerLabel = "Clear todo"
		markerValue = ""
	}
	executable := hostedSessionExecutable()
	setMarkerCommand := hostedInteractionShellQuote(executable) + " hosted-session set-marker --config-dir " + hostedInteractionShellQuote(resolvedConfigDir) + " --window-id " + hostedInteractionShellQuote(session.TmuxWindowID) + " --marker " + hostedInteractionShellQuote(markerValue)
	entries := []string{
		"Open", "o", "select-window -t " + target,
		markerLabel, "m", "run-shell -b " + hostedInteractionCommandQuote(setMarkerCommand),
		"Rename", "r", hostedSessionRenamePromptCommand(resolvedConfigDir, executable, session.TmuxWindowID, session.SessionLabel),
	}
	state := snapshot.Turn.State
	terminal := state == manager.HostedTurnStateDone || state == manager.HostedTurnStateFailed || state == manager.HostedTurnStateInterrupted
	if terminal && !snapshot.Turn.Unread {
		markUnreadCommand := hostedInteractionShellQuote(executable) + " hosted-session mark-unread --config-dir " + hostedInteractionShellQuote(resolvedConfigDir) + " --window-id " + hostedInteractionShellQuote(session.TmuxWindowID)
		entries = append(entries, "Mark unread", "u", "run-shell -b "+hostedInteractionCommandQuote(markUnreadCommand))
	}
	entries = append(entries, "-", "", "")
	managerURL := strings.TrimSpace(os.Getenv("AINN_URL"))
	if managerURL == "" {
		managerURL = defaultManagerURL
	}
	entries = append(entries, "Hosted Terminal", "h", hostedInteractionDisplayPopupCommand(resolvedConfigDir, managerURL, executable))
	runner := launchRunnerFactory(io.Discard, stderr)
	if _, err := runner.Run(manager.TmuxDisplayMenuCommandForSettings(cfg.Settings, *clientName, *x, *y, entries...)); err != nil {
		fmt.Fprintf(stderr, "failed to display hosted session menu: %v\n", err)
		return 1
	}
	return 0
}

func runHostedSessionResetInteractions(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session reset-interactions", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	cfg, resolvedConfigDir, err := loadHostedInteractionConfig(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	runner := launchRunnerFactory(io.Discard, stderr)
	if err := resetTmuxHostedInteractions(runner, cfg.Settings, resolvedConfigDir); err != nil {
		fmt.Fprintf(stderr, "failed to reset tmux hosted interactions: %v\n", err)
		return 1
	}
	return 0
}
