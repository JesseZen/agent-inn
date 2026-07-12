package manager

import (
	"encoding/hex"
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
