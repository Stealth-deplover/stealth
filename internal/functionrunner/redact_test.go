package functionrunner

import (
	"strings"
	"testing"
)

func TestRedactSecretsAndBoundUTF8Output(t *testing.T) {
	if got := Redact("token=abc abc", []string{"abc"}); got != "token=[REDACTED] [REDACTED]" {
		t.Fatalf("Redact() = %q", got)
	}
	got, truncated := boundedText(strings.Repeat("é", 9000), 16000)
	if !truncated || len(got) > 16000 || !strings.HasSuffix(got, "é") {
		t.Fatalf("boundedText() = len %d truncated=%v", len(got), truncated)
	}
}
