package cmd

import (
	"fmt"
	"io"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
)

var hostedTurnWatcherSidecarStarter = startHostedTurnWatcherSidecar

func startHostedTmuxServer(settings config.Settings, configDir string, initialCommand []string) (string, error) {
	response, err := managedTmuxServerStarter(tmuxServerStartRequest{
		ConfigDir:      configDir,
		LogDir:         expandHome(settings.LogDir),
		SocketName:     settings.Terminal.Tmux.SocketName,
		HostSession:    settings.Terminal.Tmux.HostSession,
		InitialCommand: initialCommand,
	})
	return response.Stdout, err
}

func finishHostedTerminalLaunch(settings config.Settings, configDir string, runner launchRunner, stderr io.Writer, noAttach bool) int {
	if settings.Terminal.Tmux.TurnStatusHooks && !noAttach {
		if err := hostedTurnWatcherSidecarStarter(configDir); err != nil {
			fmt.Fprintf(stderr, "failed to start hosted turn watcher: %v\n", err)
			return 1
		}
	}
	if noAttach {
		return 0
	}
	attachOutput, attachErr := runner.Run(manager.TmuxAttachCommandForSettings(settings))
	if err := writeTmuxClientExit(settings, attachOutput, attachErr); err != nil {
		fmt.Fprintf(stderr, "failed to log tmux client exit: %v\n", err)
		return 1
	}
	if attachErr != nil {
		fmt.Fprintf(stderr, "failed to attach tmux host: %v\n", attachErr)
		return 1
	}
	return 0
}
