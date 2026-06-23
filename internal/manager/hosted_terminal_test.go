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
	want := []string{"tmux", "-L", "cap", "has-session", "-t", "cap-host"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxStartHostCommand(t *testing.T) {
	got := TmuxStartHostCommand()
	want := []string{"tmux", "-L", "cap", "new-session", "-d", "-s", "cap-host"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxCreateWindowCommand(t *testing.T) {
	got := TmuxCreateWindowCommand("codex:cli-openai", []string{"codex", "--profile", "cli-openai", "--cd", "/tmp/work"})
	want := []string{"tmux", "-L", "cap", "new-window", "-t", "cap-host", "-n", "codex:cli-openai", "-P", "-F", "#{window_id}", "codex", "--profile", "cli-openai", "--cd", "/tmp/work"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxSelectWindowCommand(t *testing.T) {
	got := TmuxSelectWindowCommand("codex:cli-openai")
	want := []string{"tmux", "-L", "cap", "select-window", "-t", "cap-host:codex:cli-openai"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTmuxAttachCommand(t *testing.T) {
	got := TmuxAttachCommand()
	want := []string{"tmux", "-L", "cap", "attach-session", "-t", "cap-host"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestSafeWindowName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"cli-openai", "codex:cli-openai"},
		{"cli_openai", "codex:cli_openai"},
		{"cli openai", "codex:cli-openai"},
		{"cli/openai", "codex:cli-openai"},
		{"cli.openai", "codex:cli-openai"},
		{"", "codex:"},
	}
	for _, tc := range cases {
		got := SafeWindowName(tc.input)
		if got != tc.want {
			t.Errorf("SafeWindowName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
