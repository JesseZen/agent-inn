package provider

import (
	"fmt"
	"os"
	"strings"

	"github.com/jesse/codex-app-proxy/internal/config"
)

type RuntimeProvider struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key,omitempty"`
	APIFormat string `json:"api_format,omitempty"`
}

type RedactedProvider struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key,omitempty"`
	HasAPIKey bool   `json:"has_api_key"`
	APIFormat string `json:"api_format,omitempty"`
}

func Resolve(name string, profile config.ProviderProfile) (RuntimeProvider, error) {
	apiKey, err := resolveSecretRef(profile.APIKeyRef)
	if err != nil {
		return RuntimeProvider{}, err
	}
	return RuntimeProvider{
		Name:      name,
		BaseURL:   profile.BaseURL,
		APIKey:    apiKey,
		APIFormat: profile.APIFormat,
	}, nil
}

func (p RuntimeProvider) Redacted() RedactedProvider {
	return RedactedProvider{
		Name:      p.Name,
		BaseURL:   p.BaseURL,
		HasAPIKey: p.APIKey != "",
		APIFormat: p.APIFormat,
	}
}

func resolveSecretRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if strings.HasPrefix(ref, "${") && strings.HasSuffix(ref, "}") {
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(ref, "${"), "}"))
		if name == "" {
			return "", fmt.Errorf("empty secret reference")
		}
		value := os.Getenv(name)
		if value == "" {
			return "", fmt.Errorf("secret reference %s is missing", name)
		}
		return value, nil
	}
	return "", fmt.Errorf("unsupported secret reference %q", ref)
}
