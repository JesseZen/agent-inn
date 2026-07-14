package manager

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/hostedhooks"
	"github.com/jesse/agent-inn/internal/logging"
	"github.com/jesse/agent-inn/internal/module"
	"github.com/jesse/agent-inn/internal/modulehook"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
	"github.com/jesse/agent-inn/internal/worker"
)

type Config struct {
	Config             config.Config
	ConfigPath         string
	ConfigStatus       config.Status
	Executable         string
	Starter            Starter
	HealthChecker      HealthChecker
	WorkerClient       WorkerClient
	ReconcileTurnHooks bool
	Logger             *slog.Logger
	HealthLogger       *slog.Logger
}

type Manager struct {
	mu                  sync.RWMutex
	failoverMu          sync.Mutex
	config              config.Config
	configPath          string
	configStatus        config.Status
	executable          string
	starter             Starter
	healthChecker       HealthChecker
	workerClient        WorkerClient
	clock               func() time.Time
	healthWait          time.Duration
	healthPoll          time.Duration
	store               *config.Store
	stopConfigWriter    func()
	events              *eventBus
	logger              *slog.Logger
	healthLogger        *slog.Logger
	portIndex           map[int]string
	supervisors         map[string]*WorkerSupervisor
	processes           map[string]ManagedProcess
	statuses            map[string]WorkerState
	retries             map[string]int
	healthySince        map[string]time.Time
	generations         map[string]int
	logs                map[string]*logging.WorkerLogSink
	hookStatuses        map[string]map[string]modulehook.Status
	metricsStore        *metricsStore
	metricsTrackers     map[string]*worker.MetricsTracker
	pendingMetrics      map[string]*pendingMetricsUpdate
	metricsStatusSem    chan struct{}
	circuits            *circuitBreaker
	desiredProbes       map[probeExecutionKey]probeSpec
	manualProbes        map[probeExecutionKey]probeSpec
	inFlightProbes      map[probeExecutionKey]probeSpec
	pendingProbes       map[probeExecutionKey]probeSpec
	probeGenerations    map[probeExecutionKey]int
	probeRunner         func(context.Context, probeSpec) upstream.ProbeResult
	probeContext        context.Context
	cancelProbes        context.CancelFunc
	probeWait           sync.WaitGroup
	readiness           map[string]readinessObservation
	readinessTimers     map[string]*time.Timer
	probeSchedules      map[poolProbeScheduleKey]poolProbeSchedule
	exhaustedPools      map[string]string
	hostedSessions      *HostedSessionRegistry
	hostedSnapshotMu    sync.Mutex
	hostedSnapshotCache map[string]HostedSessionSnapshot
	hostedSnapshotSize  int64
	hostedSnapshotMtime time.Time
	hostedSnapshotReady bool
	batchRegistry       *BatchRegistry
	reconcileTurnHooks  bool
}

type WorkerSummary struct {
	ID                 string                                      `json:"id"`
	Name               string                                      `json:"name"`
	Port               int                                         `json:"port"`
	Role               string                                      `json:"role"`
	Launcher           string                                      `json:"launcher"`
	UpstreamID         string                                      `json:"upstream_id"`
	UpstreamPool       string                                      `json:"upstream_pool,omitempty"`
	Upstream           upstream.RedactedUpstream                   `json:"upstream"`
	ProxyURL           string                                      `json:"proxy_url,omitempty"`
	ProxyURLRedacted   bool                                        `json:"proxy_url_redacted,omitempty"`
	Protocol           appruntime.ProtocolKind                     `json:"protocol,omitempty"`
	ModuleSupport      map[string]appruntime.ModuleProtocolSupport `json:"module_support,omitempty"`
	Status             string                                      `json:"status"`
	SnapshotGeneration int                                         `json:"snapshot_generation"`
	LogLevel           string                                      `json:"log_level"`
	Modules            map[string]config.ModuleConfig              `json:"modules,omitempty"`
	Hooks              map[string]config.ModuleConfig              `json:"hooks,omitempty"`
	Metrics            worker.MetricsSnapshot                      `json:"metrics"`
}

