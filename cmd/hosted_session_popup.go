package cmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
)

func runHostedSessionPopupOpen(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session popup-open", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	managerURL := flags.String("manager-url", "", "manager API URL")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	resolvedConfigDir, err := hostedSessionPopupConfigDir(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to resolve config dir: %v\n", err)
		return 1
	}
	cfg, err := config.LoadFile(filepath.Join(resolvedConfigDir, config.ConfigFileName))
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}

	runner := launchRunnerFactory(io.Discard, stderr)
	if _, err := runner.Run(manager.TmuxDetectCommand()); err != nil {
		fmt.Fprintf(stderr, "tmux is required for hosted popup: %v\n", err)
		return 1
	}
	command := manager.TmuxDisplayHostedPopupCommandForSettings(cfg.Settings, resolvedConfigDir, hostedSessionPopupManagerURL(*managerURL), hostedSessionExecutable())
	if _, err := runner.Run(command); err != nil {
		fmt.Fprintf(stderr, "failed to open hosted popup: %v\n", err)
		return 1
	}
	return 0
}

func runHostedSessionPopup(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session popup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	managerURL := flags.String("manager-url", "", "manager API URL")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	resolvedConfigDir, err := hostedSessionPopupConfigDir(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to resolve config dir: %v\n", err)
		return 1
	}
	program := &hostedSessionPopupProgram{managerURL: hostedSessionPopupManagerURL(*managerURL), configDir: resolvedConfigDir}
	if err := program.Run(); err != nil {
		fmt.Fprintf(stderr, "failed to start hosted popup: %v\n", err)
		return 1
	}
	return 0
}

func hostedSessionPopupConfigDir(configDir string) (string, error) {
	expandedConfigDir := expandHome(configDir)
	absConfigDir, err := filepath.Abs(expandedConfigDir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(filepath.Clean(absConfigDir))
}

func hostedSessionPopupManagerURL(value string) string {
	managerURL := strings.TrimSpace(value)
	if managerURL == "" {
		managerURL = strings.TrimSpace(os.Getenv("AINN_URL"))
	}
	if managerURL == "" {
		managerURL = defaultManagerURL
	}
	return managerURL
}

type hostedSessionPopupProgram struct {
	managerURL string
	configDir  string
}

func (p *hostedSessionPopupProgram) CommandLine() []string {
	return []string{bunPath(), "run", "src/cli.ts"}
}

func (p *hostedSessionPopupProgram) WorkingDir() string {
	return "tui"
}

func (p *hostedSessionPopupProgram) Env() map[string]string {
	env := map[string]string{
		"AINN_URL":                   p.managerURL,
		"AINN_CONFIG_DIR":            p.configDir,
		"AINN_EXECUTABLE":            hostedSessionExecutable(),
		"AINN_FAST_BOOT":             "1",
		"AINN_HOSTED_TERMINAL_POPUP": "1",
	}
	if cwd, err := os.Getwd(); err == nil {
		env["AINN_PROJECT_DIR"] = cwd
	}
	return env
}

func (p *hostedSessionPopupProgram) Run() error {
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
