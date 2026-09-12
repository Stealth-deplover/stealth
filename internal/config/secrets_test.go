package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadSecretSettings(t *testing.T) {
	functionsKey := []byte(strings.Repeat("f", 32))
	bootstrapKey := []byte(strings.Repeat("b", 32))
	t.Setenv("FUNCTIONS_SECRET_KEY", base64.StdEncoding.EncodeToString(functionsKey))
	t.Setenv("BOOTSTRAP_CLI_KEY", base64.StdEncoding.EncodeToString(bootstrapKey))
	t.Setenv("GITHUB_APP_CLIENT_ID", "  Iv1.test-client-id  ")

	settings, err := loadSecretSettings()
	if err != nil {
		t.Fatal(err)
	}
	if string(settings.functionsSecretKey) != string(functionsKey) || string(settings.bootstrapCLIKey) != string(bootstrapKey) || settings.githubAppClientID != "Iv1.test-client-id" {
		t.Fatal("secret settings were not decoded and normalized as expected")
	}

	var config Config
	settings.apply(&config)
	config.FunctionsSecretKey[0] = 'x'
	config.BootstrapCLIKey[0] = 'x'
	if settings.functionsSecretKey[0] == 'x' || settings.bootstrapCLIKey[0] == 'x' {
		t.Fatal("applying secret settings did not clone key material")
	}
}

func TestLoadOptionalSecretKeyRejectsInvalidValue(t *testing.T) {
	t.Setenv("FUNCTIONS_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte("too short")))
	if _, err := loadSecretSettings(); err == nil || !strings.Contains(err.Error(), "FUNCTIONS_SECRET_KEY") {
		t.Fatalf("invalid function secret key returned %v", err)
	}

	t.Setenv("FUNCTIONS_SECRET_KEY", "")
	t.Setenv("BOOTSTRAP_CLI_KEY", "not-base64")
	if _, err := loadSecretSettings(); err == nil || !strings.Contains(err.Error(), "BOOTSTRAP_CLI_KEY") {
		t.Fatalf("invalid bootstrap key returned %v", err)
	}
}
