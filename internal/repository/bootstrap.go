package repository

import (
	"context"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapRepository owns the persistence boundary for first-run instance
// bootstrap, GitHub Device Flow onboarding, and legacy owner adoption.
//
// Its transaction methods share only package-level audit and error adapters
// with the rest of persistence, so the setup flow has a clear ownership seam
// without changing its transaction semantics.
type BootstrapRepository struct {
	pool *pgxpool.Pool
}

type BootstrapStatus struct {
	SetupRequired bool
}

type BootstrapVerification struct {
	ID        uuid.UUID
	ExpiresAt time.Time
}

type BootstrapSessionInput struct {
	ID        uuid.UUID
	CodeHash  []byte
	ExpiresAt time.Time
}

type GitHubDeviceFlow struct {
	ID                   uuid.UUID
	CodeHash             []byte
	DeviceCodeCiphertext []byte
	UserCode             string
	VerificationURI      string
	ExpiresAt            time.Time
	GitHubExpiresAt      time.Time
	Interval             time.Duration
	NextPollAt           time.Time
	Status               string
}

type GitHubDeviceFlowInput struct {
	ID                   uuid.UUID
	CodeHash             []byte
	DeviceCodeCiphertext []byte
	UserCode             string
	VerificationURI      string
	ExpiresAt            time.Time
	GitHubExpiresAt      time.Time
	Interval             time.Duration
}

type GitHubOwnerInput struct {
	BootstrapSessionID uuid.UUID
	BootstrapCodeHash  []byte
	AccountID          uuid.UUID
	SessionID          uuid.UUID
	TokenHash          []byte
	SessionExpiresAt   time.Time
	ProviderUserID     string
	ProviderLogin      string
	ProviderEmail      string
	DisplayName        string
	AvatarURL          string
}

type BootstrapAdoptionAccount struct {
	ID            string
	Email         string
	Provider      string
	ProviderLogin string
	CreatedAt     time.Time
}

// BootstrapStore is the narrow persistence capability consumed by the HTTP
// bootstrap flow. It keeps first-run setup independently replaceable and
// testable instead of exposing the full repository surface.
type BootstrapStore interface {
	BootstrapStatus(context.Context) (BootstrapStatus, error)
	CreateBootstrapSession(context.Context, BootstrapSessionInput) error
	VerifyBootstrapCode(context.Context, []byte) (BootstrapVerification, error)
	StartGitHubDeviceFlow(context.Context, GitHubDeviceFlowInput) (GitHubDeviceFlow, error)
	ClaimGitHubDevicePoll(context.Context, uuid.UUID) (GitHubDeviceFlow, bool, error)
	UpdateGitHubDeviceFlow(context.Context, uuid.UUID, string, time.Duration, time.Time) error
	CreateGitHubInstanceOwner(context.Context, GitHubOwnerInput) (domain.Account, error)
	ListBootstrapAdoptionAccounts(context.Context) ([]BootstrapAdoptionAccount, error)
	AdoptInstanceOwner(context.Context, uuid.UUID) error
}

var _ BootstrapStore = (*BootstrapRepository)(nil)
