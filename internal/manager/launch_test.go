package manager

import (
	"strings"
	"testing"
)

func TestBuildCodexLaunchCommandUsesOpenAIBaseURLOutput(t *testing.T) {
	cmd := buildCodexLaunchCommand(CodexLaunchOptions{
		Profile:   "ainn-proxy",
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