type Starter interface {
	Start(spawn WorkerSpawn) (ManagedProcess, error)
}

type ManagedProcess interface {
	Stop() error
}

type forcedStopReporter interface {
	ForcedStop() bool
}

type HealthChecker interface {
	Check(port int) bool
}

type WorkerClient interface {
	ToggleModule(port int, moduleName string) error
	PatchModule(port int, moduleName string, cfg config.ModuleConfig) error
	ApplyRuntime(port int, runtime appruntime.WorkerRuntime) (ApplyRuntimeStatus, error)
	SwitchUpstream(port int, runtime upstream.RuntimeUpstream) error
	GetStatus(port int) (WorkerStatus, error)
}

type ApplyRuntimeStatus struct {
	AppliedGeneration  appruntime.Generation `json:"applied_generation"`
	SnapshotGeneration int                   `json:"snapshot_generation,omitempty"`
}

type WorkerStatus struct {
	SnapshotGeneration int                                         `json:"snapshot_generation"`
	Upstream           upstream.RedactedUpstream                   `json:"upstream"`
	ProxyURL           string                                      `json:"proxy_url,omitempty"`
	ProxyURLRedacted   bool                                        `json:"proxy_url_redacted,omitempty"`
	Protocol           appruntime.ProtocolKind                     `json:"protocol,omitempty"`
	ModuleSupport      map[string]appruntime.ModuleProtocolSupport `json:"module_support,omitempty"`
	Modules            map[string]config.ModuleConfig              `json:"modules"`
	Hooks              map[string]config.ModuleConfig              `json:"hooks,omitempty"`
	HookStatuses       map[string]modulehook.Status                `json:"hook_statuses,omitempty"`
	Metrics            worker.MetricsSnapshot                      `json:"metrics"`
}

type WorkerDetail struct {
	ID                 string                                      `json:"id"`
	Name               string                                      `json:"name"`
	Port               int                                         `json:"port"`
	Role               string                                      `json:"role"`
	Launcher           string                                      `json:"launcher"`
	UpstreamID         string                                      `json:"upstream_id"`
	UpstreamPool       string                                      `json:"upstream_pool,omitempty"`
	Upstream           upstream.RedactedUpstream                   `json:"upstream"`
	ProxyURL           string                                      `json:"proxy_url,omitempty"`
	ProxyURLRedacted   bool                                        `json:"proxy_url_redacted,omitempty"`
	Protocol           appruntime.ProtocolKind                     `json:"protocol,omitempty"`
	ModuleSupport      map[string]appruntime.ModuleProtocolSupport `json:"module_support,omitempty"`
	Status             string                                      `json:"status"`
	SnapshotGeneration int                                         `json:"snapshot_generation"`
	LogLevel           string                                      `json:"log_level"`
	HookStatuses       map[string]modulehook.Status                `json:"hook_statuses,omitempty"`
	Modules            map[string]config.ModuleConfig              `json:"modules,omitempty"`
	Hooks              map[string]config.ModuleConfig              `json:"hooks,omitempty"`
	Metrics            worker.MetricsSnapshot                      `json:"metrics"`
}

const (
	healthyRetryResetWindow          = 60 * time.Second
	metricsHydrationConcurrencyLimit = 4
)

