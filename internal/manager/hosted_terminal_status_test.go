package manager

import "testing"

func TestTmuxListWindowsCommand(t *testing.T) {
	got := TmuxListWindowsCommand()
	want := []string{"tmux", "-L", "ainn", "list-windows", "-t", "ainn-host", "-F", "#{window_id}"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestTmuxKillWindowCommand(t *testing.T) {
	got := TmuxKillWindowCommand("ainn:cli-openai")
	want := []string{"tmux", "-L", "ainn", "kill-window", "-t", "ainn-host:ainn:cli-openai"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestHostedSessionStatusForWindow(t *testing.T) {
	if got := hostedSessionStatusForWindow(hostedWindowSet("ainn:one\nainn:two\n"), "ainn:two"); got != hostedSessionStatusActive {
		t.Fatalf("got %q, want active", got)
	}
	if got := hostedSessionStatusForWindow(hostedWindowSet("ainn:one\n"), "ainn:two"); got != hostedSessionStatusStale {
		t.Fatalf("got %q, want stale", got)
	}
}
