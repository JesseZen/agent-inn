package manager

import (
	"strconv"
	"strings"
)

type CodexLaunchOptions struct {
	Profile   string
	Workspace string
	AddDirs   []string
	WorkerPort int
	Model     string
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
