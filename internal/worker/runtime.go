package worker

import (
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/jesse/agent-inn/internal/module"
	"github.com/jesse/agent-inn/internal/modulehook"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
)

type RuntimeConfigSnapshot struct {
	Generation       int
	ProxyURL         string
	HTTPClient       *http.Client
	Upstream         upstream.RuntimeUpstream
	CompiledUpstream upstream.Compiled
	// RequestModuleConfigs keeps raw operator config so upstream changes can rebuild derived defaults.
	RequestModuleConfigs map[string]module.ModuleConfig
	// RequestModuleStates exposes the normalized config produced by middleware construction.
	RequestModuleStates map[string]module.ModuleConfig
	HookConfigs         map[string]module.ModuleConfig
	Plugins             map[string]appruntime.PluginRuntime
	Protocol            appruntime.ProtocolKind
	ModuleSupport       map[string]appruntime.ModuleProtocolSupport
	Modules             []module.Middleware
	HookStatuses        map[string]modulehook.Status
}

func (s RuntimeConfigSnapshot) Validate() error {
	if s.Upstream.BaseURL == "" {
		return fmt.Errorf("upstream base URL is required")
	}
	return nil
}

func (s RuntimeConfigSnapshot) withCompiledUpstream() RuntimeConfigSnapshot {
	if s.CompiledUpstream.BaseURL != nil || s.Upstream.BaseURL == "" {
		return s
	}
	compiled, err := upstream.Compile(appruntime.UpstreamRuntime{
		ID:        appruntime.UpstreamID(s.Upstream.Name),
		BaseURL:   s.Upstream.BaseURL,
		APIKey:    s.Upstream.APIKey,
		APIFormat: appruntime.APIFormat(s.Upstream.APIFormat),
	})
	if err != nil {
		return s
	}
	s.CompiledUpstream = compiled
	return s
}

func snapshotFromRuntime(runtime appruntime.WorkerRuntime) (RuntimeConfigSnapshot, error) {
	compiled, err := upstream.Compile(appruntime.UpstreamRuntime{
		ID:        runtime.Upstream.ID,
		BaseURL:   runtime.Upstream.BaseURL,
		APIKey:    runtime.Upstream.APIKey,
		APIFormat: runtime.Upstream.APIFormat,
	})
	if err != nil {
		return RuntimeConfigSnapshot{}, err
	}
	moduleConfigs := make(map[string]module.ModuleConfig, len(runtime.Modules))
	for name, cfg := range runtime.Modules {
		moduleConfigs[name] = module.ModuleConfig{
			Enabled: cfg.Enabled,
			Params:  cloneRuntimeParams(cfg.Params),
		}
	}
	modules, requestStates, support, err := buildRuntimeModules(moduleConfigs, runtime.Plugins, runtime.Upstream.APIFormat)
	if err != nil {
		return RuntimeConfigSnapshot{}, err
	}
	hookConfigs := make(map[string]module.ModuleConfig, len(runtime.Hooks))
	for name, cfg := range runtime.Hooks {
		if !modulehook.IsLifecycleHook(name) {
			plugin := runtime.Plugins[name]
			if plugin.Source != "external" || plugin.Kind != "lifecycle_hook" {
				return RuntimeConfigSnapshot{}, fmt.Errorf("unknown lifecycle hook %q", name)
			}
		}
		hookConfigs[name] = module.ModuleConfig{
			Enabled: cfg.Enabled,
			Params:  cloneRuntimeParams(cfg.Params),
		}
	}
	externalHooks := map[string]modulehook.ExternalHookRuntime{}
	for name, plugin := range runtime.Plugins {
		if plugin.Source == "external" && plugin.Kind == "lifecycle_hook" {
			externalHooks[name] = modulehook.ExternalHookRuntime{
				Command:         plugin.Command,
				Args:            append([]string(nil), plugin.Args...),
				ProtocolVersion: plugin.ProtocolVersion,
				ProtocolSupport: plugin.ProtocolSupport,
			}
		}
	}
	for name, hookSupport := range modulehook.Support(externalHooks) {
		support[name] = hookSupport
	}
	snapshot := RuntimeConfigSnapshot{
		Generation: int(runtime.Generation),
		ProxyURL:   runtime.ProxyURL,
		Upstream: upstream.RuntimeUpstream{
			Name:      string(runtime.Upstream.ID),
			BaseURL:   runtime.Upstream.BaseURL,
			APIKey:    runtime.Upstream.APIKey,
			APIFormat: string(runtime.Upstream.APIFormat),
		},
		CompiledUpstream:     compiled,
		RequestModuleConfigs: moduleConfigs,
		RequestModuleStates:  requestStates,
		HookConfigs:          hookConfigs,
		Plugins:              clonePluginRuntimes(runtime.Plugins),
		Protocol:             appruntime.ProtocolKindFromAPIFormat(runtime.Upstream.APIFormat),
		ModuleSupport:        support,
		Modules:              modules,
	}
	if snapshot.Generation == 0 {
		snapshot.Generation = 1
	}
	if err := snapshot.Validate(); err != nil {
		return RuntimeConfigSnapshot{}, err
	}
	return snapshot, nil
}

