package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type WorkerSpawn struct {
	Port           int
	Args           []string
	RuntimeJSON    []byte
	LogWriter      io.Writer
	MetricsHandler func(io.Reader)
}

type ProcessExit struct {
	ExitCode int
	Signal   string
	Error    string
	Forced   bool
}

type ExecStarter struct {
	Executable      string
	StopGracePeriod time.Duration
}

type ExecProcess struct {
	mu              sync.Mutex
	cmd             *exec.Cmd
	stdin           *os.File
	configRead      *os.File
	metricsDone     <-chan struct{}
	waitDone        <-chan processWaitResult
	stopGracePeriod time.Duration
	forcedStop      bool
	lastExit        ProcessExit
}

type processWaitResult struct {
	exit ProcessExit
	err  error
}

const defaultManagerStopGracePeriod = 15 * time.Second

func (s ExecStarter) Start(spawn WorkerSpawn) (ManagedProcess, error) {
	executable := s.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	configRead, configWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		_ = configRead.Close()
		_ = configWrite.Close()
		return nil, err
	}
	var metricsRead *os.File
	var metricsWrite *os.File
	if spawn.MetricsHandler != nil {
		metricsRead, metricsWrite, err = os.Pipe()
		if err != nil {
			_ = configRead.Close()
			_ = configWrite.Close()
			_ = stdinRead.Close()
			_ = stdinWrite.Close()
			return nil, err
		}
	}

	args := append([]string{}, spawn.Args...)
	extraFiles := []*os.File{configRead}
	if spawn.MetricsHandler != nil {
		args = append(args, "--metrics-fd", "4")
		extraFiles = append(extraFiles, metricsWrite)
	}
	cmd := exec.Command(executable, args...)
	cmd.Env = sanitizedWorkerEnv(os.Environ())
	cmd.ExtraFiles = extraFiles
	cmd.Stdin = stdinRead
	if spawn.LogWriter != nil {
		cmd.Stdout = spawn.LogWriter
		cmd.Stderr = spawn.LogWriter
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		_ = configRead.Close()
		_ = configWrite.Close()
		_ = stdinRead.Close()
		_ = stdinWrite.Close()
		if metricsRead != nil {
			_ = metricsRead.Close()
			_ = metricsWrite.Close()
		}
		return nil, err
	}
	_ = configRead.Close()
	_ = stdinRead.Close()
	waitDone := make(chan processWaitResult, 1)
	go func() { waitDone <- waitForProcess(cmd) }()
	var metricsDone chan struct{}
	if spawn.MetricsHandler != nil {
		_ = metricsWrite.Close()
		metricsDone = make(chan struct{})
		go func() {
			defer func() {
				_ = metricsRead.Close()
				close(metricsDone)
			}()
			spawn.MetricsHandler(metricsRead)
		}()
	}
	if _, err := configWrite.Write(spawn.RuntimeJSON); err != nil {
		_ = configWrite.Close()
		_ = stdinWrite.Close()
		_ = cmd.Process.Kill()
		<-waitDone
		return nil, err
	}
	if err := configWrite.Close(); err != nil {
		_ = stdinWrite.Close()
		_ = cmd.Process.Kill()
		<-waitDone
		return nil, err
	}
	stopGracePeriod := s.StopGracePeriod
	if stopGracePeriod <= 0 {
		stopGracePeriod = defaultManagerStopGracePeriod
	}
	return &ExecProcess{cmd: cmd, stdin: stdinWrite, metricsDone: metricsDone, waitDone: waitDone, stopGracePeriod: stopGracePeriod}, nil
}

func sanitizedWorkerEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || isSecretEnvName(name) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func isSecretEnvName(name string) bool {
	upper := strings.ToUpper(name)
	secretMarkers := []string{
		"API_KEY",
		"ACCESS_KEY",
		"SECRET_KEY",
		"PRIVATE_KEY",
		"AUTH_TOKEN",
		"ACCESS_TOKEN",
		"REFRESH_TOKEN",
		"BEARER_TOKEN",
		"CLIENT_SECRET",
		"WEBHOOK_SECRET",
		"PASSWORD",
		"PASSWD",
		"TOKEN",
		"SECRET",
	}
	for _, marker := range secretMarkers {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func (p *ExecProcess) Stop() error {
	p.mu.Lock()
	cmd := p.cmd
	stdin := p.stdin
	metricsDone := p.metricsDone
	waitDone := p.waitDone
	stopGracePeriod := p.stopGracePeriod
	p.cmd = nil
	p.stdin = nil
	p.metricsDone = nil
	p.waitDone = nil
	p.mu.Unlock()

	if cmd == nil {
		return nil
	}
	if cmd.Process != nil {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errorsIsProcessDone(err) {
			return err
		}
	}
	stopDeadline := time.Now().Add(stopGracePeriod)
	if stdin != nil {
		_ = stdin.Close()
	}

	var result processWaitResult
	forced := false
	remainingGrace := time.Until(stopDeadline)
	select {
	case result = <-waitDone:
	case <-time.After(remainingGrace):
		forced = true
		p.markForcedStop()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		result = <-waitDone
	}
	result.exit.Forced = forced
	p.mu.Lock()
	p.lastExit = result.exit
	p.mu.Unlock()
	if metricsDone != nil {
		remainingGrace = time.Until(stopDeadline)
		select {
		case <-metricsDone:
		case <-time.After(remainingGrace):
		}
	}
	return ignoreManagedStopExit(result.err)
}

func (p *ExecProcess) ForcedStop() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.forcedStop
}

func (p *ExecProcess) Exit() ProcessExit {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastExit
}

func (p *ExecProcess) markForcedStop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.forcedStop = true
}

func errorsIsProcessDone(err error) bool {
	return errors.Is(err, os.ErrProcessDone)
}

func waitForProcess(cmd *exec.Cmd) processWaitResult {
	err := cmd.Wait()
	exit := ProcessExit{ExitCode: 0}
	if err == nil {
		return processWaitResult{exit: exit}
	}
	exit.ExitCode = -1
	exit.Error = err.Error()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
		exit.ExitCode = exitErr.ProcessState.ExitCode()
		if status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			exit.Signal = status.Signal().String()
		}
	}
	return processWaitResult{exit: exit, err: err}
}

func ignoreManagedStopExit(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
		if status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			switch status.Signal() {
			case syscall.SIGTERM, syscall.SIGKILL:
				return nil
			}
		}
	}
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (m *Manager) BuildWorkerSpawn(workerName string) (WorkerSpawn, error) {
	runtime, err := m.runtimeForWorker(workerName)
	if err != nil {
		return WorkerSpawn{}, err
	}
	payload, err := json.Marshal(runtime)
	if err != nil {
		return WorkerSpawn{}, err
	}
	return WorkerSpawn{
		Port:        runtime.ListenPort,
		Args:        []string{"worker", "--port", fmt.Sprintf("%d", runtime.ListenPort), "--config-fd", "3"},
		RuntimeJSON: payload,
	}, nil
}
