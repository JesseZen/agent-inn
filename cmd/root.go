package cmd

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
)

type rootManager interface {
	http.Handler
	Close()
	StartConfiguredWorkers() error
	StartHealthMonitor(interval time.Duration) func()
	StartUpstreamProber(interval time.Duration) func()
	StartHostedTurnWatcher(interval time.Duration) func()
}

type rootServer interface {
	ListenAndServe() error
	Close() error
}

type rootProgram interface {
	Run(context.Context) error
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
		case "hosted-session":
			return runHostedSession(args[1:], stdout, stderr)
		case "hooks":
			return runHooks(args[1:], stdout, stderr)
		}
	}

	if len(args) > 0 && args[0] != "--config-dir" && args[0] != "--manager-port" {
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
	return runRoot(args, stdout, stderr)
}

type RootOptions struct {
	ConfigDir           string
	ConfigPath          string
	ManagerPort         int
	Config              config.Config
	ManagerLogger       *slog.Logger
	ManagerHealthLogger *slog.Logger
	ProcessArgs         []string
	Stdin               io.Reader
	Stdout              io.Writer
	Stderr              io.Writer
}

var rootManagerFactory = func(opts RootOptions) rootManager {
	return manager.New(manager.Config{
		Config:             opts.Config,
		ConfigPath:         opts.ConfigPath,
		Starter:            manager.ExecStarter{},
		ReconcileTurnHooks: true,
		Logger:             opts.ManagerLogger,
		HealthLogger:       opts.ManagerHealthLogger,
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

// flockLocker 用文件锁实现独占。锁文件路径由 canonical config-dir 推导，进程退出时由 OS 释放。
type flockLocker struct {
	path string
}

func rootLockDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return os.TempDir()
}

func rootLockPath(configDir string) (string, error) {
	canonicalConfigDir, err := filepath.Abs(filepath.Clean(expandHome(configDir)))
	if err != nil {
		return "", fmt.Errorf("canonicalize config dir %s: %w", configDir, err)
	}
	canonicalConfigDir, err = filepath.EvalSymlinks(canonicalConfigDir)
	if err != nil {
		return "", fmt.Errorf("canonicalize config dir %s: %w", configDir, err)
	}
	hash := sha256.Sum256([]byte(canonicalConfigDir))
	return filepath.Join(rootLockDir(), fmt.Sprintf("ainn-%x.lock", hash)), nil
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

var rootLockerFactory = func(lockPath string) rootLocker {
	return flockLocker{path: lockPath}
}

// setRootLockerFactoryForTest 替换锁工厂，让走 runRoot 的测试不依赖真实实例锁文件。
func setRootLockerFactoryForTest(locker rootLocker) func() {
	previous := rootLockerFactory
	rootLockerFactory = func(string) rootLocker { return locker }
	return func() { rootLockerFactory = previous }
}

// errAlreadyLocked 是抢锁失败的哨兵错误，runRoot 据此输出友好提示。
var errAlreadyLocked = fmt.Errorf("another instance is already running")

var rootRunner = runRootProcess

func SetRootRunnerForTest(runner func(RootOptions) error) func() {
	previous := rootRunner
	previousSupervisor := rootSupervisor
	rootRunner = runner
	rootSupervisor = runner
	return func() {
		rootRunner = previous
		rootSupervisor = previousSupervisor
	}
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
	expandedConfigDir := expandHome(*configDir)
	absConfigDir, err := filepath.Abs(expandedConfigDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to resolve config dir: %v\n", err)
		return 1
	}
	resolvedConfigDir := filepath.Clean(absConfigDir)
	configPath := filepath.Join(resolvedConfigDir, config.ConfigFileName)

	cfg, err := config.LoadFile(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	if cfg.Settings.Terminal.Tmux.HostStartMode == config.TmuxHostStartModeMainTUIWindow && os.Getenv(tmuxRootChildEnvVar) == "" {
		return runRootTmuxBootstrap(cfg, resolvedConfigDir, *managerPort, stdout, stderr)
	}

	lockPath, err := rootLockPath(resolvedConfigDir)
	if err != nil {
		writeRootStartupFailureLog(cfg.Settings, err)
		fmt.Fprintf(stderr, "failed to start: %v\n", err)
		return 1
	}
	if os.Getenv(rootProcessEnvVar) == "" {
		release, err := rootLockerFactory(lockPath).Acquire()
		if err != nil {
			writeRootStartupFailureLog(cfg.Settings, err)
			fmt.Fprintf(stderr, "failed to start: %v\n", err)
			return 1
		}
		defer release()
	}
	runner := rootRunner
	if os.Getenv(rootProcessEnvVar) == "" {
		runner = rootSupervisor
	}
	if err := runner(RootOptions{
		ConfigDir:   resolvedConfigDir,
		ConfigPath:  configPath,
		ManagerPort: *managerPort,
		Config:      cfg,
		ProcessArgs: append([]string(nil), args...),
		Stdin:       os.Stdin,
		Stdout:      stdout,
		Stderr:      stderr,
	}); err != nil {
		writeRootStartupFailureLog(cfg.Settings, err)
		fmt.Fprintf(stderr, "failed to start: %v\n", err)
		return 1
	}
	return 0
}

func writeRootStartupFailureLog(settings config.Settings, err error) {
	logDir := expandHome(settings.LogDir)
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return
	}
	f, openErr := os.OpenFile(filepath.Join(logDir, "ainn.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if openErr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "failed to start: %v\n", err)
}

func expandHome(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}
