package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
)

var hostedSessionMarkInput io.Reader = os.Stdin

func runHostedSession(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "hosted-session requires a subcommand")
		return 2
	}
	switch args[0] {
	case "mark":
		return runHostedSessionMark(args[1:], stdout, stderr)
	case "acknowledge":
		return runHostedSessionAcknowledge(args[1:], stdout, stderr)
	case "toggle-todo":
		return runHostedSessionToggleTodo(args[1:], stdout, stderr)
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
	launcherSessionID := flags.String("launcher-session-id", "", "launcher session id")
	captureLauncherSessionID := flags.Bool("capture-launcher-session-id", false, "read launcher session id from hook input")
	watchCodexTurn := flags.Bool("watch-codex-turn", false, "watch codex transcript for terminal turn state")
	transcriptPath := ""
	turnID := ""
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
	if *captureLauncherSessionID {
		var payload struct {
			HookEventName       string `json:"hook_event_name"`
			SessionID           string `json:"session_id"`
			TranscriptPath      string `json:"transcript_path"`
			TurnID              string `json:"turn_id"`
			AgentID             string `json:"agent_id"`
			AgentTranscriptPath string `json:"agent_transcript_path"`
		}
		if err := json.NewDecoder(hostedSessionMarkInput).Decode(&payload); err != nil {
			if !errors.Is(err, io.EOF) {
				fmt.Fprintf(stderr, "failed to parse hook input: %v\n", err)
				return 2
			}
		}
		if payload.AgentID != "" || payload.AgentTranscriptPath != "" || payload.HookEventName == "SubagentStart" || payload.HookEventName == "SubagentStop" {
			return 0
		}
		*launcherSessionID = strings.TrimSpace(payload.SessionID)
		if *watchCodexTurn {
			transcriptPath = strings.TrimSpace(payload.TranscriptPath)
			turnID = strings.TrimSpace(payload.TurnID)
			if transcriptPath == "" || turnID == "" {
				transcriptPath = ""
				turnID = ""
			}
		}
	}

	cfg, err := config.LoadFile(filepath.Join(*configDir, config.ConfigFileName))
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.MarkTurnStateWithWatch(*sessionID, *state, *reason, *launcherSessionID, transcriptPath, turnID)
	if err != nil {
		fmt.Fprintf(stderr, "failed to mark hosted session: %v\n", err)
		return 1
	}
	if session.TmuxWindowID == "" {
		return 0
	}
	runner := launchRunnerFactory(io.Discard, stderr)
	if *state == manager.HostedTurnStateDone {
		var err error
		session, err = acknowledgeHostedSessionDoneIfCurrent(cfg.Settings, registry, session, runner)
		if err != nil {
			fmt.Fprintf(stderr, "failed to acknowledge active hosted session: %v\n", err)
			return 1
		}
	}
	if _, err := runner.Run(manager.TmuxHostedTurnStatusCommandForRecord(cfg.Settings, session)); err != nil {
		fmt.Fprintf(stderr, "failed to update tmux turn status: %v\n", err)
		return 1
	}
	return 0
}

func runHostedSessionAcknowledge(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session acknowledge", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	windowID := flags.String("window-id", "", "tmux window id")
	windowName := flags.String("window-name", "", "tmux window name")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	*windowID = strings.TrimSpace(*windowID)
	*windowName = strings.TrimSpace(*windowName)
	if *windowID == "" {
		fmt.Fprintln(stderr, "hosted-session acknowledge requires --window-id")
		return 2
	}

	cfg, err := config.LoadFile(filepath.Join(*configDir, config.ConfigFileName))
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, ok, err := registry.AcknowledgeTurnByWindow(*windowID, *windowName)
	if err != nil {
		fmt.Fprintf(stderr, "failed to acknowledge hosted session: %v\n", err)
		return 1
	}
	if !ok {
		return 0
	}
	runner := launchRunnerFactory(io.Discard, stderr)
	if _, err := runner.Run(manager.TmuxHostedTurnStatusCommandForRecord(cfg.Settings, session)); err != nil {
		fmt.Fprintf(stderr, "failed to update tmux turn status: %v\n", err)
		return 1
	}
	return 0
}

func runHostedSessionToggleTodo(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session toggle-todo", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	windowID := flags.String("window-id", "", "tmux window id")
	windowName := flags.String("window-name", "", "tmux window name")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	*windowID = strings.TrimSpace(*windowID)
	*windowName = strings.TrimSpace(*windowName)
	if *windowID == "" {
		fmt.Fprintln(stderr, "hosted-session toggle-todo requires --window-id")
		return 2
	}

	cfg, err := config.LoadFile(filepath.Join(*configDir, config.ConfigFileName))
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, ok, err := registry.ToggleUserMarkerByWindow(*windowID, *windowName)
	if err != nil {
		fmt.Fprintf(stderr, "failed to toggle hosted session todo: %v\n", err)
		return 1
	}
	if !ok {
		return 0
	}
	runner := launchRunnerFactory(io.Discard, stderr)
	if _, err := runner.Run(manager.TmuxHostedTurnStatusCommandForRecord(cfg.Settings, session)); err != nil {
		fmt.Fprintf(stderr, "failed to update tmux turn status: %v\n", err)
		return 1
	}
	return 0
}

func acknowledgeHostedSessionDoneIfCurrent(settings config.Settings, registry *manager.HostedSessionRegistry, session manager.HostedSessionRecord, runner launchRunner) (manager.HostedSessionRecord, error) {
	activeWindowOut, err := runner.Run(manager.TmuxActiveWindowDetailsCommandForSettings(settings))
	if err != nil {
		return session, err
	}
	parts := strings.SplitN(strings.TrimSpace(activeWindowOut), "\t", 2)
	if len(parts) != 2 {
		return session, nil
	}
	activeWindows := map[string]string{parts[0]: parts[1]}
	if _, ok := manager.HostedSessionActiveWindowID(activeWindows, session); !ok {
		return session, nil
	}
	acknowledged, found, err := registry.AcknowledgeTurnByWindow(parts[0], parts[1])
	if err != nil {
		return session, err
	}
	if found {
		session = acknowledged
	}
	return session, nil
}
