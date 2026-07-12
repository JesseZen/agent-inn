package manager

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/jesse/agent-inn/internal/constants"
)

const (
	claudeCodeLauncherName = "claudecode"
	grokLauncherName       = "grok"
	openCodeLauncherName   = "opencode"
	piLauncherName         = "pi"
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
	GrokHome            string
	GrokExecutable      string
	Model               string
	APIFormat           string
	PiAgentDir          string
	LauncherSessionID   string
	LauncherSessionMode LauncherSessionMode
}

type openCodeConfig struct {
	Provider map[string]openCodeProvider `json:"provider"`
	Model    string                      `json:"model"`
}

type openCodeProvider struct {
	NPM     string                       `json:"npm"`
	Name    string                       `json:"name"`
	Options openCodeProviderOptions      `json:"options"`
	Models  map[string]openCodeModelInfo `json:"models"`
}

type openCodeProviderOptions struct {
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
}

type openCodeModelInfo struct {
	Name string `json:"name"`
}

func buildCodexLaunchCommand(opts CodexLaunchOptions) ([]string, error) {
	profile, err := CodexProfileName(opts.Profile)
	if err != nil {
		return nil, err
	}
	cmd := []string{"codex"}
	if opts.LauncherSessionMode == LauncherSessionModeResume {
		cmd = append(cmd, "resume")
	}
	cmd = append(cmd, "--profile", profile)
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
	return cmd, nil
}

func BuildCodexLaunchCommand(opts CodexLaunchOptions) ([]string, error) {
	return buildCodexLaunchCommand(opts)
}

func BuildLaunchCommand(opts LaunchOptions) ([]string, error) {
	if opts.Launcher == piLauncherName {
		cmd := []string{"env", "PI_CODING_AGENT_DIR=" + opts.PiAgentDir, "pi", "--provider", opts.Profile, "--model", opts.Model, "--api-key", "ainn"}
		return cmd, nil
	}
	if opts.Launcher == openCodeLauncherName {
		npm := "@ai-sdk/openai-compatible"
		if opts.APIFormat == "" || opts.APIFormat == "responses" {
			npm = "@ai-sdk/openai"
		} else if opts.APIFormat == "anthropic" {
			npm = "@ai-sdk/anthropic"
		}
		model := opts.Model
		if model == "" {
			model = opts.Profile
		}
		config := openCodeConfig{
			Provider: map[string]openCodeProvider{
				"ainn": {
					NPM:  npm,
					Name: "AINN",
					Options: openCodeProviderOptions{
						BaseURL: "http://" + constants.LocalhostAddr + ":" + strconv.Itoa(opts.WorkerPort) + "/v1",
						APIKey:  "ainn",
					},
					Models: map[string]openCodeModelInfo{model: {Name: model}},
				},
			},
			Model: "ainn/" + model,
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			return nil, err
		}
		cmd := []string{"env", "OPENCODE_CONFIG_CONTENT=" + string(encoded), "opencode"}
		if opts.Workspace != "" {
			cmd = append(cmd, opts.Workspace)
		}
		cmd = append(cmd, "--model", config.Model)
		return cmd, nil
	}
	if opts.Launcher == grokLauncherName {
		cmd := []string{"env"}
		if opts.GrokHome != "" {
			cmd = append(cmd, "HOME="+opts.GrokHome)
		}
		executable := opts.GrokExecutable
		if executable == "" {
			executable = "grok"
		}
		cmd = append(cmd, "XAI_API_KEY=ainn", executable)
		model := opts.Profile
		if model == "" {
			model = opts.Model
		}
		if model != "" {
			cmd = append(cmd, "--model", model)
		}
		return cmd, nil
	}
	if opts.Launcher == claudeCodeLauncherName {
		cmd := []string{
			"env",
			"ANTHROPIC_BASE_URL=http://" + constants.LocalhostAddr + ":" + strconv.Itoa(opts.WorkerPort),
			"ANTHROPIC_AUTH_TOKEN=ainn",
			constants.ClaudeCodeProviderManagedEnv,
			"claude",
		}
		if opts.LauncherSessionMode == LauncherSessionModeResume {
			cmd = append(cmd, "--resume", opts.LauncherSessionID)
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
		return cmd, nil
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
