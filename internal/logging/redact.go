package logging

import "regexp"

type redactPattern struct {
	pattern     *regexp.Regexp
	replacement string
}

var redactPatterns = []redactPattern{
	{regexp.MustCompile(`(?i)(bearer\s+)(["']?)[^"'\s]+(["']?)`), `${1}${2}***REDACTED***${3}`},
	{regexp.MustCompile(`(?i)((?:authorization|proxy-authorization)\s*[:=]\s*(?:basic|bearer|token)\s+)(["']?)[^"'\s]+(["']?)`), `${1}${2}***REDACTED***${3}`},
	{regexp.MustCompile(`(?i)("(?:api_key|access_token|auth_token|client_secret|webhook_secret|password|secret|token)"\s*:\s*")[^"]*(")`), `${1}***REDACTED***${2}`},
	{regexp.MustCompile(`(?i)([?&](?:api_key|access_token|auth_token|token|key)=)[^&\s"]+`), `${1}***REDACTED***`},
	{regexp.MustCompile(`(?i)((?:x-api-key|api-key|api_key|access-token|access_token|auth-token|auth_token|client-secret|client_secret|password|secret|token)\s*[:=]\s*["']?)[^"'\s,;}]+(["']?)`), `${1}***REDACTED***${2}`},
	{regexp.MustCompile(`(?i)((?:[a-z0-9_]*(?:api_key|access_token|auth_token|client_secret|webhook_secret|password|secret|token))\s*=\s*["']?)[^"'\s,;}]+(["']?)`), `${1}***REDACTED***${2}`},
	{regexp.MustCompile(`(?i)(https?://[^/\s:@]+:)[^@\s]+(@)`), `${1}***REDACTED***${2}`},
	{regexp.MustCompile(`(?i)((?:cookie|set-cookie)\s*:\s*)[^\r\n]+`), `${1}***REDACTED***`},
}

func Redact(line string) string {
	for _, pattern := range redactPatterns {
		line = pattern.pattern.ReplaceAllString(line, pattern.replacement)
	}
	return line
}
