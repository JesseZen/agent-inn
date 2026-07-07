package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
)

var hostedSessionMarkInput io.Reader = os.Stdin

const (
	codexTranscriptPollInterval = 500 * time.Millisecond
	codexTranscriptWatchTimeout = 24 * time.Hour
	codexTranscriptMaxLineBytes = 10 * 1024 * 1024
	codexTaskFailedReason       = "codex_task_failed"
)

var hostedSessionWatchStarter = func(configDir string, sessionID string, transcriptPath string, turnID string, turnGeneration int) error {
	cmd := exec.Command(hostedSessionExecutable(),
		"hosted-session", "watch-turn",
		"--config-dir", configDir,
		"--session-id", sessionID,
		"--transcript-path", transcriptPath,
		"--turn-id", turnID,
		"--turn-generation", strconv.Itoa(turnGeneration),
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

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
	case "watch-turn":
		return runHostedSessionWatchTurn(args[1:], stdout, stderr)
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
			SessionID      string `json:"session_id"`
			TranscriptPath string `json:"transcript_path"`
			TurnID         string `json:"turn_id"`
		}
		if err := json.NewDecoder(hostedSessionMarkInput).Decode(&payload); err != nil {
			if !errors.Is(err, io.EOF) {
				fmt.Fprintf(stderr, "failed to parse hook input: %v\n", err)
				return 2
			}
		}
		*launcherSessionID = payload.SessionID
		if *watchCodexTurn {
			transcriptPath = strings.TrimSpace(payload.TranscriptPath)
			turnID = strings.TrimSpace(payload.TurnID)
			if transcriptPath == "" || turnID == "" {
				fmt.Fprintln(stderr, "hosted-session mark --watch-codex-turn requires transcript_path and turn_id in hook input")
				return 2
			}
		}
	}
	if *watchCodexTurn && (transcriptPath == "" || turnID == "") {
		fmt.Fprintln(stderr, "hosted-session mark --watch-codex-turn requires transcript_path and turn_id in hook input")
		return 2
	}

	cfg, err := config.LoadFile(filepath.Join(*configDir, config.ConfigFileName))
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	session, err := registry.MarkTurnState(*sessionID, *state, *reason, *launcherSessionID)
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
	if *watchCodexTurn {
		if err := hostedSessionWatchStarter(*configDir, *sessionID, transcriptPath, turnID, session.TurnGeneration); err != nil {
			fmt.Fprintf(stderr, "failed to start codex turn watcher: %v\n", err)
			return 1
		}
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

func runHostedSessionWatchTurn(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session watch-turn", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	sessionID := flags.String("session-id", "", "hosted session id")
	transcriptPath := flags.String("transcript-path", "", "launcher transcript path")
	turnID := flags.String("turn-id", "", "launcher turn id")
	turnGeneration := flags.Int("turn-generation", 0, "hosted turn generation")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	*sessionID = strings.TrimSpace(*sessionID)
	*transcriptPath = strings.TrimSpace(*transcriptPath)
	*turnID = strings.TrimSpace(*turnID)
	if *sessionID == "" || *transcriptPath == "" || *turnID == "" || *turnGeneration <= 0 {
		fmt.Fprintln(stderr, "hosted-session watch-turn requires --session-id, --transcript-path, --turn-id, and --turn-generation")
		return 2
	}

	cfg, err := config.LoadFile(filepath.Join(*configDir, config.ConfigFileName))
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	deadline := time.Now().Add(codexTranscriptWatchTimeout)
	for time.Now().Before(deadline) {
		session, ok, err := registry.Get(*sessionID)
		if err != nil {
			fmt.Fprintf(stderr, "failed to load hosted session: %v\n", err)
			return 1
		}
		if !ok || session.TurnGeneration != *turnGeneration {
			return 0
		}
		if session.TurnState != manager.HostedTurnStateRunning && session.TurnState != manager.HostedTurnStateDone {
			return 0
		}

		file, err := os.Open(*transcriptPath)
		if err == nil {
			scanner := bufio.NewScanner(file)
			scanner.Buffer(nil, codexTranscriptMaxLineBytes)
			for scanner.Scan() {
				var event struct {
					Type    string `json:"type"`
					Payload struct {
						Type             string          `json:"type"`
						TurnID           string          `json:"turn_id"`
						LastAgentMessage json.RawMessage `json:"last_agent_message"`
					} `json:"payload"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
					_ = file.Close()
					fmt.Fprintf(stderr, "failed to parse codex transcript: %v\n", err)
					return 1
				}
				if event.Type != "event_msg" || event.Payload.Type != "task_complete" || event.Payload.TurnID != *turnID {
					continue
				}
				state := manager.HostedTurnStateDone
				reason := ""
				lastAgentMessage := strings.TrimSpace(string(event.Payload.LastAgentMessage))
				if lastAgentMessage == "" || lastAgentMessage == "null" {
					state = manager.HostedTurnStateFailed
					reason = codexTaskFailedReason
				}
				session, err = registry.MarkTurnState(*sessionID, state, reason, "")
				if err != nil {
					_ = file.Close()
					fmt.Fprintf(stderr, "failed to mark hosted session: %v\n", err)
					return 1
				}
				if session.TmuxWindowID != "" {
					runner := launchRunnerFactory(io.Discard, stderr)
					if state == manager.HostedTurnStateDone {
						session, err = acknowledgeHostedSessionDoneIfCurrent(cfg.Settings, registry, session, runner)
						if err != nil {
							_ = file.Close()
							fmt.Fprintf(stderr, "failed to acknowledge active hosted session: %v\n", err)
							return 1
						}
					}
					if _, err := runner.Run(manager.TmuxHostedTurnStatusCommandForRecord(cfg.Settings, session)); err != nil {
						_ = file.Close()
						fmt.Fprintf(stderr, "failed to update tmux turn status: %v\n", err)
						return 1
					}
				}
				_ = file.Close()
				return 0
			}
			if err := scanner.Err(); err != nil {
				_ = file.Close()
				fmt.Fprintf(stderr, "failed to read codex transcript: %v\n", err)
				return 1
			}
			_ = file.Close()
		} else if !os.IsNotExist(err) {
			fmt.Fprintf(stderr, "failed to open codex transcript: %v\n", err)
			return 1
		}
		time.Sleep(codexTranscriptPollInterval)
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
