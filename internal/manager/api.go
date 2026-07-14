package manager

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/modulehook"
)

func (m *Manager) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/events", m.handleEvents)
	mux.HandleFunc("/api/metrics", m.handleMetrics)
	mux.HandleFunc("/api/workers", m.handleWorkers)
	mux.HandleFunc("/api/workers/", m.handleWorkerByPort)
	mux.HandleFunc("/api/hosted-sessions", m.handleHostedSessions)
	mux.HandleFunc("/api/hosted-sessions/", m.handleHostedSessionByID)
	mux.HandleFunc("/api/batches", m.handleBatches)
	mux.HandleFunc("/api/batches/", m.handleBatchByID)
	mux.HandleFunc("/api/upstreams", m.handleUpstreams)
	mux.HandleFunc("/api/upstreams/", m.handleUpstreamByName)
	mux.HandleFunc("/api/upstream-pools", m.handleUpstreamPools)
	mux.HandleFunc("/api/upstream-pools/", m.handleUpstreamPoolByName)
	mux.HandleFunc("/api/settings", m.handleSettings)
	mux.HandleFunc("/api/config", m.handleConfig)
}

func (m *Manager) handleMetrics(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(rw, r)
		return
	}
	status, err := metricsStatusFromQuery(r.URL.Query().Get("status"))
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	query := MetricsQuery{
		Range:    MetricsRangeName(r.URL.Query().Get("range")),
		Worker:   r.URL.Query().Get("worker"),
		Upstream: r.URL.Query().Get("upstream"),
		Model:    r.URL.Query().Get("model"),
		Path:     r.URL.Query().Get("path"),
		Status:   status,
	}
	m.mu.RLock()
	store := m.metricsStore
	client := m.workerClient
	hydrationSem := m.metricsStatusSem
	m.mu.RUnlock()
	summaries := m.workerSummaries()
	liveAvailable := make([]bool, len(summaries))
	if query.Upstream == "" && query.Model == "" && query.Path == "" && query.Status == 0 {
		if client == nil {
			client = defaultWorkerClient()
		}
		indices := make([]int, 0, len(summaries))
		for i := range summaries {
			if summaries[i].Status == string(WorkerStateRunning) && (query.Worker == "" || summaries[i].Name == query.Worker) {
				indices = append(indices, i)
			}
		}
		jobs := make(chan int, len(indices))
		for _, index := range indices {
			jobs <- index
		}
		close(jobs)
		workerCount := min(len(indices), metricsHydrationConcurrencyLimit)
		var wg sync.WaitGroup
		wg.Add(workerCount)
		for i := 0; i < workerCount; i++ {
			go func() {
				defer wg.Done()
				for index := range jobs {
					hydrationSem <- struct{}{}
					status, err := client.GetStatus(summaries[index].Port)
					<-hydrationSem
					if err == nil {
						summaries[index].Metrics = status.Metrics
						liveAvailable[index] = true
					}
				}
			}()
		}
		wg.Wait()
	}
	response, err := store.Query(query, summaries)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	liveAvailableByWorker := make(map[string]bool, len(summaries))
	for i, summary := range summaries {
		liveAvailableByWorker[summary.Name] = liveAvailable[i]
	}
	for i := range response.Workers {
		response.Workers[i].LiveAvailable = liveAvailableByWorker[response.Workers[i].Worker]
	}
	writeJSON(rw, http.StatusOK, response)
}

func (m *Manager) handleWorkers(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(rw, http.StatusOK, map[string]any{"workers": m.workerSummaries()})
		return
	}
	if r.Method == http.MethodPost {
		m.handleCreateWorker(rw, r)
		return
	}
	http.NotFound(rw, r)
}

