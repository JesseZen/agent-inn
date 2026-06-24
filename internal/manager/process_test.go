package manager

import (
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const processTestTimeout = 5 * time.Second

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
}

func TestExecProcessHelper(t *testing.T) {
	if os.Getenv("AINN_PROCESS_TEST_HELPER") != "1" {
		return
	}
	args := helperArgsAfterSeparator()
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
