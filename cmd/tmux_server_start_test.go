package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
)

func TestClassifyTmuxClientExit(t *testing.T) {
	tests := []struct {
		name   string
		output string
		err    error
		want   tmuxClientExit
	}{
		{
			name:   "detached",
			output: "[detached (from session host)]\n",
			want:   tmuxClientExit{Reason: tmuxClientExitReasonDetached},
		},
		{
			name:   "empty",
			output: "[exited]\n",
			want:   tmuxClientExit{Reason: tmuxClientExitReasonEmpty},
		},
		{
			name:   "terminated",
			output: "[server exited]\n",
			err:    errors.New("exit status 1"),
			want:   tmuxClientExit{Reason: tmuxClientExitReasonServerTerminated, ExitCode: 1, Error: "exit status 1"},
		},
		{
			name:   "unexpected",
			output: "[server exited unexpectedly]\n",
			err:    errors.New("exit status 1"),
			want:   tmuxClientExit{Reason: tmuxClientExitReasonServerUnexpected, ExitCode: 1, Error: "exit status 1"},
		},
		{
			name:   "client error",
			output: "open terminal failed",
			err:    errors.New("exit status 1"),
			want:   tmuxClientExit{Reason: tmuxClientExitReasonClientError, ExitCode: 1, Error: "exit status 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyTmuxClientExit(tt.output, tt.err)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tmux client exit mismatch:\n got %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestWriteTmuxClientExitWritesRedactedLifecycleEvent(t *testing.T) {
	logDir := t.TempDir()
	settings := config.Settings{
		LogDir: logDir,
		Terminal: config.TerminalSettings{Tmux: config.TmuxSettings{
			SocketName:  "ainn-test",
			HostSession: "ainn-test-host",
		}},
	}
	err := errors.New("exit status 1: Authorization: Bearer sk-secret")
	if writeErr := writeTmuxClientExit(settings, "[server exited unexpectedly]\n", err); writeErr != nil {
		t.Fatal(writeErr)
	}

	logPath := filepath.Join(logDir, "tmux-ainn-test.log")
	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	line := strings.TrimSpace(string(data))
	eventIndex := strings.Index(line, "tmux.supervisor tmux.client.exit ")
	if eventIndex == -1 {
		t.Fatalf("tmux client exit event missing from %q", line)
	}
	got := struct {
		Path   string
		Event  string
		Leaked bool
	}{
		Path:   logPath,
		Event:  line[eventIndex:],
		Leaked: strings.Contains(line, "sk-secret"),
	}
	want := struct {
		Path   string
		Event  string
		Leaked bool
	}{
		Path:  logPath,
		Event: `tmux.supervisor tmux.client.exit socket=ainn-test host_session=ainn-test-host reason=server_unexpected exit_code=1 error="exit status 1: Authorization: Bearer ***REDACTED***"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tmux client exit log mismatch:\n got %#v\nwant %#v\nline %q", got, want, line)
	}
}
