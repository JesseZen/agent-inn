package config

import "os"

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
	Settings  Settings                    `yaml:"settings"`
	Plugins   map[string]PluginDefinition `yaml:"plugins" json:"plugins,omitempty"`
	Workers   map[string]WorkerConfig     `yaml:"workers"`
	Upstreams map[string]UpstreamProfile  `yaml:"upstreams"`
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
}

type WorkerConfig struct {
	Role           string                  `yaml:"role,omitempty" json:"role,omitempty"`
	Launcher       string                  `yaml:"launcher,omitempty" json:"launcher,omitempty"`
	Port           int                     `yaml:"port"`
	Upstream       string                  `yaml:"upstream"`
	LogLevel       string                  `yaml:"log_level,omitempty" json:"log_level,omitempty"`
	RequestModules map[string]ModuleConfig `yaml:"request_modules" json:"request_modules,omitempty"`
	Hooks          map[string]ModuleConfig `yaml:"hooks" json:"hooks,omitempty"`
}

type ModuleConfig struct {
	Enabled bool           `yaml:"enabled" json:"enabled"`
	Params  map[string]any `yaml:",inline" json:"params,omitempty"`
}

type UpstreamProfile struct {
	BaseURL   string `yaml:"base_url" json:"base_url"`
	APIKey    string `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	APIFormat string `yaml:"api_format,omitempty" json:"api_format,omitempty"`
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
	if c.Workers == nil {
		c.Workers = map[string]WorkerConfig{}
	}
	if c.Plugins == nil {
		c.Plugins = map[string]PluginDefinition{}
	}
	if c.Upstreams == nil {
		c.Upstreams = map[string]UpstreamProfile{}
	}
	for name, worker := range c.Workers {
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
		c.Workers[name] = worker
	}
}

func defaultDirMode() os.FileMode {
	return 0700
}
