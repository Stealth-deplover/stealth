package mailer

import (
	"bytes"
	"context"
	"log/slog"
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

func TestLogSenderNeverLogsSensitiveMessageContent(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	sender := LogSender{Logger: logger}
	message := Message{
		To:       "user@example.test",
		Subject:  "reset-token-123456",
		TextBody: "Use this link: https://console.example.test/reset?token=STEALTH_TEST_SECRET_DO_NOT_LOG",
	}
	if err := sender.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	logs := output.String()
	for _, secret := range []string{"STEALTH_TEST_SECRET_DO_NOT_LOG", "reset-token-123456", message.To} {
		if strings.Contains(logs, secret) {
			t.Fatalf("LogSender leaked %q in output: %s", secret, logs)
		}
	}
	if !strings.Contains(logs, "recipient_count=1") {
		t.Fatalf("safe delivery metadata missing from output: %s", logs)
	}
}

func TestSMTPSenderRejectsControlCharactersInAllHeadersBeforeDial(t *testing.T) {
	tests := []struct {
		name   string
		sender *SMTP
		msg    Message
		field  string
	}{
		{
			name:   "recipient",
			sender: &SMTP{Host: "127.0.0.1", Port: 1, From: "no-reply@example.test"},
			msg:    Message{To: "user@example.test\r\nBcc: attacker@example.test"},
			field:  "recipient",
		},
		{
			name:   "subject",
			sender: &SMTP{Host: "127.0.0.1", Port: 1, From: "no-reply@example.test"},
			msg:    Message{To: "user@example.test", Subject: "subject\nBcc: attacker@example.test"},
			field:  "subject",
		},
		{
			name:   "sender",
			sender: &SMTP{Host: "127.0.0.1", Port: 1, From: "no-reply@example.test\x00"},
			msg:    Message{To: "user@example.test"},
			field:  "sender",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.sender.Send(context.Background(), test.msg)
			if err == nil || !strings.Contains(err.Error(), "smtp "+test.field) {
				t.Fatalf("header validation error = %v", err)
			}
		})
	}
}