func New(cfg Config) *Manager {
	cfg.Config.ApplyDefaults()
	probeContext, cancelProbes := context.WithCancel(context.Background())
	store := config.NewStore(cfg.ConfigPath, cfg.Config)
	logger := cfg.Logger
	if logger == nil {
		logger = logging.New(io.Discard, "simple", logging.ComponentManagerSuper)
	}
	healthLogger := cfg.HealthLogger
	if healthLogger == nil {
		healthLogger = logger
	}
	m := &Manager{
		config:              cfg.Config,
		configPath:          cfg.ConfigPath,
		configStatus:        cfg.ConfigStatus,
		executable:          cfg.Executable,
		starter:             cfg.Starter,
		healthChecker:       cfg.HealthChecker,
		workerClient:        cfg.WorkerClient,
		clock:               time.Now,
		healthWait:          10 * time.Second,
		healthPoll:          100 * time.Millisecond,
		store:               store,
		events:              newEventBus(defaultEventBusCapacity),
		logger:              logger,
		healthLogger:        healthLogger,
		portIndex:           map[int]string{},
		supervisors:         map[string]*WorkerSupervisor{},
		processes:           map[string]ManagedProcess{},
		statuses:            map[string]WorkerState{},
		retries:             map[string]int{},
		healthySince:        map[string]time.Time{},
		generations:         map[string]int{},
		logs:                map[string]*logging.WorkerLogSink{},
		hookStatuses:        map[string]map[string]modulehook.Status{},
		metricsTrackers:     map[string]*worker.MetricsTracker{},
		pendingMetrics:      map[string]*pendingMetricsUpdate{},
		metricsStatusSem:    make(chan struct{}, metricsHydrationConcurrencyLimit),
		desiredProbes:       map[probeExecutionKey]probeSpec{},
		manualProbes:        map[probeExecutionKey]probeSpec{},
		inFlightProbes:      map[probeExecutionKey]probeSpec{},
		pendingProbes:       map[probeExecutionKey]probeSpec{},
		probeGenerations:    map[probeExecutionKey]int{},
		probeContext:        probeContext,
		cancelProbes:        cancelProbes,
		readiness:           map[string]readinessObservation{},
		readinessTimers:     map[string]*time.Timer{},
		probeSchedules:      map[poolProbeScheduleKey]poolProbeSchedule{},
		exhaustedPools:      map[string]string{},
		hostedSessions:      NewHostedSessionRegistry(hostedSessionRegistryPath(cfg.Config.Settings.StateDir)),
		hostedSnapshotCache: map[string]HostedSessionSnapshot{},
		batchRegistry:       NewBatchRegistry(BatchRegistryPath(cfg.Config.Settings.StateDir)),
		reconcileTurnHooks:  cfg.ReconcileTurnHooks,
	}
	m.circuits = newCircuitBreaker(func() time.Time { return m.clock() })
	m.probeRunner = runProtocolProbe
	m.metricsStore = newMetricsStore(cfg.Config.Settings, func() time.Time { return m.clock() })
	if err := m.metricsStore.CleanupRetention(); err != nil {
		m.logger.Error(logging.EventMetricsPersist, "operation", "retention_cleanup", "err", err.Error())
	}
	if cfg.ConfigPath != "" {
		m.stopConfigWriter = store.StartAsyncWriter()
	}
	m.portIndex = buildPortIndex(m.config.Workers)
	if err := syncCodexProfileFiles(cfg.Config); err != nil {
		m.configStatus.LastSaveError = err.Error()
	}
	if err := syncGrokConfig(cfg.Config); err != nil {
		m.configStatus.LastSaveError = err.Error()
	}
	if err := syncPiConfig(cfg.Config); err != nil {
		m.configStatus.LastSaveError = err.Error()
	}
	if cfg.ReconcileTurnHooks {
		if err := hostedhooks.Reconcile(cfg.Config.Settings); err != nil {
			m.configStatus.LastSaveError = err.Error()
		}
	}
	return m
}

func hostedSessionRegistryPath(stateDir string) string {
	if stateDir == "" {
		stateDir = "~/.ainn"
	}
	return filepath.Join(expandHomePath(stateDir), hostedSessionsFileName)
}

func (m *Manager) CheckPortAvailable(workerName string, port int) error {
	workers := m.workerConfigSnapshot()
	for name, worker := range workers {
		if name != workerName && worker.Port == port {
			return fmt.Errorf("port :%d is used by worker '%s'", port, name)
		}
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", constants.LocalhostAddr, port))
	if err != nil {
		return fmt.Errorf("port :%d is already in use by another process", port)
	}
	return listener.Close()
}

func (m *Manager) supervisorFor(name string) *WorkerSupervisor {
	if supervisor := m.supervisors[name]; supervisor != nil {
		return supervisor
	}
	supervisor := newWorkerSupervisor(name)
	m.supervisors[name] = supervisor
	return supervisor
}

func (m *Manager) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	m.registerRoutes(mux)
	mux.ServeHTTP(rw, r)
}

