package cmd

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
)

func TestRunVersionPrintsVersion(t *testing.T) {
	var stdout bytes.Buffer
	code := Run([]string{"version"}, &stdout, &bytes.Buffer{})

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "ainn") {
		t.Fatalf("expected version output to name ainn, got %q", stdout.String())
	}
}

func TestRunUnknownCommandReturnsUsageError(t *testing.T) {
	var stderr bytes.Buffer
	code := Run([]string{"unknown"}, &bytes.Buffer{}, &stderr)

	if code == 0 {
		t.Fatal("expected non-zero exit code")
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("expected unknown command error, got %q", stderr.String())
	}
}

func TestRunDefaultStartsRootRunnerWithConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
workers:
  app:
    port: 6767
    provider: openai
providers:
  openai:
    base_url: https://api.openai.com/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	var called bool
	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		called = true
		if opts.ConfigPath != configPath {
			t.Fatalf("unexpected config path %s", opts.ConfigPath)
		}
		if opts.ConfigDir != dir {
			t.Fatalf("unexpected config dir %s", opts.ConfigDir)
		}
		if len(opts.Config.Workers) != 1 {
			t.Fatalf("config was not loaded: %#v", opts.Config)
		}
		return nil
	})
	defer restore()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if !called {
		t.Fatal("root runner was not called")
	}
}

func TestRunDefaultRejectsLegacyConfigFlag(t *testing.T) {
	var stderr bytes.Buffer
	code := Run([]string{"--config", "config.yaml"}, &bytes.Buffer{}, &stderr)

	if code == 0 {
		t.Fatal("expected legacy --config to fail")
	}
	if !strings.Contains(stderr.String(), "--config") {
		t.Fatalf("expected --config flag error, got %q", stderr.String())
	}
}

func TestRunDefaultContinuesWhenConfiguredWorkerWillFailToStart(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
workers:
  bad:
    port: 6767
    provider: missing
providers:
  openai:
    base_url: https://api.openai.com/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	var called bool
	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		called = true
		if len(opts.Config.Workers) != 1 {
			t.Fatalf("config was not loaded: %#v", opts.Config)
		}
		return nil
	})
	defer restore()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0 despite failed worker config, got %d: %s", code, stderr.String())
	}
	if !called {
		t.Fatal("root runner was not called")
	}
}

func TestRunRootMainTUIWindowCreatesHostAndAttaches(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "new-session", "-d", "-s", "ainn-test-host", "-n", "ainn", "-P", "-F", "#{window_id}", "env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", dir, "--manager-port", "19090"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if strings.Join(got[i], " ") != strings.Join(want[i], " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], want[i])
		}
	}
}

func TestRunRootMainTUIWindowAttachesExistingHost(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run when tmux host exists: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:ainn"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if strings.Join(got[i], " ") != strings.Join(want[i], " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], want[i])
		}
	}
}

func TestRunRootMainTUIWindowRecreatesMissingMainWindowOnExistingHost(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")

	var got [][]string
	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		t.Fatalf("root runner should not run in tmux bootstrap parent: %#v", opts)
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"tmux", "-V"},
		{"tmux", "-L", "ainn-test", "has-session", "-t", "ainn-test-host"},
		{"tmux", "-L", "ainn-test", "select-window", "-t", "ainn-test-host:ainn"},
		{"tmux", "-L", "ainn-test", "new-window", "-t", "ainn-test-host", "-n", "ainn", "-P", "-F", "#{window_id}", "env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", dir, "--manager-port", "19090"},
		{"tmux", "-L", "ainn-test", "attach-session", "-t", "ainn-test-host"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if strings.Join(got[i], " ") != strings.Join(want[i], " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], want[i])
		}
	}
}

func TestRunRootMainTUIWindowChildRunsRootRunnerDirectly(t *testing.T) {
	dir := t.TempDir()
	writeRootConfig(t, dir, "ainn-test", "ainn-test-host", "main-tui-window")
	t.Setenv(tmuxRootChildEnvVar, "1")

	restoreTmux := func() func() {
		previous := rootTmuxRunnerFactory
		rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
			return rootTmuxRunnerFunc(func(args []string) (string, error) {
				t.Fatalf("tmux runner should not be used in child root: %#v", args)
				return "", nil
			})
		}
		return func() { rootTmuxRunnerFactory = previous }
	}()
	defer restoreTmux()

	var called bool
	restoreRoot := SetRootRunnerForTest(func(opts RootOptions) error {
		called = true
		if opts.ConfigDir != dir {
			t.Fatalf("unexpected config dir %s", opts.ConfigDir)
		}
		return nil
	})
	defer restoreRoot()
	restoreLocker := setRootLockerFactoryForTest(noopLocker{})
	defer restoreLocker()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if !called {
		t.Fatal("expected root runner to run in tmux child process")
	}
}

