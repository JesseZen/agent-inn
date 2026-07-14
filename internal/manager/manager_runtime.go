package manager

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/logging"
	"github.com/jesse/agent-inn/internal/module"
	"github.com/jesse/agent-inn/internal/modulehook"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
)

func (m *Manager) setConfigPatchStatus(name string, state modulehook.ConfigPatchState, detail map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := modulehook.Status{State: string(state), Detail: detail}
	m.supervisorFor(name).setHookStatus(modulehook.ConfigPatchName, status)
	if state == "" || state == modulehook.ConfigPatchClean || state == modulehook.ConfigPatchRecovered || state == modulehook.ConfigPatchActive {
		delete(m.hookStatuses[name], modulehook.ConfigPatchName)
		if len(m.hookStatuses[name]) == 0 {
			delete(m.hookStatuses, name)
		}
		return
	}
	if m.hookStatuses[name] == nil {
		m.hookStatuses[name] = map[string]modulehook.Status{}
	}
	m.hookStatuses[name][modulehook.ConfigPatchName] = cloneHookStatus(status)
}

func (m *Manager) workerGeneration(name string) int {
	m.mu.RLock()
	if supervisor := m.supervisors[name]; supervisor != nil && supervisor.AppliedGeneration() > 0 {
		generation := supervisor.AppliedGeneration()
		m.mu.RUnlock()
		return generation
	}
	defer m.mu.RUnlock()
	return m.workerGenerationLocked(name)
}

func (m *Manager) workerStatusLocked(name string) WorkerState {
	if status := m.statuses[name]; status != "" {
		return status
	}
	return WorkerStateConfigured
}

func (m *Manager) workerGenerationLocked(name string) int {
	if generation := m.generations[name]; generation > 0 {
		return generation
	}
	if _, ok := m.config.Workers[name]; ok {
		return 1
	}
	return 0
}

func (m *Manager) setWorkerGenerationLocked(name string, generation int) {
	if generation < 1 {
		generation = 1
	}
	m.generations[name] = generation
}

func (m *Manager) bumpWorkerGeneration(name string) {
	m.mu.Lock()
	next := m.workerGenerationLocked(name) + 1
	m.generations[name] = next
	m.supervisorFor(name).setAppliedGeneration(next)
	m.mu.Unlock()
}

func (m *Manager) StartWorker(name string) error {
	return m.startWorker(name, true)
}

func (m *Manager) startWorker(name string, resetRetries bool) error {
	if resetRetries {
		m.mu.Lock()
		m.retries[name] = 0
		delete(m.healthySince, name)
		m.supervisorFor(name).setRetryCount(0)
		m.supervisorFor(name).setHealthySince(time.Time{})
		m.mu.Unlock()
	}
	spawn, err := m.BuildWorkerSpawn(name)
	if err != nil {
		m.mu.Lock()
		m.supervisorFor(name).setStatus(WorkerStateFailed)
		m.statuses[name] = WorkerStateFailed
		m.mu.Unlock()
		m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
		return err
	}
	sink, err := m.LogSink(name)
	if err != nil {
		m.mu.Lock()
		m.supervisorFor(name).setStatus(WorkerStateFailed)
		m.statuses[name] = WorkerStateFailed
		m.mu.Unlock()
		m.logger.Error(logging.EventWorkerSpawn, "worker", name, "err", err.Error())
		m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
		return err
	}
	spawn.LogWriter = sink
	metricSource := workerMetricSource{name: name, port: spawn.Port}
	spawn.MetricsHandler = func(r io.Reader) {
		m.readWorkerMetricsFrom(metricSource, r)
	}
	if m.starter == nil {
		m.mu.Lock()
		m.supervisorFor(name).setStatus(WorkerStateRunning)
		m.supervisorFor(name).setAppliedGeneration(1)
		m.statuses[name] = WorkerStateRunning
		m.setWorkerGenerationLocked(name, 1)
		m.mu.Unlock()
		m.publishEvent(EventWorkerStarted, map[string]any{"worker": name, "status": string(WorkerStateRunning)})
		return nil
	}
	process, err := m.starter.Start(spawn)
	if err != nil {
		m.mu.Lock()
		m.supervisorFor(name).setStatus(WorkerStateFailed)
		m.statuses[name] = WorkerStateFailed
		m.mu.Unlock()
		m.logger.Error(logging.EventWorkerSpawn, "worker", name, "err", err.Error())
		m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
		return err
	}
	m.mu.Lock()
	m.processes[name] = process
	m.supervisorFor(name).setProcess(process)
	m.supervisorFor(name).setStatus(WorkerStateRunning)
	m.supervisorFor(name).setAppliedGeneration(1)
	m.statuses[name] = WorkerStateRunning
	m.setWorkerGenerationLocked(name, 1)
	m.mu.Unlock()
	m.logger.Info(logging.EventWorkerSpawn, "worker", name, "port", spawn.Port)
	m.publishEvent(EventWorkerStarted, map[string]any{"worker": name, "status": string(WorkerStateRunning)})
	return nil
}