func (m *Manager) Close() {
	m.cancelProbes()
	m.probeWait.Wait()
	m.failoverMu.Lock()
	for key, timer := range m.readinessTimers {
		timer.Stop()
		delete(m.readinessTimers, key)
	}
	m.failoverMu.Unlock()
	m.mu.RLock()
	workerNames := make([]string, 0, len(m.processes))
	for workerName := range m.processes {
		workerNames = append(workerNames, workerName)
	}
	m.mu.RUnlock()
	sort.Strings(workerNames)
	for _, workerName := range workerNames {
		if err := m.StopWorker(workerName); err != nil {
			m.logger.Error(logging.EventWorkerExit, "worker", workerName, "status", string(WorkerStateFailed), "err", err.Error())
		}
	}

	m.mu.Lock()
	stopConfigWriter := m.stopConfigWriter
	m.stopConfigWriter = nil
	for workerName, pending := range m.pendingMetrics {
		pending.timer.Stop()
		delete(m.pendingMetrics, workerName)
	}
	events := m.events
	m.events = nil
	m.mu.Unlock()

	if stopConfigWriter != nil {
		stopConfigWriter()
	}
	if events != nil {
		events.Close()
	}
}

func (m *Manager) workerSummaries() []WorkerSummary {
	type summarySeed struct {
		name          string
		worker        config.WorkerConfig
		profile       config.UpstreamProfile
		plugins       map[string]config.PluginDefinition
		providerFound bool
		status        string
		generation    int
	}

	m.mu.RLock()
	names := make([]string, 0, len(m.config.Workers))
	for name := range m.config.Workers {
		names = append(names, name)
	}
	sort.Strings(names)

	seeds := make([]summarySeed, 0, len(names))
	for _, name := range names {
		workerConfig := m.config.Workers[name]
		upstreamID := workerConfig.UpstreamID
		if upstreamID == "" {
			upstreamID = workerConfig.Upstream
		}
		profile, ok := m.config.Upstreams[upstreamID]
		status := string(m.workerStatusLocked(name))
		seeds = append(seeds, summarySeed{
			name:          name,
			worker:        cloneWorkerConfig(workerConfig),
			profile:       profile,
			plugins:       clonePluginDefinitions(m.config.Plugins),
			providerFound: ok,
			status:        status,
			generation:    m.workerGenerationLocked(name),
		})
	}
	m.mu.RUnlock()

	out := make([]WorkerSummary, 0, len(seeds))
	for _, seed := range seeds {
		upstreamID := seed.worker.UpstreamID
		if upstreamID == "" {
			upstreamID = seed.worker.Upstream
		}
		displayName := seed.worker.Name
		if displayName == "" {
			displayName = seed.name
		}
		redactedUpstream := upstream.MissingRedacted(upstreamID)
		runtimeUpstream := upstream.RuntimeUpstream{ID: upstreamID, Name: upstreamID}
		if seed.providerFound {
			runtimeUpstream, _ = upstream.ResolveWithDisplayName(upstreamID, seed.profile.Name, seed.profile)
			redactedUpstream = runtimeUpstream.Redacted()
		}
		summary := WorkerSummary{
			ID:                 seed.name,
			Name:               displayName,
			Port:               seed.worker.Port,
			Role:               seed.worker.Role,
			Launcher:           seed.worker.Launcher,
			UpstreamID:         upstreamID,
			UpstreamPool:       seed.worker.UpstreamPool,
			Upstream:           redactedUpstream,
			ProxyURL:           appruntime.RedactProxyURL(seed.worker.ProxyURL),
			ProxyURLRedacted:   appruntime.ProxyURLRedacted(seed.worker.ProxyURL),
			Protocol:           appruntime.ProtocolKindFromAPIFormat(appruntime.APIFormat(runtimeUpstream.APIFormat)),
			ModuleSupport:      supportForPluginDefinitions(seed.plugins),
			Status:             seed.status,
			SnapshotGeneration: seed.generation,
			LogLevel:           workerLogLevel(seed.worker),
			Modules:            cloneModules(seed.worker.RequestModules),
			Hooks:              cloneModules(seed.worker.Hooks),
		}
		out = append(out, summary)
	}
	return out
}

