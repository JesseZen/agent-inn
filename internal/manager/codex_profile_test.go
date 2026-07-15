package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/pelletier/go-toml/v2"
)

func TestCodexProfileName(t *testing.T) {
	tests := []struct {
		workerID string
		want     string
	}{
		{workerID: "clanker", want: "clanker"},
		{workerID: "0p02", want: "0p02"},
		{workerID: "zero-02", want: "zero-02"},
		{workerID: "zero_02", want: "zero_02"},
		{workerID: "002", want: "002"},
		{workerID: "0.02", want: "ainn-x-302e3032"},
		{workerID: "zero 02", want: "ainn-x-7a65726f203032"},
		{workerID: "中文", want: "ainn-x-e4b8ade69687"},
		{workerID: "ainn-x-302e3032", want: "ainn-x-61696e6e2d782d3330326533303332"},
	}

	for _, test := range tests {
		t.Run(test.workerID, func(t *testing.T) {
			got, err := CodexProfileName(test.workerID)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestCodexProfileNamePreventsPassthroughCollision(t *testing.T) {
	encoded, err := CodexProfileName("0.02")
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := CodexProfileName(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if encoded == reserved {
		t.Fatalf("profiles collided: %q", encoded)
	}
	again, err := CodexProfileName("0.02")
	if err != nil {
		t.Fatal(err)
	}
	if again != encoded {
		t.Fatalf("mapping is not deterministic: first %q, second %q", encoded, again)
	}
}

func TestCodexProfileNameLengthLimit(t *testing.T) {
	workerID := strings.Repeat("a", 244)
	profile, err := CodexProfileName(workerID)
	if err == nil {
		t.Fatalf("expected %d-byte profile %q to fail", len(profile), profile)
	}
	want := `worker "` + workerID + `" derived Codex profile "` + workerID + `" is 244 bytes; limit is 243 bytes`
	if err.Error() != want {
		t.Fatalf("got error %q, want %q", err, want)
	}
}

func TestSyncCodexProfileFilesUsesDerivedWorkerProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := config.UpstreamProfile{BaseURL: "https://api.openai.com/v1", APIFormat: "responses"}
	cfg := config.Config{
		Workers: map[string]config.WorkerConfig{
			"0.02": {Port: 11199, Upstream: "openai", Launcher: "codex"},
		},
		Upstreams: map[string]config.UpstreamProfile{"openai": profile},
	}

	if err := syncCodexProfileFiles(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "ainn-x-302e3032.config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var got codexProfileFile
	if err := toml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := codexProfileFile{
		ModelProvider: "OpenAI",
		ModelProviders: map[string]codexProfileEntry{
			"OpenAI": {Name: "OpenAI", BaseURL: "http://127.0.0.1:11199", WireAPI: "responses"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "0.02.config.toml")); !os.IsNotExist(err) {
		t.Fatalf("raw invalid profile file must not be written, stat error: %v", err)
	}
}

func TestSyncGrokConfigWritesSharedDefaultWithoutWorkerModels(t *testing.T) {
	home := t.TempDir()
	cfg := config.Config{
		Settings: config.Settings{StateDir: filepath.Join(home, "state")},
		Workers: map[string]config.WorkerConfig{
			"hututu": {Port: 53379, Upstream: "jws", Launcher: "grok"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"jws": {ProtocolProbe: config.ProtocolProbeConfig{Model: "grok-4.5"}},
		},
	}

	if err := syncGrokConfig(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "state", "grok-home", ".grok", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "[model.hututu]") || strings.Contains(text, "name = 'hututu'") || strings.Contains(text, `name = "hututu"`) {
		t.Fatalf("worker-named custom model must not be written:\n%s", text)
	}
	if strings.Contains(text, "53379") {
		t.Fatalf("managed config must not embed worker base_url/port:\n%s", text)
	}
	var got grokConfig
	if err := toml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := grokConfig{Models: grokModelsSettings{Default: "grok-4.5", WebSearch: "grok-4.5"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if strings.Contains(text, "supports_backend_search") || strings.Contains(text, "[model.") {
		t.Fatalf("must not enable backend search or per-model blocks (drops client web_search):\n%s", text)
	}
}

func TestSyncGrokConfigClearsStaleWorkerModelsWhenNoGrokWorkers(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	path := filepath.Join(stateDir, "grok-home", ".grok", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := "[model.hututu]\nmodel = 'grok-4.5'\nbase_url = 'http://127.0.0.1:53379'\nname = 'hututu'\nenv_key = 'XAI_API_KEY'\n"
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Settings: config.Settings{StateDir: stateDir},
		Workers: map[string]config.WorkerConfig{
			"claude": {Port: 53054, Upstream: "up_4", Launcher: "claudecode"},
		},
		Upstreams: map[string]config.UpstreamProfile{"up_4": {}},
	}
	if err := syncGrokConfig(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hututu") || strings.Contains(string(data), "[model.") {
		t.Fatalf("stale worker model residue remained:\n%s", data)
	}
}

func TestSyncPiConfigWritesWorkerProxyModels(t *testing.T) {
	stateDir := t.TempDir()
	cfg := config.Config{
		Settings: config.Settings{StateDir: stateDir},
		Workers: map[string]config.WorkerConfig{
			"worker-main": {Port: 11199, Upstream: "openai", Launcher: "pi"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"openai": {APIFormat: "responses", ProtocolProbe: config.ProtocolProbeConfig{Model: "gpt-5.5"}},
		},
	}

	if err := syncPiConfig(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "pi-agent", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got piModelsConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := piModelsConfig{Providers: map[string]piProvider{
		"worker-main": {
			BaseURL: "http://127.0.0.1:11199/v1",
			API:     "openai-responses",
			APIKey:  "ainn",
			Models:  []piModel{{ID: "gpt-5.5", Name: "gpt-5.5"}},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestSyncPiConfigMapsWorkerProtocols(t *testing.T) {
	stateDir := t.TempDir()
	cfg := config.Config{
		Settings: config.Settings{StateDir: stateDir},
		Workers: map[string]config.WorkerConfig{
			"chat":      {Port: 11199, Upstream: "chat", Launcher: "pi"},
			"anthropic": {Port: 11200, Upstream: "anthropic", Launcher: "pi"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"chat":      {APIFormat: "chat_completions", ProtocolProbe: config.ProtocolProbeConfig{Model: "deepseek-chat"}},
			"anthropic": {APIFormat: "anthropic", ProtocolProbe: config.ProtocolProbeConfig{Model: "claude-sonnet"}},
		},
	}

	if err := syncPiConfig(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "pi-agent", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got piModelsConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := piModelsConfig{Providers: map[string]piProvider{
		"chat": {
			BaseURL: "http://127.0.0.1:11199/v1",
			API:     "openai-completions",
			APIKey:  "ainn",
			Models:  []piModel{{ID: "deepseek-chat", Name: "deepseek-chat"}},
		},
		"anthropic": {
			BaseURL: "http://127.0.0.1:11200/v1",
			API:     "anthropic-messages",
			APIKey:  "ainn",
			Models:  []piModel{{ID: "claude-sonnet", Name: "claude-sonnet"}},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestSyncCodexProfileFilesRejectsLongProfileBeforeWriting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workerID := strings.Repeat("a", 244)
	cfg := config.Config{
		Workers: map[string]config.WorkerConfig{
			workerID: {Port: 11199, Upstream: "openai", Launcher: "codex"},
			"valid":  {Port: 11200, Upstream: "openai", Launcher: "codex"},
		},
		Upstreams: map[string]config.UpstreamProfile{"openai": {BaseURL: "https://api.openai.com/v1"}},
	}

	err := syncCodexProfileFiles(cfg)
	if err == nil {
		t.Fatal("expected long profile to fail")
	}
	if !strings.Contains(err.Error(), "244 bytes; limit is 243 bytes") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("profile directory must not be created, stat error: %v", err)
	}
}

func TestWriteCodexProfileFileUsesOpenAIProviderForWorkerProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := writeCodexProfileFile("cli-openai", config.UpstreamProfile{
		BaseURL:   "http://127.0.0.1:6767",
		APIFormat: "chat_completions",
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".codex", "cli-openai.config.toml"))
	if err != nil {
		t.Fatal(err)
	}

	var got codexProfileFile
	if err := toml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	want := codexProfileFile{
		ModelProvider: "OpenAI",
		ModelProviders: map[string]codexProfileEntry{
			"OpenAI": {
				Name:    "OpenAI",
				BaseURL: "http://127.0.0.1:6767",
				WireAPI: "responses",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected profile: got %#v want %#v", got, want)
	}
}
