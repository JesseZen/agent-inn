package config

import "os"

const (
	DefaultConfigDir = "~/.codex-proxy"
	ConfigFileName   = "config.yaml"
)

type Config struct {
	Settings  Settings                   `yaml:"settings"`
	Workers   map[string]WorkerConfig    `yaml:"workers"`
	Upstreams map[string]UpstreamProfile `yaml:"upstreams"`
}

type Settings struct {
	StateDir string           `yaml:"state_dir" json:"state_dir"`
	LogDir   string           `yaml:"log_dir" json:"log_dir"`
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
	SocketName  string `yaml:"socket_name" json:"socket_name"`
	HostSession string `yaml:"host_session" json:"host_session"`
}

type WorkerConfig struct {
	Role     string                  `yaml:"role,omitempty" json:"role,omitempty"`
	Port     int                     `yaml:"port"`
	Upstream string                  `yaml:"upstream"`
	LogLevel string                  `yaml:"log_level,omitempty" json:"log_level,omitempty"`
	Modules  map[string]ModuleConfig `yaml:"modules"`
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
		c.Settings.Terminal.Opener = "terminal_app"
	}
	if c.Settings.Terminal.Tmux.SocketName == "" {
		c.Settings.Terminal.Tmux.SocketName = "cap"
	}
	if c.Settings.Terminal.Tmux.HostSession == "" {
		c.Settings.Terminal.Tmux.HostSession = "cap-host"
	}
	if c.Workers == nil {
		c.Workers = map[string]WorkerConfig{}
	}
	if c.Upstreams == nil {
		c.Upstreams = map[string]UpstreamProfile{}
	}
	for name, worker := range c.Workers {
		if worker.Role == "" {
			worker.Role = "cli"
		}
		if worker.LogLevel == "" {
			worker.LogLevel = "simple"
		}
		if worker.Modules == nil {
			worker.Modules = map[string]ModuleConfig{}
		}
		c.Workers[name] = worker
	}
}

func defaultDirMode() os.FileMode {
	return 0700
}
