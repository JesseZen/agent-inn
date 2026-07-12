package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLoadAppliesDefaultsAndKeepsSecretRefs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(`
settings:
  state_dir: ~/.ainn-state
  log_dir: ~/.ainn-logs
  launch:
    default_mode: hosted-terminal
  terminal:
    host: tmux
    opener: default
    tmux:
      socket_name: ainn-test
      host_session: ainn-test-host
      host_start_mode: reuse-first-window
      turn_status_hooks: true
      hosted_popup_key: H
plugins:
  tool_filter:
    kind: request_middleware
    source: external
    path: plugins/request/tool_filter/plugin.yaml
workers:
  codex-app:
    port: 6767
    upstream: openai
    request_modules:
      tool_filter:
        enabled: true
upstreams:
  openai:
    base_url: https://api.openai.com/v1
    api_key: plain-key
`), 0600)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Settings.StateDir != "~/.ainn-state" || cfg.Settings.LogDir != "~/.ainn-logs" {
		t.Fatalf("expected settings paths to load, got %#v", cfg.Settings)
	}
	if cfg.Settings.Launch.DefaultMode != "hosted-terminal" {
		t.Fatalf("expected launch default mode to load, got %#v", cfg.Settings.Launch)
	}
	if cfg.Settings.Terminal.Host != "tmux" || cfg.Settings.Terminal.Opener != "default" {
		t.Fatalf("expected terminal settings to load, got %#v", cfg.Settings.Terminal)
	}
	if cfg.Settings.Terminal.Tmux.SocketName != "ainn-test" || cfg.Settings.Terminal.Tmux.HostSession != "ainn-test-host" {
		t.Fatalf("expected tmux settings to load, got %#v", cfg.Settings.Terminal.Tmux)
	}
	if cfg.Settings.Terminal.Tmux.HostStartMode != "reuse-first-window" {
		t.Fatalf("expected host start mode to load, got %#v", cfg.Settings.Terminal.Tmux)
	}
	if !cfg.Settings.Terminal.Tmux.TurnStatusHooks {
		t.Fatalf("expected turn status hooks to load, got %#v", cfg.Settings.Terminal.Tmux)
	}
	if cfg.Settings.Terminal.Tmux.HostedPopupKey != "H" {
		t.Fatalf("expected hosted popup key to load, got %#v", cfg.Settings.Terminal.Tmux)
	}
	if cfg.Upstreams["openai"].APIKey != "plain-key" {
		t.Fatalf("expected plain api key to load, got %#v", cfg.Upstreams["openai"])
	}
	if !cfg.Workers["codex-app"].RequestModules["tool_filter"].Enabled {
		t.Fatal("expected module enabled")
	}
	if cfg.Workers["codex-app"].Role != "cli" {
		t.Fatalf("expected default cli role, got %q", cfg.Workers["codex-app"].Role)
	}
	if cfg.Workers["codex-app"].Launcher != "codex" {
		t.Fatalf("expected worker launcher defaults, got %#v", cfg.Workers["codex-app"])
	}
}

func TestLoadFileDecodesUpstreamPoolRouting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
workers:
  codex:
    port: 6767
    upstream: primary
    upstream_pool: coding-ha
upstream_pools:
  coding-ha:
    upstreams:
      - primary
      - backup
    circuit_breaker:
      failure_threshold: 5
      recovery_success_threshold: 3
      recovery_wait_seconds: 90
upstreams:
  primary:
    base_url: https://primary.example/v1
    api_key: sk-primary
    stream_timeouts:
      first_byte_seconds: 75
      idle_seconds: 180
    protocol_probe:
      model: gpt-5-mini
  backup:
    base_url: https://backup.example/v1
    protocol_probe:
      model: gpt-5-mini-backup
