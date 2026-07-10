package manager

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
)

func (m *Manager) handleWorkerByPort(rw http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/workers/")
	parts := strings.Split(rest, "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		workerName, worker, ok := m.workerByRouteKey(parts[0])
		if !ok {
			http.NotFound(rw, r)
			return
		}
		writeJSON(rw, http.StatusOK, m.workerDetail(workerName, worker))
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		workerName, _, ok := m.workerByRouteKey(parts[0])
		if !ok {
			http.NotFound(rw, r)
			return
		}
		if err := m.StopWorker(workerName); err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"worker": workerName, "status": string(m.workerStatus(workerName))})
		return
	}
	if len(parts) == 2 && parts[1] == "config" && r.Method == http.MethodDelete {
		workerName, _, ok := m.workerByRouteKey(parts[0])
		if !ok {
			http.NotFound(rw, r)
			return
		}
		if err := m.StopWorker(workerName); err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		m.updateConfig(func(cfgRoot *config.Config) {
			delete(cfgRoot.Workers, workerName)
		})
		writeJSON(rw, http.StatusOK, map[string]any{"worker": workerName})
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPatch {
		workerName, current, ok := m.workerByRouteKey(parts[0])
		if !ok {
			http.NotFound(rw, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		var patch struct {
			Name           string                         `json:"name"`
			Port           int                            `json:"port"`
			Launcher       string                         `json:"launcher"`
			Upstream       string                         `json:"upstream"`
			UpstreamID     string                         `json:"upstream_id"`
			UpstreamPool   string                         `json:"upstream_pool"`
			ProxyURL       string                         `json:"proxy_url"`
			LogLevel       string                         `json:"log_level"`
			RequestModules map[string]config.ModuleConfig `json:"request_modules"`
			Hooks          map[string]config.ModuleConfig `json:"hooks"`
		}
		if err := json.Unmarshal(body, &patch); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		next := current
		if _, ok := fields["name"]; ok {
			next.Name = strings.TrimSpace(patch.Name)
		}
		if next.Name == "" {
			next.Name = workerName
		}
		if _, ok := fields["port"]; ok {
			next.Port = patch.Port
		}
		if _, ok := fields["launcher"]; ok {
			next.Launcher = strings.TrimSpace(patch.Launcher)
		}
		if _, ok := fields["proxy_url"]; ok {
			next.ProxyURL = strings.TrimSpace(patch.ProxyURL)
		}
		if _, ok := fields["log_level"]; ok {
			next.LogLevel = patch.LogLevel
		}
		if _, ok := fields["upstream_pool"]; ok {
			next.UpstreamPool = strings.TrimSpace(patch.UpstreamPool)
		}
		if _, ok := fields["request_modules"]; ok {
			next.RequestModules = patch.RequestModules
		}
		if _, ok := fields["hooks"]; ok {
			next.Hooks = patch.Hooks
		}
		if next.Port <= 0 {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker port is required"})
			return
		}
		next.UpstreamID = strings.TrimSpace(patch.UpstreamID)
		if next.UpstreamID == "" {
			next.UpstreamID = strings.TrimSpace(patch.Upstream)
		}
		if next.UpstreamID == "" {
			next.UpstreamID = workerUpstreamID(current)
		}
		next.Upstream = next.UpstreamID
		if next.UpstreamID == "" {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker provider is required"})
			return
		}
		if next.RequestModules == nil {
			next.RequestModules = map[string]config.ModuleConfig{}
		}
		if next.Hooks == nil {
			next.Hooks = map[string]config.ModuleConfig{}
		}
		if next.LogLevel == "" {
			next.LogLevel = "simple"
		}
		if next.Launcher == "" {
			next.Launcher = current.Launcher
		}
		if !validWorkerLogLevel(next.LogLevel) {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker log_level must be simple or detail"})
			return
		}
		profiles := m.upstreamProfileSnapshot()
		_, upstreamExists := profiles[next.UpstreamID]
		upstreamChanged := workerUpstreamID(current) != next.UpstreamID
		if upstreamChanged || upstreamExists {
			if _, err := m.resolveUpstream(next.UpstreamID); err != nil {
				writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
		}
		if upstreamExists {
			if err := m.validateWorkerRuntime(workerName, next); err != nil {
				writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
		}
		if next.Port != current.Port {
			cfg, _ := m.syncConfigFromStore()
			sessions, err := m.hostedSessions.SummariesForSettings(cfg.Settings)
			if err != nil {
				writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
			for _, session := range sessions {
				sessionWorkerID := session.WorkerID
				if sessionWorkerID == "" {
					sessionWorkerID = session.WorkerName
				}
				if sessionWorkerID == workerName && session.Status == hostedSessionStatusActive {
					writeJSON(rw, http.StatusConflict, map[string]any{"error": fmt.Sprintf("worker port cannot change while active hosted session %q exists", session.SessionLabel)})
					return
				}
			}
			if err := m.CheckPortAvailable(workerName, next.Port); err != nil {
				writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
		}
		if err := m.UpdateWorker(workerName, current, next); err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		for _, summary := range m.workerSummaries() {
			if summary.ID == workerName {
				writeJSON(rw, http.StatusOK, summary)
				return
			}
		}
		writeJSON(rw, http.StatusOK, map[string]any{"worker": workerName, "status": string(m.workerStatus(workerName))})
		return
	}
	if len(parts) == 2 && parts[1] == "restart" && r.Method == http.MethodPost {
		workerName, _, ok := m.workerByRouteKey(parts[0])
		if !ok {
			http.NotFound(rw, r)
			return
		}
		if err := m.RestartWorker(workerName); err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"worker": workerName, "status": string(m.workerStatus(workerName))})
		return
	}
	if len(parts) == 2 && parts[1] == "stream" && r.Method == http.MethodGet {
		workerName, _, ok := m.workerByRouteKey(parts[0])
		if !ok {
			http.NotFound(rw, r)
			return
		}
		m.handleWorkerStream(rw, r, workerName)
		return
	}
	if len(parts) == 2 && parts[1] == "logs" && r.Method == http.MethodGet {
		workerName, _, ok := m.workerByRouteKey(parts[0])
		if !ok {
			http.NotFound(rw, r)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"lines": m.LogSink(workerName).Lines()})
		return
	}
	if len(parts) == 3 && parts[1] == "modules" && r.Method == http.MethodPatch {
		moduleName := parts[2]
		workerName, worker, ok := m.workerByRouteKey(parts[0])
		if !ok {
			http.NotFound(rw, r)
			return
		}
		port := worker.Port
		var cfg config.ModuleConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		if worker.RequestModules == nil {
			worker.RequestModules = map[string]config.ModuleConfig{}
		}
		if worker.Hooks == nil {
			worker.Hooks = map[string]config.ModuleConfig{}
		}
		definition, ok := m.pluginDefinition(moduleName)
		if !ok {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("plugin %q is not defined", moduleName)})
			return
		}
		if definition.Kind == config.PluginKindLifecycleHook {
			if cfg.Enabled {
				if err := m.validateConfigPatchEnablePolicy(workerName, moduleName); err != nil {
					writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
					return
				}
			}
			m.updateConfig(func(cfgRoot *config.Config) {
				worker.Hooks[moduleName] = cfg
				cfgRoot.Workers[workerName] = worker
			})
			if m.workerStatus(workerName) == WorkerStateRunning {
				if err := m.RestartWorker(workerName); err != nil {
					writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
					return
				}
			}
			m.publishEvent(EventModuleUpdated, map[string]any{"worker": workerName, "port": port, "module": moduleName, "enabled": cfg.Enabled, "params": cfg.Params})
			writeJSON(rw, http.StatusOK, map[string]any{
				"worker": workerName,
				"port":   port,
				"module": map[string]any{
					"name":    moduleName,
					"enabled": cfg.Enabled,
					"params":  cfg.Params,
				},
			})
			return
		}
		if definition.Kind != config.PluginKindRequestMiddleware {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("plugin %q has invalid kind %q", moduleName, definition.Kind)})
			return
		}
		if cfg.Enabled {
			if err := m.validateConfigPatchEnablePolicy(workerName, moduleName); err != nil {
				writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
		}
		_, configured := worker.RequestModules[moduleName]
		if configured {
			if err := m.patchLiveWorkerModule(workerName, port, moduleName, cfg); err != nil {
				writeJSON(rw, http.StatusBadGateway, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
		} else {
			nextWorker := worker
			nextWorker.RequestModules[moduleName] = cfg
			if err := m.validateWorkerRuntime(workerName, nextWorker); err != nil {
				writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
		}
		m.updateConfig(func(cfgRoot *config.Config) {
			worker.RequestModules[moduleName] = cfg
			cfgRoot.Workers[workerName] = worker
		})
		if m.workerStatus(workerName) == WorkerStateRunning {
			m.bumpWorkerGeneration(workerName)
			if !configured {
				client := m.workerClient
				if client == nil {
					client = defaultWorkerClient()
				}
				runtime, err := m.runtimeForWorker(workerName)
				if err == nil {
					_, err = client.ApplyRuntime(port, runtime)
				}
				if err != nil {
					m.mu.Lock()
					m.statuses[workerName] = WorkerStateOutOfSync
					m.mu.Unlock()
					m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": workerName, "status": string(WorkerStateOutOfSync), "error": redactedErrorMessage(err)})
					writeJSON(rw, http.StatusBadGateway, map[string]any{"error": redactedErrorMessage(err)})
					return
				}
			}
		}
		m.publishEvent(EventModuleUpdated, map[string]any{"worker": workerName, "port": port, "module": moduleName, "enabled": cfg.Enabled, "params": cfg.Params})
		writeJSON(rw, http.StatusOK, map[string]any{
			"worker": workerName,
			"port":   port,
			"module": map[string]any{
				"name":    moduleName,
				"enabled": cfg.Enabled,
				"params":  cfg.Params,
			},
		})
		return
	}
	if len(parts) != 4 || parts[1] != "modules" || parts[3] != "toggle" || r.Method != http.MethodPost {
		http.NotFound(rw, r)
		return
	}
	moduleName := parts[2]
	workerName, worker, ok := m.workerByRouteKey(parts[0])
	if !ok {
		http.NotFound(rw, r)
		return
	}
	port := worker.Port
	if worker.RequestModules == nil {
		worker.RequestModules = map[string]config.ModuleConfig{}
	}
	if worker.Hooks == nil {
		worker.Hooks = map[string]config.ModuleConfig{}
	}
	definition, ok := m.pluginDefinition(moduleName)
	if !ok {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("plugin %q is not defined", moduleName)})
		return
	}
	if definition.Kind == config.PluginKindLifecycleHook {
		cfg := worker.Hooks[moduleName]
		cfg.Enabled = !cfg.Enabled
		if cfg.Enabled {
			if err := m.validateConfigPatchEnablePolicy(workerName, moduleName); err != nil {
				writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
		}
		m.updateConfig(func(cfgRoot *config.Config) {
			worker.Hooks[moduleName] = cfg
			cfgRoot.Workers[workerName] = worker
		})
		if m.workerStatus(workerName) == WorkerStateRunning {
			if err := m.RestartWorker(workerName); err != nil {
				writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
		}
		m.publishEvent(EventModuleUpdated, map[string]any{"worker": workerName, "port": port, "module": moduleName, "enabled": cfg.Enabled, "params": cfg.Params})
		writeJSON(rw, http.StatusOK, map[string]any{
			"worker": workerName,
			"port":   port,
			"module": map[string]any{
				"name":    moduleName,
				"enabled": cfg.Enabled,
			},
		})
		return
	}
	if definition.Kind != config.PluginKindRequestMiddleware {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("plugin %q has invalid kind %q", moduleName, definition.Kind)})
		return
	}
	cfg := worker.RequestModules[moduleName]
	cfg.Enabled = !cfg.Enabled
	if cfg.Enabled {
		if err := m.validateConfigPatchEnablePolicy(workerName, moduleName); err != nil {
			writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
	}
	if err := m.toggleLiveWorkerModule(workerName, port, moduleName); err != nil {
		writeJSON(rw, http.StatusBadGateway, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	m.updateConfig(func(cfgRoot *config.Config) {
		worker.RequestModules[moduleName] = cfg
		cfgRoot.Workers[workerName] = worker
	})
	if m.workerStatus(workerName) == WorkerStateRunning {
		m.bumpWorkerGeneration(workerName)
	}
	m.publishEvent(EventModuleUpdated, map[string]any{"worker": workerName, "port": port, "module": moduleName, "enabled": cfg.Enabled, "params": cfg.Params})
	writeJSON(rw, http.StatusOK, map[string]any{
		"worker": workerName,
		"port":   port,
		"module": map[string]any{
			"name":    moduleName,
			"enabled": cfg.Enabled,
		},
	})
}
