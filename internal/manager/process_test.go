package manager

import (
	"bytes"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	processTestTimeout        = 5 * time.Second
	delayedChildExitTestDelay = time.Second
	sharedStopGracePeriod     = 3 * time.Second
	sharedStopTestTimeout     = 3500 * time.Millisecond
	workerHTTPStopBudget      = 10 * time.Second
	workerMetricsDrainBudget  = 2 * time.Second
	workerDrainTestDelay      = workerHTTPStopBudget + workerMetricsDrainBudget + 100*time.Millisecond
	managerStopProcessTimeout = 18 * time.Second
)

func TestExecStarterPassesRuntimeConfigOnFD3NotArgv(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "runtime.json")
	argvPath := filepath.Join(dir, "argv.txt")
	scriptPath := filepath.Join(dir, "worker-shim.sh")
	script := "#!/bin/sh\ncat <&3 > " + shellQuote(outPath) + "\nprintf '%s\\n' \"$*\" > " + shellQuote(argvPath) + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	starter := ExecStarter{Executable: scriptPath}
	process, err := starter.Start(WorkerSpawn{
		Args:        []string{"worker", "--port", "6767", "--config-fd", "3"},
		RuntimeJSON: []byte(`{"api_key":"sk-secret"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, processTestTimeout, func() bool {
		data, err := os.ReadFile(outPath)
		return err == nil && string(data) == `{"api_key":"sk-secret"}`
	})
	waitForFile(t, argvPath)
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}

	runtimeBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(runtimeBytes) != `{"api_key":"sk-secret"}` {
		t.Fatalf("runtime payload was not passed through fd3: %s", runtimeBytes)
	}
	argvBytes, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argvBytes), "sk-secret") {
		t.Fatalf("secret leaked into argv: %s", argvBytes)
	}
}

func TestExecStarterDoesNotInheritSecretEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	t.Setenv("CODING_TOOLS_API_KEY", "sk-other-secret")
	t.Setenv("SAFE_BASE_URL", "https://example.test")
	dir := t.TempDir()
	envPath := filepath.Join(dir, "env.txt")
	scriptPath := filepath.Join(dir, "worker-shim.sh")
	script := "#!/bin/sh\nenv > " + shellQuote(envPath) + "\ncat <&3 >/dev/null\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	starter := ExecStarter{Executable: scriptPath}
	process, err := starter.Start(WorkerSpawn{
		Args:        []string{"worker", "--port", "6767", "--config-fd", "3"},
		RuntimeJSON: []byte(`{"api_key":"sk-secret"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, processTestTimeout, func() bool {
		data, err := os.ReadFile(envPath)
		return err == nil && strings.Contains(string(data), "SAFE_BASE_URL=https://example.test")
	})
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}

	envBytes, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	envText := string(envBytes)
	if strings.Contains(envText, "OPENAI_API_KEY=") || strings.Contains(envText, "CODING_TOOLS_API_KEY=") {
		t.Fatalf("secret-bearing env var inherited by worker:\n%s", envText)
	}
	if !strings.Contains(envText, "SAFE_BASE_URL=https://example.test") {
		t.Fatalf("expected non-secret environment to remain available:\n%s", envText)
	}
}

func TestExecStarterPassesMetricsPipeOnFD4(t *testing.T) {
	t.Setenv("AINN_PROCESS_TEST_HELPER", "1")
	gotMetrics := make(chan string, 1)
	spawn := WorkerSpawn{
		Args:        helperProcessArgs("metrics-fd", "", ""),
		RuntimeJSON: []byte(`{"ok":true}`),
		MetricsHandler: func(r io.Reader) {
			data, _ := io.ReadAll(r)
			gotMetrics <- string(bytes.TrimSpace(data))
		},
	}

	process, err := ExecStarter{Executable: os.Args[0]}.Start(spawn)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-gotMetrics:
		if got != `{"ok":true}` {
			t.Fatalf("unexpected metrics payload: %q", got)
		}
	case <-time.After(processTestTimeout):
		t.Fatal("timed out waiting for metrics payload")
	}
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestExecProcessStopSendsSIGTERM(t *testing.T) {
	dir := t.TempDir()
	signalPath := filepath.Join(dir, "signal.txt")
	readyPath := filepath.Join(dir, "ready")
	t.Setenv("AINN_PROCESS_TEST_HELPER", "1")

	process, err := ExecStarter{Executable: os.Args[0], StopGracePeriod: 2 * time.Second}.Start(WorkerSpawn{
		Args:        helperProcessArgs("term-exits", signalPath, readyPath),
		RuntimeJSON: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	execProcess := process.(*ExecProcess)
	osProcess := execProcess.cmd.Process
	waitForFile(t, readyPath)

	done := make(chan error, 1)
	go func() {
		done <- process.Stop()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		_ = osProcess.Kill()
		<-done
		t.Fatal("Stop did not terminate the worker with SIGTERM")
	}
	if execProcess.ForcedStop() {
		t.Fatal("expected SIGTERM to stop the worker without SIGKILL fallback")
	}

	signalBytes, err := os.ReadFile(signalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(signalBytes) != "TERM" {
		t.Fatalf("expected SIGTERM trap to run, got %q", signalBytes)
	}
}

func TestExecProcessDefaultGraceAllowsWorkerMetricsDrain(t *testing.T) {
	dir := t.TempDir()
	signalPath := filepath.Join(dir, "signal.txt")
	readyPath := filepath.Join(dir, "ready")
	t.Setenv("AINN_PROCESS_TEST_HELPER", "1")
	gotMetrics := make(chan string, 1)

	process, err := ExecStarter{Executable: os.Args[0]}.Start(WorkerSpawn{
		Args:        helperProcessArgs("term-drains-metrics", signalPath, readyPath),
		RuntimeJSON: []byte(`{}`),
		MetricsHandler: func(r io.Reader) {
			data, _ := io.ReadAll(r)
			gotMetrics <- string(bytes.TrimSpace(data))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForFile(t, readyPath)

	done := make(chan error, 1)
	go func() {
		done <- process.Stop()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(managerStopProcessTimeout):
		t.Fatal("manager stop did not allow the worker drain to finish")
	}
	if process.(*ExecProcess).ForcedStop() {
		t.Fatal("manager default grace force-killed the worker during its metrics drain window")
	}
	select {
	case got := <-gotMetrics:
		if got != `{"drained":true}` {
			t.Fatalf("unexpected drained metrics payload: %q", got)
		}
	case <-time.After(processTestTimeout):
		t.Fatal("timed out waiting for drained metrics payload")
	}
}

func TestExecProcessStopWaitsForMetricsHandler(t *testing.T) {
	t.Setenv("AINN_PROCESS_TEST_HELPER", "1")
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})

	process, err := ExecStarter{Executable: os.Args[0]}.Start(WorkerSpawn{
		Args:        helperProcessArgs("metrics-fd", "", ""),
		RuntimeJSON: []byte(`{}`),
		MetricsHandler: func(r io.Reader) {
			_, _ = io.ReadAll(r)
			close(handlerStarted)
			<-releaseHandler
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-handlerStarted:
	case <-time.After(processTestTimeout):
		t.Fatal("metrics handler did not start")
	}

	done := make(chan error, 1)
	go func() {
		done <- process.Stop()
	}()
	select {
	case err := <-done:
		close(releaseHandler)
		t.Fatalf("Stop returned before metrics handler drained: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseHandler)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(processTestTimeout):
		t.Fatal("Stop did not return after metrics handler drained")
	}
}

func TestExecProcessStopUsesRemainingGraceForMetricsHandler(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	t.Setenv("AINN_PROCESS_TEST_HELPER", "1")
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})

	process, err := ExecStarter{Executable: os.Args[0], StopGracePeriod: sharedStopGracePeriod}.Start(WorkerSpawn{
		Args:        helperProcessArgs("term-delayed-exit-metrics", "", readyPath),
		RuntimeJSON: []byte(`{}`),
		MetricsHandler: func(r io.Reader) {
			_, _ = io.ReadAll(r)
			close(handlerStarted)
			<-releaseHandler
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForFile(t, readyPath)

	done := make(chan error, 1)
	startedAt := time.Now()
	go func() {
		done <- process.Stop()
	}()
	select {
	case <-handlerStarted:
	case <-time.After(processTestTimeout):
		close(releaseHandler)
		<-done
		t.Fatal("metrics handler did not start")
	}

	select {
	case err := <-done:
		close(releaseHandler)
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Until(startedAt.Add(sharedStopTestTimeout))):
		close(releaseHandler)
		<-done
		t.Fatalf("Stop exceeded its shared grace budget: %v", time.Since(startedAt))
	}
	if process.(*ExecProcess).ForcedStop() {
		t.Fatal("metrics handler timeout marked a normally exited worker as forced")
	}
}

func TestExecProcessStopKillsAfterGracePeriod(t *testing.T) {
	dir := t.TempDir()
	signalPath := filepath.Join(dir, "signal.txt")
	readyPath := filepath.Join(dir, "ready")
	t.Setenv("AINN_PROCESS_TEST_HELPER", "1")

	process, err := ExecStarter{Executable: os.Args[0], StopGracePeriod: 50 * time.Millisecond}.Start(WorkerSpawn{
		Args:        helperProcessArgs("term-ignores", signalPath, readyPath),
		RuntimeJSON: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForFile(t, readyPath)

	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	execProcess := process.(*ExecProcess)
	if !execProcess.ForcedStop() {
		t.Fatal("expected Stop to force-kill the worker after the grace period")
	}
	if _, err := os.ReadFile(signalPath); err != nil {
		t.Fatal("expected SIGTERM before forced kill")
	}
	wantExit := ProcessExit{ExitCode: -1, Signal: "killed", Error: "signal: killed", Forced: true}
	if got := execProcess.Exit(); !reflect.DeepEqual(got, wantExit) {
		t.Fatalf("process exit = %#v, want %#v", got, wantExit)
	}
}

func TestExecProcessExitReportsNonzeroCode(t *testing.T) {
	t.Setenv("AINN_PROCESS_TEST_HELPER", "1")
	readyPath := filepath.Join(t.TempDir(), "ready")
	process, err := ExecStarter{Executable: os.Args[0]}.Start(WorkerSpawn{
		Args:        helperProcessArgs("exit-code", "", readyPath),
		RuntimeJSON: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForFile(t, readyPath)
	if err := process.Stop(); err == nil {
		t.Fatal("expected non-zero worker exit error")
	}
	wantExit := ProcessExit{ExitCode: 17, Error: "exit status 17"}
	if got := process.(*ExecProcess).Exit(); !reflect.DeepEqual(got, wantExit) {
		t.Fatalf("process exit = %#v, want %#v", got, wantExit)
	}
}

func TestExecProcessHelper(t *testing.T) {
	if os.Getenv("AINN_PROCESS_TEST_HELPER") != "1" {
		return
	}
	args := helperArgsAfterSeparator()
	if len(args) == 5 && args[0] == "metrics-fd" {
		if args[3] != "--metrics-fd" || args[4] != "4" {
			os.Exit(2)
		}
		if file := os.NewFile(uintptr(3), "config-fd"); file != nil {
			_, _ = io.Copy(io.Discard, file)
			_ = file.Close()
		}
		if file := os.NewFile(uintptr(4), "metrics-fd"); file != nil {
			_, _ = file.Write([]byte(`{"ok":true}` + "\n"))
			_ = file.Close()
			os.Exit(0)
		}
		os.Exit(2)
	}
	if len(args) == 3 && args[0] == "exit-code" {
		if file := os.NewFile(uintptr(3), "config-fd"); file != nil {
			_, _ = io.Copy(io.Discard, file)
			_ = file.Close()
		}
		_ = os.WriteFile(args[2], []byte("ready"), 0600)
		os.Exit(17)
	}
	if len(args) == 5 && args[0] == "term-drains-metrics" {
		if args[3] != "--metrics-fd" || args[4] != "4" {
			os.Exit(2)
		}
		if file := os.NewFile(uintptr(3), "config-fd"); file != nil {
			_, _ = io.Copy(io.Discard, file)
			_ = file.Close()
		}
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
		defer signal.Stop(signals)
		if err := os.WriteFile(args[2], []byte("ready"), 0600); err != nil {
			os.Exit(2)
		}
		if sig := <-signals; sig == syscall.SIGTERM {
			_ = os.WriteFile(args[1], []byte("TERM"), 0600)
		}
		time.Sleep(workerDrainTestDelay)
		if file := os.NewFile(uintptr(4), "metrics-fd"); file != nil {
			_, _ = file.Write([]byte(`{"drained":true}` + "\n"))
			_ = file.Close()
			os.Exit(0)
		}
		os.Exit(2)
	}
	if len(args) == 5 && args[0] == "term-delayed-exit-metrics" {
		if args[3] != "--metrics-fd" || args[4] != "4" {
			os.Exit(2)
		}
		if file := os.NewFile(uintptr(3), "config-fd"); file != nil {
			_, _ = io.Copy(io.Discard, file)
			_ = file.Close()
		}
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
		defer signal.Stop(signals)
		if err := os.WriteFile(args[2], []byte("ready"), 0600); err != nil {
			os.Exit(2)
		}
		if sig := <-signals; sig == syscall.SIGTERM {
			time.Sleep(delayedChildExitTestDelay)
			os.Exit(0)
		}
		os.Exit(2)
	}
	if len(args) != 3 {
		os.Exit(2)
	}
	mode, signalPath, readyPath := args[0], args[1], args[2]
	if file := os.NewFile(uintptr(3), "config-fd"); file != nil {
		_, _ = io.Copy(io.Discard, file)
		_ = file.Close()
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := os.WriteFile(readyPath, []byte("ready"), 0600); err != nil {
		os.Exit(2)
	}

	sig := <-signals
	if sig == syscall.SIGTERM {
		_ = os.WriteFile(signalPath, []byte("TERM"), 0600)
	}
	if mode == "term-exits" {
		os.Exit(0)
	}
	select {}
}

func helperProcessArgs(mode string, signalPath string, readyPath string) []string {
	return []string{"-test.run=TestExecProcessHelper", "--", mode, signalPath, readyPath}
}

func helperArgsAfterSeparator() []string {
	for i, arg := range os.Args {
		if arg == "--" {
			return os.Args[i+1:]
		}
	}
	return nil
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	eventually(t, processTestTimeout, func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
