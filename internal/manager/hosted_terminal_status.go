package manager

import (
	"bytes"
	"os/exec"
	"strings"
)

type hostedTMuxRunner interface {
	Run(args []string) (string, error)
}

var hostedTMuxRunnerFactory = func() hostedTMuxRunner {
	return hostedTMuxRunnerFunc(func(args []string) (string, error) {
		cmd := exec.Command(args[0], args[1:]...)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &bytes.Buffer{}
		err := cmd.Run()
		return stdout.String(), err
	})
}

type hostedTMuxRunnerFunc func([]string) (string, error)

func (f hostedTMuxRunnerFunc) Run(args []string) (string, error) {
	return f(args)
}

func TmuxListWindowsCommand() []string {
	return append(tmuxPrefix(), "list-windows", "-t", tmuxHostSession, "-F", "#{window_id}")
}

func TmuxKillWindowCommand(windowID string) []string {
	target := tmuxHostSession + ":" + windowID
	return append(tmuxPrefix(), "kill-window", "-t", target)
}

func hostedSessionStatusForWindow(windowSet map[string]struct{}, windowID string) string {
	if windowID == "" {
		return hostedSessionStatusStale
	}
	if _, ok := windowSet[windowID]; ok {
		return hostedSessionStatusActive
	}
	return hostedSessionStatusStale
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

func hostedWindowSetFromRunner(runner hostedTMuxRunner) (map[string]struct{}, error) {
	if runner == nil {
		return map[string]struct{}{}, nil
	}
	if _, err := runner.Run(TmuxHasSessionCommand()); err != nil {
		return map[string]struct{}{}, nil
	}
	stdout, err := runner.Run(TmuxListWindowsCommand())
	if err != nil {
		return nil, err
	}
	return hostedWindowSet(stdout), nil
}
