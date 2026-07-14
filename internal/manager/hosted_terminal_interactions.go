package manager

import "github.com/jesse/agent-inn/internal/config"

const (
	TmuxHostedInteractionOwnerOption = "@ainn_hosted_interaction_owner"
	TmuxHostedInteractionMouseKey    = "MouseDown3Status"
	TmuxHostedInteractionRenameKey   = ","
)

func TmuxHostedInteractionOwnerCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings),
		"show-option", "-qv", "-t", tmuxHostSessionForSettings(settings),
		TmuxHostedInteractionOwnerOption,
	)
}

func TmuxSetHostedInteractionOwnerCommandForSettings(settings config.Settings, configDir string) []string {
	return append(tmuxPrefixForSettings(settings),
		"set-option", "-t", tmuxHostSessionForSettings(settings),
		TmuxHostedInteractionOwnerOption, configDir,
	)
}

func TmuxUnsetHostedInteractionOwnerCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings),
		"set-option", "-u", "-t", tmuxHostSessionForSettings(settings),
		TmuxHostedInteractionOwnerOption,
	)
}

func TmuxListHostedInteractionBindingCommandForSettings(settings config.Settings, table string, key string) []string {
	return append(tmuxPrefixForSettings(settings), "list-keys", "-T", table, key)
}

func TmuxUnbindHostedInteractionBindingCommandForSettings(settings config.Settings, table string, key string) []string {
	return append(tmuxPrefixForSettings(settings), "unbind-key", "-T", table, key)
}

func TmuxHostedInteractionMouseBindingCommandForSettings(settings config.Settings, configDir string, executable string) []string {
	shellCommand := tmuxShellQuote(executable) +
		" hosted-session menu --config-dir " + tmuxShellQuote(configDir) +
		" --window-id #{window_id}" +
		" --window-name " + tmuxShellEscapedWindowNameFormat +
		" --client-name #{q:client_name}" +
		" --x #{mouse_x} --y #{mouse_y}"
	command := "run-shell -b -t = " + tmuxCommandQuote(shellCommand)
	return append(tmuxPrefixForSettings(settings),
		"bind-key", "-T", "root", TmuxHostedInteractionMouseKey, command,
	)
}

func TmuxHostedInteractionRenameBindingCommandForSettings(settings config.Settings, configDir string, executable string) []string {
	shellCommand := tmuxShellQuote(executable) +
		" hosted-session rename-or-native --config-dir " + tmuxShellQuote(configDir) +
		" --window-id #{window_id}" +
		" --window-name " + tmuxShellEscapedWindowNameFormat
	command := "run-shell -b -t = " + tmuxCommandQuote(shellCommand)
	return append(tmuxPrefixForSettings(settings),
		"bind-key", "-T", "prefix", TmuxHostedInteractionRenameKey, command,
	)
}

func TmuxHostedSessionRenamePromptCommandForSettings(settings config.Settings, configDir string, executable string, windowID string, windowName string) []string {
	shellCommand := tmuxShellQuote(executable) +
		" hosted-session rename-current --config-dir " + tmuxShellQuote(configDir) +
		" --window-id " + tmuxShellQuote(windowID) +
		" --window-name " + tmuxShellEscapedWindowNameFormat
	command := "rename-window \"%%%\" ; run-shell -b " + tmuxCommandQuote(shellCommand)
	return append(tmuxPrefixForSettings(settings),
		"command-prompt", "-p", "Rename hosted session", "-I", windowName, command,
	)
}

func TmuxNativeRenameWindowPromptCommandForSettings(settings config.Settings, windowName string) []string {
	return append(tmuxPrefixForSettings(settings),
		"command-prompt", "-p", "Rename window", "-I", windowName, "rename-window \"%%%\"",
	)
}

func TmuxDisplayMenuCommandForSettings(settings config.Settings, clientName string, x string, y string, entries ...string) []string {
	args := append(tmuxPrefixForSettings(settings), "display-menu", "-c", clientName, "-x", x, "-y", y, "-T", "Hosted Sessions")
	return append(args, entries...)
}
