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
