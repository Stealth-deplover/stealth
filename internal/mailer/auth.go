package mailer

import (
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
func NewAuthMessage(to string, kind AuthEmailKind, link AuthLink, ttl time.Duration) (Message, error) {
	if err := validHeaderValue(to, "recipient"); err != nil {
		return Message{}, err
	}
	if strings.TrimSpace(to) == "" {
		return Message{}, errors.New("recipient is required")
	}
	if link.value == "" {
		return Message{}, errors.New("auth link is required")
	}

	subject, action, ok := authEmailCopy(kind)
	if !ok {
		return Message{}, errors.New("unknown authentication email kind")
	}
	body := "Use the following one-time link to complete " + action + ":\n\n" + link.value + "\n\nThis link expires in " + ttl.String() + " and can only be used once. If you did not request this, you can ignore this email."
	return Message{To: to, Subject: subject, TextBody: body}, nil
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