func (m *Manager) workerDetail(name string, worker config.WorkerConfig) WorkerDetail {
	upstreamID := worker.UpstreamID
	if upstreamID == "" {
		upstreamID = worker.Upstream
	}
	displayName := worker.Name
	if displayName == "" {
		displayName = name
	}
	redactedUpstream := upstream.MissingRedacted(upstreamID)
	runtimeUpstream := upstream.RuntimeUpstream{ID: upstreamID, Name: upstreamID}
	if profile, ok := m.upstreamProfileSnapshot()[upstreamID]; ok {
		runtimeUpstream, _ = upstream.ResolveWithDisplayName(upstreamID, profile.Name, profile)
		redactedUpstream = runtimeUpstream.Redacted()
	}

	detail := WorkerDetail{
		ID:                 name,
		Name:               displayName,
		Port:               worker.Port,
		Role:               worker.Role,
		Launcher:           worker.Launcher,
		UpstreamID:         upstreamID,
		UpstreamPool:       worker.UpstreamPool,
		Upstream:           redactedUpstream,
		ProxyURL:           appruntime.RedactProxyURL(worker.ProxyURL),
		ProxyURLRedacted:   appruntime.ProxyURLRedacted(worker.ProxyURL),
		Protocol:           appruntime.ProtocolKindFromAPIFormat(appruntime.APIFormat(runtimeUpstream.APIFormat)),
		ModuleSupport:      supportForPluginDefinitions(m.pluginDefinitionsSnapshot()),
		Status:             string(m.workerStatus(name)),
		SnapshotGeneration: m.workerGeneration(name),
		LogLevel:           workerLogLevel(worker),
		Modules:            cloneModules(worker.RequestModules),
		Hooks:              cloneModules(worker.Hooks),
		HookStatuses:       m.hookStatusesForWorker(name),
	}

	if detail.Status != string(WorkerStateRunning) {
		return detail
	}

	client := m.workerClient
	if client == nil {
		client = defaultWorkerClient()
	}
	status, err := client.GetStatus(worker.Port)
	if err != nil {
		return detail
	}
	if status.SnapshotGeneration > 0 {
		detail.SnapshotGeneration = status.SnapshotGeneration
	}
	if status.Upstream.Name != "" {
		if status.Upstream.ID == "" {
			status.Upstream.ID = upstreamID
		}
		detail.Upstream = status.Upstream
	}
	detail.ProxyURL = status.ProxyURL
	detail.ProxyURLRedacted = status.ProxyURLRedacted
	if status.Protocol != "" {
		detail.Protocol = status.Protocol
	}
	if status.ModuleSupport != nil {
		overlayModuleSupport(detail.ModuleSupport, status.ModuleSupport)
	}
	if status.Modules != nil {
		detail.Modules = cloneModules(status.Modules)
	}
	if status.Hooks != nil {
		detail.Hooks = cloneModules(status.Hooks)
	}
	if status.HookStatuses != nil {
		detail.HookStatuses = cloneHookStatuses(status.HookStatuses)
	}
	detail.Metrics = status.Metrics
	return detail
}

func supportForPluginDefinitions(plugins map[string]config.PluginDefinition) map[string]appruntime.ModuleProtocolSupport {
	externalRequest := map[string]module.ExternalRequestRuntime{}
	externalHooks := map[string]modulehook.ExternalHookRuntime{}
	for name, definition := range plugins {
		if definition.Source != config.PluginSourceExternal {
			continue
		}
		resolved, err := resolveExternalPlugin(name, definition)
		if err != nil {
			continue
		}
		if definition.Kind == config.PluginKindRequestMiddleware {
			externalRequest[name] = module.ExternalRequestRuntime{ProtocolSupport: resolved.runtime.ProtocolSupport}
		}
		if definition.Kind == config.PluginKindLifecycleHook && !modulehook.IsLifecycleHook(name) {
			externalHooks[name] = modulehook.ExternalHookRuntime{ProtocolSupport: resolved.runtime.ProtocolSupport}
		}
	}
	support := module.RequestMiddlewareSupport(externalRequest)
	for name, declared := range modulehook.Support(externalHooks) {
		support[name] = cloneProtocolSupport(declared)
	}
	return support
}

