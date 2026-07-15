package manager

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
)

const (
	tmuxNoServerRunningError = "no server running"
	tmuxCantFindSessionError = "can't find session"
	tmuxErrorConnectingError = "error connecting to "
	tmuxNoSuchFileError      = "No such file or directory"
	// tmux hooks are array options; this slot lets AINN replace its own hook without clearing user hooks.
	TmuxAcknowledgeTurnHook          = "after-select-window[90]"
	TmuxAcknowledgeMouseKey          = "MouseDown1Status"
	TmuxToggleTodoMouseKey           = "DoubleClick1Status"
	TmuxHostedPopupTitle             = "Hosted Terminal"
	TmuxHostedPopupWidth             = "40%"
	TmuxHostedPopupHeight            = "100%"
	tmuxTurnStatusOwnerOption        = "@ainn_turn_status_owner"
	tmuxHostedPopupOwnerOption       = "@ainn_hosted_popup_owner"
	tmuxHostedPopupKeyOption         = "@ainn_hosted_popup_key"
	tmuxShellEscapedWindowNameFormat = "#{q:window_name}"
	tmuxHostedPopupStatusRange       = "ainn-sessions"
)

type TmuxHostedPopupMouseMode int

const (
	TmuxHostedPopupMouseModeSelect TmuxHostedPopupMouseMode = iota
	TmuxHostedPopupMouseModeAcknowledge
)

type hostedTMuxRunner interface {
	Run(args []string) (string, error)
}

var hostedTMuxRunnerFactory = func() hostedTMuxRunner {
	return hostedTMuxRunnerFunc(func(args []string) (string, error) {
		cmd := exec.Command(args[0], args[1:]...)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		stderrText := strings.TrimSpace(stderr.String())
		if err != nil && stderrText != "" {
			err = fmt.Errorf("%w: %s", err, stderrText)
		}
		return stdout.String(), err
	})
}

type hostedTMuxRunnerFunc func([]string) (string, error)

func (f hostedTMuxRunnerFunc) Run(args []string) (string, error) {
	return f(args)
}

func TmuxListWindowsCommand() []string {
	return TmuxListWindowsCommandForSettings(defaultTmuxSettings())
}

func TmuxListWindowsCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "list-windows", "-t", tmuxHostSessionForSettings(settings), "-F", "#{window_id}")
}

func TmuxListWindowDetailsCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "list-windows", "-t", tmuxHostSessionForSettings(settings), "-F", "#{window_id}\t#{window_name}")
}

func TmuxActiveWindowDetailsCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "display-message", "-p", "-t", tmuxHostSessionForSettings(settings), "#{window_id}\t#{window_name}")
}

func TmuxKillWindowCommand(windowID string) []string {
	return TmuxKillWindowCommandForSettings(defaultTmuxSettings(), windowID)
}

func TmuxKillWindowCommandForSettings(settings config.Settings, windowID string) []string {
	target := tmuxHostSessionForSettings(settings) + ":" + windowID
	return append(tmuxPrefixForSettings(settings), "kill-window", "-t", target)
}

func TmuxRenameWindowCommandForSettings(settings config.Settings, windowID string, name string) []string {
	target := tmuxHostSessionForSettings(settings) + ":" + windowID
	return append(tmuxPrefixForSettings(settings), "rename-window", "-t", target, name)
}

