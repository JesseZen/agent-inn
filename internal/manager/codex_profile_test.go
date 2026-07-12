package manager

import (
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