func overlayModuleSupport(base map[string]appruntime.ModuleProtocolSupport, live map[string]appruntime.ModuleProtocolSupport) {
	for name, declared := range live {
		base[name] = cloneProtocolSupport(declared)
	}
}

func cloneModuleSupport(support map[string]appruntime.ModuleProtocolSupport) map[string]appruntime.ModuleProtocolSupport {
	out := make(map[string]appruntime.ModuleProtocolSupport, len(support))
	for name, declared := range support {
		out[name] = cloneProtocolSupport(declared)
	}
	return out
}

func cloneProtocolSupport(s appruntime.ModuleProtocolSupport) appruntime.ModuleProtocolSupport {
	out := appruntime.ModuleProtocolSupport{}
	if s.Protocols != nil {
		out.Protocols = append([]appruntime.ProtocolKind(nil), s.Protocols...)
	}
	if s.Capabilities != nil {
		out.Capabilities = append([]appruntime.ProtocolCapability(nil), s.Capabilities...)
	}
	return out
}

func workerLogLevel(worker config.WorkerConfig) string {
	if worker.LogLevel == "" {
		return "simple"
	}
	return worker.LogLevel
}

func workerUpstreamID(worker config.WorkerConfig) string {
	if worker.UpstreamID != "" {
		return worker.UpstreamID
	}
	return worker.Upstream
}

func (m *Manager) resolveUpstream(name string) (upstream.RuntimeUpstream, error) {
	m.mu.RLock()
	profile, ok := m.config.Upstreams[name]
	m.mu.RUnlock()
	if !ok {
		return upstream.RuntimeUpstream{ID: name, Name: name}, fmt.Errorf("upstream %q not found", name)
	}
	return upstream.ResolveWithDisplayName(name, profile.Name, profile)
}

func (m *Manager) pluginDefinitionsSnapshot() map[string]config.PluginDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return clonePluginDefinitions(m.config.Plugins)
}

func (m *Manager) runtimeForWorker(name string) (appruntime.WorkerRuntime, error) {
	m.mu.RLock()
	cfg := cloneConfig(m.config)
	generation := appruntime.Generation(m.workerGenerationLocked(name))
	m.mu.RUnlock()
	return (RuntimeBuilder{}).Build(cfg, name, generation)
}

func (m *Manager) validateWorkerRuntime(name string, worker config.WorkerConfig) error {
	m.mu.RLock()
	cfg := cloneConfig(m.config)
	generation := appruntime.Generation(m.workerGenerationLocked(name))
	m.mu.RUnlock()
	cfg.Workers[name] = cloneWorkerConfig(worker)
	_, err := (RuntimeBuilder{}).Build(cfg, name, generation)
	return err
}

func (m *Manager) workerByPort(port int) (string, config.WorkerConfig, bool) {
	m.mu.RLock()
	workerName := m.portIndex[port]
	if workerName == "" {
		m.mu.RUnlock()
		return "", config.WorkerConfig{}, false
	}
	worker, ok := m.config.Workers[workerName]
	m.mu.RUnlock()
	if !ok {
		return "", config.WorkerConfig{}, false
	}
	return workerName, cloneWorkerConfig(worker), true
}

func (m *Manager) workerByRouteKey(key string) (string, config.WorkerConfig, bool) {
	if port, err := strconv.Atoi(key); err == nil {
		return m.workerByPort(port)
	}
	worker, ok := m.workerConfig(key)
	return key, worker, ok
}

