package manager

import (
	"strconv"
	"strings"

	"github.com/jesse/agent-inn/internal/constants"
)

type CodexLaunchOptions struct {
	Profile    string
	Workspace  string
	AddDirs    []string
	WorkerPort int
	Model      string
}

type LaunchOptions struct {
	Launcher   string
	Profile    string
	Workspace  string
	AddDirs    []string
	WorkerPort int
	Model      string
}

func buildCodexLaunchCommand(opts CodexLaunchOptions) []string {
	cmd := []string{"codex", "--profile", opts.Profile}
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
	return cmd
}

func BuildCodexLaunchCommand(opts CodexLaunchOptions) []string {
	return buildCodexLaunchCommand(opts)
}

func BuildLaunchCommand(opts LaunchOptions) []string {
	if opts.Launcher == "claudecode" {
		return []string{
			"env",
			"ANTHROPIC_BASE_URL=http://" + constants.LocalhostAddr + ":" + strconv.Itoa(opts.WorkerPort),
			"ANTHROPIC_AUTH_TOKEN=ainn",
			"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1",
			"claude",
		}
	}
	return buildCodexLaunchCommand(CodexLaunchOptions{
		Profile:    opts.Profile,
		Workspace:  opts.Workspace,
		AddDirs:    opts.AddDirs,
		WorkerPort: opts.WorkerPort,
		Model:      opts.Model,
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
