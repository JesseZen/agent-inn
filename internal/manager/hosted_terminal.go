package manager

import "strings"

// CAP-owned tmux namespace. All hosted-terminal commands use `tmux -L cap` to
// isolate CAP-managed sessions from user tmux sessions.
const (
	tmuxSocketName   = "cap"
	tmuxHostSession  = "cap-host"
	tmuxWindowPrefix = "codex"
)

func tmuxPrefix() []string {
	return []string{"tmux", "-L", tmuxSocketName}
}

// TmuxDetectCommand returns the argv used to verify tmux is installed.
func TmuxDetectCommand() []string {
	return []string{"tmux", "-V"}
}

// TmuxHasSessionCommand returns the argv that checks whether the CAP host session exists.
func TmuxHasSessionCommand() []string {
	return append(tmuxPrefix(), "has-session", "-t", tmuxHostSession)
}

// TmuxStartHostCommand returns the argv that starts the detached CAP host session.
func TmuxStartHostCommand() []string {
	return append(tmuxPrefix(), "new-session", "-d", "-s", tmuxHostSession)
}

// TmuxCreateWindowCommand returns the argv that creates a new window in the CAP host
// running the given command.
func TmuxCreateWindowCommand(windowName string, command []string) []string {
	args := append(tmuxPrefix(), "new-window", "-t", tmuxHostSession, "-n", windowName)
	return append(args, command...)
}

// TmuxSelectWindowCommand returns the argv that switches to a window in the CAP host.
func TmuxSelectWindowCommand(windowID string) []string {
	target := tmuxHostSession + ":" + windowID
	return append(tmuxPrefix(), "select-window", "-t", target)
}

// TmuxAttachCommand returns the argv that attaches to the CAP host session.
func TmuxAttachCommand() []string {
	return append(tmuxPrefix(), "attach-session", "-t", tmuxHostSession)
}

// SafeWindowName generates a tmux-safe window name from a session identifier.
// Non-alphanumeric characters (except `-` and `_`) are replaced with `-` so the
// name can be used unambiguously in tmux targets like `cap-host:<window>`.
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