`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	got := struct {
		Pool     UpstreamPool
		Worker   WorkerConfig
		Upstream UpstreamProfile
	}{
		Pool:     cfg.UpstreamPools["coding-ha"],
		Worker:   cfg.Workers["codex"],
		Upstream: cfg.Upstreams["primary"],
	}
	want := struct {
		Pool     UpstreamPool
		Worker   WorkerConfig
		Upstream UpstreamProfile
	}{
		Pool: UpstreamPool{
			Name:      "coding-ha",
			Upstreams: []string{"primary", "backup"},
			CircuitBreaker: CircuitBreakerConfig{
				FailureThreshold:         5,
				RecoverySuccessThreshold: 3,
				RecoveryWaitSeconds:      90,
			},
		},
		Worker: WorkerConfig{
			Name:           "codex",
			Role:           "cli",
			Launcher:       "codex",
			Port:           6767,
			Upstream:       "primary",
			UpstreamID:     "primary",
			UpstreamPool:   "coding-ha",
			LogLevel:       "simple",
			RequestModules: map[string]ModuleConfig{},
			Hooks:          map[string]ModuleConfig{},
		},
		Upstream: UpstreamProfile{
			Name:           "primary",
			BaseURL:        "https://primary.example/v1",
			APIKey:         "sk-primary",
			StreamTimeouts: StreamTimeoutConfig{FirstByteSeconds: 75, IdleSeconds: 180},
			ProtocolProbe:  ProtocolProbeConfig{Model: "gpt-5-mini"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected pool routing config:\n got %#v\nwant %#v", got, want)
	}
}

func TestLoadAppliesSettingsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
workers:
  app:
    port: 6767
    upstream: openai
upstreams:
  openai:
    base_url: https://api.openai.com/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	want := Settings{
		StateDir: "~/.ainn",
		LogDir:   "~/.ainn/logs",
		Launch: LaunchSettings{
			DefaultMode: "hosted-terminal",
		},
		Terminal: TerminalSettings{
			Host:   "tmux",
			Opener: "default",
			Tmux: TmuxSettings{
				SocketName:     "ainn",
				HostSession:    "ainn-host",
				HostStartMode:  "new-window",
				HostedPopupKey: "",
			},
		},
		Metrics: MetricsSettings{
			RetentionDays: 30,
		},
	}
	if !reflect.DeepEqual(cfg.Settings, want) {
		t.Fatalf("unexpected settings defaults:\n got %#v\nwant %#v", cfg.Settings, want)
	}
}

func TestMetricsSettingsExposeOnlyRetention(t *testing.T) {
	settings := MetricsSettings{RetentionDays: 14}

	jsonData, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var gotJSON map[string]any
	if err := json.Unmarshal(jsonData, &gotJSON); err != nil {
		t.Fatal(err)
	}
	wantJSON := map[string]any{"retention_days": float64(14)}
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("unexpected metrics JSON:\n got %#v\nwant %#v", gotJSON, wantJSON)
	}

	yamlData, err := yaml.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var gotYAML map[string]any
	if err := yaml.Unmarshal(yamlData, &gotYAML); err != nil {
		t.Fatal(err)
	}
	wantYAML := map[string]any{"retention_days": 14}
	if !reflect.DeepEqual(gotYAML, wantYAML) {
		t.Fatalf("unexpected metrics YAML:\n got %#v\nwant %#v", gotYAML, wantYAML)
	}
}

func TestLoadFileRemovesStaleConfigTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
workers:
  app:
    port: 6767
    upstream: openai
upstreams:
  openai:
    base_url: https://api.openai.com/v1
`), 0600); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, ".config.yaml.tmp.stale")
	if err := os.WriteFile(stale, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadFile(path); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale temp file removed, got %v", err)
	}
}

func TestAtomicSaveLeavesValidYAMLAndTracksGeneration(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "config.yaml"), Config{
		Workers: map[string]WorkerConfig{
			"one": {Port: 6767, Upstream: "openai"},
		},
		Upstreams: map[string]UpstreamProfile{
			"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "plain-key"},
		},
	})

	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	if store.Status().Generation != 1 || store.Status().Dirty {
		t.Fatalf("unexpected status: %#v", store.Status())
	}

	loaded, err := LoadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Workers["one"].Port != 6767 {
		t.Fatalf("unexpected worker: %#v", loaded.Workers["one"])
	}
}

