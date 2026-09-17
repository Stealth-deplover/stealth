package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/githubauth"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/setuphandoff"
	"github.com/google/uuid"
)

type ownerStoreFake struct {
	input   repository.GitHubOwnerInput
	account domain.Account
	err     error
}

func (*ownerStoreFake) BootstrapStatus(context.Context) (repository.BootstrapStatus, error) {
	return repository.BootstrapStatus{SetupRequired: true}, nil
}
func (*ownerStoreFake) CreateBootstrapSession(context.Context, repository.BootstrapSessionInput) error {
	return nil
}
func (*ownerStoreFake) VerifyBootstrapCode(context.Context, []byte) (repository.BootstrapVerification, error) {
	return repository.BootstrapVerification{}, nil
}
func (*ownerStoreFake) StartGitHubDeviceFlow(context.Context, repository.GitHubDeviceFlowInput) (repository.GitHubDeviceFlow, error) {
	return repository.GitHubDeviceFlow{}, nil
}
func (*ownerStoreFake) ClaimGitHubDevicePoll(context.Context, uuid.UUID) (repository.GitHubDeviceFlow, bool, error) {
	return repository.GitHubDeviceFlow{}, false, nil
}
func (*ownerStoreFake) UpdateGitHubDeviceFlow(context.Context, uuid.UUID, string, time.Duration, time.Time) error {
	return nil
}
func (f *ownerStoreFake) CreateGitHubInstanceOwner(_ context.Context, input repository.GitHubOwnerInput) (domain.Account, error) {
	f.input = input
	if f.err != nil {
		return domain.Account{}, f.err
	}
	return f.account, nil
}
func (*ownerStoreFake) ListBootstrapAdoptionAccounts(context.Context) ([]repository.BootstrapAdoptionAccount, error) {
	return nil, nil
}
func (*ownerStoreFake) AdoptInstanceOwner(context.Context, uuid.UUID) error { return nil }

type ownerHandoffFake struct {
	saved   bool
	discard bool
}

func (f *ownerHandoffFake) Save(context.Context, string, string, time.Time) error {
	f.saved = true
	return nil
}
func (*ownerHandoffFake) Issue(context.Context) (string, error)           { return "", nil }
func (*ownerHandoffFake) Consume(context.Context, string) (string, error) { return "", nil }
func (f *ownerHandoffFake) Discard(context.Context) error {
	f.discard = true
	return nil
}

var _ repository.BootstrapStore = (*ownerStoreFake)(nil)
var _ setuphandoff.Store = (*ownerHandoffFake)(nil)

func TestGitHubOwnerInputNormalizesProviderIdentityBehindBootstrapModule(t *testing.T) {
	sessionID := uuid.Must(uuid.NewV7())
	codeHash := []byte("bootstrap-hash")
	input, err := GitHubOwnerInput(githubauth.User{
		ID:        424242,
		Login:     " stealth-owner ",
		Email:     "owner@example.test",
		Name:      "Stealth Owner",
		AvatarURL: "https://avatars.githubusercontent.com/u/424242?v=4",
	}, GitHubAuthorization{SessionID: sessionID, CodeHash: codeHash})
	if err != nil {
		t.Fatal(err)
	}
	if input.BootstrapSessionID != sessionID || string(input.BootstrapCodeHash) != string(codeHash) || input.ProviderUserID != "424242" || input.ProviderEmail != "owner@example.test" {
		t.Fatalf("owner input = %#v", input)
	}
}

func TestOwnerCreatorCreatesSessionAndCleansHandoffOnRepositoryFailure(t *testing.T) {
	store := &ownerStoreFake{err: errors.New("owner conflict")}
	handoff := &ownerHandoffFake{}
	creator := OwnerCreator{
		Store:      store,
		Handoff:    handoff,
		SetupMode:  true,
		SessionTTL: time.Hour,
		Now:        func() time.Time { return time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC) },
	}
	_, err := creator.CreateGitHubInstanceOwner(context.Background(), GitHubAuthorization{SessionID: uuid.Must(uuid.NewV7()), CodeHash: []byte("hash")}, githubauth.User{ID: 7, Login: "owner"})
	if err == nil || !handoff.saved || !handoff.discard {
		t.Fatalf("owner creation error = %v, handoff = %#v", err, handoff)
	}
	if len(store.input.TokenHash) == 0 || store.input.ProviderLogin != "owner" {
		t.Fatalf("repository input = %#v", store.input)
	}
}

func TestOwnerCreatorClassifiesInvalidProviderIdentity(t *testing.T) {
	creator := OwnerCreator{Store: &ownerStoreFake{}, SessionTTL: time.Hour}
	_, err := creator.CreateGitHubInstanceOwner(context.Background(), GitHubAuthorization{}, githubauth.User{ID: 7, Login: "unsafe\nlogin"})
	if !errors.Is(err, ErrInvalidGitHubIdentity) {
		t.Fatalf("invalid identity error = %v", err)
	}
}
