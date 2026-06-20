package cmd

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/jesse/codex-app-proxy/internal/manager"
)

func TestRunLaunchRequiresWorker(t *testing.T) {
	var stderr bytes.Buffer
	code := runLaunch([]string{"--cd", "/tmp/work"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr.String(), "launch requires --worker") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunLaunchRunsBuiltCommand(t *testing.T) {
	var got []string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) error {
				got = append([]string{}, args...)
				return nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--worker", "11199", "--profile", "cli-openai", "--cd", "/tmp/work", "--add-dir", "/tmp/shared", "--model", "gpt-5.5"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}
	if len(got) == 0 || got[0] != "codex" {
		t.Fatalf("unexpected command: %#v", got)
	}
	if strings.Join(got, " ") != strings.Join([]string{"codex", "--profile", "cli-openai", "--cd", "/tmp/work", "--add-dir", "/tmp/shared", "--model", "gpt-5.5"}, " ") {
		t.Fatalf("unexpected launch args: %#v", got)
	}
}

func TestRenderCodexLaunchCommand(t *testing.T) {
	got := manager.BuildCodexLaunchCommand(manager.CodexLaunchOptions{Profile: "11199", WorkerPort: 11199})
	if len(got) != 3 {
		t.Fatalf("unexpected launch command: %#v", got)
	}
}

func TestRunLaunchRejectsBadWorker(t *testing.T) {
	var stderr bytes.Buffer
	code := runLaunch([]string{"--worker", "abc"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr.String(), "invalid worker port") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}