func (m *Manager) handleCreateWorker(rw http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name           string                         `json:"name"`
		Port           int                            `json:"port"`
		Launcher       string                         `json:"launcher"`
		Upstream       string                         `json:"upstream"`
		UpstreamPool   string                         `json:"upstream_pool"`
		ProxyURL       string                         `json:"proxy_url"`
		RequestModules map[string]config.ModuleConfig `json:"request_modules"`
		Hooks          map[string]config.ModuleConfig `json:"hooks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	payload.Name = strings.TrimSpace(payload.Name)
	payload.Upstream = strings.TrimSpace(payload.Upstream)
	if payload.Name == "" || strings.Contains(payload.Name, "/") {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker name is required"})
		return
	}
	if payload.Port < 0 {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker port is required"})
		return
	}
	if payload.Upstream == "" {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker provider is required"})
		return
	}
	if _, err := m.resolveUpstream(payload.Upstream); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	if _, ok := m.workerConfig(payload.Name); ok {
		writeJSON(rw, http.StatusConflict, map[string]any{"error": "worker already exists"})
		return
	}
	if payload.Port == 0 {
		listener, err := net.Listen("tcp", constants.LocalhostAddr+":0")
		if err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		payload.Port = listener.Addr().(*net.TCPAddr).Port
		if err := listener.Close(); err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
	}
	if err := m.CheckPortAvailable(payload.Name, payload.Port); err != nil {
		writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	worker := config.WorkerConfig{
		Launcher:       strings.TrimSpace(payload.Launcher),
		Port:           payload.Port,
		Upstream:       payload.Upstream,
		UpstreamPool:   strings.TrimSpace(payload.UpstreamPool),
		ProxyURL:       strings.TrimSpace(payload.ProxyURL),
		RequestModules: payload.RequestModules,
		Hooks:          payload.Hooks,
	}
	if worker.RequestModules == nil {
		worker.RequestModules = map[string]config.ModuleConfig{}
	}
	if worker.Hooks == nil {
		worker.Hooks = map[string]config.ModuleConfig{}
	}
	pooled := worker.UpstreamPool != ""
	oldProxyURL := ""
	attachmentsBefore := 0
	if pooled {
		m.failoverMu.Lock()
		if err := m.validatePoolAttachmentLocked(payload.Name, worker); err != nil {
			m.failoverMu.Unlock()
			writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		if err := m.validateWorkerRuntime(payload.Name, worker); err != nil {
			m.failoverMu.Unlock()
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		oldProxyURL = m.poolProxyURL(worker.UpstreamPool)
		m.mu.RLock()
		for _, configured := range m.config.Workers {
			if configured.UpstreamPool == worker.UpstreamPool {
				attachmentsBefore++
			}
		}
		m.mu.RUnlock()
	} else {
		if err := m.validateWorkerRuntime(payload.Name, worker); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
	}
	m.updateConfig(func(cfgRoot *config.Config) { cfgRoot.Workers[payload.Name] = worker })
	if err := m.StartWorker(payload.Name); err != nil {
		m.updateConfig(func(cfgRoot *config.Config) {
			delete(cfgRoot.Workers, payload.Name)
		})
		if pooled {
			m.failoverMu.Unlock()
		}
		writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	if pooled {
		m.mu.RLock()
		pool := m.config.UpstreamPools[worker.UpstreamPool]
		m.mu.RUnlock()
		for _, upstreamName := range pool.Upstreams {
			m.invalidatePoolReadinessLocked(worker.UpstreamPool, upstreamName)
		}
		m.updatePoolAttachmentAuthorityLocked(worker.UpstreamPool, attachmentsBefore, attachmentsBefore+1)
		if oldProxyURL != m.poolProxyURL(worker.UpstreamPool) {
			m.resetPoolIdentityLocked(worker.UpstreamPool)
		}
		m.failoverMu.Unlock()
		m.probeAllUpstreams(m.probeContext)
	}
	for _, summary := range m.workerSummaries() {
		if summary.Name == payload.Name {
			writeJSON(rw, http.StatusCreated, summary)
			return
		}
	}
	writeJSON(rw, http.StatusCreated, map[string]any{"name": payload.Name, "port": payload.Port, "status": string(m.workerStatus(payload.Name))})
}

func (m *Manager) validateConfigPatchEnablePolicy(workerName string, moduleName string) error {
	if moduleName != modulehook.ConfigPatchName {
		return nil
	}
	if err := m.validateConfigPatchRecoveryState(workerName); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for otherName, worker := range m.config.Workers {
		if otherName == workerName || m.workerStatusLocked(otherName) != WorkerStateRunning {
			continue
		}
		if worker.Hooks[modulehook.ConfigPatchName].Enabled {
			return configPatchAlreadyActiveError{}
		}
	}
	return nil
}

func (m *Manager) pluginDefinition(name string) (config.PluginDefinition, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	definition, ok := m.config.Plugins[name]
	return definition, ok
}

func (m *Manager) validateConfigPatchRecoveryState(workerName string) error {
	switch state := m.configPatchState(workerName); state {
	case modulehook.ConfigPatchUnresolved, modulehook.ConfigPatchFailed:
		return configPatchRecoveryStateError{state: state}
	}
	worker, ok := m.workerConfig(workerName)
	if !ok || m.workerStatus(workerName) != WorkerStateRunning {
		return nil
	}
	client := m.workerClient
	if client == nil {
		client = defaultWorkerClient()
	}
	status, err := client.GetStatus(worker.Port)
	if err != nil {
		return nil
	}
	switch state := status.HookStatuses[modulehook.ConfigPatchName].State; state {
	case modulehook.ConfigPatchUnresolved, modulehook.ConfigPatchFailed:
		return configPatchRecoveryStateError{state: state}
	default:
		return nil
	}
}

type configPatchAlreadyActiveError struct{}

func (configPatchAlreadyActiveError) Error() string {
	return "config_patch already active on another worker"
}

type configPatchRecoveryStateError struct {
	state string
}

func (e configPatchRecoveryStateError) Error() string {
	return "config_patch recovery state " + e.state + " must be resolved before enabling"
}

func validWorkerLogLevel(level string) bool {
	return level == "simple" || level == "detail"
}

func (m *Manager) patchLiveWorkerModule(workerName string, port int, moduleName string, cfg config.ModuleConfig) error {
	if m.workerStatus(workerName) != WorkerStateRunning {
		return nil
	}
	client := m.workerClient
	if client == nil {
		client = defaultWorkerClient()
	}
	return client.PatchModule(port, moduleName, cfg)
}

func (m *Manager) toggleLiveWorkerModule(workerName string, port int, moduleName string) error {
	if m.workerStatus(workerName) != WorkerStateRunning {
		return nil
	}
	client := m.workerClient
	if client == nil {
		client = defaultWorkerClient()
	}
	return client.ToggleModule(port, moduleName)
}

func (m *Manager) applyRuntimeToLiveWorkersUsingUpstream(upstreamName string) []string {
	client := m.workerClient
	if client == nil {
		client = defaultWorkerClient()
	}
	failures := []string{}
	for _, target := range m.liveWorkersUsingUpstream(upstreamName) {
		runtime, err := m.runtimeForWorker(target.name)
		if err == nil {
			_, err = client.ApplyRuntime(target.port, runtime)
		}
		if err != nil {
			m.mu.Lock()
			m.statuses[target.name] = WorkerStateOutOfSync
			m.mu.Unlock()
			failures = append(failures, target.name+": "+redactedErrorMessage(err))
			m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": target.name, "status": string(WorkerStateOutOfSync), "error": redactedErrorMessage(err)})
			continue
		}
		m.mu.Lock()
		if m.workerStatusLocked(target.name) != WorkerStateStopped {
			m.statuses[target.name] = WorkerStateRunning
		}
		m.mu.Unlock()
	}
	return failures
}

func (m *Manager) bumpLiveWorkersUsingUpstream(upstreamName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for workerName, worker := range m.config.Workers {
		if workerUpstreamID(worker) == upstreamName && m.workerStatusLocked(workerName) == WorkerStateRunning {
			next := m.workerGenerationLocked(workerName) + 1
			m.generations[workerName] = next
			m.supervisorFor(workerName).setAppliedGeneration(next)
		}
	}
}

func (m *Manager) handleConfig(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		if m.configuredConfigPath() == "" {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "config path is required"})
			return
		}
		if err := m.store.Save(); err != nil {
			status := m.syncConfigStatusFromStore()
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err), "status": status})
			return
		}
		_, status := m.syncConfigFromStore()
		writeJSON(rw, http.StatusOK, map[string]any{"status": status})
		return
	}
	if r.Method != http.MethodGet {
		http.NotFound(rw, r)
		return
	}
	cfg, status := m.syncConfigFromStore()
	writeJSON(rw, http.StatusOK, map[string]any{
		"config": sanitizeConfig(cfg),
		"status": map[string]any{
			"generation":      status.Generation,
			"dirty":           status.Dirty,
			"last_save_error": status.LastSaveError,
		},
	})
}

func sanitizeConfig(cfg configLike) any {
	return cfg
}

type configLike interface{}

func writeJSON(rw http.ResponseWriter, status int, value any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	encoder := json.NewEncoder(rw)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func redactedErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(err.Error(), "\n", " ")
}
