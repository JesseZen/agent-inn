package cmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/jesse/codex-app-proxy/internal/manager"
)

const (
	modeExternalWindow = "external-window"
	modeHostedTerminal = "hosted-terminal"
)

type launchRunner interface {
	Run(args []string) error
}

type launchRunnerFunc func([]string) error

func (f launchRunnerFunc) Run(args []string) error {
	return f(args)
}

type multiString []string

func (m *multiString) String() string {
	return strings.Join(*m, ",")
}

func (m *multiString) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		*m = append(*m, part)
	}
	return nil
}

var launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
	return launchRunnerFunc(func(args []string) error {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Stdin = os.Stdin
		return cmd.Run()
	})
}

func runLaunch(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("launch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	worker := flags.String("worker", "", "worker port")
	profile := flags.String("profile", "", "codex profile")
	workspace := flags.String("cd", "", "workspace directory")
	var addDirs multiString
	flags.Var(&addDirs, "add-dir", "extra directories, comma separated")
	model := flags.String("model", "", "model override")
	mode := flags.String("mode", modeExternalWindow, "launch mode: external-window or hosted-terminal")
	noAttach := flags.Bool("no-attach", false, "hosted-terminal: set up window without attaching (for TUI use)")
	sessionID := flags.String("session-id", "", "hosted-terminal: existing CAP session id")
	sessionLabel := flags.String("session-label", "", "hosted-terminal: session label for new CAP sessions")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *worker == "" {
		fmt.Fprintln(stderr, "launch requires --worker")
		return 2
	}
	port, err := strconv.Atoi(*worker)
	if err != nil {
		fmt.Fprintf(stderr, "invalid worker port %q\n", *worker)
		return 2
	}
	if *profile == "" {
		*profile = *worker
	}
	switch *mode {
	case modeExternalWindow, modeHostedTerminal:
	default:
		fmt.Fprintf(stderr, "invalid mode %q\n", *mode)
		return 2
	}

	opts := manager.CodexLaunchOptions{
		Profile:    *profile,
		Workspace:  *workspace,
		AddDirs:    addDirs,
		WorkerPort: port,
		Model:      *model,
	}
	cmd := manager.BuildCodexLaunchCommand(opts)

	if *mode == modeHostedTerminal {
		return runHostedTerminalLaunch(opts, *profile, *sessionID, *sessionLabel, stdout, stderr, *noAttach)
	}

	runner := launchRunnerFactory(stdout, stderr)
	if err := runner.Run(cmd); err != nil {
		fmt.Fprintf(stderr, "failed to launch: %v\n", err)
		return 1
	}
	return 0
}

// runHostedTerminalLaunch runs the Codex CLI inside a CAP-owned tmux host.
// It ensures the host session exists, creates or switches to a window for the
// session, and attaches to the host. The sessionID determines the tmux window
// name so re-launching the same session switches to the existing window.
// When noAttach is true, the setup runs but the attach step is skipped so the
// caller (TUI) can decide whether to open a new terminal.
func runHostedTerminalLaunch(opts manager.CodexLaunchOptions, workerName string, sessionID string, sessionLabel string, stdout io.Writer, stderr io.Writer, noAttach bool) int {
	runner := launchRunnerFactory(stdout, stderr)

	if err := runner.Run(manager.TmuxDetectCommand()); err != nil {
		fmt.Fprintf(stderr, "tmux is required for hosted-terminal mode: %v\n", err)
		return 1
	}

	if err := runner.Run(manager.TmuxHasSessionCommand()); err != nil {
		if err := runner.Run(manager.TmuxStartHostCommand()); err != nil {
			fmt.Fprintf(stderr, "failed to start tmux host: %v\n", err)
			return 1
		}
	}

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(""))
	if sessionID != "" {
		session, ok, err := registry.Get(sessionID)
		if err != nil {
			fmt.Fprintf(stderr, "failed to load hosted session: %v\n", err)
			return 1
		}
		if !ok {
			fmt.Fprintf(stderr, "hosted session %q not found\n", sessionID)
			return 1
		}
		if session.TmuxWindowID == "" {
			fmt.Fprintf(stderr, "hosted session %q is stale\n", sessionID)
			return 1
		}
		if err := runner.Run(manager.TmuxSelectWindowCommand(session.TmuxWindowID)); err != nil {
			fmt.Fprintf(stderr, "failed to select tmux window: %v\n", err)
			return 1
		}
		if noAttach {
			return 0
		}
		if err := runner.Run(manager.TmuxAttachCommand()); err != nil {
			fmt.Fprintf(stderr, "failed to attach tmux host: %v\n", err)
			return 1
		}
		return 0
	}

	session, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: sessionLabel,
		WorkerName:   workerName,
		WorkerPort:   opts.WorkerPort,
		Workspace:    opts.Workspace,
		Model:        opts.Model,
		AddDirs:      append([]string{}, opts.AddDirs...),
	})
	if err != nil {
		fmt.Fprintf(stderr, "failed to create hosted session: %v\n", err)
		return 1
	}
	windowName := manager.SafeWindowName(session.SessionLabel)
	codexCmd := manager.BuildCodexLaunchCommand(opts)
	if err := runner.Run(manager.TmuxSelectWindowCommand(windowName)); err != nil {
		if err := runner.Run(manager.TmuxCreateWindowCommand(windowName, codexCmd)); err != nil {
			fmt.Fprintf(stderr, "failed to create tmux window: %v\n", err)
			return 1
		}
	}
	if err := registry.UpdateWindowID(session.SessionID, windowName); err != nil {
		fmt.Fprintf(stderr, "failed to persist hosted session: %v\n", err)
		return 1
	}
	if noAttach {
		return 0
	}
	if err := runner.Run(manager.TmuxAttachCommand()); err != nil {
		fmt.Fprintf(stderr, "failed to attach tmux host: %v\n", err)
		return 1
	}
	return 0
}
