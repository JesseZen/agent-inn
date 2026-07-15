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
	// DefaultGrokModel is used when a Grok worker has no protocol_probe.model.
	DefaultGrokModel = "grok-4.5"
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
	Models grokModelsSettings `toml:"models,omitempty"`
}

type grokModelsSettings struct {
	Default   string `toml:"default,omitempty"`
	WebSearch string `toml:"web_search,omitempty"`
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
	stateDir := cfg.Settings.StateDir
	if stateDir == "" {
		stateDir = "~/.ainn"
	}
	path := filepath.Join(expandHomePath(stateDir), "grok-home", ".grok", "config.toml")

	defaultModel := ""
	for _, worker := range cfg.Workers {
		if worker.Launcher != grokLauncherName {
			continue
		}
		defaultModel = cfg.Upstreams[worker.Upstream].ProtocolProbe.Model
		if defaultModel == "" {
			defaultModel = DefaultGrokModel
		}
		break
	}

	if defaultModel == "" {
		// No Grok workers: clear managed custom-model residue if present.
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		return writeTextFile(path, "", 0o600)
	}

	// Point web_search at the chat model so BYOK/proxy tokens that lack
	// grok-4.20-multi-agent still run the client web_search tool. Do NOT set
	// supports_backend_search on the chat model: that makes Grok drop the
	// client web_search tool as a "backend-hosted" collision, and ChatCompletions
	// proxies do not re-expose a callable search tool.
	data, err := toml.Marshal(grokConfig{
		Models: grokModelsSettings{
			Default:   defaultModel,
			WebSearch: defaultModel,
		},
	})
	if err != nil {
		return err
	}
	return writeTextFile(path, string(data), 0o600)
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
