package cmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/jesse/codex-app-proxy/internal/manager"
)

type launchRunner interface {
	Run(args []string) error
}

type launchRunnerFunc func([]string) error

func (f launchRunnerFunc) Run(args []string) error {
	return f(args)
}

type multiString []string

func (m *multiString) String() string {
	return strings.Join(*m, ",")
}

func (m *multiString) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		*m = append(*m, part)
	}
	return nil
}

var launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
	return launchRunnerFunc(func(args []string) error {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Stdin = os.Stdin
		return cmd.Run()
	})
}

func runLaunch(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("launch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	worker := flags.String("worker", "", "worker port")
	profile := flags.String("profile", "", "codex profile")
	workspace := flags.String("cd", "", "workspace directory")
	var addDirs multiString
	flags.Var(&addDirs, "add-dir", "extra directories, comma separated")
	model := flags.String("model", "", "model override")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *worker == "" {
		fmt.Fprintln(stderr, "launch requires --worker")
		return 2
	}
	port, err := strconv.Atoi(*worker)
	if err != nil {
		fmt.Fprintf(stderr, "invalid worker port %q\n", *worker)
		return 2
	}
	if *profile == "" {
		*profile = *worker
	}

	opts := manager.CodexLaunchOptions{
		Profile:    *profile,
		Workspace:  *workspace,
		AddDirs:    addDirs,
		WorkerPort: port,
		Model:      *model,
	}
	cmd := manager.BuildCodexLaunchCommand(opts)
	runner := launchRunnerFactory(stdout, stderr)
	if err := runner.Run(cmd); err != nil {
		fmt.Fprintf(stderr, "failed to launch: %v\n", err)
		return 1
	}
	return 0
}
