package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

func TestTmuxServerOutputTailRedactsCredentialAcrossLimitBoundary(t *testing.T) {
	var tail tmuxServerOutputTail
	credential := "Authorization: Bearer sk-boundary-secret"
	boundaryOffset := strings.Index(credential, "Bearer") + len("Be")
	suffixLength := tmuxServerOutputTailBytes - (len(credential) - boundaryOffset)
	input := strings.Repeat("x", tmuxServerOutputRedactionOverlapBytes+100) + credential + strings.Repeat("y", suffixLength)
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
		Leaked:      strings.Contains(output, "sk-boundary-secret"),
		Redacted:    strings.Contains(output, "Bearer ***REDACTED***"),
	}
	want := struct {
		WithinLimit bool
		Leaked      bool
		Redacted    bool
	}{WithinLimit: true, Redacted: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("boundary redaction mismatch:\n got %#v\nwant %#v", got, want)
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

func TestConcurrentTmuxServerStartupLockSerializesFreshServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ainn-tmux-concurrent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	logDir := filepath.Join(t.TempDir(), "logs")
	socketName := "ainn-concurrent-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hostSession := "ainn-concurrent-host"
	request := tmuxServerStartRequest{
		ConfigDir:   t.TempDir(),
		LogDir:      logDir,
		SocketName:  socketName,
		HostSession: hostSession,
		InitialCommand: []string{
			"tmux", "-L", socketName, "new-session", "-d", "-s", hostSession, "sleep 60",
		},
	}
	type startupResult struct {
		Started          bool
		Response         tmuxServerStartResponse
		SupervisorResult <-chan error
		Error            string
	}
	results := make(chan startupResult, 2)
	var startCountMu sync.Mutex
	startCount := 0
	for range 2 {
		go func() {
			release, lockErr := acquireTmuxServerStartupLock(socketName)
			if lockErr != nil {
				results <- startupResult{Error: lockErr.Error()}
				return
			}
			defer release()
			if err := exec.Command("tmux", "-L", socketName, "has-session", "-t", hostSession).Run(); err == nil {
				results <- startupResult{}
				return
			}
			startCountMu.Lock()
			startCount++
			startCountMu.Unlock()
			responseReader, responseWriter := io.Pipe()
			supervisorResult := make(chan error, 1)
			go func() {
				supervisorResult <- superviseTmuxServer(request, responseWriter)
				_ = responseWriter.Close()
			}()
			var response tmuxServerStartResponse
			if err := json.NewDecoder(responseReader).Decode(&response); err != nil {
				results <- startupResult{Error: err.Error()}
				return
			}
			_ = responseReader.Close()
			if response.Error != "" {
				results <- startupResult{Error: response.Error}
				return
			}
			results <- startupResult{Started: true, Response: response, SupervisorResult: supervisorResult}
		}()
	}
	var started startupResult
	startedCount := 0
	var startupErrors []string
	for range 2 {
		result := <-results
		if result.Error != "" {
			startupErrors = append(startupErrors, result.Error)
		}
		if result.Started {
			started = result
			startedCount++
		}
	}
	startCountMu.Lock()
	observedStartCount := startCount
	startCountMu.Unlock()
	got := struct {
		ObservedStartCount int
		StartedCount       int
		ValidServerPID     bool
		Errors             []string
	}{ObservedStartCount: observedStartCount, StartedCount: startedCount, ValidServerPID: started.Response.ServerPID > 0, Errors: startupErrors}
	want := struct {
		ObservedStartCount int
		StartedCount       int
		ValidServerPID     bool
		Errors             []string
	}{ObservedStartCount: 1, StartedCount: 1, ValidServerPID: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("concurrent startup mismatch:\n got %#v\nwant %#v", got, want)
	}
	if output, err := exec.Command("tmux", "-L", socketName, "kill-session", "-t", hostSession).CombinedOutput(); err != nil {
		t.Fatalf("kill final tmux session: %v: %s", err, output)
	}
	if err := <-started.SupervisorResult; err != nil {
		t.Fatalf("clean tmux exit returned error: %v", err)
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

func TestSuperviseTmuxServerRecordsExternalForwardedSignal(t *testing.T) {
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
	signalPattern := regexp.MustCompile(`tmux\.server\.signal pid=(\d+) signal=terminated initiator=external_or_unknown`)
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
			Initiator: tmuxServerInitiatorExternal,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwarded tmux signal mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestSuperviseTmuxServerReapsServerWhenSignalForwardingFails(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ainn-tmux-forward-fail-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	logDir := filepath.Join(t.TempDir(), "logs")
	socketName := "ainn-forward-fail-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hostSession := "ainn-forward-fail-host"
	request := tmuxServerStartRequest{
		ConfigDir:   t.TempDir(),
		LogDir:      logDir,
		SocketName:  socketName,
		HostSession: hostSession,
		InitialCommand: []string{
			"tmux", "-L", socketName, "new-session", "-d", "-s", hostSession, "sleep 60",
		},
	}
	previousForwardSignal := tmuxServerForwardSignal
	tmuxServerForwardSignal = func(*os.Process, os.Signal) error { return errors.New("injected signal failure") }
	defer func() { tmuxServerForwardSignal = previousForwardSignal }()
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
	signals <- syscall.SIGTERM
	waitErr := <-supervisorResult
	logPath := filepath.Join(logDir, "tmux-"+socketName+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	exit := readTmuxServerExitView(t, logPath)
	processErr := syscall.Kill(response.ServerPID, 0)
	got := struct {
		ReturnedError bool
		ForwardError  bool
		ProcessGone   bool
		Exit          tmuxServerExitView
	}{
		ReturnedError: waitErr != nil,
		ForwardError:  strings.Contains(string(data), "injected signal failure"),
		ProcessGone:   errors.Is(processErr, syscall.ESRCH),
		Exit:          exit,
	}
	want := struct {
		ReturnedError bool
		ForwardError  bool
		ProcessGone   bool
		Exit          tmuxServerExitView
	}{
		ReturnedError: true,
		ForwardError:  true,
		ProcessGone:   true,
		Exit: tmuxServerExitView{
			PID:       response.ServerPID,
			ExitCode:  -1,
			Reason:    tmuxServerExitReasonSignal,
			Signal:    "killed",
			Initiator: tmuxServerInitiatorAINN,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("signal-forward cleanup mismatch:\n got %#v\nwant %#v\nlog %s", got, want, data)
	}
}

func TestSuperviseTmuxServerStopsWhenStartupClientDisconnects(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ainn-tmux-control-loss-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	logDir := filepath.Join(t.TempDir(), "logs")
	socketName := "ainn-control-loss-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hostSession := "ainn-control-loss-host"
	request := tmuxServerStartRequest{
		ConfigDir:   t.TempDir(),
		LogDir:      logDir,
		SocketName:  socketName,
		HostSession: hostSession,
		InitialCommand: []string{
			"tmux", "-L", socketName, "new-session", "-d", "-s", hostSession, "sleep 60",
		},
	}
	controlReader, controlWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var response bytes.Buffer
	supervisorResult := make(chan error, 1)
	go func() {
		supervisorResult <- superviseTmuxServerWithControl(request, &response, controlReader)
	}()
	if err := controlWriter.Close(); err != nil {
		t.Fatal(err)
	}
	waitErr := <-supervisorResult
	logPath := filepath.Join(logDir, "tmux-"+socketName+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	exit := readTmuxServerExitView(t, logPath)
	processErr := syscall.Kill(exit.PID, 0)
	got := struct {
		ReturnedError bool
		ControlError  bool
		ProcessGone   bool
		Exit          tmuxServerExitView
	}{
		ReturnedError: waitErr != nil,
		ControlError:  strings.Contains(string(data), "startup client disconnected"),
		ProcessGone:   errors.Is(processErr, syscall.ESRCH),
		Exit:          exit,
	}
	want := []struct {
		ReturnedError bool
		ControlError  bool
		ProcessGone   bool
		Exit          tmuxServerExitView
	}{
		{
			ReturnedError: true,
			ControlError:  true,
			ProcessGone:   true,
			Exit: tmuxServerExitView{
				PID:       exit.PID,
				Reason:    tmuxServerExitReasonClean,
				Initiator: tmuxServerInitiatorAINN,
			},
		},
		{
			ReturnedError: true,
			ControlError:  true,
			ProcessGone:   true,
			Exit: tmuxServerExitView{
				PID:       exit.PID,
				ExitCode:  -1,
				Reason:    tmuxServerExitReasonSignal,
				Signal:    "terminated",
				Initiator: tmuxServerInitiatorAINN,
			},
		},
	}
	matched := false
	for _, candidate := range want {
		if reflect.DeepEqual(got, candidate) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("startup control cleanup mismatch:\n got %#v\nwant %#v\nresponse %s\nlog %s", got, want, response.String(), data)
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

func TestSuperviseTmuxServerRecordsStartupResponseFailure(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ainn-tmux-response-fail-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	logDir := filepath.Join(t.TempDir(), "logs")
	socketName := "ainn-response-fail-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hostSession := "ainn-response-fail-host"
	request := tmuxServerStartRequest{
		ConfigDir:   t.TempDir(),
		LogDir:      logDir,
		SocketName:  socketName,
		HostSession: hostSession,
		InitialCommand: []string{
			"tmux", "-L", socketName, "new-session", "-d", "-s", hostSession, "sleep 60",
		},
	}
	responseReader, responseWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := responseReader.Close(); err != nil {
		t.Fatal(err)
	}
	defer responseWriter.Close()
	if err := superviseTmuxServer(request, responseWriter); err == nil {
		t.Fatal("expected startup response error")
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
	exit := readTmuxServerExitView(t, logPath)
	got := struct {
		SignalPID int
		Exit      tmuxServerExitView
	}{SignalPID: signalPID, Exit: exit}
	want := struct {
		SignalPID int
		Exit      tmuxServerExitView
	}{
		SignalPID: exit.PID,
		Exit: tmuxServerExitView{
			PID:       exit.PID,
			Reason:    tmuxServerExitReasonClean,
			Initiator: tmuxServerInitiatorAINN,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("startup response failure lifecycle mismatch:\n got %#v\nwant %#v\nlog %s", got, want, data)
	}
}

func TestSuperviseTmuxServerTimesOutInitialSessionCommand(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	tmuxTmpDir, err := os.MkdirTemp("/tmp", "ainn-tmux-command-timeout-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxTmpDir) })
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	logDir := filepath.Join(t.TempDir(), "logs")
	socketName := "ainn-command-timeout-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hostSession := "ainn-command-timeout-host"
	request := tmuxServerStartRequest{
		ConfigDir:      t.TempDir(),
		LogDir:         logDir,
		SocketName:     socketName,
		HostSession:    hostSession,
		InitialCommand: []string{"sh", "-c", "sleep 60"},
	}
	previousTimeout := tmuxServerCommandTimeout
	tmuxServerCommandTimeout = 100 * time.Millisecond
	defer func() { tmuxServerCommandTimeout = previousTimeout }()
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
		t.Fatal("expected initial session timeout")
	}
	if err := <-supervisorResult; err == nil {
		t.Fatal("expected supervisor timeout error")
	}
	logPath := filepath.Join(logDir, "tmux-"+socketName+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := struct {
		TimedOut     bool
		SignalLogged bool
		Exit         tmuxServerExitView
	}{
		TimedOut:     strings.Contains(response.Error, "context deadline exceeded"),
		SignalLogged: regexp.MustCompile(`tmux\.server\.signal pid=\d+ signal=terminated initiator=ainn`).Match(data),
		Exit:         readTmuxServerExitView(t, logPath),
	}
	want := struct {
		TimedOut     bool
		SignalLogged bool
		Exit         tmuxServerExitView
	}{
		TimedOut:     true,
		SignalLogged: true,
		Exit: tmuxServerExitView{
			PID:       response.ServerPID,
			Reason:    tmuxServerExitReasonClean,
			Initiator: tmuxServerInitiatorAINN,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("initial-session timeout lifecycle mismatch:\n got %#v\nwant %#v\nlog %s", got, want, data)
	}
}
