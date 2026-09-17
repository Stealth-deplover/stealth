package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAuthSettings(t *testing.T) {
	t.Setenv("SESSION_COOKIE_NAME", "stealth_test")
	t.Setenv("SESSION_TTL", "48h")
	t.Setenv("APP_SESSION_TTL", "30m")
	t.Setenv("AUTH_VERIFICATION_TTL", "12h")
	t.Setenv("AUTH_PASSWORD_RESET_TTL", "90m")
	t.Setenv("PUBLIC_APP_URL", "https://console.example.test/auth/")
	t.Setenv("CONSOLE_CORS_ORIGINS", "http://localhost:5173,https://console.example.test:443")
	t.Setenv("EMAIL_DELIVERY_MODE", "smtp")
	t.Setenv("SMTP_HOST", "smtp.example.test")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "secret")
	t.Setenv("SMTP_FROM", "no-reply@example.test")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("AUTH_RATE_LIMIT", "20")
	t.Setenv("AUTH_RATE_WINDOW", "2m")
	t.Setenv("PROJECT_OPERATION_RATE_LIMIT", "240")
	t.Setenv("PROJECT_OPERATION_RATE_WINDOW", "2m")

	settings, err := loadAuthSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.sessionCookieName != "stealth_test" || settings.sessionTTL != 48*time.Hour || settings.appSessionTTL != 30*time.Minute || settings.publicAppURL != "https://console.example.test/auth" || settings.smtpPort != 2525 || !settings.cookieSecure || settings.authRateLimit != 20 || settings.projectOperationRateLimit != 240 {
		t.Fatalf("unexpected auth settings: %+v", settings)
	}
	if len(settings.consoleCORSOrigins) != 2 || settings.consoleCORSOrigins[1] != "https://console.example.test" {
		t.Fatalf("unexpected CORS origins: %#v", settings.consoleCORSOrigins)
	}
	var config Config
	settings.apply(&config)
	if config.SMTPPassword != "secret" || config.PublicAppURL != settings.publicAppURL {
		t.Fatalf("settings were not applied: %+v", config)
	}
}

func TestLoadAuthSettingsRejectsInvalidDeliveryMode(t *testing.T) {
	t.Setenv("EMAIL_DELIVERY_MODE", "unknown")
	if _, err := loadAuthSettings(); err == nil || !strings.Contains(err.Error(), "EMAIL_DELIVERY_MODE") {
		t.Fatalf("invalid delivery mode returned %v", err)
	}
}

func TestApplyAuthDefaults(t *testing.T) {
	config := Config{}
	config.applyAuthDefaults()
	if config.SessionCookieName != "stealth_session" || config.SessionTTL != 720*time.Hour || config.AppSessionTTL != 720*time.Hour || config.AuthVerificationTTL != 24*time.Hour || config.AuthPasswordResetTTL != time.Hour || config.AuthRateLimit != 10 || config.ProjectOperationRateLimit != 120 || config.PublicAppURL != "http://localhost:4173" {
		t.Fatalf("unexpected auth defaults: %+v", config)
	}
}
