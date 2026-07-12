package manager

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildCodexLaunchCommandUsesOpenAIBaseURLOutput(t *testing.T) {
	cmd := buildCodexLaunchCommand(CodexLaunchOptions{
		Profile:    "ainn-proxy",
		Workspace:  "/tmp/work",
		AddDirs:    []string{"/tmp/shared"},
		WorkerPort: 11199,
		Model:      "gpt-5.5",
	})
	got := strings.Join(cmd, " ")
	if !strings.Contains(got, "--profile ainn-proxy") {
		t.Fatalf("missing profile flag: %s", got)
	}
	if !strings.Contains(got, "--cd /tmp/work") {
		t.Fatalf("missing workspace flag: %s", got)
	}
	if !strings.Contains(got, "--add-dir /tmp/shared") {
		t.Fatalf("missing add-dir flag: %s", got)
	}
	if !strings.Contains(got, "--model gpt-5.5") {
		t.Fatalf("missing model flag: %s", got)
	}
}

func TestRenderCodexLaunchCommandQuotesArguments(t *testing.T) {
	got := renderCodexLaunchCommand([]string{"codex", "--cd", "/tmp/my work"})
	if !strings.Contains(got, `"\/tmp/my work"`) && !strings.Contains(got, `"/tmp/my work"`) {
		t.Fatalf("expected quoted path, got %s", got)
	}
}

func TestBuildClaudeCodeLaunchCommandInjectsManagedAnthropicEnv(t *testing.T) {
	cmd := BuildLaunchCommand(LaunchOptions{
		Launcher:   "claudecode",
		AddDirs:    []string{"/tmp/shared"},
		WorkerPort: 11199,
		Model:      "claude-opus-4-8",
	})
	want := []string{
		"env",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:11199",
		"ANTHROPIC_AUTH_TOKEN=ainn",
		"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1",
		"claude",
		"--add-dir",
		"/tmp/shared",
		"--model",
		"claude-opus-4-8",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected claude launch command:\ngot  %#v\nwant %#v", cmd, want)
	}
}

func TestBuildClaudeCodeExternalLaunchKeepsAgentView(t *testing.T) {
	cmd := BuildLaunchCommand(LaunchOptions{Launcher: "claudecode", WorkerPort: 11199})
	want := []string{
		"env",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:11199",
		"ANTHROPIC_AUTH_TOKEN=ainn",
		"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1",
		"claude",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected external claude launch command:\ngot  %#v\nwant %#v", cmd, want)
	}
}

func TestBuildLaunchCommandResumesCodexSession(t *testing.T) {
	cmd := BuildLaunchCommand(LaunchOptions{
		Launcher:            "codex",
		Profile:             "cli-openai",
		Workspace:           "/tmp/work",
		AddDirs:             []string{"/tmp/shared"},
		WorkerPort:          11199,
		Model:               "gpt-5.5",
		LauncherSessionID:   "019e7c18-0ee7-7ff2-bc82-9c410511ede3",
		LauncherSessionMode: LauncherSessionModeResume,
	})
	want := []string{
		"codex",
		"resume",
		"--profile",
		"cli-openai",
		"--cd",
		"/tmp/work",
		"--add-dir",
		"/tmp/shared",
		"--model",
		"gpt-5.5",
		"019e7c18-0ee7-7ff2-bc82-9c410511ede3",
	}
	if strings.Join(cmd, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected codex resume command:\ngot  %#v\nwant %#v", cmd, want)
	}
}

func TestBuildLaunchCommandResumesClaudeCodeSession(t *testing.T) {
	cmd := BuildLaunchCommand(LaunchOptions{
		Launcher:            "claudecode",
		AddDirs:             []string{"/tmp/shared"},
		WorkerPort:          11199,
		Model:               "claude-opus-4-8",
		LauncherSessionID:   "9e98a56c-7224-4bf2-9263-b4e470e9673d",
		LauncherSessionMode: LauncherSessionModeResume,
	})
	want := []string{
		"env",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:11199",
		"ANTHROPIC_AUTH_TOKEN=ainn",
		"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1",
		"claude",
		"--resume",
		"9e98a56c-7224-4bf2-9263-b4e470e9673d",
		"--add-dir",
		"/tmp/shared",
		"--model",
		"claude-opus-4-8",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected claude resume command:\ngot  %#v\nwant %#v", cmd, want)
	}
}
