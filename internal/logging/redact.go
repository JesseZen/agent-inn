package logging

import "regexp"

var redactPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bearer\s+)\S+`),
	regexp.MustCompile(`(?i)("api_key"\s*:\s*")\S+(")`),
	regexp.MustCompile(`(?i)([?&]key=)[^&\s]+`),
}

func Redact(line string) string {
	for _, pattern := range redactPatterns {
		line = pattern.ReplaceAllString(line, "${1}***REDACTED***${2}")
	}
	return line
}
