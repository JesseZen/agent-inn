package manager

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildCodexLaunchCommandUsesOpenAIBaseURLOutput(t *testing.T) {
	cmd, err := buildCodexLaunchCommand(CodexLaunchOptions{
		Profile:    "ainn-proxy",
		Workspace:  "/tmp/work",
		AddDirs:    []string{"/tmp/shared"},
		WorkerPort: 11199,
		Model:      "gpt-5.5",
	})
	if err != nil {
		t.Fatal(err)
	}
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

func TestBuildCodexLaunchCommandEncodesWorkerID(t *testing.T) {
	cmd, err := BuildCodexLaunchCommand(CodexLaunchOptions{Profile: "0.02", WorkerPort: 11199})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "--profile", "ainn-x-302e3032"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("got %#v, want %#v", cmd, want)
	}
}

func TestBuildCodexLaunchCommandRejectsLongProfile(t *testing.T) {
	workerID := strings.Repeat("a", 244)
	cmd, err := BuildCodexLaunchCommand(CodexLaunchOptions{Profile: workerID, WorkerPort: 11199})
	if err == nil {
		t.Fatalf("expected long profile to fail, got %#v", cmd)
	}
	if cmd != nil {
		t.Fatalf("failed command must be nil, got %#v", cmd)
	}
	if !strings.Contains(err.Error(), "244 bytes; limit is 243 bytes") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderCodexLaunchCommandQuotesArguments(t *testing.T) {
	got := renderCodexLaunchCommand([]string{"codex", "--cd", "/tmp/my work"})
	if !strings.Contains(got, `"\/tmp/my work"`) && !strings.Contains(got, `"/tmp/my work"`) {
		t.Fatalf("expected quoted path, got %s", got)
	}
}

func TestBuildClaudeCodeLaunchCommandInjectsManagedAnthropicEnv(t *testing.T) {
	cmd, err := BuildLaunchCommand(LaunchOptions{
		Launcher:   "claudecode",
		AddDirs:    []string{"/tmp/shared"},
		WorkerPort: 11199,
		Model:      "claude-opus-4-8",
	})
	if err != nil {
		t.Fatal(err)
	}
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
	cmd, err := BuildLaunchCommand(LaunchOptions{Launcher: "claudecode", Profile: "中文", WorkerPort: 11199})
	if err != nil {
		t.Fatal(err)
	}
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

func TestBuildGrokLaunchCommandUsesWorkerProxy(t *testing.T) {
	cmd, err := BuildLaunchCommand(LaunchOptions{
		Launcher:       "grok",
		Profile:        "worker-main",
		GrokHome:       "/tmp/ainn-grok-home",
		GrokExecutable: "/Users/test/.grok/bin/grok",
		WorkerPort:     11199,
		Model:          "worker-main",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"env",
		"HOME=/tmp/ainn-grok-home",
		"XAI_API_KEY=ainn",
		"/Users/test/.grok/bin/grok",
		"--model",
		"worker-main",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected grok launch command:\ngot  %#v\nwant %#v", cmd, want)
	}
}

func TestBuildOpenCodeLaunchCommandInjectsWorkerProvider(t *testing.T) {
	cmd, err := BuildLaunchCommand(LaunchOptions{
		Launcher:   "opencode",
		Workspace:  "/tmp/work",
		WorkerPort: 11199,
		Model:      "gpt-5.5",
		APIFormat:  "responses",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"env",
		`OPENCODE_CONFIG_CONTENT={"provider":{"ainn":{"npm":"@ai-sdk/openai","name":"AINN","options":{"baseURL":"http://127.0.0.1:11199/v1","apiKey":"ainn"},"models":{"gpt-5.5":{"name":"gpt-5.5"}}}},"model":"ainn/gpt-5.5"}`,
		"opencode",
		"/tmp/work",
		"--model",
		"ainn/gpt-5.5",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected opencode launch command:\ngot  %#v\nwant %#v", cmd, want)
	}
}

func TestBuildOpenCodeLaunchCommandUsesChatCompletionsProvider(t *testing.T) {
	cmd, err := BuildLaunchCommand(LaunchOptions{
		Launcher:   "opencode",
		WorkerPort: 11199,
		Model:      "deepseek-chat",
		APIFormat:  "chat_completions",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"env",
		`OPENCODE_CONFIG_CONTENT={"provider":{"ainn":{"npm":"@ai-sdk/openai-compatible","name":"AINN","options":{"baseURL":"http://127.0.0.1:11199/v1","apiKey":"ainn"},"models":{"deepseek-chat":{"name":"deepseek-chat"}}}},"model":"ainn/deepseek-chat"}`,
		"opencode",
		"--model",
		"ainn/deepseek-chat",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected opencode launch command:\ngot  %#v\nwant %#v", cmd, want)
	}
}

func TestBuildOpenCodeLaunchCommandUsesAnthropicProvider(t *testing.T) {
	cmd, err := BuildLaunchCommand(LaunchOptions{
		Launcher:   "opencode",
		WorkerPort: 11199,
		Model:      "claude-sonnet",
		APIFormat:  "anthropic",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"env",
		`OPENCODE_CONFIG_CONTENT={"provider":{"ainn":{"npm":"@ai-sdk/anthropic","name":"AINN","options":{"baseURL":"http://127.0.0.1:11199/v1","apiKey":"ainn"},"models":{"claude-sonnet":{"name":"claude-sonnet"}}}},"model":"ainn/claude-sonnet"}`,
		"opencode",
		"--model",
		"ainn/claude-sonnet",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected opencode launch command:\ngot  %#v\nwant %#v", cmd, want)
	}
}

func TestBuildPiLaunchCommandUsesIsolatedWorkerProvider(t *testing.T) {
	cmd, err := BuildLaunchCommand(LaunchOptions{
		Launcher:   "pi",
		Profile:    "worker-main",
		Workspace:  "/tmp/work",
		PiAgentDir: "/tmp/ainn-pi-agent",
		Model:      "gpt-5.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"env",
		"PI_CODING_AGENT_DIR=/tmp/ainn-pi-agent",
		"pi",
		"--provider",
		"worker-main",
		"--model",
		"gpt-5.5",
		"--api-key",
		"ainn",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected pi launch command:\ngot  %#v\nwant %#v", cmd, want)
	}
}
func TestBuildLaunchCommandResumesCodexSession(t *testing.T) {
	cmd, err := BuildLaunchCommand(LaunchOptions{
		Launcher:            "codex",
		Profile:             "0.02",
		Workspace:           "/tmp/work",
		AddDirs:             []string{"/tmp/shared"},
		WorkerPort:          11199,
		Model:               "gpt-5.5",
		LauncherSessionID:   "019e7c18-0ee7-7ff2-bc82-9c410511ede3",
		LauncherSessionMode: LauncherSessionModeResume,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"codex",
		"resume",
		"--profile",
		"ainn-x-302e3032",
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
	cmd, err := BuildLaunchCommand(LaunchOptions{
		Launcher:            "claudecode",
		AddDirs:             []string{"/tmp/shared"},
		WorkerPort:          11199,
		Model:               "claude-opus-4-8",
		LauncherSessionID:   "9e98a56c-7224-4bf2-9263-b4e470e9673d",
		LauncherSessionMode: LauncherSessionModeResume,
	})
	if err != nil {
		t.Fatal(err)
	}
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
