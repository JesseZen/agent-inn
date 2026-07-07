package logging

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	line := `Authorization: Bearer sk-live "api_key":"abc" url?key=xyz`
	got := Redact(line)
	if strings.Contains(got, "sk-live") || strings.Contains(got, "abc") || strings.Contains(got, "xyz") {
		t.Fatalf("secret leaked after redaction: %s", got)
	}
}

func TestRedactSecretQueryParameters(t *testing.T) {
	line := `url=https://api.example/v1/messages?api_key=sk-api&token=tok-live&access_token=access-live&key=legacy-key`
	got := Redact(line)
	if strings.Contains(got, "sk-api") || strings.Contains(got, "tok-live") || strings.Contains(got, "access-live") || strings.Contains(got, "legacy-key") {
		t.Fatalf("query secret leaked after redaction: %s", got)
	}
}
