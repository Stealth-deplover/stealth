package mailer

import (
	"context"
	"strings"
	"testing"
)

func TestDisabledSenderFailsClosed(t *testing.T) {
	if err := (DisabledSender{}).Send(context.Background(), Message{To: "user@example.test"}); err != ErrDisabled {
		t.Fatalf("disabled sender error = %v, want %v", err, ErrDisabled)
	}
}

func TestSMTPSenderRejectsHeaderInjectionBeforeDial(t *testing.T) {
	sender := &SMTP{Host: "127.0.0.1", Port: 1, From: "no-reply@example.test"}
	if err := sender.Send(context.Background(), Message{To: "user@example.test\r\nBcc: attacker@example.test"}); err == nil || !strings.Contains(err.Error(), "recipient contains a newline") {
		t.Fatalf("header injection error = %v", err)
	}
}
