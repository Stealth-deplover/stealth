package mailer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
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

func TestSMTPSenderQuotesDynamicPlainTextBody(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	messageCh := make(chan string, 1)
	serverErrCh := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrCh <- acceptErr
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		writeResponse := func(response string) error {
			_, writeErr := io.WriteString(connection, response+"\r\n")
			return writeErr
		}
		if err := writeResponse("220 test"); err != nil {
			serverErrCh <- err
			return
		}
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				serverErrCh <- readErr
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				if err := writeResponse("250 test"); err != nil {
					serverErrCh <- err
					return
				}
			case strings.HasPrefix(line, "MAIL FROM"), strings.HasPrefix(line, "RCPT TO"):
				if err := writeResponse("250 ok"); err != nil {
					serverErrCh <- err
					return
				}
			case strings.TrimSpace(line) == "DATA":
				if err := writeResponse("354 continue"); err != nil {
					serverErrCh <- err
					return
				}
				var data strings.Builder
				for {
					bodyLine, bodyErr := reader.ReadString('\n')
					if bodyErr != nil {
						serverErrCh <- bodyErr
						return
					}
					if bodyLine == ".\r\n" {
						break
					}
					data.WriteString(bodyLine)
				}
				messageCh <- data.String()
				if err := writeResponse("250 queued"); err != nil {
					serverErrCh <- err
					return
				}
			case strings.TrimSpace(line) == "QUIT":
				serverErrCh <- writeResponse("221 bye")
				return
			}
		}
	}()

	sender := &SMTP{
		Host:    "127.0.0.1",
		Port:    listener.Addr().(*net.TCPAddr).Port,
		From:    "no-reply@example.test",
		Timeout: time.Second,
	}
	message := "first line\r\nBcc: attacker@example.test\r\nlast line"
	if err := sender.Send(context.Background(), Message{To: "user@example.test", Subject: "subject", TextBody: message}); err != nil {
		t.Fatal(err)
	}
	select {
	case serverErr := <-serverErrCh:
		if serverErr != nil {
			t.Fatal(serverErr)
		}
	case <-time.After(time.Second):
		t.Fatal("fake SMTP server did not finish")
	}
	var data string
	select {
	case data = <-messageCh:
	case <-time.After(time.Second):
		t.Fatal("fake SMTP server did not capture a message")
	}
	headerAndBody := strings.SplitN(data, "\r\n\r\n", 2)
	if len(headerAndBody) != 2 {
		t.Fatalf("SMTP message did not contain a header/body boundary: %q", data)
	}
	if !strings.Contains(headerAndBody[0], "Content-Transfer-Encoding: base64") {
		t.Fatalf("SMTP headers did not select base64 encoding: %q", headerAndBody[0])
	}
	if strings.Contains(headerAndBody[0], "To:") || strings.Contains(headerAndBody[0], "Bcc:") {
		t.Fatalf("SMTP headers copied the recipient into message headers: %q", headerAndBody[0])
	}
	decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, strings.NewReader(headerAndBody[1])))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != message+"\r\n" {
		t.Fatalf("decoded SMTP body = %q, want %q", decoded, message+"\r\n")
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