func TestRunRootRejectsSecondInstanceWhenLockHeld(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
workers:
  app:
    port: 6767
    provider: openai
providers:
  openai:
    base_url: https://api.openai.com/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	holdLockForTest(t)

	var called bool
	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		called = true
		return nil
	})
	defer restore()

	var stderr bytes.Buffer
	code := Run([]string{"--config-dir", dir, "--manager-port", "19091"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit when lock held, got 0: %s", stderr.String())
	}
	if called {
		t.Fatal("root runner should not be called when lock is held")
	}
	if !strings.Contains(stderr.String(), "another instance") {
		t.Fatalf("expected 'another instance' error, got: %s", stderr.String())
	}
}

func TestRootRunnerContinuesAfterConfiguredWorkerStartupFailure(t *testing.T) {
	startErr := errors.New("app: config_patch recovery state unresolved must be resolved before enabling")
	mgr := &fakeRootManager{startErr: startErr}
	server := &fakeRootServer{listenStarted: make(chan struct{})}
	program := &fakeRootProgram{waitForListen: server.listenStarted}

	restoreManager := setRootManagerFactoryForTest(func(opts RootOptions) rootManager {
		return mgr
	})
	defer restoreManager()
	restoreServer := setRootServerFactoryForTest(func(addr string, handler http.Handler) rootServer {
		server.addr = addr
		server.handler = handler
		return server
	})
	defer restoreServer()
	restoreProgram := func() func() {
		previous := rootProgramFactory
		rootProgramFactory = func(addr string, startupStatus string, configDir string) rootProgram {
			program.addr = addr
			program.startupStatus = startupStatus
			program.configDir = configDir
			return program
		}
		return func() { rootProgramFactory = previous }
	}()
	defer restoreProgram()

	err := rootRunner(RootOptions{
		ManagerPort: 19090,
		Config:      config.Config{},
	})
	if err != nil {
		t.Fatalf("expected root runner to keep manager running despite worker startup failure, got %v", err)
	}
	if !mgr.startConfiguredWorkersCalled {
		t.Fatal("expected root runner to attempt configured worker startup")
	}
	if !mgr.startHealthMonitorCalled {
		t.Fatal("expected health monitor to start after worker startup failure")
	}
	if !mgr.startUpstreamProberCalled {
		t.Fatal("expected upstream prober to start after worker startup failure")
	}
	if !server.listenCalled {
		t.Fatal("expected manager API server to start after worker startup failure")
	}
	if !program.runCalled {
		t.Fatal("expected TUI program to run after worker startup failure")
	}
	if !server.closeCalled {
		t.Fatal("expected server to be closed when program exits")
	}
	if program.startupStatus != startErr.Error() {
		t.Fatalf("expected startup status %q, got %q", startErr.Error(), program.startupStatus)
	}
}

func TestRootProgramFactoryBuildsTypeScriptTUICommand(t *testing.T) {
	program := rootProgramFactory("127.0.0.1:8787", "", "/tmp/ainn-config")
	cmd := program.CommandLine()
	if cmd[len(cmd)-2] != "run" || cmd[len(cmd)-1] != "src/cli.ts" {
		t.Fatalf("expected bun run src/cli.ts command, got %#v", cmd)
	}
	if program.WorkingDir() != "tui" {
		t.Fatalf("expected tui working dir, got %q", program.WorkingDir())
	}
	if program.Env()["AINN_URL"] != "http://127.0.0.1:8787" {
		t.Fatalf("expected AINN_URL for manager API, got %#v", program.Env())
	}
	if program.Env()["AINN_PROJECT_DIR"] == "" {
		t.Fatalf("expected AINN_PROJECT_DIR to be set, got %#v", program.Env())
	}
	if program.Env()["AINN_CONFIG_DIR"] != "/tmp/ainn-config" {
		t.Fatalf("expected AINN_CONFIG_DIR for TUI, got %#v", program.Env())
	}
}

func TestRootProgramEnvIncludesConfigDir(t *testing.T) {
	program := newTUIProgram("127.0.0.1:8787", "", "/tmp/ainn-config")

	if program.Env()["AINN_CONFIG_DIR"] != "/tmp/ainn-config" {
		t.Fatalf("expected AINN_CONFIG_DIR, got %#v", program.Env())
	}
}

