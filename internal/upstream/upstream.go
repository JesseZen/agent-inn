package upstream

import (
	"fmt"
	"os"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
)

type RuntimeUpstream struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key,omitempty"`
	APIFormat string `json:"api_format,omitempty"`
}

type RedactedUpstream struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url,omitempty"`
	APIKey    string `json:"api_key,omitempty"`
	HasAPIKey bool   `json:"has_api_key"`
	APIFormat string `json:"api_format,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
}

func Resolve(name string, profile config.UpstreamProfile) (RuntimeUpstream, error) {
	if apiKey := runtimeAPIKey(name, profile); apiKey != "" {
		return RuntimeUpstream{ID: name, Name: name, BaseURL: profile.BaseURL, APIKey: apiKey, APIFormat: profile.APIFormat}, nil
	}
	return RuntimeUpstream{
		ID:        name,
		Name:      name,
		BaseURL:   profile.BaseURL,
		APIKey:    strings.TrimSpace(profile.APIKey),
		APIFormat: profile.APIFormat,
	}, nil
}

func ResolveWithDisplayName(id string, displayName string, profile config.UpstreamProfile) (RuntimeUpstream, error) {
	resolved, err := Resolve(id, profile)
	if err != nil {
		return RuntimeUpstream{}, err
	}
	resolved.Name = strings.TrimSpace(displayName)
	if resolved.Name == "" {
		resolved.Name = id
	}
	return resolved, nil
}

func ResolveRuntime(name string, profile config.UpstreamProfile) (appruntime.UpstreamRuntime, error) {
	name = strings.TrimSpace(name)
	if strings.TrimSpace(profile.BaseURL) == "" {
		return appruntime.UpstreamRuntime{ID: appruntime.UpstreamID(name)}, fmt.Errorf("upstream base URL is required")
	}
	if apiKey := runtimeAPIKey(name, profile); apiKey != "" {
		return appruntime.UpstreamRuntime{
			ID:        appruntime.UpstreamID(name),
			BaseURL:   strings.TrimSpace(profile.BaseURL),
			APIKey:    apiKey,
			APIFormat: appruntime.APIFormat(strings.TrimSpace(profile.APIFormat)),
		}, nil
	}
	return appruntime.UpstreamRuntime{
		ID:        appruntime.UpstreamID(name),
		BaseURL:   strings.TrimSpace(profile.BaseURL),
		APIKey:    strings.TrimSpace(profile.APIKey),
		APIFormat: appruntime.APIFormat(strings.TrimSpace(profile.APIFormat)),
	}, nil
}

func (p RuntimeUpstream) Redacted() RedactedUpstream {
	return RedactedUpstream{
		ID:        p.ID,
		Name:      p.Name,
		BaseURL:   p.BaseURL,
		HasAPIKey: p.APIKey != "",
		APIFormat: p.APIFormat,
	}
}

func MissingRedacted(id string) RedactedUpstream {
	id = strings.TrimSpace(id)
	return RedactedUpstream{ID: id, Name: id, Missing: true}
}

func runtimeAPIKey(upstreamName string, profile config.UpstreamProfile) string {
	name := strings.ToUpper(strings.TrimSpace(upstreamName))
	if name == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(name + "_API_KEY"))
}
