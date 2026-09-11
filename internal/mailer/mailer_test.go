package mailer

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
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

func TestSMTPSenderRejectsUnexpectedBodyControlCharactersBeforeDial(t *testing.T) {
	sender := &SMTP{Host: "127.0.0.1", Port: 1, From: "no-reply@example.test"}
	if err := sender.Send(context.Background(), Message{To: "user@example.test", TextBody: "body\x00"}); err == nil || !strings.Contains(err.Error(), "smtp body contains an unexpected control character") {
		t.Fatalf("body control validation error = %v", err)
	}
}

func TestNewAuthMessageUsesFixedPlainTextTemplate(t *testing.T) {
	link, err := NewAuthLink("https://console.example.test/reset-password?token=server-generated-token")
	if err != nil {
		t.Fatal(err)
	}
	message, err := NewAuthMessage("user@example.test", AuthEmailAccountPasswordReset, link, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if message.Subject != "Reset your Stealth password" {
		t.Fatalf("subject = %q", message.Subject)
	}
	want := "Use the following one-time link to complete password recovery:\n\nhttps://console.example.test/reset-password?token=server-generated-token\n\nThis link expires in 15m0s and can only be used once. If you did not request this, you can ignore this email."
	if message.TextBody != want {
		t.Fatalf("body = %q, want %q", message.TextBody, want)
	}
}

func TestNewAuthLinkRejectsUnsafeValues(t *testing.T) {
	for _, raw := range []string{
		"javascript:alert(1)",
		"data:text/html,alert(1)",
		"https://console.example.test/reset\r\nBcc:attacker@example.test",
		"https://user:password@console.example.test/reset",
		"https://console.example.test/reset#fragment",
	} {
		if _, err := NewAuthLink(raw); err == nil {
			t.Fatalf("NewAuthLink(%q) accepted unsafe URL", raw)
		}
	}
}