func (m *Manager) LogSink(name string) (*logging.WorkerLogSink, error) {
	m.mu.RLock()
	if sink := m.logs[name]; sink != nil {
		sink.SetLevel(workerLogLevel(m.config.Workers[name]))
		m.mu.RUnlock()
		return sink, nil
	}
	worker := m.config.Workers[name]
	logDir := m.config.Settings.LogDir
	m.mu.RUnlock()

	if logDir == "" {
		logDir = "~/.ainn/logs"
	}
	logDir = expandHomePath(logDir)
	sink, err := logging.NewWorkerLogSink(filepath.Join(logDir, fmt.Sprintf("worker-%d.log", worker.Port)), 1000)
	if err != nil {
		return nil, fmt.Errorf("open worker log %s: %w", filepath.Join(logDir, fmt.Sprintf("worker-%d.log", worker.Port)), err)
	}
	sink.SetLevel(workerLogLevel(worker))

	m.mu.Lock()
	if existing := m.logs[name]; existing != nil {
		m.mu.Unlock()
		_ = sink.Close()
		return existing, nil
	}
	m.logs[name] = sink
	m.mu.Unlock()
	return sink, nil
}

func (m *Manager) StopWorker(name string) error {
	m.mu.Lock()
	process := m.processes[name]
	if process != nil {
		delete(m.processes, name)
		m.statuses[name] = WorkerStateStopping
		m.supervisorFor(name).setStatus(WorkerStateStopping)
		m.supervisorFor(name).clearProcess()
	}
	m.mu.Unlock()

	status, err := stopManagedProcess(process)
	exit := managedProcessExit(process)
	if err != nil {
		m.logger.Error(logging.EventWorkerExit,
			"worker", name,
			"status", string(WorkerStateFailed),
			"exit_code", exit.ExitCode,
			"signal", exit.Signal,
			"forced", exit.Forced,
			"process_error", exit.Error,
			"err", err.Error(),
		)
		m.mu.Lock()
		m.supervisorFor(name).setStatus(WorkerStateFailed)
		m.statuses[name] = WorkerStateFailed
		m.mu.Unlock()
		m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
		return err
	}
	m.mu.Lock()
	m.statuses[name] = status
	m.supervisorFor(name).setStatus(status)
	m.mu.Unlock()
	m.logger.Info(logging.EventWorkerExit,
		"worker", name,
		"status", string(status),
		"exit_code", exit.ExitCode,
		"signal", exit.Signal,
		"forced", exit.Forced,
		"process_error", exit.Error,
	)
	m.publishEvent(EventWorkerStopped, map[string]any{"worker": name, "status": string(status)})
	return nil
}

func (m *Manager) RestartWorker(name string) error {
	return m.restartWorker(name, true)
}