func TestAtomicWriteBeforeRenameKeepsPreviousConfigValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
workers:
  app:
    port: 6767
    upstream: openai
upstreams:
  openai:
    base_url: https://old.example/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	restore := setAtomicWriteHooksForTest(nil, func(string, string) error {
		return errors.New("rename failed")
	}, nil)
	defer restore()

	err := atomicWriteFile(path, []byte(`
workers:
  app:
    port: 6767
    upstream: openai
upstreams:
  openai:
    base_url: https://new.example/v1
`), 0600)
	if err == nil {
		t.Fatal("expected atomic write to fail before rename")
	}

	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Upstreams["openai"].BaseURL != "https://old.example/v1" {
		t.Fatalf("expected previous config to remain valid, got %#v", loaded.Upstreams["openai"])
	}
}

func TestAtomicWriteAfterRenameStillLeavesLoadableConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
workers:
  app:
    port: 6767
    upstream: openai
upstreams:
  openai:
    base_url: https://old.example/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	restore := setAtomicWriteHooksForTest(nil, nil, func(string) error {
		return errors.New("fsync dir failed")
	})
	defer restore()

	err := atomicWriteFile(path, []byte(`
workers:
  app:
    port: 6767
    upstream: openai
upstreams:
  openai:
    base_url: https://new.example/v1
`), 0600)
	if err == nil {
		t.Fatal("expected atomic write to fail after rename")
	}

	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Upstreams["openai"].BaseURL != "https://new.example/v1" {
		t.Fatalf("expected complete renamed config after post-rename failure, got %#v", loaded.Upstreams["openai"])
	}
}

func TestStoreAsyncSaveKeepsDirtyOnFailureAndRetriesLatest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	store := NewStore(path, Config{
		Workers: map[string]WorkerConfig{
			"one": {Port: 6767, Upstream: "openai"},
		},
		Upstreams: map[string]UpstreamProfile{
			"openai": {BaseURL: "https://api.openai.com/v1"},
		},
	})
	store.SetWriterForTest(func(string, []byte, os.FileMode) error {
		return errors.New("permission denied")
	})
	store.Update(func(cfg *Config) {
		cfg.Upstreams["openai"] = UpstreamProfile{BaseURL: "https://failed.example/v1"}
	})
	if err := store.Save(); err == nil {
		t.Fatal("expected save failure")
	}
	if !store.Status().Dirty || store.Status().LastSaveError == "" {
		t.Fatalf("expected dirty status after failed save: %#v", store.Status())
	}

	store.SetWriterForTest(nil)
	store.Update(func(cfg *Config) {
		cfg.Upstreams["openai"] = UpstreamProfile{BaseURL: "https://latest.example/v1"}
	})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	if store.Status().Dirty || store.Status().Generation != 1 {
		t.Fatalf("expected clean generation 1, got %#v", store.Status())
	}
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Upstreams["openai"].BaseURL != "https://latest.example/v1" {
		t.Fatalf("did not persist latest config: %#v", loaded.Upstreams["openai"])
	}
}

