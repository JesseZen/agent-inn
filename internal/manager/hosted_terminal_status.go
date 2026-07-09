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
	tmuxAcknowledgeTurnHook          = "after-select-window[90]"
	tmuxAcknowledgeMouseKey          = "MouseDown1Status"
	tmuxToggleTodoMouseKey           = "DoubleClick1Status"
	tmuxTurnStatusOwnerOption        = "@ainn_turn_status_owner"
	tmuxShellEscapedWindowNameFormat = "#{q:window_name}"
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

func TmuxHostedTurnStatusCommandForSettings(settings config.Settings, windowID string, state string) []string {
	return tmuxHostedTurnStatusCommand(settings, windowID, state, true, "")
}

func TmuxHostedTurnStatusCommandForRecord(settings config.Settings, session HostedSessionRecord) []string {
	unread := !isHostedTurnTerminalState(session.TurnState) || session.TurnGeneration > session.TurnAcknowledgedGeneration
	return tmuxHostedTurnStatusCommand(settings, session.TmuxWindowID, session.TurnState, unread, session.UserMarker)
}

func TmuxAcknowledgeTurnHookCommandForSettings(settings config.Settings, configDir string, executable string) []string {
	shellCommand := tmuxShellQuote(executable) +
		" hosted-session acknowledge --config-dir " + tmuxShellQuote(configDir) +
		" --window-id #{window_id}" +
		" --window-name " + tmuxShellEscapedWindowNameFormat
	command := "run-shell -b " + tmuxCommandQuote(shellCommand)
	return append(tmuxPrefixForSettings(settings),
		"set-hook", "-t", tmuxHostSessionForSettings(settings),
		tmuxAcknowledgeTurnHook, command,
	)
}

func TmuxAcknowledgeTurnMouseBindingCommandForSettings(settings config.Settings, configDir string, executable string) []string {
	shellCommand := tmuxShellQuote(executable) +
		" hosted-session acknowledge --config-dir " + tmuxShellQuote(configDir) +
		" --window-id #{window_id}" +
		" --window-name " + tmuxShellEscapedWindowNameFormat
	command := "switch-client -t = ; run-shell -b -t = " + tmuxCommandQuote(shellCommand)
	return append(tmuxPrefixForSettings(settings),
		"bind-key", "-T", "root", tmuxAcknowledgeMouseKey,
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
		"bind-key", "-T", "root", tmuxToggleTodoMouseKey,
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

func TmuxShowHooksCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "show-hooks", "-t", tmuxHostSessionForSettings(settings))
}

func TmuxListAcknowledgeTurnMouseBindingCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "list-keys", "-T", "root", tmuxAcknowledgeMouseKey)
}

func TmuxListToggleTodoMouseBindingCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "list-keys", "-T", "root", tmuxToggleTodoMouseKey)
}

func tmuxHostedTurnStatusCommand(settings config.Settings, windowID string, state string, unread bool, userMarker string) []string {
	target := tmuxHostSessionForSettings(settings) + ":" + windowID
	format := "#[fg=colour244,bg=colour235] #I:#W #[default]"
	currentFormat := "#[fg=colour0,bg=colour45,bold] #I:#W #[default]"
	switch state {
	case HostedTurnStateRunning:
		format = "#[fg=colour45,bg=colour235,bold] #I:* #W #[default]"
		currentFormat = "#[fg=colour0,bg=colour45,bold] #I:* #W #[default]"
	case HostedTurnStateDone:
		format = "#[fg=colour46,bg=colour235,bold] #I:+ #W #[default]"
		currentFormat = "#[fg=colour0,bg=colour46,bold] #I:+ #W #[default]"
		if !unread {
			format = "#[fg=colour244,bg=colour235] #I:+ #W #[default]"
			currentFormat = "#[fg=colour0,bg=colour45,bold] #I:+ #W #[default]"
		}
	case HostedTurnStateFailed, HostedTurnStateInterrupted:
		format = "#[fg=colour196,bg=colour235,bold] #I:! #W #[default]"
		currentFormat = "#[fg=colour231,bg=colour196,bold] #I:! #W #[default]"
		if !unread {
			format = "#[fg=colour244,bg=colour235] #I:! #W #[default]"
			currentFormat = "#[fg=colour0,bg=colour45,bold] #I:! #W #[default]"
		}
	}
	if userMarker == HostedUserMarkerTodo {
		switch state {
		case HostedTurnStateRunning:
		case HostedTurnStateDone, HostedTurnStateFailed, HostedTurnStateInterrupted:
			if !unread {
				format = "#[fg=colour226,bg=colour235,bold] #I:~ #W #[default]"
				currentFormat = "#[fg=colour0,bg=colour226,bold] #I:~ #W #[default]"
			}
		default:
			format = "#[fg=colour226,bg=colour235,bold] #I:~ #W #[default]"
			currentFormat = "#[fg=colour0,bg=colour226,bold] #I:~ #W #[default]"
		}
	}
	return append(tmuxPrefixForSettings(settings),
		"set-window-option", "-t", target, "window-status-format", format, ";",
		"set-window-option", "-t", target, "window-status-current-format", currentFormat,
	)
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
