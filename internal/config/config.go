package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

const (
	DefaultConfigDir = "~/.ainn"
	ConfigFileName   = "config.yaml"
)

const (
	TmuxHostStartModeNewWindow        = "new-window"
	TmuxHostStartModeReuseFirstWindow = "reuse-first-window"
	TmuxHostStartModeMainTUIWindow    = "main-tui-window"
)

type Config struct {
	Settings      Settings                    `yaml:"settings"`
	Plugins       map[string]PluginDefinition `yaml:"plugins" json:"plugins,omitempty"`
	Workers       map[string]WorkerConfig     `yaml:"workers"`
	Upstreams     map[string]UpstreamProfile  `yaml:"upstreams"`
	UpstreamPools map[string]UpstreamPool     `yaml:"upstream_pools"`
}

const (
	DefaultCircuitFailureThreshold         = 3
	DefaultCircuitRecoverySuccessThreshold = 2
	DefaultCircuitRecoveryWaitSeconds      = 60
)

type CircuitBreakerConfig struct {
	FailureThreshold         int `yaml:"failure_threshold" json:"failure_threshold"`
	RecoverySuccessThreshold int `yaml:"recovery_success_threshold" json:"recovery_success_threshold"`
	RecoveryWaitSeconds      int `yaml:"recovery_wait_seconds" json:"recovery_wait_seconds"`
}

type UpstreamPool struct {
	Name           string               `yaml:"name,omitempty" json:"name,omitempty"`
	Upstreams      []string             `yaml:"upstreams" json:"upstreams"`
	CircuitBreaker CircuitBreakerConfig `yaml:"circuit_breaker" json:"circuit_breaker"`
}

type StreamTimeoutConfig struct {
	FirstByteSeconds int `yaml:"first_byte_seconds" json:"first_byte_seconds"`
	IdleSeconds      int `yaml:"idle_seconds" json:"idle_seconds"`
}

type ProtocolProbeConfig struct {
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
}

const (
	PluginKindRequestMiddleware = "request_middleware"
	PluginKindLifecycleHook     = "lifecycle_hook"

	PluginSourceBuiltin  = "builtin"
	PluginSourceExternal = "external"
)

type PluginDefinition struct {
	Kind   string `yaml:"kind" json:"kind"`
	Source string `yaml:"source" json:"source"`
	Path   string `yaml:"path,omitempty" json:"path,omitempty"`
}

type Settings struct {
	StateDir string           `yaml:"state_dir" json:"state_dir"`
	LogDir   string           `yaml:"log_dir" json:"log_dir"`
	LogLevel string           `yaml:"log_level,omitempty" json:"log_level,omitempty"`
	Launch   LaunchSettings   `yaml:"launch" json:"launch"`
	Terminal TerminalSettings `yaml:"terminal" json:"terminal"`
	Metrics  MetricsSettings  `yaml:"metrics" json:"metrics"`
}

type MetricsSettings struct {
	RetentionDays int `yaml:"retention_days" json:"retention_days"`
}

type LaunchSettings struct {
	DefaultMode string `yaml:"default_mode" json:"default_mode"`
}

type TerminalSettings struct {
	Host   string       `yaml:"host" json:"host"`
	Opener string       `yaml:"opener" json:"opener"`
	Tmux   TmuxSettings `yaml:"tmux" json:"tmux"`
}

type TmuxSettings struct {
	SocketName      string `yaml:"socket_name" json:"socket_name"`
	HostSession     string `yaml:"host_session" json:"host_session"`
	HostStartMode   string `yaml:"host_start_mode" json:"host_start_mode"`
	TurnStatusHooks bool   `yaml:"turn_status_hooks" json:"turn_status_hooks"`
	HostedPopupKey  string `yaml:"hosted_popup_key" json:"hosted_popup_key"`
}