func TmuxHostedTurnStatusCommandForSnapshot(settings config.Settings, windowID string, snapshot HostedSessionSnapshot) []string {
	target := tmuxHostSessionForSettings(settings) + ":" + windowID
	marker := ""
	inactiveStyle := tmuxWindowStatusStyle
	currentStyle := tmuxWindowStatusCurrentStyle
	switch {
	case snapshot.Turn.State == HostedTurnStateRunning && snapshot.Turn.NeedsInput:
		marker = "?"
		inactiveStyle = "fg=colour208,bg=colour235,bold"
		currentStyle = "fg=colour0,bg=colour208,bold"
	case snapshot.Turn.State == HostedTurnStateRunning:
		marker = "*"
		inactiveStyle = "fg=colour45,bg=colour235,bold"
	case snapshot.Turn.Unread && snapshot.Turn.State == HostedTurnStateDone:
		marker = "+"
		inactiveStyle = "fg=colour46,bg=colour235,bold"
		currentStyle = "fg=colour0,bg=colour46,bold"
	case snapshot.Turn.Unread && (snapshot.Turn.State == HostedTurnStateFailed || snapshot.Turn.State == HostedTurnStateInterrupted):
		marker = "!"
		inactiveStyle = "fg=colour196,bg=colour235,bold"
		currentStyle = "fg=colour231,bg=colour196,bold"
	case snapshot.UserMarker == HostedUserMarkerTodo:
		marker = "~"
		inactiveStyle = "fg=colour226,bg=colour235,bold"
		currentStyle = "fg=colour0,bg=colour226,bold"
	case snapshot.Turn.State == HostedTurnStateDone:
		marker = "+"
	case snapshot.Turn.State == HostedTurnStateFailed || snapshot.Turn.State == HostedTurnStateInterrupted:
		marker = "!"
	}
	inactiveLabel := tmuxWindowStatusFormat
	currentLabel := tmuxWindowStatusCurrentFormat
	if marker != "" {
		inactiveLabel = " #I:" + marker + " #W "
		currentLabel = inactiveLabel
	}
	return append(tmuxPrefixForSettings(settings),
		"set-window-option", "-t", target, "window-status-format", inactiveLabel, ";",
		"set-window-option", "-t", target, "window-status-current-format", currentLabel, ";",
		"set-window-option", "-t", target, "window-status-style", inactiveStyle, ";",
		"set-window-option", "-t", target, "window-status-current-style", currentStyle,
	)
}

func (w *hostedTurnWatcher) tmuxStatusCommand(session HostedSessionRecord) []string {
	snapshot := MapHostedSessionSnapshot(session, HostedSessionStatusActive, HostedSessionWorkerSnapshot{})
	return TmuxHostedTurnStatusCommandForSnapshot(w.settings, session.TmuxWindowID, snapshot)
}

func (w *hostedTurnWatcher) runTmuxStatus(session HostedSessionRecord, path string, position int64) error {
	if _, err := w.runner.Run(w.tmuxStatusCommand(session)); err != nil {
		w.startupReconciled = false
		return hostedTurnPollFailureWith(hostedTurnProjectionCategory, path, position, session.SessionID, err)
	}
	return nil
}

func TmuxAcknowledgeTurnHookCommandForSettings(settings config.Settings, configDir string, executable string) []string {
	shellCommand := tmuxShellQuote(executable) +
		" hosted-session acknowledge --config-dir " + tmuxShellQuote(configDir) +
		" --window-id #{window_id}" +
		" --window-name " + tmuxShellEscapedWindowNameFormat
	command := "run-shell -b " + tmuxCommandQuote(shellCommand)
	return append(tmuxPrefixForSettings(settings),
		"set-hook", "-t", tmuxHostSessionForSettings(settings),
		TmuxAcknowledgeTurnHook, command,
	)
}

func TmuxAcknowledgeTurnMouseBindingCommandForSettings(settings config.Settings, configDir string, executable string) []string {
	shellCommand := tmuxShellQuote(executable) +
		" hosted-session acknowledge --config-dir " + tmuxShellQuote(configDir) +
		" --window-id #{window_id}" +
		" --window-name " + tmuxShellEscapedWindowNameFormat
	command := "switch-client -t = ; run-shell -b -t = " + tmuxCommandQuote(shellCommand)
	return append(tmuxPrefixForSettings(settings),
		"bind-key", "-T", "root", TmuxAcknowledgeMouseKey,
		command,
	)
}

