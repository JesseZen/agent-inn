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

func TmuxKillWindowCommand(windowID string) []string {
	return TmuxKillWindowCommandForSettings(defaultTmuxSettings(), windowID)
}

func TmuxKillWindowCommandForSettings(settings config.Settings, windowID string) []string {
	target := tmuxHostSessionForSettings(settings) + ":" + windowID
	return append(tmuxPrefixForSettings(settings), "kill-window", "-t", target)
}

func hostedSessionStatusForWindow(windows map[string]string, session HostedSessionRecord) string {
	if session.TmuxWindowID == "" {
		return hostedSessionStatusStale
	}
	windowName, ok := windows[session.TmuxWindowID]
	if !ok {
		return hostedSessionStatusStale
	}
	if windowName != session.SessionLabel {
		return hostedSessionStatusStale
	}
	return hostedSessionStatusActive
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
		errText := err.Error()
		if strings.Contains(errText, tmuxNoServerRunningError) ||
			strings.Contains(errText, tmuxCantFindSessionError) ||
			(strings.Contains(errText, tmuxErrorConnectingError) && strings.Contains(errText, tmuxNoSuchFileError)) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	stdout, err := runner.Run(TmuxListWindowDetailsCommandForSettings(settings))
	if err != nil {
		return nil, err
	}
	return hostedWindowDetails(stdout), nil
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
