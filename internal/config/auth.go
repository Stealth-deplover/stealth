package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// authSettings owns browser-session, auth-rate-limit, CORS, and email
// delivery configuration. Keeping these values together makes the security
// boundary explicit while Config remains the immutable application snapshot
// consumed by the API and worker.
type authSettings struct {
	sessionCookieName          string
	sessionTTL                 time.Duration
	appSessionTTL              time.Duration
	authVerificationTTL        time.Duration
	authPasswordResetTTL       time.Duration
	publicAppURL               string
	consoleCORSOrigins         []string
	emailDeliveryMode          string
	smtpHost                   string
	smtpPort                   int
	smtpUsername               string
	smtpPassword               string
	smtpFrom                   string
	cookieSecure               bool
	authRateLimit              int
	authRateWindow             time.Duration
	projectOperationRateLimit  int
	projectOperationRateWindow time.Duration
}

func loadAuthSettings() (authSettings, error) {
	sessionTTL, err := time.ParseDuration(value("SESSION_TTL", "720h"))
	if err != nil || sessionTTL <= 0 {
		return authSettings{}, fmt.Errorf("SESSION_TTL must be a positive duration")
	}
	appSessionTTL, err := time.ParseDuration(value("APP_SESSION_TTL", "720h"))
	if err != nil || appSessionTTL <= 0 || appSessionTTL > 720*time.Hour {
		return authSettings{}, fmt.Errorf("APP_SESSION_TTL must be a positive duration no longer than 720h")
	}
	verificationTTL, err := time.ParseDuration(value("AUTH_VERIFICATION_TTL", "24h"))
	if err != nil || verificationTTL <= 0 || verificationTTL > 7*24*time.Hour {
		return authSettings{}, fmt.Errorf("AUTH_VERIFICATION_TTL must be a positive duration no longer than 168h")
	}
	passwordResetTTL, err := time.ParseDuration(value("AUTH_PASSWORD_RESET_TTL", "1h"))
	if err != nil || passwordResetTTL <= 0 || passwordResetTTL > 24*time.Hour {
		return authSettings{}, fmt.Errorf("AUTH_PASSWORD_RESET_TTL must be a positive duration no longer than 24h")
	}
	publicAppURL := value("PUBLIC_APP_URL", "http://localhost:4173")
	if !isPublicAppURL(publicAppURL) {
		return authSettings{}, fmt.Errorf("PUBLIC_APP_URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	consoleCORSOrigins, err := parseConsoleCORSOrigins(os.Getenv("CONSOLE_CORS_ORIGINS"))
	if err != nil {
		return authSettings{}, err
	}
	emailDeliveryMode := strings.ToLower(value("EMAIL_DELIVERY_MODE", "disabled"))
	if emailDeliveryMode != "disabled" && emailDeliveryMode != "log" && emailDeliveryMode != "smtp" {
		return authSettings{}, fmt.Errorf("EMAIL_DELIVERY_MODE must be disabled, log, or smtp")
	}
	smtpHost := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	smtpPort, err := strconv.Atoi(value("SMTP_PORT", "587"))
	if err != nil || smtpPort < 1 || smtpPort > 65535 {
		return authSettings{}, fmt.Errorf("SMTP_PORT must be an integer between 1 and 65535")
	}
	smtpFrom := strings.TrimSpace(os.Getenv("SMTP_FROM"))
	if emailDeliveryMode == "smtp" && (smtpHost == "" || !isACMEEmail(smtpFrom)) {
		return authSettings{}, fmt.Errorf("SMTP_HOST and SMTP_FROM must be configured when EMAIL_DELIVERY_MODE is smtp")
	}
	cookieSecure, err := strconv.ParseBool(value("COOKIE_SECURE", "false"))
	if err != nil {
		return authSettings{}, fmt.Errorf("COOKIE_SECURE must be true or false")
	}
	authRateLimit, err := strconv.Atoi(value("AUTH_RATE_LIMIT", "10"))
	if err != nil || authRateLimit < 1 || authRateLimit > 1000 {
		return authSettings{}, fmt.Errorf("AUTH_RATE_LIMIT must be an integer between 1 and 1000")
	}
	authRateWindow, err := time.ParseDuration(value("AUTH_RATE_WINDOW", "1m"))
	if err != nil || authRateWindow <= 0 || authRateWindow > time.Hour {
		return authSettings{}, fmt.Errorf("AUTH_RATE_WINDOW must be a positive duration no longer than 1h")
	}
	projectOperationRateLimit, err := strconv.Atoi(value("PROJECT_OPERATION_RATE_LIMIT", "120"))
	if err != nil || projectOperationRateLimit < 1 || projectOperationRateLimit > 10000 {
		return authSettings{}, fmt.Errorf("PROJECT_OPERATION_RATE_LIMIT must be an integer between 1 and 10000")
	}
	projectOperationRateWindow, err := time.ParseDuration(value("PROJECT_OPERATION_RATE_WINDOW", "1m"))
	if err != nil || projectOperationRateWindow <= 0 || projectOperationRateWindow > time.Hour {
		return authSettings{}, fmt.Errorf("PROJECT_OPERATION_RATE_WINDOW must be a positive duration no longer than 1h")
	}
	return authSettings{
		sessionCookieName:          value("SESSION_COOKIE_NAME", "stealth_session"),
		sessionTTL:                 sessionTTL,
		appSessionTTL:              appSessionTTL,
		authVerificationTTL:        verificationTTL,
		authPasswordResetTTL:       passwordResetTTL,
		publicAppURL:               strings.TrimRight(publicAppURL, "/"),
		consoleCORSOrigins:         consoleCORSOrigins,
		emailDeliveryMode:          emailDeliveryMode,
		smtpHost:                   smtpHost,
		smtpPort:                   smtpPort,
		smtpUsername:               strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		smtpPassword:               os.Getenv("SMTP_PASSWORD"),
		smtpFrom:                   smtpFrom,
		cookieSecure:               cookieSecure,
		authRateLimit:              authRateLimit,
		authRateWindow:             authRateWindow,
		projectOperationRateLimit:  projectOperationRateLimit,
		projectOperationRateWindow: projectOperationRateWindow,
	}, nil
}

func (s authSettings) apply(c *Config) {
	c.SessionCookieName = s.sessionCookieName
	c.SessionTTL = s.sessionTTL
	c.AppSessionTTL = s.appSessionTTL
	c.AuthVerificationTTL = s.authVerificationTTL
	c.AuthPasswordResetTTL = s.authPasswordResetTTL
	c.PublicAppURL = s.publicAppURL
	c.ConsoleCORSOrigins = s.consoleCORSOrigins
	c.EmailDeliveryMode = s.emailDeliveryMode
	c.SMTPHost = s.smtpHost
	c.SMTPPort = s.smtpPort
	c.SMTPUsername = s.smtpUsername
	c.SMTPPassword = s.smtpPassword
	c.SMTPFrom = s.smtpFrom
	c.CookieSecure = s.cookieSecure
	c.AuthRateLimit = s.authRateLimit
	c.AuthRateWindow = s.authRateWindow
	c.ProjectOperationRateLimit = s.projectOperationRateLimit
	c.ProjectOperationRateWindow = s.projectOperationRateWindow
}

func (c *Config) applyAuthDefaults() {
	if c.SessionTTL <= 0 {
		c.SessionTTL = 720 * time.Hour
	}
	if strings.TrimSpace(c.SessionCookieName) == "" {
		c.SessionCookieName = "stealth_session"
	}
	if c.AppSessionTTL <= 0 {
		c.AppSessionTTL = c.SessionTTL
	}
	if c.AuthRateLimit <= 0 {
		c.AuthRateLimit = 10
	}
	if c.AuthRateWindow <= 0 {
		c.AuthRateWindow = time.Minute
	}
	if c.ProjectOperationRateLimit <= 0 {
		c.ProjectOperationRateLimit = 120
	}
	if c.ProjectOperationRateWindow <= 0 {
		c.ProjectOperationRateWindow = time.Minute
	}
	if c.AuthVerificationTTL <= 0 {
		c.AuthVerificationTTL = 24 * time.Hour
	}
	if c.AuthPasswordResetTTL <= 0 {
		c.AuthPasswordResetTTL = time.Hour
	}
	if strings.TrimSpace(c.PublicAppURL) == "" {
		c.PublicAppURL = "http://localhost:4173"
	}
}
