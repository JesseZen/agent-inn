package cmd

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/manager"
)

type rootManager interface {
	http.Handler
	Close()
	StartConfiguredWorkers() error
	StartHealthMonitor(interval time.Duration) func()
}

type rootServer interface {
	ListenAndServe() error
	Close() error
}

type rootProgram interface {
	Run() error
	CommandLine() []string
	WorkingDir() string
	Env() map[string]string
}

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "version":
			return runVersion(stdout)
		case "worker":
			return runWorker(args[1:], stdout, stderr)
		case "launch":
			return runLaunch(args[1:], stdout, stderr)
		}
	}

	if len(args) > 0 && args[0] != "--config-dir" && args[0] != "--manager-port" {
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
	return runRoot(args, stdout, stderr)
}

type RootOptions struct {
	ConfigDir   string
	ConfigPath  string
	ManagerPort int
	Config      config.Config
}

var rootManagerFactory = func(opts RootOptions) rootManager {
	return manager.New(manager.Config{
		Config:     opts.Config,
		ConfigPath: opts.ConfigPath,
		Starter:    manager.ExecStarter{},
	})
}

var rootServerFactory = func(addr string, handler http.Handler) rootServer {
	return &http.Server{Addr: addr, Handler: handler}
}

var rootProgramFactory = func(addr string, startupStatus string, configDir string) rootProgram {
	return newTUIProgram(addr, startupStatus, configDir)
}

var rootLogWriter io.Writer = os.Stderr

// rootLocker 抢占独占锁，避免两个 root 进程同时运行 manager + TUI 导致状态不同步。
type rootLocker interface {
	Acquire() (release func(), err error)
}

// flockLocker 用文件锁实现独占。锁文件路径固定，进程退出时由 OS 释放。
type flockLocker struct {
	path string
}

func defaultLockPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, constants.LockFileName)
	}
	return filepath.Join(os.TempDir(), constants.LockFileName)
}

func (l flockLocker) Acquire() (func(), error) {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", l.path, err)
	}
	if err := flockTryLock(f); err != nil {
		_ = f.Close()
		return nil, errAlreadyLocked
	}
	return func() { _ = f.Close() }, nil
}

var rootLockerFactory = func() rootLocker {
	return flockLocker{path: defaultLockPath()}
}

// setRootLockerFactoryForTest 替换锁工厂，让走 runRoot 的测试不依赖真 /tmp/ainn.lock。
func setRootLockerFactoryForTest(locker rootLocker) func() {
	previous := rootLockerFactory
	rootLockerFactory = func() rootLocker { return locker }
	return func() { rootLockerFactory = previous }
}

// errAlreadyLocked 是抢锁失败的哨兵错误，runRoot 据此输出友好提示。
var errAlreadyLocked = fmt.Errorf("another instance is already running")

var rootRunner = func(opts RootOptions) error {
	mgr := rootManagerFactory(opts)
	defer mgr.Close()
	startupStatus := ""
	if err := mgr.StartConfiguredWorkers(); err != nil {
		startupStatus = err.Error()
	}
	stopHealthMonitor := mgr.StartHealthMonitor(0)
	defer stopHealthMonitor()
	addr := constants.LocalhostAddr + ":" + strconv.Itoa(opts.ManagerPort)
	server := rootServerFactory(addr, mgr)
	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	program := rootProgramFactory(addr, startupStatus, opts.ConfigDir)
	if err := program.Run(); err != nil {
		_ = server.Close()
		return err
	}
	_ = server.Close()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func SetRootRunnerForTest(runner func(RootOptions) error) func() {
	previous := rootRunner
	rootRunner = runner
	return func() { rootRunner = previous }
}

func setRootManagerFactoryForTest(factory func(RootOptions) rootManager) func() {
	previous := rootManagerFactory
	rootManagerFactory = factory
	return func() { rootManagerFactory = previous }
}

func setRootServerFactoryForTest(factory func(string, http.Handler) rootServer) func() {
	previous := rootServerFactory
	rootServerFactory = factory
	return func() { rootServerFactory = previous }
}

func setRootProgramFactoryForTest(factory func(string) rootProgram) func() {
	previous := rootProgramFactory
	rootProgramFactory = func(addr string, _ string, _ string) rootProgram {
		return factory(addr)
	}
	return func() { rootProgramFactory = previous }
}

type tuiProgram struct {
	addr          string
	startupStatus string
	configDir     string
}

func newTUIProgram(addr string, startupStatus string, configDir string) *tuiProgram {
	return &tuiProgram{addr: addr, startupStatus: startupStatus, configDir: configDir}
}

func (p *tuiProgram) CommandLine() []string {
	return []string{bunPath(), "run", "src/cli.ts"}
}

func bunPath() string {
	if path := os.Getenv("BUN_PATH"); path != "" {
		return path
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := home + "/.bun/bin/bun"
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "bun"
}

func (p *tuiProgram) WorkingDir() string {
	return "tui"
}

func (p *tuiProgram) Env() map[string]string {
	env := map[string]string{
		"AINN_URL": "http://" + p.addr,
	}
	if p.configDir != "" {
		env["AINN_CONFIG_DIR"] = p.configDir
	}
	if exe, err := os.Executable(); err == nil {
		env["AINN_EXECUTABLE"] = exe
	}
	if cwd, err := os.Getwd(); err == nil {
		env["AINN_PROJECT_DIR"] = cwd
	}
	if p.startupStatus != "" {
		env["AINN_STARTUP_STATUS"] = p.startupStatus
	}
	return env
}

func (p *tuiProgram) Run() error {
	line := p.CommandLine()
	cmd := exec.Command(line[0], line[1:]...)
	cmd.Dir = p.WorkingDir()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()
	for key, value := range p.Env() {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	return cmd.Run()
}

func setRootLogWriterForTest(writer io.Writer) func() {
	previous := rootLogWriter
	rootLogWriter = writer
	return func() { rootLogWriter = previous }
}

func runRoot(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("ainn", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	managerPort := flags.Int("manager-port", 9090, "manager API port")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	configPath := filepath.Join(*configDir, config.ConfigFileName)

	release, err := rootLockerFactory().Acquire()
	if err != nil {
		fmt.Fprintf(stderr, "failed to start: %v\n", err)
		return 1
	}
	defer release()

	cfg, err := config.LoadFile(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	if err := rootRunner(RootOptions{ConfigDir: *configDir, ConfigPath: configPath, ManagerPort: *managerPort, Config: cfg}); err != nil {
		fmt.Fprintf(stderr, "failed to start: %v\n", err)
		return 1
	}
	return 0
}

func expandHome(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}
