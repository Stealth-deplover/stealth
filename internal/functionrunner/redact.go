package functionrunner

import (
	"strings"
	"unicode/utf8"
)

const (
	maxExecutionLogBytes = 16000
	maxExecutionError    = 4000
)

// Redact replaces exact variable values before output is persisted. A secret
// can therefore appear in stdout/stderr without being copied to PostgreSQL.
// Empty values are ignored because replacing an empty string would corrupt
// every log message.
func Redact(value string, variables []string) string {
	for _, secret := range variables {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}

func boundedText(value string, limit int) (string, bool) {
	if limit <= 0 {
		limit = maxExecutionLogBytes
	}
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= limit {
		return value, false
	}
	// Avoid splitting a UTF-8 sequence while enforcing a byte limit accepted
	// by PostgreSQL's text columns.
	cut := limit
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut], true
}

func executionLogText(value string) (string, bool)   { return boundedText(value, maxExecutionLogBytes) }
func executionErrorText(value string) (string, bool) { return boundedText(value, maxExecutionError) }