func (m *Manager) UpdateWorker(name string, current config.WorkerConfig, next config.WorkerConfig) error {
	if next.LogLevel == "" {
		next.LogLevel = "simple"
	}
	sink, err := m.LogSink(name)
	if err != nil {
		return err
	}
	sink.SetLevel(next.LogLevel)
	wasRunning := m.workerStatus(name) == WorkerStateRunning
	if next.Port == current.Port {
		m.updateConfig(func(cfgRoot *config.Config) {
			cfgRoot.Workers[name] = next
		})
		m.publishWorkerUpdated(name, next)
		if wasRunning {
			if !reflect.DeepEqual(current.Hooks, next.Hooks) {
				return m.RestartWorker(name)
			}
			m.bumpWorkerGeneration(name)
			client := m.workerClient
			if client == nil {
				client = defaultWorkerClient()
			}
			runtime, err := m.runtimeForWorker(name)
			if err == nil {
				_, err = client.ApplyRuntime(next.Port, runtime)
			}
			if err != nil {
				m.mu.Lock()
				m.statuses[name] = WorkerStateOutOfSync
				m.mu.Unlock()
				m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateOutOfSync), "error": redactedErrorMessage(err)})
				return err
			}
		}
		return nil
	}

	oldProcess := m.processForWorker(name)
	m.updateConfig(func(cfgRoot *config.Config) {
		cfgRoot.Workers[name] = next
	})
	if wasRunning {
		if err := m.startWorker(name, true); err != nil {
			m.updateConfig(func(cfgRoot *config.Config) {
				cfgRoot.Workers[name] = current
			})
			m.mu.Lock()
			if oldProcess != nil {
				m.processes[name] = oldProcess
				m.statuses[name] = WorkerStateRunning
				m.supervisorFor(name).setStatus(WorkerStateRunning)
				m.supervisorFor(name).setProcess(oldProcess)
			}
			m.mu.Unlock()
			return err
		}
		if err := m.waitForWorkerHealth(next.Port); err != nil {
			newProcess := m.processForWorker(name)
			_, _ = stopManagedProcess(newProcess)
			m.updateConfig(func(cfgRoot *config.Config) {
				cfgRoot.Workers[name] = current
			})
			m.mu.Lock()
			if oldProcess != nil {
				m.processes[name] = oldProcess
				m.statuses[name] = WorkerStateRunning
				m.supervisorFor(name).setStatus(WorkerStateRunning)
				m.supervisorFor(name).setProcess(oldProcess)
			} else {
				delete(m.processes, name)
				m.statuses[name] = WorkerStateFailed
				m.supervisorFor(name).setStatus(WorkerStateFailed)
			}
			m.mu.Unlock()
			if oldProcess == nil {
				m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
			}
			return err
		}
		m.publishWorkerUpdated(name, next)
		if _, err := stopManagedProcess(oldProcess); err != nil {
			m.mu.Lock()
			m.statuses[name] = WorkerStateFailed
			m.supervisorFor(name).setStatus(WorkerStateFailed)
			m.mu.Unlock()
			m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
			return err
		}
		m.mu.Lock()
		m.statuses[name] = WorkerStateRunning
		m.supervisorFor(name).setStatus(WorkerStateRunning)
		m.mu.Unlock()
		return nil
	}
	m.publishWorkerUpdated(name, next)
	return nil
}

func (m *Manager) publishWorkerUpdated(name string, worker config.WorkerConfig) {
	m.publishEvent(EventWorkerUpdated, map[string]any{
		"worker":             name,
		"port":               worker.Port,
		"role":               worker.Role,
		"launcher":           worker.Launcher,
		"upstream":           worker.Upstream,
		"upstream_pool":      worker.UpstreamPool,
		"proxy_url":          appruntime.RedactProxyURL(worker.ProxyURL),
		"proxy_url_redacted": appruntime.ProxyURLRedacted(worker.ProxyURL),
		"log_level":          workerLogLevel(worker),
		"modules":            cloneModules(worker.RequestModules),
		"hooks":              cloneModules(worker.Hooks),
	})
}

func (m *Manager) waitForWorkerHealth(port int) error {
	checker := m.healthChecker
	if checker == nil {
		checker = HTTPHealthChecker{Client: http.DefaultClient}
	}
	deadline := m.clock().Add(m.healthWait)
	for {
		if checker.Check(port) {
			return nil
		}
		if !m.clock().Before(deadline) {
			return fmt.Errorf("worker on port :%d did not become healthy", port)
		}
		time.Sleep(m.healthPoll)
	}
}

func (m *Manager) processForWorker(name string) ManagedProcess {
	m.mu.RLock()
	if supervisor := m.supervisors[name]; supervisor != nil {
		process := supervisor.Process()
		m.mu.RUnlock()
		return process
	}
	defer m.mu.RUnlock()
	return m.processes[name]
}

func stopManagedProcess(process ManagedProcess) (WorkerState, error) {
	if process == nil {
		return WorkerStateStopped, nil
	}
	if err := process.Stop(); err != nil {
		return WorkerStateFailed, err
	}
	if reporter, ok := process.(forcedStopReporter); ok && reporter.ForcedStop() {
		return WorkerStateStoppedForced, nil
	}
	return WorkerStateStopped, nil
}

type processExitReporter interface {
	Exit() ProcessExit
}

func managedProcessExit(process ManagedProcess) ProcessExit {
	if reporter, ok := process.(processExitReporter); ok {
		return reporter.Exit()
	}
	return ProcessExit{}
}

