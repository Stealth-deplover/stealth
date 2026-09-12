package config

import (
	"os"
	"strings"
)

// secretSettings owns the credential material used by function encryption and
// first-owner bootstrap. These values deliberately remain optional while
// loading so tests and embedded callers can build partial Config values; the
// production entrypoints enforce their required security gates separately.
type secretSettings struct {
	functionsSecretKey []byte
	bootstrapCLIKey    []byte
	githubAppClientID  string
}

func loadSecretSettings() (secretSettings, error) {
	functionsSecretKey, err := loadOptionalSecretKey("FUNCTIONS_SECRET_KEY")
	if err != nil {
		return secretSettings{}, err
	}
	bootstrapCLIKey, err := loadOptionalSecretKey("BOOTSTRAP_CLI_KEY")
	if err != nil {
		return secretSettings{}, err
	}
	return secretSettings{
		functionsSecretKey: functionsSecretKey,
		bootstrapCLIKey:    bootstrapCLIKey,
		githubAppClientID:  strings.TrimSpace(os.Getenv("GITHUB_APP_CLIENT_ID")),
	}, nil
}

func loadOptionalSecretKey(name string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}
	return decodeSecretKey(raw, name)
}

func (s secretSettings) apply(c *Config) {
	c.FunctionsSecretKey = cloneBytes(s.functionsSecretKey)
	c.BootstrapCLIKey = cloneBytes(s.bootstrapCLIKey)
	c.GitHubAppClientID = s.githubAppClientID
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
