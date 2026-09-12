package mailer

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// AuthEmailKind identifies one of the fixed security messages Stealth sends.
// Keeping the copy and action in this package prevents request handlers from
// turning an authentication message into an arbitrary email template.
type AuthEmailKind uint8

const (
	AuthEmailAccountVerification AuthEmailKind = iota + 1
	AuthEmailProjectUserVerification
	AuthEmailAccountPasswordReset
	AuthEmailProjectUserPasswordReset
	AuthEmailOrganizationInvitation
)

// AuthLink is a validated absolute HTTP(S) link produced by the server. Its
// value is intentionally private so callers cannot assemble an auth message
// from an arbitrary body or an unvalidated URL string.
type AuthLink struct{ value string }

// AuthMessage is a security email assembled from a fixed Stealth-owned
// template. Its transport payload is private so HTTP handlers cannot provide
// an arbitrary authentication email body.
type AuthMessage struct{ message Message }

// AuthSender is the typed delivery boundary for authentication emails. The
// generic Sender remains available to project messaging, where user-authored
// message bodies are an explicit product feature.
type AuthSender interface {
	SendAuth(context.Context, AuthMessage) error
}

type authSender struct{ sender Sender }

// NewAuthSender adapts the generic delivery transport to the constrained
// authentication-email boundary.
func NewAuthSender(sender Sender) AuthSender {
	if sender == nil {
		return nil
	}
	return authSender{sender: sender}
}

func (s authSender) SendAuth(ctx context.Context, message AuthMessage) error {
	if s.sender == nil {
		return ErrDisabled
	}
	if err := validateAuthMessage(message); err != nil {
		return err
	}
	return s.sender.Send(ctx, message.message)
}

func validateAuthMessage(message AuthMessage) error {
	if err := validHeaderValue(message.message.To, "recipient"); err != nil {
		return err
	}
	if strings.TrimSpace(message.message.To) == "" {
		return errors.New("recipient is required")
	}
	if err := validHeaderValue(message.message.Subject, "subject"); err != nil {
		return err
	}
	if strings.TrimSpace(message.message.Subject) == "" {
		return errors.New("subject is required")
	}
	if strings.TrimSpace(message.message.TextBody) == "" {
		return errors.New("authentication email body is invalid")
	}
	if _, err := normalizeTextBody(message.message.TextBody); err != nil {
		return err
	}
	return nil
}

// NewAuthLink validates the final link at the mailer boundary. The origin and
// route policy is enforced by the HTTP layer; this check ensures that even a
// future caller cannot put a control character, fragment, credential, or
// non-HTTP URL into an authentication message.
func NewAuthLink(raw string) (AuthLink, error) {
	if raw == "" || len(raw) > 2048 || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return AuthLink{}, errors.New("auth link must be a non-empty URL without control characters")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return AuthLink{}, errors.New("auth link must be an absolute HTTP(S) URL without credentials or a fragment")
	}
	return AuthLink{value: parsed.String()}, nil
}

func (l AuthLink) String() string { return l.value }

// NewAuthMessage builds a plain-text security email from fixed Stealth-owned
// copy plus the server-generated link. There is deliberately no body or
// purpose string parameter.
func NewAuthMessage(to string, kind AuthEmailKind, link AuthLink, ttl time.Duration) (AuthMessage, error) {
	if err := validHeaderValue(to, "recipient"); err != nil {
		return AuthMessage{}, err
	}
	if strings.TrimSpace(to) == "" {
		return AuthMessage{}, errors.New("recipient is required")
	}
	if link.value == "" {
		return AuthMessage{}, errors.New("auth link is required")
	}

	subject, action, ok := authEmailCopy(kind)
	if !ok {
		return AuthMessage{}, errors.New("unknown authentication email kind")
	}
	body := "Use the following one-time link to complete " + action + ":\n\n" + link.value + "\n\nThis link expires in " + ttl.String() + " and can only be used once. If you did not request this, you can ignore this email."
	message := AuthMessage{message: Message{To: to, Subject: subject, TextBody: body}}
	if err := validateAuthMessage(message); err != nil {
		return AuthMessage{}, err
	}
	return message, nil
}

func authEmailCopy(kind AuthEmailKind) (subject, action string, ok bool) {
	switch kind {
	case AuthEmailAccountVerification:
		return "Verify your Stealth email", "email verification", true
	case AuthEmailProjectUserVerification:
		return "Verify your email", "project user email verification", true
	case AuthEmailAccountPasswordReset:
		return "Reset your Stealth password", "password recovery", true
	case AuthEmailProjectUserPasswordReset:
		return "Reset your password", "project user password recovery", true
	case AuthEmailOrganizationInvitation:
		return "You are invited to a Stealth organization", "organization invitation", true
	default:
		return "", "", false
	}
}
