package logging

import (
	"reflect"
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

func TestRedactCommonCredentialForms(t *testing.T) {
	cases := []struct {
		name   string
		line   string
		secret string
	}{
		{name: "environment api key", line: "OPENAI_API_KEY=sk-env", secret: "sk-env"},
		{name: "header api key", line: "X-Api-Key: sk-header", secret: "sk-header"},
		{name: "basic authorization", line: "Authorization: Basic dXNlcjpwYXNz", secret: "dXNlcjpwYXNz"},
		{name: "json access token", line: `{"access_token":"access-secret"}`, secret: "access-secret"},
		{name: "url password", line: "https://user:password-secret@example.com/v1", secret: "password-secret"},
		{name: "cookie", line: "Cookie: session=secret-cookie", secret: "secret-cookie"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.line)
			if strings.Contains(got, tc.secret) {
				t.Fatalf("secret leaked: %q", got)
			}
			if !strings.Contains(got, "***REDACTED***") {
				t.Fatalf("redaction marker missing: %q", got)
			}
		})
	}
}

func TestRedactPreservesQuotedBearerBoundaryAcrossRepeatedPasses(t *testing.T) {
	once := Redact(`error="Authorization: Bearer sk-secret"`)
	twice := Redact(once)
	got := struct {
		Once  string
		Twice string
	}{Once: once, Twice: twice}
	want := struct {
		Once  string
		Twice string
	}{
		Once:  `error="Authorization: Bearer ***REDACTED***"`,
		Twice: `error="Authorization: Bearer ***REDACTED***"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repeated redaction mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestRedactQuotedAuthorizationValues(t *testing.T) {
	got := []string{
		Redact(`Authorization: Bearer "sk-secret"`),
		Redact(`Authorization: Basic 'basic-secret'`),
	}
	want := []string{
		`Authorization: Bearer "***REDACTED***"`,
		`Authorization: Basic '***REDACTED***'`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("quoted authorization redaction mismatch:\n got %#v\nwant %#v", got, want)
	}
}
