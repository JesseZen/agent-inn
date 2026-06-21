package cmd

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/jesse/codex-app-proxy/internal/config"
	"github.com/jesse/codex-app-proxy/internal/constants"
	"github.com/jesse/codex-app-proxy/internal/manager"
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

	if len(args) > 0 && args[0] != "--config" && args[0] != "--manager-port" {
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
	return runRoot(args, stdout, stderr)
}

type RootOptions struct {
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

var rootProgramFactory = func(addr string, startupStatus string) rootProgram {
	return newTUIProgram(addr, startupStatus)
}

var rootLogWriter io.Writer = os.Stderr

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

	program := rootProgramFactory(addr, startupStatus)
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
	rootProgramFactory = func(addr string, _ string) rootProgram {
		return factory(addr)
	}
	return func() { rootProgramFactory = previous }
}

type tuiProgram struct {
	addr          string
	startupStatus string
}

func newTUIProgram(addr string, startupStatus string) *tuiProgram {
	return &tuiProgram{addr: addr, startupStatus: startupStatus}
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
		"CODEX_PROXY_URL": "http://" + p.addr,
	}
	if exe, err := os.Executable(); err == nil {
		env["CODEX_PROXY_EXECUTABLE"] = exe
	}
	if cwd, err := os.Getwd(); err == nil {
		env["CODEX_PROXY_PROJECT_DIR"] = cwd
	}
	if p.startupStatus != "" {
		env["CODEX_PROXY_STARTUP_STATUS"] = p.startupStatus
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
	flags := flag.NewFlagSet("codex-proxy", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", expandHome("~/.codex-proxy/config.yaml"), "config path")
	managerPort := flags.Int("manager-port", 9090, "manager API port")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	if err := rootRunner(RootOptions{ConfigPath: *configPath, ManagerPort: *managerPort, Config: cfg}); err != nil {
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