func (s RuntimeConfigSnapshot) withHTTPClient(defaultClient *http.Client) (RuntimeConfigSnapshot, error) {
	if s.ProxyURL == "" {
		if s.HTTPClient == nil {
			if defaultClient != nil {
				s.HTTPClient = defaultClient
			} else {
				s.HTTPClient = http.DefaultClient
			}
		}
		return s, nil
	}
	proxyURL, err := appruntime.ParseProxyURL(s.ProxyURL)
	if err != nil {
		return RuntimeConfigSnapshot{}, err
	}
	baseClient := defaultClient
	if baseClient == nil {
		baseClient = http.DefaultClient
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	client := *baseClient
	client.Transport = transport
	s.HTTPClient = &client
	return s, nil
}

type snapshotHolder struct {
	value atomic.Value
}

func newSnapshotHolder(snapshot RuntimeConfigSnapshot) *snapshotHolder {
	holder := &snapshotHolder{}
	holder.value.Store(snapshot)
	return holder
}

func (h *snapshotHolder) Load() RuntimeConfigSnapshot {
	return h.value.Load().(RuntimeConfigSnapshot)
}

func (h *snapshotHolder) Store(snapshot RuntimeConfigSnapshot) {
	h.value.Store(snapshot)
}

func (s RuntimeConfigSnapshot) requestModules() map[string]module.ModuleConfig {
	if s.RequestModuleConfigs != nil {
		return module.CloneModuleConfigs(s.RequestModuleConfigs)
	}
	return moduleStates(s.Modules)
}

func (s RuntimeConfigSnapshot) requestModuleStates() map[string]module.ModuleConfig {
	if s.RequestModuleStates != nil {
		return module.CloneModuleConfigs(s.RequestModuleStates)
	}
	return moduleStates(s.Modules)
}

func (s RuntimeConfigSnapshot) hookModules() map[string]module.ModuleConfig {
	return module.CloneModuleConfigs(s.HookConfigs)
}

func (s RuntimeConfigSnapshot) withRequestModuleConfigs(configs map[string]module.ModuleConfig) (RuntimeConfigSnapshot, error) {
	modules, requestStates, support, err := buildRuntimeModules(configs, s.Plugins, appruntime.APIFormat(s.Upstream.APIFormat))
	if err != nil {
		return RuntimeConfigSnapshot{}, err
	}
	externalHooks := map[string]modulehook.ExternalHookRuntime{}
	for name, plugin := range s.Plugins {
		if plugin.Source == "external" && plugin.Kind == "lifecycle_hook" {
			externalHooks[name] = modulehook.ExternalHookRuntime{
				Command:         plugin.Command,
				Args:            append([]string(nil), plugin.Args...),
				ProtocolVersion: plugin.ProtocolVersion,
				ProtocolSupport: plugin.ProtocolSupport,
			}
		}
	}
	for name, hookSupport := range modulehook.Support(externalHooks) {
		support[name] = hookSupport
	}
	next := s
	next.RequestModuleConfigs = module.CloneModuleConfigs(configs)
	next.RequestModuleStates = requestStates
	next.Protocol = appruntime.ProtocolKindFromAPIFormat(appruntime.APIFormat(s.Upstream.APIFormat))
	next.ModuleSupport = support
	next.Modules = modules
	return next, nil
}

func (s RuntimeConfigSnapshot) withUpstream(runtimeUpstream upstream.RuntimeUpstream) (RuntimeConfigSnapshot, error) {
	compiled, err := upstream.Compile(appruntime.UpstreamRuntime{
		ID:        appruntime.UpstreamID(runtimeUpstream.Name),
		BaseURL:   runtimeUpstream.BaseURL,
		APIKey:    runtimeUpstream.APIKey,
		APIFormat: appruntime.APIFormat(runtimeUpstream.APIFormat),
	})
	if err != nil {
		return RuntimeConfigSnapshot{}, err
	}
	next := s
	next.Upstream = runtimeUpstream
	next.CompiledUpstream = compiled
	return next.withRequestModuleConfigs(s.requestModules())
}

func clonePluginRuntimes(plugins map[string]appruntime.PluginRuntime) map[string]appruntime.PluginRuntime {
	out := make(map[string]appruntime.PluginRuntime, len(plugins))
	for name, plugin := range plugins {
		out[name] = plugin
	}
	return out
}
