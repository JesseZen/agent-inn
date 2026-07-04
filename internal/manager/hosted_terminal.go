package manager

import (
	"strings"

	"github.com/jesse/agent-inn/internal/config"
)

// AINN-owned tmux namespace. All hosted-terminal commands use `tmux -L ainn` to
// isolate AINN-managed sessions from user tmux sessions.
const (
	tmuxSocketName   = "ainn"
	tmuxHostSession  = "ainn-host"
	tmuxMainWindowID = "0"
	tmuxWindowPrefix = "ainn"
)

func defaultTmuxSettings() config.Settings {
	var cfg config.Config
	cfg.ApplyDefaults()
	return cfg.Settings
}

func tmuxPrefixForSettings(settings config.Settings) []string {
	settingsConfig := config.Config{Settings: settings}
	settingsConfig.ApplyDefaults()
	return []string{"tmux", "-L", settingsConfig.Settings.Terminal.Tmux.SocketName}
}

func tmuxHostSessionForSettings(settings config.Settings) string {
	settingsConfig := config.Config{Settings: settings}
	settingsConfig.ApplyDefaults()
	return settingsConfig.Settings.Terminal.Tmux.HostSession
}

func tmuxPrefix() []string {
	return tmuxPrefixForSettings(defaultTmuxSettings())
}

// TmuxDetectCommand returns the argv used to verify tmux is installed.
func TmuxDetectCommand() []string {
	return []string{"tmux", "-V"}
}

// TmuxHasSessionCommand returns the argv that checks whether the AINN host session exists.
func TmuxHasSessionCommand() []string {
	return TmuxHasSessionCommandForSettings(defaultTmuxSettings())
}

func TmuxHasSessionCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "has-session", "-t", tmuxHostSessionForSettings(settings))
}

// TmuxStartHostCommand returns the argv that starts the detached AINN host session.
func TmuxStartHostCommand() []string {
	return TmuxStartHostCommandForSettings(defaultTmuxSettings())
}

func TmuxStartHostCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "new-session", "-d", "-s", tmuxHostSessionForSettings(settings))
}

func TmuxStartHostWithWindowCommandForSettings(settings config.Settings, windowName string, command []string) []string {
	args := append(
		tmuxPrefixForSettings(settings),
		"new-session", "-d", "-s", tmuxHostSessionForSettings(settings),
		"-n", windowName,
		"-P", "-F", "#{window_id}",
	)
	return append(args, command...)
}

func TmuxStartMainWindowHostCommandForSettings(settings config.Settings, windowName string, command []string) []string {
	args := append(
		tmuxPrefixForSettings(settings),
		"new-session", "-d", "-s", tmuxHostSessionForSettings(settings),
		"-n", windowName,
		"-P", "-F", "#{window_index}",
	)
	return append(args, command...)
}

// TmuxCreateWindowCommand returns the argv that creates a new window in the AINN host
// running the given command.
func TmuxCreateWindowCommand(windowName string, command []string) []string {
	return TmuxCreateWindowCommandForSettings(defaultTmuxSettings(), windowName, command)
}

func TmuxCreateWindowCommandForSettings(settings config.Settings, windowName string, command []string) []string {
	args := append(tmuxPrefixForSettings(settings), "new-window", "-t", tmuxHostSessionForSettings(settings), "-n", windowName, "-P", "-F", "#{window_id}")
	return append(args, command...)
}

func tmuxMainWindowTargetForSettings(settings config.Settings) string {
	return tmuxHostSessionForSettings(settings) + ":" + tmuxMainWindowID
}

func TmuxCreateMainWindowCommandForSettings(settings config.Settings, windowName string, command []string) []string {
	args := append(tmuxPrefixForSettings(settings), "new-window", "-t", tmuxMainWindowTargetForSettings(settings), "-n", windowName, "-P", "-F", "#{window_id}")
	return append(args, command...)
}

// TmuxSelectWindowCommand returns the argv that switches to a window in the AINN host.
func TmuxSelectWindowCommand(windowID string) []string {
	return TmuxSelectWindowCommandForSettings(defaultTmuxSettings(), windowID)
}

