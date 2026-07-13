package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
)

func TestStartManagedTmuxServer(t *testing.T) {
	if os.Getenv("AINN_TMUX_START_HELPER") == "1" {
		responseWriter := os.NewFile(tmuxServerResponseFD, "tmux-test-response")
		if responseWriter == nil {
			os.Exit(2)
		}
		args := flag.Args()
		data, err := json.Marshal(args)
		if err != nil {
			os.Exit(3)
		}
		response := tmuxServerStartResponse{
			Stdout:        string(data),
			SupervisorPID: os.Getpid(),
			ServerPID:     4242,
		}
		if err := json.NewEncoder(responseWriter).Encode(response); err != nil {
			os.Exit(4)
		}
		_ = responseWriter.Close()
		os.Exit(0)
	}

	request := tmuxServerStartRequest{
		ConfigDir:      "/tmp/ainn-config",
		LogDir:         "/tmp/ainn-logs",
		SocketName:     "ainn-test",
		HostSession:    "ainn-test-host",
		InitialCommand: []string{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host"},
	}
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapperPath := filepath.Join(t.TempDir(), "tmux-server-helper")
	wrapper := "#!/bin/sh\nexec " + strconv.Quote(testExecutable) + " -test.run=TestStartManagedTmuxServer -- \"$@\"\n"
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	previousExecutable := managedTmuxServerExecutable
	managedTmuxServerExecutable = func() (string, error) { return wrapperPath, nil }
	defer func() { managedTmuxServerExecutable = previousExecutable }()
	t.Setenv("AINN_TMUX_START_HELPER", "1")

	response, err := startManagedTmuxServer(request)
	if err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	if err := json.Unmarshal([]byte(response.Stdout), &gotArgs); err != nil {
		t.Fatal(err)
	}
	got := struct {
		Args      []string
		ServerPID int
	}{Args: gotArgs, ServerPID: response.ServerPID}
	want := struct {
		Args      []string
		ServerPID int
	}{
		Args: []string{
			"tmux-server",
			"--config-dir", request.ConfigDir,
			"--log-dir", request.LogDir,
			"--socket", request.SocketName,
			"--host-session", request.HostSession,
			"--",
			"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host",
		},
		ServerPID: 4242,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("managed tmux startup mismatch:\n got %#v\nwant %#v", got, want)
	}
}

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

func TestIsTmuxServerMissingError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "server missing", err: errors.New("no server running on /tmp/tmux-501/ainn-test"), want: true},
		{name: "socket missing", err: errors.New("error connecting to /tmp/tmux-501/ainn-test (No such file or directory)"), want: true},
		{name: "session missing on existing server", err: errors.New("can't find session: ainn-test-host")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTmuxServerMissingError(tt.err); got != tt.want {
				t.Fatalf("isTmuxServerMissingError(%q) = %v, want %v", tt.err, got, tt.want)
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
