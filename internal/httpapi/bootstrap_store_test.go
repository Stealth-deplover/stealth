package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type bootstrapStoreFake struct {
	status      repository.BootstrapStatus
	statusCalls int
}

func (f *bootstrapStoreFake) BootstrapStatus(context.Context) (repository.BootstrapStatus, error) {
	f.statusCalls++
	return f.status, nil
}

func (*bootstrapStoreFake) CreateBootstrapSession(context.Context, repository.BootstrapSessionInput) error {
	return nil
}

func (*bootstrapStoreFake) VerifyBootstrapCode(context.Context, []byte) (repository.BootstrapVerification, error) {
	return repository.BootstrapVerification{}, nil
}

func (*bootstrapStoreFake) StartGitHubDeviceFlow(context.Context, repository.GitHubDeviceFlowInput) (repository.GitHubDeviceFlow, error) {
	return repository.GitHubDeviceFlow{}, nil
}

func (*bootstrapStoreFake) ClaimGitHubDevicePoll(context.Context, uuid.UUID) (repository.GitHubDeviceFlow, bool, error) {
	return repository.GitHubDeviceFlow{}, false, nil
}

func (*bootstrapStoreFake) UpdateGitHubDeviceFlow(context.Context, uuid.UUID, string, time.Duration, time.Time) error {
	return nil
}

func (*bootstrapStoreFake) CreateGitHubInstanceOwner(context.Context, repository.GitHubOwnerInput) (domain.Account, error) {
	return domain.Account{}, nil
}

func (*bootstrapStoreFake) ListBootstrapAdoptionAccounts(context.Context) ([]repository.BootstrapAdoptionAccount, error) {
	return nil, nil
}

func (*bootstrapStoreFake) AdoptInstanceOwner(context.Context, uuid.UUID) error {
	return nil
}

var _ repository.BootstrapStore = (*bootstrapStoreFake)(nil)

func TestBootstrapHTTPUsesInjectedCapability(t *testing.T) {
	store := &bootstrapStoreFake{status: repository.BootstrapStatus{SetupRequired: true}}
	handler := NewWithDependencies(config.Config{}, nil, slog.Default(), Dependencies{BootstrapStore: store})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/bootstrap/status", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if store.statusCalls != 1 {
		t.Fatalf("bootstrap store status calls = %d, want 1", store.statusCalls)
	}
}
