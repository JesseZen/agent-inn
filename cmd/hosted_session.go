package cmd

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
)

func runHostedSession(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "hosted-session requires a subcommand")
		return 2
	}
	switch args[0] {
	case "mark":
		return runHostedSessionMark(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown hosted-session subcommand %q\n", args[0])
		return 2
	}
}

func runHostedSessionMark(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session mark", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	sessionID := flags.String("session-id", "", "hosted session id")
	state := flags.String("state", "", "turn state")
	reason := flags.String("reason", "", "turn state reason")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	*sessionID = strings.TrimSpace(*sessionID)
	*state = strings.TrimSpace(*state)
	if *sessionID == "" {
		fmt.Fprintln(stderr, "hosted-session mark requires --session-id")
		return 2
	}
	switch *state {
	case manager.HostedTurnStateRunning, manager.HostedTurnStateDone, manager.HostedTurnStateFailed, manager.HostedTurnStateInterrupted, manager.HostedTurnStateIdle:
	default:
		fmt.Fprintf(stderr, "invalid hosted session turn state %q\n", *state)
		return 2
	}

	cfg, err := config.LoadFile(filepath.Join(*configDir, config.ConfigFileName))
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.MarkTurnState(*sessionID, *state, *reason)
	if err != nil {
		fmt.Fprintf(stderr, "failed to mark hosted session: %v\n", err)
		return 1
	}
	if session.TmuxWindowID == "" {
		return 0
	}
	runner := launchRunnerFactory(stdout, stderr)
	if _, err := runner.Run(manager.TmuxHostedTurnStatusCommandForSettings(cfg.Settings, session.TmuxWindowID, session.TurnState)); err != nil {
		fmt.Fprintf(stderr, "failed to update tmux turn status: %v\n", err)
		return 1
	}
	return 0
}