func (m *Manager) updateConfig(fn func(*config.Config)) {
	m.store.Update(fn)
	_, status := m.syncConfigFromStore()
	if err := syncCodexProfileFiles(m.config); err != nil {
		m.mu.Lock()
		m.configStatus.LastSaveError = err.Error()
		m.mu.Unlock()
	}
	if err := syncGrokConfig(m.config); err != nil {
		m.mu.Lock()
		m.configStatus.LastSaveError = err.Error()
		m.mu.Unlock()
	}
	if err := syncPiConfig(m.config); err != nil {
		m.mu.Lock()
		m.configStatus.LastSaveError = err.Error()
		m.mu.Unlock()
	}
	m.publishEvent(EventConfigStatusChanged, map[string]any{"dirty": status.Dirty, "generation": status.Generation})
}

func (m *Manager) syncConfigFromStore() (config.Config, config.Status) {
	cfg := cloneConfig(m.store.Config())
	status := m.store.Status()
	m.mu.Lock()
	m.config = cfg
	m.configStatus = status
	m.portIndex = buildPortIndex(cfg.Workers)
	store := m.metricsStore
	m.mu.Unlock()
	store.UpdateSettings(cfg.Settings)
	return cfg, status
}

func (m *Manager) syncConfigStatusFromStore() config.Status {
	status := m.store.Status()
	m.mu.Lock()
	m.configStatus = status
	m.mu.Unlock()
	return status
}

func (m *Manager) publishEvent(eventType EventType, payload map[string]any) {
	m.mu.RLock()
	events := m.events
	m.mu.RUnlock()
	if events != nil {
		events.Publish(eventType, payload)
	}
}

func (m *Manager) workerStatus(name string) WorkerState {
	m.mu.RLock()
	if supervisor := m.supervisors[name]; supervisor != nil {
		status := supervisor.Status()
		if status != WorkerStateConfigured {
			m.mu.RUnlock()
			return status
		}
		if fallback := m.statuses[name]; fallback != "" {
			m.mu.RUnlock()
			return fallback
		}
		m.mu.RUnlock()
		return status
	}
	defer m.mu.RUnlock()
	return m.workerStatusLocked(name)
}

func (m *Manager) configPatchState(name string) string {
	return m.hookStatus(name, modulehook.ConfigPatchName).State
}

func (m *Manager) hookStatus(workerName string, hookName string) modulehook.Status {
	m.mu.RLock()
	if supervisor := m.supervisors[workerName]; supervisor != nil {
		statuses := supervisor.HookStatuses()
		if status := statuses[hookName]; status.State != "" || len(status.Detail) > 0 {
			m.mu.RUnlock()
			return status
		}
	}
	defer m.mu.RUnlock()
	return cloneHookStatus(m.hookStatuses[workerName][hookName])
}

func (m *Manager) hookStatusesForWorker(workerName string) map[string]modulehook.Status {
	m.mu.RLock()
	statuses := cloneHookStatuses(m.hookStatuses[workerName])
	if supervisor := m.supervisors[workerName]; supervisor != nil {
		for name, status := range supervisor.HookStatuses() {
			if statuses == nil {
				statuses = map[string]modulehook.Status{}
			}
			statuses[name] = cloneHookStatus(status)
		}
	}
	m.mu.RUnlock()
	return statuses
}

func cloneHookStatuses(statuses map[string]modulehook.Status) map[string]modulehook.Status {
	if len(statuses) == 0 {
		return nil
	}
	out := make(map[string]modulehook.Status, len(statuses))
	for name, status := range statuses {
		out[name] = cloneHookStatus(status)
	}
	return out
}

func cloneHookStatus(status modulehook.Status) modulehook.Status {
	next := modulehook.Status{State: status.State}
	if len(status.Detail) > 0 {
		next.Detail = make(map[string]string, len(status.Detail))
		for key, value := range status.Detail {
			next.Detail[key] = value
		}
	}
	return next
}

func (m *Manager) configPatchDetail(name string) map[string]string {
	return m.hookStatus(name, modulehook.ConfigPatchName).Detail
}
