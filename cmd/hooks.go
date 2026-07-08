package cmd

import (
	"fmt"
	"io"

	"github.com/jesse/agent-inn/internal/hostedhooks"
)

func runHooks(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: ainn hooks install|uninstall|status")
		return 2
	}
	switch args[0] {
	case "install":
		if err := hostedhooks.Install(); err != nil {
			fmt.Fprintf(stderr, "install hooks: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "installed")
		return 0
	case "uninstall":
		if err := hostedhooks.Uninstall(); err != nil {
			fmt.Fprintf(stderr, "uninstall hooks: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "uninstalled")
		return 0
	case "status":
		report, err := hostedhooks.Status()
		if err != nil {
			fmt.Fprintf(stderr, "status hooks: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "script: %s\n", hookStatusLabel(report.ScriptInstalled))
		fmt.Fprintf(stdout, "codex: %s\n", hookStatusLabel(report.CodexInstalled))
		fmt.Fprintf(stdout, "claude: %s\n", hookStatusLabel(report.ClaudeInstalled))
		return 0
	default:
		fmt.Fprintf(stderr, "unknown hooks command %q\n", args[0])
		return 2
	}
}

func hookStatusLabel(installed bool) string {
	if installed {
		return "installed"
	}
	return "missing"
}
