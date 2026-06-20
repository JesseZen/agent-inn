package provider

import (
	"testing"

	"github.com/jesse/codex-app-proxy/internal/config"
)

func TestResolveProviderExpandsSecretRefWithoutMutatingProfile(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-secret")
	profile := config.ProviderProfile{
		BaseURL:   "https://api.openai.com/v1",
		APIKeyRef: "${OPENAI_API_KEY}",
		APIFormat: "responses",
	}

	runtime, err := Resolve("openai", profile)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.APIKey != "sk-test-secret" {
		t.Fatal("secret was not resolved")
	}
	if profile.APIKeyRef != "${OPENAI_API_KEY}" {
		t.Fatalf("profile was mutated: %#v", profile)
	}

	redacted := runtime.Redacted()
	if redacted.APIKey != "" || !redacted.HasAPIKey {
		t.Fatalf("redacted view leaked or lost key state: %#v", redacted)
	}
}

func TestResolveProviderFailsWhenSecretRefMissing(t *testing.T) {
	profile := config.ProviderProfile{
		BaseURL:   "https://api.openai.com/v1",
		APIKeyRef: "${MISSING_API_KEY}",
	}
	if _, err := Resolve("openai", profile); err == nil {
		t.Fatal("expected missing secret ref to fail")
	}
}
