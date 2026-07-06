package manager

import (
	"strconv"
	"strings"

	"github.com/jesse/agent-inn/internal/constants"
)

type CodexLaunchOptions struct {
	Profile             string
	Workspace           string
	AddDirs             []string
	WorkerPort          int
	Model               string
	LauncherSessionID   string
	LauncherSessionMode LauncherSessionMode
}

type LauncherSessionMode int

const (
	LauncherSessionModeNew LauncherSessionMode = iota
	LauncherSessionModeResume
)

type LaunchOptions struct {
	Launcher            string
	Profile             string
	Workspace           string
	AddDirs             []string
	WorkerPort          int
	Model               string
	LauncherSessionID   string
	LauncherSessionMode LauncherSessionMode
}

func buildCodexLaunchCommand(opts CodexLaunchOptions) []string {
	cmd := []string{"codex"}
	if opts.LauncherSessionMode == LauncherSessionModeResume {
		cmd = append(cmd, "resume")
	}
	cmd = append(cmd, "--profile", opts.Profile)
	if opts.Workspace != "" {
		cmd = append(cmd, "--cd", opts.Workspace)
	}
	for _, dir := range opts.AddDirs {
		if dir == "" {
			continue
		}
		cmd = append(cmd, "--add-dir", dir)
	}
	if opts.Model != "" {
		cmd = append(cmd, "--model", opts.Model)
	}
	if opts.LauncherSessionMode == LauncherSessionModeResume {
		cmd = append(cmd, opts.LauncherSessionID)
	}
	return cmd
}

func BuildCodexLaunchCommand(opts CodexLaunchOptions) []string {
	return buildCodexLaunchCommand(opts)
}

func BuildLaunchCommand(opts LaunchOptions) []string {
	if opts.Launcher == "claudecode" {
		cmd := []string{
			"env",
			"ANTHROPIC_BASE_URL=http://" + constants.LocalhostAddr + ":" + strconv.Itoa(opts.WorkerPort),
			"ANTHROPIC_AUTH_TOKEN=ainn",
			"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1",
			"claude",
		}
		if opts.LauncherSessionMode == LauncherSessionModeResume {
			cmd = append(cmd, "--resume", opts.LauncherSessionID)
		}
		return cmd
	}
	return buildCodexLaunchCommand(CodexLaunchOptions{
		Profile:             opts.Profile,
		Workspace:           opts.Workspace,
		AddDirs:             opts.AddDirs,
		WorkerPort:          opts.WorkerPort,
		Model:               opts.Model,
		LauncherSessionID:   opts.LauncherSessionID,
		LauncherSessionMode: opts.LauncherSessionMode,
	})
}

func renderCodexLaunchCommand(cmd []string) string {
	quoted := make([]string, 0, len(cmd))
	for _, part := range cmd {
		quoted = append(quoted, strconv.Quote(part))
	}
	return strings.Join(quoted, " ")
}

func RenderCodexLaunchCommand(cmd []string) string {
	return renderCodexLaunchCommand(cmd)
}
