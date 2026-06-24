package manager

import (
	"strings"
	"testing"
)

func TestTmuxDetectCommand(t *testing.T) {
	got := TmuxDetectCommand()
	want := []string{"tmux", "-V"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxHasSessionCommand(t *testing.T) {
	got := TmuxHasSessionCommand()
	want := []string{"tmux", "-L", "ainn", "has-session", "-t", "ainn-host"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxStartHostCommand(t *testing.T) {
	got := TmuxStartHostCommand()
	want := []string{"tmux", "-L", "ainn", "new-session", "-d", "-s", "ainn-host"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxCreateWindowCommand(t *testing.T) {
	got := TmuxCreateWindowCommand("ainn:cli-openai", []string{"codex", "--profile", "cli-openai", "--cd", "/tmp/work"})
	want := []string{"tmux", "-L", "ainn", "new-window", "-t", "ainn-host", "-n", "ainn:cli-openai", "-P", "-F", "#{window_id}", "codex", "--profile", "cli-openai", "--cd", "/tmp/work"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxSelectWindowCommand(t *testing.T) {
	got := TmuxSelectWindowCommand("ainn:cli-openai")
	want := []string{"tmux", "-L", "ainn", "select-window", "-t", "ainn-host:ainn:cli-openai"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxAttachCommand(t *testing.T) {
	got := TmuxAttachCommand()
	want := []string{"tmux", "-L", "ainn", "attach-session", "-t", "ainn-host"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestSafeWindowName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"cli-openai", "ainn:cli-openai"},
		{"cli_openai", "ainn:cli_openai"},
		{"cli openai", "ainn:cli-openai"},
		{"cli/openai", "ainn:cli-openai"},
		{"cli.openai", "ainn:cli-openai"},
		{"", "ainn:"},
	}
	for _, tc := range cases {
		got := SafeWindowName(tc.input)
		if got != tc.want {
			t.Errorf("SafeWindowName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
