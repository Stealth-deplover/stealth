package validate

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

func Email(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(normalized)
	if err != nil || address.Address != normalized || len(normalized) > 320 {
		return "", fmt.Errorf("email must be a valid address")
	}
	return normalized, nil
}

func Name(value, field string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 2 || len(trimmed) > 120 {
		return "", fmt.Errorf("%s must be between 2 and 120 characters", field)
	}
	return trimmed, nil
}

func Slug(value, field string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if !slugPattern.MatchString(normalized) {
		return "", fmt.Errorf("%s must use lowercase letters, numbers, and hyphens and be 2 to 63 characters", field)
	}
	return normalized, nil
}