func TestRootRunnerDoesNotWriteConfiguredWorkerStartupFailureToTerminal(t *testing.T) {
	startErr := errors.New("cli-groq: missing API key")
	mgr := &fakeRootManager{startErr: startErr}
	server := &fakeRootServer{listenStarted: make(chan struct{})}
	program := &fakeRootProgram{waitForListen: server.listenStarted}

	var logOutput bytes.Buffer
	restoreLogWriter := setRootLogWriterForTest(&logOutput)
	defer restoreLogWriter()

	restoreManager := setRootManagerFactoryForTest(func(opts RootOptions) rootManager {
		return mgr
	})
	defer restoreManager()
	restoreServer := setRootServerFactoryForTest(func(addr string, handler http.Handler) rootServer {
		server.addr = addr
		server.handler = handler
		return server
	})
	defer restoreServer()
	restoreProgram := func() func() {
		previous := rootProgramFactory
		rootProgramFactory = func(addr string, startupStatus string, configDir string) rootProgram {
			program.addr = addr
			program.startupStatus = startupStatus
			program.configDir = configDir
			return program
		}
		return func() { rootProgramFactory = previous }
	}()
	defer restoreProgram()

	err := rootRunner(RootOptions{ManagerPort: 19090, Config: config.Config{}})
	if err != nil {
		t.Fatalf("expected root runner to keep running, got %v", err)
	}
	if strings.Contains(logOutput.String(), startErr.Error()) {
		t.Fatalf("startup error should not be written to terminal log output: %q", logOutput.String())
	}
}

// holdLockForTest 替换 rootLockerFactory 让 Run 抢锁失败，模拟第二实例启动。
func holdLockForTest(t *testing.T) {
	t.Helper()
	previous := rootLockerFactory
	rootLockerFactory = func() rootLocker {
		return lockedLocker{}
	}
	t.Cleanup(func() { rootLockerFactory = previous })
}

type lockedLocker struct{}

func (lockedLocker) Acquire() (func(), error) {
	return nil, errAlreadyLocked
}

// noopLocker 总是成功抢锁，用于走 runRoot 的测试避免依赖真 /tmp/ainn.lock。
type noopLocker struct{}

func (noopLocker) Acquire() (func(), error) {
	return func() {}, nil
}

func TestFlockLockerRejectsSecondAcquireOnSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	first := flockLocker{path: path}
	release, err := first.Acquire()
	if err != nil {
		t.Fatalf("first acquire should succeed: %v", err)
	}
	defer release()

	second := flockLocker{path: path}
	if _, err := second.Acquire(); err == nil {
		t.Fatal("second acquire on same path should fail while first is held")
	}
}

func writeRootConfig(t *testing.T, configDir string, socketName string, hostSession string, hostStartMode string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`
settings:
  state_dir: ` + filepath.Join(configDir, "state") + `
  log_dir: ` + filepath.Join(configDir, "logs") + `
  terminal:
    host: tmux
    opener: default
    tmux:
      socket_name: ` + socketName + `
      host_session: ` + hostSession + `
      host_start_mode: ` + hostStartMode + `
workers: {}
upstreams: {}
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

type fakeRootManager struct {
	startErr                     error
	startConfiguredWorkersCalled bool
	startHealthMonitorCalled     bool
	startUpstreamProberCalled    bool
	closeCalled                  bool
}

func (m *fakeRootManager) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (m *fakeRootManager) Close() {
	m.closeCalled = true
}

func (m *fakeRootManager) StartConfiguredWorkers() error {
	m.startConfiguredWorkersCalled = true
	return m.startErr
}

func (m *fakeRootManager) StartHealthMonitor(_ time.Duration) func() {
	m.startHealthMonitorCalled = true
	return func() {}
}

func (m *fakeRootManager) StartUpstreamProber(_ time.Duration) func() {
	m.startUpstreamProberCalled = true
	return func() {}
}

type fakeRootServer struct {
	addr          string
	handler       http.Handler
	listenStarted chan struct{}
	listenCalled  bool
	closeCalled   bool
}

func (s *fakeRootServer) ensureListenStarted() {
	if s.listenStarted == nil {
		s.listenStarted = make(chan struct{})
	}
}

func (s *fakeRootServer) ListenAndServe() error {
	s.ensureListenStarted()
	s.listenCalled = true
	close(s.listenStarted)
	return http.ErrServerClosed
}

func (s *fakeRootServer) Close() error {
	s.closeCalled = true
	return nil
}

type fakeRootProgram struct {
	addr          string
	waitForListen <-chan struct{}
	runCalled     bool
	startupStatus string
	configDir     string
}

func (p *fakeRootProgram) Run() error {
	p.runCalled = true
	if p.waitForListen != nil {
		select {
		case <-p.waitForListen:
		case <-time.After(time.Second):
			return errors.New("timed out waiting for server startup")
		}
	}
	return nil
}

func (p *fakeRootProgram) CommandLine() []string {
	return []string{"fake"}
}

func (p *fakeRootProgram) WorkingDir() string {
	return ""
}

func (p *fakeRootProgram) Env() map[string]string {
	return map[string]string{}
}