func TestStoreAsyncWriterDoesNotBlockUpdatesAndPersistsLatest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	store := NewStore(path, Config{
		Workers: map[string]WorkerConfig{
			"one": {Port: 6767, Upstream: "openai"},
		},
		Upstreams: map[string]UpstreamProfile{
			"openai": {BaseURL: "https://api.openai.com/v1"},
		},
	})

	firstWriteStarted := make(chan struct{})
	releaseFirstWrite := make(chan struct{})
	defer close(releaseFirstWrite)
	writes := 0
	store.SetWriterForTest(func(path string, data []byte, mode os.FileMode) error {
		writes++
		if writes == 1 {
			close(firstWriteStarted)
			<-releaseFirstWrite
			return errors.New("permission denied")
		}
		return os.WriteFile(path, data, mode)
	})
	stop := store.StartAsyncWriter()
	defer stop()

	store.Update(func(cfg *Config) {
		cfg.Upstreams["openai"] = UpstreamProfile{BaseURL: "https://first.example/v1"}
	})
	select {
	case <-firstWriteStarted:
	case <-time.After(time.Second):
		t.Fatal("async writer did not start")
	}

	updateReturned := make(chan struct{})
	go func() {
		store.Update(func(cfg *Config) {
			cfg.Upstreams["openai"] = UpstreamProfile{BaseURL: "https://latest.example/v1"}
		})
		close(updateReturned)
	}()
	select {
	case <-updateReturned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("store update blocked behind disk writer")
	}
	releaseFirstWrite <- struct{}{}

	eventually(t, time.Second, func() bool {
		loaded, err := LoadFile(path)
		return err == nil && loaded.Upstreams["openai"].BaseURL == "https://latest.example/v1" && !store.Status().Dirty
	})
}

func TestPoolMemberRequiresProtocolProbeModel(t *testing.T) {
	cfg := Config{
		Upstreams: map[string]UpstreamProfile{
			"primary": {ProtocolProbe: ProtocolProbeConfig{Model: "probe-primary"}},
			"backup":  {},
		},
		UpstreamPools: map[string]UpstreamPool{
			"coding-ha": {Upstreams: []string{"primary", "backup"}},
		},
	}
	cfg.ApplyDefaults()
	err := cfg.Validate()
	got := []string{}
	if err != nil {
		got = append(got, err.Error())
	}
	want := []string{`upstream pool "coding-ha" member "backup" requires protocol_probe.model`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected validation errors:\n got %#v\nwant %#v", got, want)
	}
}

func TestPoolWorkersRequireCommonProxyURL(t *testing.T) {
	cfg := Config{
		Workers: map[string]WorkerConfig{
			"alpha": {Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy-a.example"},
			"beta":  {Upstream: "primary", UpstreamPool: "coding-ha", ProxyURL: "http://proxy-b.example"},
		},
		Upstreams: map[string]UpstreamProfile{
			"primary": {ProtocolProbe: ProtocolProbeConfig{Model: "probe-primary"}},
		},
		UpstreamPools: map[string]UpstreamPool{
			"coding-ha": {Upstreams: []string{"primary"}},
		},
	}
	cfg.ApplyDefaults()
	err := cfg.Validate()
	got := []string{}
	if err != nil {
		got = append(got, err.Error())
	}
	want := []string{`upstream pool "coding-ha" workers must use one proxy_url`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected validation errors:\n got %#v\nwant %#v", got, want)
	}
}

func TestPoolWorkersRequireCommonActiveUpstream(t *testing.T) {
	tests := []struct {
		name    string
		workers map[string]WorkerConfig
	}{
		{
			name: "split active members",
			workers: map[string]WorkerConfig{
				"alpha": {Upstream: "primary", UpstreamPool: "coding-ha"},
				"beta":  {Upstream: "backup", UpstreamPool: "coding-ha"},
			},
		},
		{
			name: "active upstream is not a member",
			workers: map[string]WorkerConfig{
				"alpha": {Upstream: "outside", UpstreamPool: "coding-ha"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Config{
				Workers: test.workers,
				Upstreams: map[string]UpstreamProfile{
					"primary": {ProtocolProbe: ProtocolProbeConfig{Model: "probe-primary"}},
					"backup":  {ProtocolProbe: ProtocolProbeConfig{Model: "probe-backup"}},
					"outside": {},
				},
				UpstreamPools: map[string]UpstreamPool{
					"coding-ha": {Upstreams: []string{"primary", "backup"}},
				},
			}
			cfg.ApplyDefaults()
			err := cfg.Validate()
			got := []string{}
			if err != nil {
				got = append(got, err.Error())
			}
			want := []string{`upstream pool "coding-ha" workers must use one active upstream`}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected validation errors:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

func eventually(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
