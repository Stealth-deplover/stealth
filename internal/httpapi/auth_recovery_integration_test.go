package httpapi_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/mailer"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/ratelimit"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type recordingMailer struct {
	mu       sync.Mutex
	messages []mailer.Message
}

func (m *recordingMailer) Send(_ context.Context, message mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, message)
	return nil
}

func (m *recordingMailer) latestFor(subject string, projectID string) (mailer.Message, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := len(m.messages) - 1; index >= 0; index-- {
		message := m.messages[index]
		if message.Subject != subject {
			continue
		}
		parsed := messageLink(message.TextBody)
		if projectID == "" || parsed.Query().Get("project_id") == projectID {
			return message, true
		}
	}
	return mailer.Message{}, false
}

func messageLink(body string) *url.URL {
	for _, field := range strings.Fields(body) {
		parsed, err := url.Parse(field)
		if err == nil && parsed.Query().Get("token") != "" {
			return parsed
		}
	}
	return &url.URL{}
}

func TestAuthVerificationAndRecoveryIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingMailer{}
	server := httptestNewAuthServer(t, pool, recorder)
	defer server.Close()

	console := newIntegrationClient(t)
	identifier := uuid.Must(uuid.NewV7()).String()
	email := "auth-recovery-" + identifier + "@example.test"
	password := "correct-horse-battery-staple"
	newPassword := "new-correct-horse-battery"
	var registration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, console, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": email, "password": password}, http.StatusCreated, &registration)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, registration.Account.ID, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})

	verification, ok := recorder.latestFor("Verify your Stealth email", "")
	if !ok {
		t.Fatal("registration did not enqueue a Console verification email")
	}
	verificationToken := messageLink(verification.TextBody).Query().Get("token")
	if verificationToken == "" {
		t.Fatal("verification email did not contain a tokenized link")
	}
	var verified struct {
		Account struct {
			EmailVerified bool `json:"email_verified"`
		} `json:"account"`
	}
	requestJSON(t, console, http.MethodPut, server.URL+"/v1/account/verification", map[string]string{"token": verificationToken}, http.StatusOK, &verified)
	if !verified.Account.EmailVerified {
		t.Fatal("account remained unverified after token confirmation")
	}
	requestJSON(t, console, http.MethodPut, server.URL+"/v1/account/verification", map[string]string{"token": verificationToken}, http.StatusUnprocessableEntity, nil)

	requestJSON(t, console, http.MethodPost, server.URL+"/v1/account/recovery", map[string]string{"email": email}, http.StatusAccepted, nil)
	recovery, ok := recorder.latestFor("Reset your Stealth password", "")
	if !ok {
		t.Fatal("recovery request did not enqueue an email")
	}
	recoveryToken := messageLink(recovery.TextBody).Query().Get("token")
	requestJSON(t, console, http.MethodPut, server.URL+"/v1/account/recovery", map[string]string{"token": recoveryToken, "password": newPassword}, http.StatusNoContent, nil)
	requestJSON(t, console, http.MethodGet, server.URL+"/v1/account", nil, http.StatusUnauthorized, nil)
	requestJSON(t, console, http.MethodPost, server.URL+"/v1/sessions/email-password", map[string]string{"email": email, "password": newPassword}, http.StatusNoContent, nil)

	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, console, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{"name": "auth-" + identifier[:8]}, http.StatusCreated, &project)
	projectURL := server.URL + "/v1/projects/" + project.Project.ID
	requestJSON(t, console, http.MethodPatch, projectURL+"/auth/settings", map[string]any{"registration_enabled": true}, http.StatusOK, nil)

	app := newIntegrationClient(t)
	appEmail := "project-recovery-" + identifier + "@example.test"
	requestJSON(t, app, http.MethodPost, projectURL+"/account/registrations", map[string]string{"email": appEmail, "password": password}, http.StatusCreated, nil)
	projectVerification, ok := recorder.latestFor("Verify your email", project.Project.ID)
	if !ok {
		t.Fatal("project registration did not enqueue a verification email")
	}
	projectVerificationToken := messageLink(projectVerification.TextBody).Query().Get("token")
	var projectVerified struct {
		Account struct {
			EmailVerified bool `json:"email_verified"`
		} `json:"account"`
	}
	requestJSON(t, app, http.MethodPut, projectURL+"/account/verification", map[string]string{"token": projectVerificationToken}, http.StatusOK, &projectVerified)
	if !projectVerified.Account.EmailVerified {
		t.Fatal("project identity remained unverified")
	}

	requestJSON(t, app, http.MethodPost, projectURL+"/account/recovery", map[string]string{"email": appEmail}, http.StatusAccepted, nil)
	projectRecovery, ok := recorder.latestFor("Reset your password", project.Project.ID)
	if !ok {
		t.Fatal("project recovery request did not enqueue an email")
	}
	projectRecoveryToken := messageLink(projectRecovery.TextBody).Query().Get("token")
	requestJSON(t, app, http.MethodPut, projectURL+"/account/recovery", map[string]string{"token": projectRecoveryToken, "password": newPassword}, http.StatusNoContent, nil)
	requestJSON(t, app, http.MethodGet, projectURL+"/account", nil, http.StatusUnauthorized, nil)
	requestJSON(t, app, http.MethodPost, projectURL+"/sessions/email-password", map[string]string{"email": appEmail, "password": newPassword}, http.StatusNoContent, nil)
}

func httptestNewAuthServer(t *testing.T, pool *pgxpool.Pool, sender mailer.Sender) *httptest.Server {
	t.Helper()
	return httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		StorageRoot:          t.TempDir(),
		FunctionsSecretKey:   bytes.Repeat([]byte{0x41}, 32),
		SessionCookieName:    "stealth_session",
		SessionTTL:           time.Hour,
		AppSessionTTL:        time.Hour,
		AuthVerificationTTL:  time.Hour,
		AuthPasswordResetTTL: time.Hour,
		PublicAppURL:         "https://console.example.test",
		AuthRateLimit:        100,
		AuthRateWindow:       time.Minute,
	}, repository.New(pool), slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Dependencies{AuthLimiter: ratelimit.NoopLimiter{}, EmailSender: sender}))
}
