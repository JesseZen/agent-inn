package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/manager"
)

const (
	tmuxRootChildEnvVar = "AINN_TMUX_ROOT_CHILD"
	tmuxMainWindowName  = "ainn"
	tmuxMainWindowIndex = "0"
	tmuxDebugEnvVar     = "AINN_TMUX_DEBUG"
	tmuxDebugLogEnvVar  = "AINN_TMUX_DEBUG_LOG"
	tmuxTraceWriteError = "write tmux trace "
	tmuxNoServerRunning = "no server running"
	tmuxCantFindSession = "can't find session"
	tmuxErrorConnecting = "error connecting to "
	tmuxNoSuchFile      = "No such file or directory"
)

type rootManager interface {
	http.Handler
	Close()
	StartConfiguredWorkers() error
	StartHealthMonitor(interval time.Duration) func()
	StartUpstreamProber(interval time.Duration) func()
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

type rootTmuxRunner interface {
	Run(args []string) (string, error)
}

type rootTmuxRunnerFunc func([]string) (string, error)

func (f rootTmuxRunnerFunc) Run(args []string) (string, error) {
	return f(args)
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

var rootTmuxRunnerFactory = func(stdout io.Writer, stderr io.Writer) rootTmuxRunner {
	debugToStderr := os.Getenv(tmuxDebugEnvVar) == "1"
	debugLogPath := os.Getenv(tmuxDebugLogEnvVar)
	return rootTmuxRunnerFunc(func(args []string) (string, error) {
		cmd := exec.Command(args[0], args[1:]...)
		attachSession := tmuxSubcommand(args) == "attach-session"
		var stdoutBuf bytes.Buffer
		var stderrBuf bytes.Buffer
		if attachSession {
			cmd.Stdout = stdout
			cmd.Stderr = stderr
		} else {
			cmd.Stdout = &stdoutBuf
			cmd.Stderr = &stderrBuf
		}
		cmd.Stdin = os.Stdin
		startedAt := time.Now()
		err := cmd.Run()
		duration := time.Since(startedAt)
		stdoutText := stdoutBuf.String()
		stderrText := stderrBuf.String()
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		if debugToStderr {
			fmt.Fprintf(stderr, "tmux trace: %s duration_ms=%d stdout=%q stderr=%q err=%q\n", strings.Join(args, " "), duration.Milliseconds(), stdoutText, stderrText, errText)
		}
		if debugLogPath != "" {
			record := struct {
				Argv       []string `json:"argv"`
				Stdout     string   `json:"stdout"`
				Stderr     string   `json:"stderr"`
				Err        string   `json:"err"`
				DurationMS int64    `json:"duration_ms"`
			}{
				Argv:       append([]string{}, args...),
				Stdout:     stdoutText,
				Stderr:     stderrText,
				Err:        errText,
				DurationMS: duration.Milliseconds(),
			}
			f, openErr := os.OpenFile(debugLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if openErr != nil {
				return stdoutText, fmt.Errorf("write tmux trace %s: %w", debugLogPath, openErr)
			}
			encodeErr := json.NewEncoder(f).Encode(record)
			closeErr := f.Close()
			if encodeErr != nil {
				return stdoutText, fmt.Errorf("write tmux trace %s: %w", debugLogPath, encodeErr)
			}
			if closeErr != nil {
				return stdoutText, fmt.Errorf("write tmux trace %s: %w", debugLogPath, closeErr)
			}
		}
		if err != nil && strings.TrimSpace(stderrText) != "" {
			return stdoutText, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderrText))
		}
		return stdoutText, err
	})
}

func isTmuxHostMissingError(err error) bool {
	errText := err.Error()
	return strings.Contains(errText, tmuxNoServerRunning) ||
		strings.Contains(errText, tmuxCantFindSession) ||
		(strings.Contains(errText, tmuxErrorConnecting) && strings.Contains(errText, tmuxNoSuchFile))
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

var rootRunner = func(opts RootOptions) error {
	mgr := rootManagerFactory(opts)
	defer mgr.Close()
	startupStatus := ""
	if err := mgr.StartConfiguredWorkers(); err != nil {
		startupStatus = err.Error()
	}
	stopHealthMonitor := mgr.StartHealthMonitor(0)
	defer stopHealthMonitor()
	stopUpstreamProber := mgr.StartUpstreamProber(0)
	defer stopUpstreamProber()
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

	cfg, err := config.LoadFile(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	if cfg.Settings.Terminal.Tmux.HostStartMode == config.TmuxHostStartModeMainTUIWindow && os.Getenv(tmuxRootChildEnvVar) == "" {
		runner := rootTmuxRunnerFactory(stdout, stderr)
		if _, err := runner.Run(manager.TmuxDetectCommand()); err != nil {
			if strings.HasPrefix(err.Error(), tmuxTraceWriteError) {
				fmt.Fprintln(stderr, err)
				return 1
			}
			fmt.Fprintf(stderr, "tmux is required for main-tui-window mode: %v\n", err)
			return 1
		}
		insideTmux := os.Getenv("TMUX") != "" && os.Getenv("TMUX_PANE") != ""
		currentSocketPath, _, _ := strings.Cut(os.Getenv("TMUX"), ",")
		if insideTmux {
			currentSocketName := filepath.Base(currentSocketPath)
			configuredSocketName := cfg.Settings.Terminal.Tmux.SocketName
			if currentSocketName != configuredSocketName {
				fmt.Fprintf(stderr, "unsupported tmux startup state: current tmux socket %q differs from configured tmux socket %q\n", currentSocketName, configuredSocketName)
				return 1
			}
		}
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(stderr, "failed to locate executable: %v\n", err)
			return 1
		}
		rootCmd := []string{"env", tmuxRootChildEnvVar + "=1", exe, "--config-dir", *configDir, "--manager-port", strconv.Itoa(*managerPort)}
		if _, err := runner.Run(manager.TmuxHasSessionCommandForSettings(cfg.Settings)); err != nil {
			if strings.HasPrefix(err.Error(), tmuxTraceWriteError) {
				fmt.Fprintln(stderr, err)
				return 1
			}
			if !isTmuxHostMissingError(err) {
				fmt.Fprintf(stderr, "failed to inspect tmux host session: %v\n", err)
				return 1
			}
			createdWindowIndex, err := runner.Run(manager.TmuxStartMainWindowHostCommandForSettings(cfg.Settings, tmuxMainWindowName, rootCmd))
			if err != nil {
				fmt.Fprintf(stderr, "failed to start tmux host: %v\n", err)
				return 1
			}
			createdWindowIndex = strings.TrimSpace(createdWindowIndex)
			if createdWindowIndex != tmuxMainWindowIndex {
				if _, err := runner.Run(manager.TmuxMoveWindowToMainWindowCommandForSettings(cfg.Settings, createdWindowIndex)); err != nil {
					fmt.Fprintf(stderr, "failed to move tmux main window: %v\n", err)
					return 1
				}
			}
		} else if paneStartCommand, err := runner.Run(manager.TmuxMainWindowPaneStartCommandForSettings(cfg.Settings)); err != nil {
			if strings.HasPrefix(err.Error(), tmuxTraceWriteError) {
				fmt.Fprintln(stderr, err)
				return 1
			}
			if !strings.Contains(err.Error(), "can't find window") {
				fmt.Fprintf(stderr, "failed to inspect main tmux window: %v\n", err)
				return 1
			}
			if _, err := runner.Run(manager.TmuxCreateMainWindowCommandForSettings(cfg.Settings, tmuxMainWindowName, rootCmd)); err != nil {
				fmt.Fprintf(stderr, "failed to create main tmux window: %v\n", err)
				return 1
			}
		} else if !strings.Contains(paneStartCommand, exe) {
			if _, err := runner.Run(manager.TmuxRespawnMainWindowCommandForSettings(cfg.Settings, rootCmd)); err != nil {
				if strings.HasPrefix(err.Error(), tmuxTraceWriteError) {
					fmt.Fprintln(stderr, err)
					return 1
				}
				fmt.Fprintf(stderr, "failed to respawn main tmux window: %v\n", err)
				return 1
			}
		}
		if insideTmux {
			clientRows, err := runner.Run(manager.TmuxListClientPanesCommand(currentSocketPath))
			if err != nil {
				if strings.HasPrefix(err.Error(), tmuxTraceWriteError) {
					fmt.Fprintln(stderr, err)
					return 1
				}
				fmt.Fprintf(stderr, "failed to identify tmux client: %v\n", err)
				return 1
			}
			currentPaneID := os.Getenv("TMUX_PANE")
			clientName := ""
			for _, row := range strings.Split(strings.TrimSpace(clientRows), "\n") {
				rowClientName, rowPaneID, _ := strings.Cut(row, "\t")
				if rowPaneID == currentPaneID {
					clientName = rowClientName
					break
				}
			}
			if clientName == "" {
				fmt.Fprintf(stderr, "failed to identify tmux client: no client found for pane %s\n", currentPaneID)
				return 1
			}
			if _, err := runner.Run(manager.TmuxSwitchClientToMainWindowCommandForSettings(cfg.Settings, clientName)); err != nil {
				if strings.HasPrefix(err.Error(), tmuxTraceWriteError) {
					fmt.Fprintln(stderr, err)
					return 1
				}
				fmt.Fprintf(stderr, "failed to switch tmux client: %v\n", err)
				return 1
			}
			return 0
		}
		if _, err := runner.Run(manager.TmuxSelectMainWindowCommandForSettings(cfg.Settings)); err != nil {
			if strings.HasPrefix(err.Error(), tmuxTraceWriteError) {
				fmt.Fprintln(stderr, err)
				return 1
			}
			fmt.Fprintf(stderr, "failed to select main tmux window: %v\n", err)
			return 1
		}
		if _, err := runner.Run(manager.TmuxAttachCommandForSettings(cfg.Settings)); err != nil {
			fmt.Fprintf(stderr, "failed to attach tmux host: %v\n", err)
			return 1
		}
		return 0
	}

	lockPath, err := rootLockPath(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to start: %v\n", err)
		return 1
	}
	release, err := rootLockerFactory(lockPath).Acquire()
	if err != nil {
		fmt.Fprintf(stderr, "failed to start: %v\n", err)
		return 1
	}
	defer release()
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