func TmuxSelectWindowCommandForSettings(settings config.Settings, windowID string) []string {
	target := tmuxHostSessionForSettings(settings) + ":" + windowID
	return append(tmuxPrefixForSettings(settings), "select-window", "-t", target)
}

func TmuxSelectMainWindowCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "select-window", "-t", tmuxMainWindowTargetForSettings(settings))
}

// TmuxAttachCommand returns the argv that attaches to the AINN host session.
func TmuxAttachCommand() []string {
	return TmuxAttachCommandForSettings(defaultTmuxSettings())
}

func TmuxAttachCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "attach-session", "-t", tmuxHostSessionForSettings(settings))
}

func TmuxMainWindowPaneStartCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "list-panes", "-t", tmuxMainWindowTargetForSettings(settings), "-F", "#{pane_start_command}")
}

func TmuxRespawnMainWindowCommandForSettings(settings config.Settings, command []string) []string {
	args := append(tmuxPrefixForSettings(settings), "respawn-pane", "-k", "-t", tmuxMainWindowTargetForSettings(settings))
	return append(args, command...)
}

func TmuxMoveWindowToMainWindowCommandForSettings(settings config.Settings, windowIndex string) []string {
	source := tmuxHostSessionForSettings(settings) + ":" + windowIndex
	return append(tmuxPrefixForSettings(settings), "move-window", "-s", source, "-t", tmuxMainWindowTargetForSettings(settings))
}

func TmuxListClientPanesCommand(socketPath string) []string {
	return []string{"tmux", "-S", socketPath, "list-clients", "-F", "#{client_name}\t#{pane_id}"}
}

func TmuxSwitchClientToMainWindowCommandForSettings(settings config.Settings, clientName string) []string {
	return append(tmuxPrefixForSettings(settings), "switch-client", "-c", clientName, "-t", tmuxMainWindowTargetForSettings(settings))
}

// TmuxShowMouseCommand returns the argv that reads the AINN host mouse setting.
func TmuxShowMouseCommand() []string {
	return TmuxShowMouseCommandForSettings(defaultTmuxSettings())
}

func TmuxShowMouseCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "show", "-gv", "mouse")
}

// TmuxEnableMouseCommand returns the argv that enables mouse support in the AINN host.
func TmuxEnableMouseCommand() []string {
	return TmuxEnableMouseCommandForSettings(defaultTmuxSettings())
}

func TmuxEnableMouseCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "set-option", "-g", "mouse", "on")
}

// TmuxThemeCommandForSettings returns the argv that injects a browser-tab-like
// status bar theme into the AINN host session. Options use -g (global) because
// the -L ainn private tmux server is isolated from the user's main tmux, so
// global here means "ainn server only". Global window options are inherited by
// every window including newly created ones, so new tabs pick up the theme
// without re-injection. This mirrors the existing mouse setting which also
// uses -g.
func TmuxThemeCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings),
		"set-option", "-g", "status", "on", ";",
		"set-option", "-g", "status-left", "", ";",
		"set-option", "-g", "status-right", "", ";",
		"set-option", "-g", "status-style", "fg=colour244,bg=colour235", ";",
		"set-window-option", "-g", "window-status-format", "#[fg=colour244,bg=colour235] #I:#W #[default]", ";",
		"set-window-option", "-g", "window-status-current-format", "#[fg=colour0,bg=colour45,bold] #I:#W #[default]", ";",
		"set-window-option", "-g", "automatic-rename", "off",
	)
}

// SafeWindowName generates a tmux-safe window name from a session identifier.
// Non-alphanumeric characters (except `-` and `_`) are replaced with `-` so the
// name can be used unambiguously in tmux targets like `ainn-host:<window>`.
func SafeWindowName(sessionID string) string {
	var b strings.Builder
	b.WriteString(tmuxWindowPrefix)
	b.WriteByte(':')
	for _, r := range sessionID {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}
