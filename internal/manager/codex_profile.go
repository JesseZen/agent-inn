package manager

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/constants"
	"github.com/pelletier/go-toml/v2"
)

const (
	codexLaunchProviderName = "OpenAI"
	codexLaunchWireAPI      = "responses"
	codexProfilePrefix      = "ainn-x-"
	codexProfileMaxBytes    = 243
	defaultGrokModel        = "grok-4.5"
)

var codexPassthroughProfilePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)

type codexProfileFile struct {
	ModelProvider  string                       `toml:"model_provider"`
	ModelProviders map[string]codexProfileEntry `toml:"model_providers"`
}

type codexProfileEntry struct {
	Name    string `toml:"name"`
	BaseURL string `toml:"base_url"`
	WireAPI string `toml:"wire_api,omitempty"`
}

type grokConfig struct {
	Model map[string]grokModel `toml:"model"`
}

type grokModel struct {
	Model   string `toml:"model"`
	BaseURL string `toml:"base_url"`
	Name    string `toml:"name"`
	EnvKey  string `toml:"env_key"`
}

type piModelsConfig struct {
	Providers map[string]piProvider `json:"providers"`
}

type piProvider struct {
	BaseURL string    `json:"baseUrl"`
	API     string    `json:"api"`
	APIKey  string    `json:"apiKey"`
	Models  []piModel `json:"models"`
}

type piModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func CodexProfileName(workerID string) (string, error) {
	if workerID == "" {
		return "", fmt.Errorf("Codex worker ID is empty")
	}
	profile := workerID
	if !codexPassthroughProfilePattern.MatchString(workerID) || strings.HasPrefix(workerID, codexProfilePrefix) {
		profile = codexProfilePrefix + hex.EncodeToString([]byte(workerID))
	}
	if len(profile) > codexProfileMaxBytes {
		return profile, fmt.Errorf("worker %q derived Codex profile %q is %d bytes; limit is %d bytes", workerID, profile, len(profile), codexProfileMaxBytes)
	}
	return profile, nil
}

func writeCodexProfileFile(name string, profile config.UpstreamProfile) error {
	encoded, err := toml.Marshal(codexProfileFile{
		ModelProvider: codexLaunchProviderName,
		ModelProviders: map[string]codexProfileEntry{
			codexLaunchProviderName: {
				Name:    codexLaunchProviderName,
				BaseURL: profile.BaseURL,
				WireAPI: codexLaunchWireAPI,
			},
		},
	})
	if err != nil {
		return err
	}
	return writeTextFile(codexProfilePath(name), string(encoded), 0600)
}

func codexProfilePath(name string) string {
	return expandHomePath(filepath.Join("~/.codex", name+".config.toml"))
}

func syncCodexProfileFiles(cfg config.Config) error {
	profileNames := make(map[string]string, len(cfg.Workers))
	for name, worker := range cfg.Workers {
		profileName := name
		if worker.Launcher != claudeCodeLauncherName {
			var err error
			profileName, err = CodexProfileName(name)
			if err != nil {
				return err
			}
		}
		profileNames[name] = profileName
	}
	for name, worker := range cfg.Workers {
		profile := cfg.Upstreams[worker.Upstream]
		profile.BaseURL = fmt.Sprintf("http://%s:%d", constants.LocalhostAddr, worker.Port)
		if err := writeCodexProfileFile(profileNames[name], profile); err != nil {
			return fmt.Errorf("write profile %s: %w", profileNames[name], err)
		}
	}
	return nil
}

func syncGrokConfig(cfg config.Config) error {
	models := make(map[string]grokModel)
	for name, worker := range cfg.Workers {
		if worker.Launcher != grokLauncherName {
			continue
		}
		model := cfg.Upstreams[worker.Upstream].ProtocolProbe.Model
		if model == "" {
			model = defaultGrokModel
		}
		models[name] = grokModel{
			Model:   model,
			BaseURL: fmt.Sprintf("http://%s:%d", constants.LocalhostAddr, worker.Port),
			Name:    name,
			EnvKey:  "XAI_API_KEY",
		}
	}
	if len(models) == 0 {
		return nil
	}
	stateDir := cfg.Settings.StateDir
	if stateDir == "" {
		stateDir = "~/.ainn"
	}
	data, err := toml.Marshal(grokConfig{Model: models})
	if err != nil {
		return err
	}
	return writeTextFile(filepath.Join(expandHomePath(stateDir), "grok-home", ".grok", "config.toml"), string(data), 0600)
}

func syncPiConfig(cfg config.Config) error {
	providers := make(map[string]piProvider)
	for name, worker := range cfg.Workers {
		if worker.Launcher != piLauncherName {
			continue
		}
		profile := cfg.Upstreams[worker.Upstream]
		api := "openai-responses"
		switch profile.APIFormat {
		case "chat_completions":
			api = "openai-completions"
		case "anthropic":
			api = "anthropic-messages"
		}
		providers[name] = piProvider{
			BaseURL: fmt.Sprintf("http://%s:%d/v1", constants.LocalhostAddr, worker.Port),
			API:     api,
			APIKey:  "ainn",
			Models:  []piModel{{ID: profile.ProtocolProbe.Model, Name: profile.ProtocolProbe.Model}},
		}
	}
	if len(providers) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(piModelsConfig{Providers: providers}, "", "  ")
	if err != nil {
		return err
	}
	stateDir := cfg.Settings.StateDir
	if stateDir == "" {
		stateDir = "~/.ainn"
	}
	return writeTextFile(filepath.Join(expandHomePath(stateDir), "pi-agent", "models.json"), string(data)+"\n", 0600)
}

func writeTextFile(path string, text string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	cleanup := func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.WriteString(text); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		cleanup()
		return err
	}
	closed = true
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return fsyncDir(dir)
}

func fsyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