func TmuxToggleTodoMouseBindingCommandForSettings(settings config.Settings, configDir string, executable string) []string {
	shellCommand := tmuxShellQuote(executable) +
		" hosted-session toggle-todo --config-dir " + tmuxShellQuote(configDir) +
		" --window-id #{window_id}" +
		" --window-name " + tmuxShellEscapedWindowNameFormat
	command := "run-shell -b -t = " + tmuxCommandQuote(shellCommand)
	return append(tmuxPrefixForSettings(settings),
		"bind-key", "-T", "root", TmuxToggleTodoMouseKey,
		command,
	)
}

func TmuxTurnStatusOwnerCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings),
		"show-option", "-qv", "-t", tmuxHostSessionForSettings(settings),
		tmuxTurnStatusOwnerOption,
	)
}

func TmuxSetTurnStatusOwnerCommandForSettings(settings config.Settings, configDir string) []string {
	return append(tmuxPrefixForSettings(settings),
		"set-option", "-t", tmuxHostSessionForSettings(settings),
		tmuxTurnStatusOwnerOption, configDir,
	)
}

func TmuxHostedPopupOwnerCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings),
		"show-option", "-qv", "-t", tmuxHostSessionForSettings(settings),
		tmuxHostedPopupOwnerOption,
	)
}

func TmuxSetHostedPopupOwnerCommandForSettings(settings config.Settings, configDir string) []string {
	return append(tmuxPrefixForSettings(settings),
		"set-option", "-t", tmuxHostSessionForSettings(settings),
		tmuxHostedPopupOwnerOption, configDir,
	)
}

func TmuxHostedPopupKeyCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings),
		"show-option", "-qv", "-t", tmuxHostSessionForSettings(settings),
		tmuxHostedPopupKeyOption,
	)
}

func TmuxSetHostedPopupKeyCommandForSettings(settings config.Settings, key string) []string {
	return append(tmuxPrefixForSettings(settings),
		"set-option", "-t", tmuxHostSessionForSettings(settings),
		tmuxHostedPopupKeyOption, key,
	)
}

func TmuxListHostedPopupBindingCommandForSettings(settings config.Settings, key string) []string {
	return append(tmuxPrefixForSettings(settings), "list-keys", "-T", "prefix", key)
}

func TmuxUnbindHostedPopupBindingCommandForSettings(settings config.Settings, key string) []string {
	return append(tmuxPrefixForSettings(settings), "unbind-key", "-T", "prefix", key)
}

func TmuxDisplayHostedPopupCommandForSettings(settings config.Settings, configDir string, managerURL string, executable string) []string {
	shellCommand := tmuxShellQuote(executable) +
		" hosted-session popup --config-dir " + tmuxShellQuote(configDir) +
		" --manager-url " + tmuxShellQuote(managerURL)
	return append(tmuxPrefixForSettings(settings),
		"display-popup", "-E",
		"-x", "R", "-y", "0",
		"-w", TmuxHostedPopupWidth, "-h", TmuxHostedPopupHeight,
		"-T", TmuxHostedPopupTitle,
		shellCommand,
	)
}

func tmuxDisplayHostedPopupCommand(configDir string, managerURL string, executable string) string {
	shellCommand := tmuxShellQuote(executable) +
		" hosted-session popup --config-dir " + tmuxShellQuote(configDir) +
		" --manager-url " + tmuxShellQuote(managerURL)
	return "display-popup -E -x R -y 0 -w " + TmuxHostedPopupWidth +
		" -h " + TmuxHostedPopupHeight +
		" -T " + tmuxShellQuote(TmuxHostedPopupTitle) +
		" " + shellCommand
}

func TmuxHostedPopupBindingCommandForSettings(settings config.Settings, key string, configDir string, managerURL string, executable string) []string {
	command := tmuxDisplayHostedPopupCommand(configDir, managerURL, executable)
	return append(tmuxPrefixForSettings(settings),
		"bind-key", "-T", "prefix", key, command,
	)
}

