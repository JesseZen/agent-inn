package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type tmuxServerExitView struct {
	PID       int
	ExitCode  int
	Reason    tmuxServerExitReason
	Signal    string
	Initiator tmuxServerInitiator
}

func TestTmuxServerOutputTailRedactsWithinLimit(t *testing.T) {
	var tail tmuxServerOutputTail
	input := strings.Repeat("x", tmuxServerOutputTailBytes) + " Authorization: Bearer s"
	if _, err := tail.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	output := tail.RedactedString()
	got := struct {
		WithinLimit bool
		Leaked      bool
		Redacted    bool
	}{
		WithinLimit: len(output) <= tmuxServerOutputTailBytes,
		Leaked:      strings.Contains(output, "Bearer s"),
		Redacted:    strings.Contains(output, "Bearer ***REDACTED***"),
	}
	want := struct {
		WithinLimit bool
		Leaked      bool
		Redacted    bool
	}{WithinLimit: true, Redacted: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tmux output tail mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestParseTmuxServerStartRequest(t *testing.T) {
	got, err := parseTmuxServerStartRequest([]string{
		"--config-dir", "/tmp/ainn-config",
		"--log-dir", "/tmp/ainn-logs",
		"--socket", "ainn-test",
		"--host-session", "ainn-test-host",
		"--",
		"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "sleep 60",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	want := tmuxServerStartRequest{
		ConfigDir:   "/tmp/ainn-config",
		LogDir:      "/tmp/ainn-logs",
		SocketName:  "ainn-test",
		HostSession: "ainn-test-host",
		InitialCommand: []string{
			"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "sleep 60",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tmux server request mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func readTmuxServerExitView(t *testing.T, logPath string) tmuxServerExitView {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`tmux\.server\.exit pid=(\d+) exit_code=(-?\d+) reason=([^ ]+) signal=(?:"([^"]*)"|([^ ]+)) initiator=([^ ]+)`)
	match := pattern.FindStringSubmatch(string(data))
	if match == nil {
		t.Fatalf("tmux.server.exit missing from %s:\n%s", logPath, data)
	}
	pid, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	exitCode, err := strconv.Atoi(match[2])
	if err != nil {
		t.Fatal(err)
	}
	return tmuxServerExitView{
		PID:       pid,
		ExitCode:  exitCode,
		Reason:    tmuxServerExitReason(match[3]),
		Signal:    match[4] + match[5],
		Initiator: tmuxServerInitiator(match[6]),
	}
}

func TestSuperviseTmuxServerRecordsSIGKILL(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ainn-tmux-kill-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	logDir := filepath.Join(t.TempDir(), "logs")
	socketName := "ainn-kill-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hostSession := "ainn-kill-host"
	request := tmuxServerStartRequest{
		ConfigDir:   t.TempDir(),
		LogDir:      logDir,
		SocketName:  socketName,
		HostSession: hostSession,
		InitialCommand: []string{
			"tmux", "-L", socketName, "new-session", "-d", "-s", hostSession, "sleep 60",
		},
	}
	responseReader, responseWriter := io.Pipe()
	supervisorResult := make(chan error, 1)
	go func() {
		supervisorResult <- superviseTmuxServer(request, responseWriter)
		_ = responseWriter.Close()
	}()

	var response tmuxServerStartResponse
	if err := json.NewDecoder(responseReader).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "" {
		t.Fatalf("tmux server startup failed: %s", response.Error)
	}
	if err := syscall.Kill(response.ServerPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err := <-supervisorResult; err == nil {
		t.Fatal("expected SIGKILL wait error")
	}

	got := readTmuxServerExitView(t, filepath.Join(logDir, "tmux-"+socketName+".log"))
	want := tmuxServerExitView{
		PID:       response.ServerPID,
		ExitCode:  -1,
		Reason:    tmuxServerExitReasonSignal,
		Signal:    "killed",
		Initiator: tmuxServerInitiatorExternal,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tmux server exit mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestSuperviseTmuxServerRecordsCleanExit(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ainn-tmux-clean-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	logDir := filepath.Join(t.TempDir(), "logs")
	socketName := "ainn-clean-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hostSession := "ainn-clean-host"
	request := tmuxServerStartRequest{
		ConfigDir:   t.TempDir(),
		LogDir:      logDir,
		SocketName:  socketName,
		HostSession: hostSession,
		InitialCommand: []string{
			"tmux", "-L", socketName, "new-session", "-d", "-s", hostSession, "sleep 60",
		},
	}
	responseReader, responseWriter := io.Pipe()
	supervisorResult := make(chan error, 1)
	go func() {
		supervisorResult <- superviseTmuxServer(request, responseWriter)
		_ = responseWriter.Close()
	}()

	var response tmuxServerStartResponse
	if err := json.NewDecoder(responseReader).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "" {
		t.Fatalf("tmux server startup failed: %s", response.Error)
	}
	exitEmpty, err := exec.Command("tmux", "-L", socketName, "show-option", "-gv", "exit-empty").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(exitEmpty) != "on\n" {
		t.Fatalf("exit-empty = %q, want on", exitEmpty)
	}
	if output, err := exec.Command("tmux", "-L", socketName, "kill-session", "-t", hostSession).CombinedOutput(); err != nil {
		t.Fatalf("kill final tmux session: %v: %s", err, output)
	}
	if err := <-supervisorResult; err != nil {
		t.Fatalf("clean tmux exit returned error: %v", err)
	}

	got := readTmuxServerExitView(t, filepath.Join(logDir, "tmux-"+socketName+".log"))
	want := tmuxServerExitView{
		PID:       response.ServerPID,
		Reason:    tmuxServerExitReasonClean,
		Initiator: tmuxServerInitiatorExternal,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tmux server exit mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestSuperviseTmuxServerRecordsCrashSignal(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ainn-tmux-crash-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	logDir := filepath.Join(t.TempDir(), "logs")
	socketName := "ainn-crash-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hostSession := "ainn-crash-host"
	request := tmuxServerStartRequest{
		ConfigDir:   t.TempDir(),
		LogDir:      logDir,
		SocketName:  socketName,
		HostSession: hostSession,
		InitialCommand: []string{
			"tmux", "-L", socketName, "new-session", "-d", "-s", hostSession, "sleep 60",
		},
	}
	responseReader, responseWriter := io.Pipe()
	supervisorResult := make(chan error, 1)
	go func() {
		supervisorResult <- superviseTmuxServer(request, responseWriter)
		_ = responseWriter.Close()
	}()

	var response tmuxServerStartResponse
	if err := json.NewDecoder(responseReader).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "" {
		t.Fatalf("tmux server startup failed: %s", response.Error)
	}
	if err := syscall.Kill(response.ServerPID, syscall.SIGSEGV); err != nil {
		t.Fatal(err)
	}
	if err := <-supervisorResult; err == nil {
		t.Fatal("expected crash-signal wait error")
	}

	got := readTmuxServerExitView(t, filepath.Join(logDir, "tmux-"+socketName+".log"))
	want := tmuxServerExitView{
		PID:       response.ServerPID,
		ExitCode:  -1,
		Reason:    tmuxServerExitReasonSignal,
		Signal:    "segmentation fault",
		Initiator: tmuxServerInitiatorExternal,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tmux server exit mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestSuperviseTmuxServerRecordsAINNForwardedSignal(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ainn-tmux-forward-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	logDir := filepath.Join(t.TempDir(), "logs")
	socketName := "ainn-forward-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hostSession := "ainn-forward-host"
	request := tmuxServerStartRequest{
		ConfigDir:   t.TempDir(),
		LogDir:      logDir,
		SocketName:  socketName,
		HostSession: hostSession,
		InitialCommand: []string{
			"tmux", "-L", socketName, "new-session", "-d", "-s", hostSession, "sleep 60",
		},
	}
	responseReader, responseWriter := io.Pipe()
	signals := make(chan os.Signal, 1)
	supervisorResult := make(chan error, 1)
	go func() {
		supervisorResult <- superviseTmuxServerWithSignals(request, responseWriter, signals)
		_ = responseWriter.Close()
	}()

	var response tmuxServerStartResponse
	if err := json.NewDecoder(responseReader).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "" {
		t.Fatalf("tmux server startup failed: %s", response.Error)
	}
	signals <- syscall.SIGTERM
	if err := <-supervisorResult; err != nil {
		t.Fatalf("forwarded SIGTERM returned error: %v", err)
	}

	logPath := filepath.Join(logDir, "tmux-"+socketName+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	signalPattern := regexp.MustCompile(`tmux\.server\.signal pid=(\d+) signal=terminated initiator=ainn`)
	match := signalPattern.FindStringSubmatch(string(data))
	if match == nil {
		t.Fatalf("tmux.server.signal missing from %s:\n%s", logPath, data)
	}
	signalPID, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	got := struct {
		SignalPID int
		Exit      tmuxServerExitView
	}{
		SignalPID: signalPID,
		Exit:      readTmuxServerExitView(t, logPath),
	}
	want := struct {
		SignalPID int
		Exit      tmuxServerExitView
	}{
		SignalPID: response.ServerPID,
		Exit: tmuxServerExitView{
			PID:       response.ServerPID,
			Reason:    tmuxServerExitReasonClean,
			Initiator: tmuxServerInitiatorAINN,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwarded tmux signal mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestSuperviseTmuxServerRecordsInitialSessionFailure(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ainn-tmux-setup-fail-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	logDir := filepath.Join(t.TempDir(), "logs")
	socketName := "ainn-setup-fail-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hostSession := "ainn-setup-fail-host"
	request := tmuxServerStartRequest{
		ConfigDir:      t.TempDir(),
		LogDir:         logDir,
		SocketName:     socketName,
		HostSession:    hostSession,
		InitialCommand: []string{"tmux", "-L", socketName, "new-session", "--invalid-option"},
	}
	responseReader, responseWriter := io.Pipe()
	supervisorResult := make(chan error, 1)
	go func() {
		supervisorResult <- superviseTmuxServer(request, responseWriter)
		_ = responseWriter.Close()
	}()

	var response tmuxServerStartResponse
	if err := json.NewDecoder(responseReader).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error == "" {
		t.Fatal("expected initial session setup error")
	}
	if err := <-supervisorResult; err == nil {
		t.Fatal("expected supervisor setup error")
	}

	logPath := filepath.Join(logDir, "tmux-"+socketName+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := struct {
		SignalLogged bool
		Exit         tmuxServerExitView
	}{
		SignalLogged: regexp.MustCompile(`tmux\.server\.signal pid=\d+ signal=terminated initiator=ainn`).Match(data),
		Exit:         readTmuxServerExitView(t, logPath),
	}
	want := struct {
		SignalLogged bool
		Exit         tmuxServerExitView
	}{
		SignalLogged: true,
		Exit: tmuxServerExitView{
			PID:       response.ServerPID,
			Reason:    tmuxServerExitReasonClean,
			Initiator: tmuxServerInitiatorAINN,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("initial-session failure lifecycle mismatch:\n got %#v\nwant %#v\nlog %s", got, want, data)
	}
}