type WorkerConfig struct {
	Name           string                  `yaml:"name,omitempty" json:"name,omitempty"`
	Role           string                  `yaml:"role,omitempty" json:"role,omitempty"`
	Launcher       string                  `yaml:"launcher,omitempty" json:"launcher,omitempty"`
	Port           int                     `yaml:"port"`
	Upstream       string                  `yaml:"upstream,omitempty" json:"upstream,omitempty"`
	UpstreamID     string                  `yaml:"upstream_id,omitempty" json:"upstream_id,omitempty"`
	UpstreamPool   string                  `yaml:"upstream_pool,omitempty" json:"upstream_pool,omitempty"`
	ProxyURL       string                  `yaml:"proxy_url,omitempty" json:"proxy_url,omitempty"`
	LogLevel       string                  `yaml:"log_level,omitempty" json:"log_level,omitempty"`
	RequestModules map[string]ModuleConfig `yaml:"request_modules" json:"request_modules,omitempty"`
	Hooks          map[string]ModuleConfig `yaml:"hooks" json:"hooks,omitempty"`
}

type ModuleConfig struct {
	Enabled bool           `yaml:"enabled" json:"enabled"`
	Params  map[string]any `yaml:",inline" json:"params,omitempty"`
}

type UpstreamProfile struct {
	Name           string              `yaml:"name,omitempty" json:"name,omitempty"`
	BaseURL        string              `yaml:"base_url" json:"base_url"`
	APIKey         string              `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	APIFormat      string              `yaml:"api_format,omitempty" json:"api_format,omitempty"`
	StreamTimeouts StreamTimeoutConfig `yaml:"stream_timeouts,omitempty" json:"stream_timeouts,omitempty"`
	ProtocolProbe  ProtocolProbeConfig `yaml:"protocol_probe,omitempty" json:"protocol_probe,omitempty"`
}

func (c *Config) ApplyDefaults() {
	if c.Settings.StateDir == "" {
		c.Settings.StateDir = DefaultConfigDir
	}
	if c.Settings.LogDir == "" {
		c.Settings.LogDir = DefaultConfigDir + "/logs"
	}
	if c.Settings.Launch.DefaultMode == "" {
		c.Settings.Launch.DefaultMode = "hosted-terminal"
	}
	if c.Settings.Terminal.Host == "" {
		c.Settings.Terminal.Host = "tmux"
	}
	if c.Settings.Terminal.Opener == "" {
		c.Settings.Terminal.Opener = "default"
	}
	if c.Settings.Terminal.Tmux.SocketName == "" {
		c.Settings.Terminal.Tmux.SocketName = "ainn"
	}
	if c.Settings.Terminal.Tmux.HostSession == "" {
		c.Settings.Terminal.Tmux.HostSession = "ainn-host"
	}
	if c.Settings.Terminal.Tmux.HostStartMode == "" {
		c.Settings.Terminal.Tmux.HostStartMode = TmuxHostStartModeNewWindow
	}
	if c.Settings.Metrics.RetentionDays == 0 {
		c.Settings.Metrics.RetentionDays = 30
	}
	if c.Workers == nil {
		c.Workers = map[string]WorkerConfig{}
	}
	if c.Plugins == nil {
		c.Plugins = map[string]PluginDefinition{}
	}
	if c.Upstreams == nil {
		c.Upstreams = map[string]UpstreamProfile{}
	}
	if c.UpstreamPools == nil {
		c.UpstreamPools = map[string]UpstreamPool{}
	}
	for name, worker := range c.Workers {
		if worker.Name == "" {
			worker.Name = name
		}
		if worker.Role == "" {
			worker.Role = "cli"
		}
		if worker.Launcher == "" {
			worker.Launcher = "codex"
		}
		if worker.LogLevel == "" {
			worker.LogLevel = "simple"
		}
		if worker.RequestModules == nil {
			worker.RequestModules = map[string]ModuleConfig{}
		}
		if worker.Hooks == nil {
			worker.Hooks = map[string]ModuleConfig{}
		}
		if worker.UpstreamID == "" {
			worker.UpstreamID = worker.Upstream
		}
		if worker.Upstream == "" {
			worker.Upstream = worker.UpstreamID
		}
		c.Workers[name] = worker
	}
	for name, profile := range c.Upstreams {
		if profile.Name == "" {
			profile.Name = name
		}
		c.Upstreams[name] = profile
	}
	for name, pool := range c.UpstreamPools {
		if pool.Name == "" {
			pool.Name = name
		}
		if pool.CircuitBreaker.FailureThreshold == 0 {
			pool.CircuitBreaker.FailureThreshold = DefaultCircuitFailureThreshold
		}
		if pool.CircuitBreaker.RecoverySuccessThreshold == 0 {
			pool.CircuitBreaker.RecoverySuccessThreshold = DefaultCircuitRecoverySuccessThreshold
		}
		if pool.CircuitBreaker.RecoveryWaitSeconds == 0 {
			pool.CircuitBreaker.RecoveryWaitSeconds = DefaultCircuitRecoveryWaitSeconds
		}
		c.UpstreamPools[name] = pool
	}
}

func (c Config) Validate() error {
	workerNames := make([]string, 0, len(c.Workers))
	for name := range c.Workers {
		workerNames = append(workerNames, name)
	}
	sort.Strings(workerNames)
	for _, name := range workerNames {
		poolName := strings.TrimSpace(c.Workers[name].UpstreamPool)
		if poolName == "" {
			continue
		}
		if _, exists := c.UpstreamPools[poolName]; !exists {
			return fmt.Errorf("worker %q upstream pool %q does not exist", name, poolName)
		}
	}
	poolNames := make([]string, 0, len(c.UpstreamPools))
	for name := range c.UpstreamPools {
		poolNames = append(poolNames, name)
	}
	sort.Strings(poolNames)
	for _, name := range poolNames {
		pool := c.UpstreamPools[name]
		if len(pool.Upstreams) == 0 {
			return fmt.Errorf("upstream pool %q requires at least one upstream", name)
		}
		if pool.CircuitBreaker.FailureThreshold < 1 {
			return fmt.Errorf("upstream pool %q failure_threshold must be positive", name)
		}
		if pool.CircuitBreaker.RecoverySuccessThreshold < 1 {
			return fmt.Errorf("upstream pool %q recovery_success_threshold must be positive", name)
		}
		if pool.CircuitBreaker.RecoveryWaitSeconds < 1 {
			return fmt.Errorf("upstream pool %q recovery_wait_seconds must be positive", name)
		}
		members := make(map[string]struct{}, len(pool.Upstreams))
		for _, member := range pool.Upstreams {
			if _, exists := members[member]; exists {
				return fmt.Errorf("upstream pool %q contains duplicate member %q", name, member)
			}
			profile, exists := c.Upstreams[member]
			if !exists {
				return fmt.Errorf("upstream pool %q member %q does not exist", name, member)
			}
			if strings.TrimSpace(profile.ProtocolProbe.Model) == "" {
				return fmt.Errorf("upstream pool %q member %q requires protocol_probe.model", name, member)
			}
			members[member] = struct{}{}
		}
		workerNames := make([]string, 0)
		for workerName, worker := range c.Workers {
			if worker.UpstreamPool == name {
				workerNames = append(workerNames, workerName)
			}
		}
		sort.Strings(workerNames)
		var activeUpstream string
		var proxyURL string
		for index, workerName := range workerNames {
			worker := c.Workers[workerName]
			upstream := strings.TrimSpace(worker.UpstreamID)
			if upstream == "" {
				upstream = strings.TrimSpace(worker.Upstream)
			}
			if _, exists := members[upstream]; !exists {
				return fmt.Errorf("upstream pool %q workers must use one active upstream", name)
			}
			if index == 0 {
				activeUpstream = upstream
				proxyURL = strings.TrimSpace(worker.ProxyURL)
				continue
			}
			if upstream != activeUpstream {
				return fmt.Errorf("upstream pool %q workers must use one active upstream", name)
			}
			if strings.TrimSpace(worker.ProxyURL) != proxyURL {
				return fmt.Errorf("upstream pool %q workers must use one proxy_url", name)
			}
		}
	}
	return nil
}

func defaultDirMode() os.FileMode {
	return 0700
}