func (m *Manager) restartWorker(name string, resetRetries bool) error {
	if err := m.StopWorker(name); err != nil {
		return err
	}
	if err := m.startWorker(name, resetRetries); err != nil {
		return err
	}
	m.logger.Info(logging.EventWorkerRestart, "worker", name)
	m.publishEvent(EventWorkerRestarted, map[string]any{"worker": name, "status": string(WorkerStateRunning)})
	return nil
}

func (m *Manager) RecordHealth(name string, healthy bool) {
	if healthy {
		now := m.clock()
		m.mu.Lock()
		since, ok := m.healthySince[name]
		if !ok {
			since = now
			m.healthySince[name] = since
			m.supervisorFor(name).setHealthySince(since)
		}
		if now.Sub(since) >= healthyRetryResetWindow {
			m.retries[name] = 0
			m.supervisorFor(name).setRetryCount(0)
		}
		if m.workerStatusLocked(name) != WorkerStateStopped {
			m.statuses[name] = WorkerStateRunning
			m.supervisorFor(name).setStatus(WorkerStateRunning)
		}
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	delete(m.healthySince, name)
	m.supervisorFor(name).setHealthySince(time.Time{})
	if m.workerStatusLocked(name) == WorkerStateFailed {
		m.mu.Unlock()
		return
	}
	m.retries[name]++
	m.supervisorFor(name).setRetryCount(m.retries[name])
	m.healthLogger.Warn(logging.EventHealthFail, "worker", name, "retries", m.retries[name])
	if m.retries[name] >= 10 {
		m.statuses[name] = WorkerStateFailed
		m.supervisorFor(name).setStatus(WorkerStateFailed)
		m.mu.Unlock()
		m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": "worker health check failed"})
		return
	}
	m.statuses[name] = WorkerStateRestarting
	m.supervisorFor(name).setStatus(WorkerStateRestarting)
	m.mu.Unlock()

	if err := m.restartWorker(name, false); err != nil {
		m.mu.Lock()
		m.statuses[name] = WorkerStateFailed
		m.supervisorFor(name).setStatus(WorkerStateFailed)
		m.mu.Unlock()
		m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
	}
}

func (m *Manager) StartHealthMonitor(interval time.Duration) func() {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	checker := m.healthChecker
	if checker == nil {
		checker = HTTPHealthChecker{Client: http.DefaultClient}
	}
	done := make(chan struct{})
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				for _, target := range m.healthTargets() {
					m.RecordHealth(target.name, checker.Check(target.port))
				}
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

type HTTPHealthChecker struct {
	Client *http.Client
}

func (c HTTPHealthChecker) Check(port int) bool {
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Get(fmt.Sprintf("http://%s:%d%s", constants.LocalhostAddr, port, constants.ProxyHealthPath))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (m *Manager) StartConfiguredWorkers() error {
	workers := m.workerConfigSnapshot()
	names := make([]string, 0, len(workers))
	for name := range workers {
		names = append(names, name)
	}
	sort.Strings(names)

	var errs []error
	for _, name := range names {
		state, detail, err := recoverConfigPatchBeforeWorkerStart(workers[name], name)
		m.setConfigPatchStatus(name, state, detail)
		if err != nil {
			m.mu.Lock()
			m.supervisorFor(name).setStatus(WorkerStateFailed)
			m.statuses[name] = WorkerStateFailed
			m.mu.Unlock()
			m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
	}
	for _, name := range names {
		if m.workerStatus(name) == WorkerStateFailed {
			continue
		}
		if err := m.StartWorker(name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func recoverConfigPatchBeforeWorkerStart(worker config.WorkerConfig, workerName string) (modulehook.ConfigPatchState, map[string]string, error) {
	moduleCfg, ok := worker.Hooks[modulehook.ConfigPatchName]
	if !ok || !moduleCfg.Enabled {
		return modulehook.ConfigPatchClean, nil, nil
	}
	patch := modulehook.NewConfigPatch(module.ModuleConfig{
		Enabled: moduleCfg.Enabled,
		Params:  cloneAnyMap(moduleCfg.Params),
	}, modulehook.BuildDependencies{
		WorkerID:   workerName,
		WorkerPort: worker.Port,
	})
	if err := patch.RecoverStaleJournal(); err != nil {
		return patch.State(), patch.Detail(), err
	}
	switch patch.State() {
	case modulehook.ConfigPatchUnresolved, modulehook.ConfigPatchFailed:
		return patch.State(), patch.Detail(), fmt.Errorf("config_patch recovery state %s must be resolved before enabling", patch.State())
	default:
		return patch.State(), patch.Detail(), nil
	}
}

func expandHomePath(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

type healthTarget struct {
	name string
	port int
}

func (m *Manager) healthTargets() []healthTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()

	targets := make([]healthTarget, 0, len(m.config.Workers))
	for name, worker := range m.config.Workers {
		if m.workerStatusLocked(name) == WorkerStateRunning {
			targets = append(targets, healthTarget{name: name, port: worker.Port})
		}
	}
	return targets
}

func (m *Manager) workerConfigSnapshot() map[string]config.WorkerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	workers := make(map[string]config.WorkerConfig, len(m.config.Workers))
	for name, worker := range m.config.Workers {
		workers[name] = cloneWorkerConfig(worker)
	}
	return workers
}

func (m *Manager) upstreamProfileSnapshot() map[string]config.UpstreamProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()

	providers := make(map[string]config.UpstreamProfile, len(m.config.Upstreams))
	for name, profile := range m.config.Upstreams {
		providers[name] = profile
	}
	return providers
}

func (m *Manager) workerConfig(name string) (config.WorkerConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	worker, ok := m.config.Workers[name]
	if !ok {
		return config.WorkerConfig{}, false
	}
	return cloneWorkerConfig(worker), true
}

func (m *Manager) configuredConfigPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.configPath
}

type liveWorkerTarget struct {
	name string
	port int
}

func (m *Manager) liveWorkersUsingUpstream(upstreamName string) []liveWorkerTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()

	targets := []liveWorkerTarget{}
	for workerName, worker := range m.config.Workers {
		if workerUpstreamID(worker) != upstreamName || m.workerStatusLocked(workerName) != WorkerStateRunning {
			continue
		}
		targets = append(targets, liveWorkerTarget{name: workerName, port: worker.Port})
	}
	return targets
}

func cloneConfig(cfg config.Config) config.Config {
	out := config.Config{
		Settings:       cfg.Settings,
		Plugins:        clonePluginDefinitions(cfg.Plugins),
		Workers:        make(map[string]config.WorkerConfig, len(cfg.Workers)),
		NextUpstreamID: cfg.NextUpstreamID,
		Upstreams:      make(map[string]config.UpstreamProfile, len(cfg.Upstreams)),
		UpstreamPools:  make(map[string]config.UpstreamPool, len(cfg.UpstreamPools)),
	}
	for name, worker := range cfg.Workers {
		out.Workers[name] = cloneWorkerConfig(worker)
	}
	for name, profile := range cfg.Upstreams {
		out.Upstreams[name] = profile
	}
	for name, pool := range cfg.UpstreamPools {
		pool.Upstreams = append([]string(nil), pool.Upstreams...)
		out.UpstreamPools[name] = pool
	}
	return out
}

func clonePluginDefinitions(plugins map[string]config.PluginDefinition) map[string]config.PluginDefinition {
	out := make(map[string]config.PluginDefinition, len(plugins))
	for name, plugin := range plugins {
		out[name] = plugin
	}
	return out
}

func buildPortIndex(workers map[string]config.WorkerConfig) map[int]string {
	out := make(map[int]string, len(workers))
	for name, worker := range workers {
		out[worker.Port] = name
	}
	return out
}

func cloneWorkerConfig(worker config.WorkerConfig) config.WorkerConfig {
	return config.WorkerConfig{
		Name:           worker.Name,
		Role:           worker.Role,
		Launcher:       worker.Launcher,
		Port:           worker.Port,
		Upstream:       worker.Upstream,
		UpstreamID:     worker.UpstreamID,
		UpstreamPool:   worker.UpstreamPool,
		ProxyURL:       worker.ProxyURL,
		LogLevel:       workerLogLevel(worker),
		RequestModules: cloneModules(worker.RequestModules),
		Hooks:          cloneModules(worker.Hooks),
	}
}

func cloneModules(modules map[string]config.ModuleConfig) map[string]config.ModuleConfig {
	out := make(map[string]config.ModuleConfig, len(modules))
	for name, module := range modules {
		out[name] = cloneModuleConfig(module)
	}
	return out
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneModuleConfig(module config.ModuleConfig) config.ModuleConfig {
	out := config.ModuleConfig{
		Enabled: module.Enabled,
	}
	if module.Params != nil {
		out.Params = make(map[string]any, len(module.Params))
		for key, value := range module.Params {
			out.Params[key] = value
		}
	}
	return out
}