func TmuxHostedPopupMouseBindingCommandForSettings(settings config.Settings, configDir string, managerURL string, executable string, mode TmuxHostedPopupMouseMode) []string {
	falseCommand := "switch-client -t ="
	if mode == TmuxHostedPopupMouseModeAcknowledge {
		shellCommand := tmuxShellQuote(executable) +
			" hosted-session acknowledge --config-dir " + tmuxShellQuote(configDir) +
			" --window-id #{window_id}" +
			" --window-name " + tmuxShellEscapedWindowNameFormat
		falseCommand += " ; run-shell -b -t = " + tmuxCommandQuote(shellCommand)
	}
	command := "if -F " +
		tmuxCommandQuote("#{==:#{mouse_status_range},"+tmuxHostedPopupStatusRange+"}") +
		" " + tmuxCommandQuote(tmuxDisplayHostedPopupCommand(configDir, managerURL, executable)) +
		" " + tmuxCommandQuote(falseCommand)
	return append(tmuxPrefixForSettings(settings),
		"bind-key", "-T", "root", TmuxAcknowledgeMouseKey,
		command,
	)
}

func TmuxShowHooksCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "show-hooks", "-t", tmuxHostSessionForSettings(settings))
}

func TmuxListAcknowledgeTurnMouseBindingCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "list-keys", "-T", "root", TmuxAcknowledgeMouseKey)
}

func TmuxListToggleTodoMouseBindingCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "list-keys", "-T", "root")
}

func tmuxShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func tmuxCommandQuote(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}

func hostedSessionStatusForWindow(windows map[string]string, session HostedSessionRecord) string {
	if _, ok := HostedSessionActiveWindowID(windows, session); !ok {
		return hostedSessionStatusStale
	}
	return hostedSessionStatusActive
}

func HostedSessionActiveWindowID(windows map[string]string, session HostedSessionRecord) (string, bool) {
	if session.TmuxWindowID == "" {
		return "", false
	}
	windowName, ok := windows[session.TmuxWindowID]
	if ok && windowName == session.SessionLabel {
		return session.TmuxWindowID, true
	}
	for windowID, windowName := range windows {
		if windowName == session.TmuxWindowID {
			return windowID, true
		}
	}
	return "", false
}

func hostedWindowSet(out string) map[string]struct{} {
	windowSet := map[string]struct{}{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		windowSet[line] = struct{}{}
	}
	return windowSet
}

func hostedWindowDetails(out string) map[string]string {
	windows := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
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
	return windows
}

func hostedWindowDetailsFromRunnerForSettings(settings config.Settings, runner hostedTMuxRunner) (map[string]string, error) {
	if runner == nil {
		return map[string]string{}, nil
	}
	if _, err := runner.Run(TmuxHasSessionCommandForSettings(settings)); err != nil {
		if isTmuxHostMissingError(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	stdout, err := runner.Run(TmuxListWindowDetailsCommandForSettings(settings))
	if err != nil {
		if isTmuxHostMissingError(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	return hostedWindowDetails(stdout), nil
}

func isTmuxHostMissingError(err error) bool {
	errText := err.Error()
	return strings.Contains(errText, tmuxNoServerRunningError) ||
		strings.Contains(errText, tmuxCantFindSessionError) ||
		(strings.Contains(errText, tmuxErrorConnectingError) && strings.Contains(errText, tmuxNoSuchFileError))
}

func hostedWindowSetFromRunnerForSettings(settings config.Settings, runner hostedTMuxRunner) (map[string]struct{}, error) {
	windows, err := hostedWindowDetailsFromRunnerForSettings(settings, runner)
	if err != nil {
		return nil, err
	}
	windowSet := map[string]struct{}{}
	for windowID := range windows {
		windowSet[windowID] = struct{}{}
	}
	return windowSet, nil
}

func hostedWindowSetFromRunner(runner hostedTMuxRunner) (map[string]struct{}, error) {
	return hostedWindowSetFromRunnerForSettings(defaultTmuxSettings(), runner)
}
