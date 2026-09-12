// Package mailer contains the small delivery boundary used by Auth. Keeping
// this interface separate from the HTTP handlers lets deployments use their
// own SMTP relay (or a provider adapter) without ever persisting email
// secrets or recovery tokens in the API process.
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strings"
	"time"
	"unicode"

	"github.com/Stealth-deplover/stealth/internal/config"
)

var ErrDisabled = errors.New("email delivery is disabled")

type Message struct {
	To       string
	Subject  string
	TextBody string
}

type Sender interface {
	Send(context.Context, Message) error
}

// NewFromConfig selects a delivery implementation. The disabled default is
// intentional for local development; production should set
// EMAIL_DELIVERY_MODE=smtp and configure an authenticated relay. The explicit
// log mode is useful for local end-to-end testing and is never selected by
// accident.
func NewFromConfig(cfg config.Config, logger *slog.Logger) Sender {
	if logger == nil {
		logger = slog.Default()
	}
	switch strings.ToLower(strings.TrimSpace(cfg.EmailDeliveryMode)) {
	case "smtp":
		return &SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom}
	case "log":
		return LogSender{Logger: logger}
	default:
		return DisabledSender{}
	}
}

type DisabledSender struct{}

func (DisabledSender) Send(context.Context, Message) error { return ErrDisabled }

// LogSender deliberately logs only delivery metadata so a developer can
// exercise the flow without an SMTP server. Message bodies can contain reset
// links, verification tokens, or other confidential content and are never
// written to logs. Select this mode explicitly with EMAIL_DELIVERY_MODE and
// keep it disabled in production.
type LogSender struct{ Logger *slog.Logger }

func (s LogSender) Send(_ context.Context, message Message) error {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("auth email delivery in log mode", "recipient_count", 1)
	return nil
}

// SMTP sends a plain-text message over an SMTP connection. It uses STARTTLS
// when advertised by the relay and refuses to send credentials over a clear
// non-local connection. Implicit-TLS port 465 is intentionally not guessed;
// use a relay that supports STARTTLS on its submission port.
type SMTP struct {
	Host        string
	Port        int
	Username    string
	Password    string
	From        string
	Timeout     time.Duration
	DialContext func(context.Context, string, string) (net.Conn, error)
}

func (s *SMTP) Send(ctx context.Context, message Message) error {
	if s == nil || strings.TrimSpace(s.Host) == "" || s.Port < 1 || s.Port > 65535 || strings.TrimSpace(s.From) == "" {
		return fmt.Errorf("smtp configuration is incomplete")
	}
	if err := validHeaderValue(message.To, "recipient"); err != nil {
		return err
	}
	if err := validHeaderValue(message.Subject, "subject"); err != nil {
		return err
	}
	if err := validHeaderValue(s.From, "sender"); err != nil {
		return err
	}
	normalizedBody, err := normalizeTextBody(message.TextBody)
	if err != nil {
		return err
	}
	if strings.TrimSpace(message.To) == "" {
		return fmt.Errorf("recipient is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialContext := s.DialContext
	if dialContext == nil {
		dialer := &net.Dialer{Timeout: timeout}
		dialContext = dialer.DialContext
	}
	address := net.JoinHostPort(s.Host, fmt.Sprintf("%d", s.Port))
	conn, err := dialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("dial smtp relay: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}
	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return fmt.Errorf("start smtp client: %w", err)
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("start smtp TLS: %w", err)
		}
	} else if s.Username != "" && !isLocalHost(s.Host) {
		return fmt.Errorf("smtp relay does not advertise STARTTLS")
	}
	if s.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("authenticate smtp relay: %w", err)
		}
	}
	if err := client.Mail(s.From); err != nil {
		return fmt.Errorf("set smtp sender: %w", err)
	}
	if err := client.Rcpt(message.To); err != nil {
		return fmt.Errorf("set smtp recipient: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("open smtp message: %w", err)
	}
	headers := "From: " + s.From + "\r\n" +
		"To: " + message.To + "\r\n" +
		"Subject: " + message.Subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n"
	if _, err := io.WriteString(writer, headers); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write smtp headers: %w", err)
	}
	bodyWriter := quotedprintable.NewWriter(writer)
	if _, err := io.WriteString(bodyWriter, normalizedBody+"\r\n"); err != nil {
		_ = bodyWriter.Close()
		_ = writer.Close()
		return fmt.Errorf("write smtp message: %w", err)
	}
	if err := bodyWriter.Close(); err != nil {
		_ = writer.Close()
		return fmt.Errorf("finish smtp message encoding: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish smtp message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("close smtp session: %w", err)
	}
	return nil
}

func validHeaderValue(value, field string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("smtp %s contains a newline", field)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("smtp %s contains a control character", field)
	}
	return nil
}

func normalizeTextBody(value string) (string, error) {
	if strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && r != '\r' && r != '\n' && r != '\t'
	}) >= 0 {
		return "", fmt.Errorf("smtp body contains an unexpected control character")
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n"), nil
}

func isLocalHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
