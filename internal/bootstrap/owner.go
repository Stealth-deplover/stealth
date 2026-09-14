package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/githubauth"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/setuphandoff"
	"github.com/Stealth-deplover/stealth/internal/validate"
	"github.com/google/uuid"
)

var ErrInvalidGitHubIdentity = errors.New("invalid GitHub identity")

// GitHubAuthorization is the provider-neutral proof needed to establish the
// first Instance Owner. Web OAuth and legacy Device Flow adapters both map to
// this small shape before they reach the owner module.
type GitHubAuthorization struct {
	SessionID uuid.UUID
	CodeHash  []byte
}

type OwnerResult struct {
	Account      domain.Account
	SessionToken string
	HandoffToken string
}

// OwnerCreator owns first-owner normalization, session creation, handoff
// persistence, and the repository write. HTTP routes only decode callbacks
// and map its errors to transport responses.
type OwnerCreator struct {
	Store      repository.BootstrapStore
	Handoff    setuphandoff.Store
	SetupMode  bool
	SessionTTL time.Duration
	Now        func() time.Time
}

func (c OwnerCreator) CreateGitHubInstanceOwner(ctx context.Context, authorization GitHubAuthorization, user githubauth.User) (OwnerResult, error) {
	if c.Store == nil {
		return OwnerResult{}, errors.New("bootstrap store is not configured")
	}
	input, err := GitHubOwnerInput(user, authorization)
	if err != nil {
		return OwnerResult{}, fmt.Errorf("%w: %v", ErrInvalidGitHubIdentity, err)
	}
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		return OwnerResult{}, err
	}
	input.TokenHash = tokenHash
	input.SessionExpiresAt = now.Add(c.SessionTTL)
	handoffToken := ""
	if c.SetupMode && c.Handoff != nil {
		handoffToken, _, err = auth.NewSessionToken()
		if err != nil {
			return OwnerResult{}, err
		}
		handoffExpiresAt := now.Add(CodeLifetime)
		if handoffExpiresAt.After(input.SessionExpiresAt) {
			handoffExpiresAt = input.SessionExpiresAt
		}
		if err := c.Handoff.Save(ctx, handoffToken, token, handoffExpiresAt); err != nil {
			return OwnerResult{}, err
		}
	}
	account, err := c.Store.CreateGitHubInstanceOwner(ctx, input)
	if err != nil {
		if handoffToken != "" {
			_ = c.Handoff.Discard(ctx)
		}
		return OwnerResult{}, err
	}
	return OwnerResult{Account: account, SessionToken: token, HandoffToken: handoffToken}, nil
}

func GitHubOwnerInput(user githubauth.User, authorization GitHubAuthorization) (repository.GitHubOwnerInput, error) {
	login := strings.TrimSpace(user.Login)
	if user.ID <= 0 || login == "" || len(login) > 120 || strings.ContainsAny(login, "\x00\r\n") {
		return repository.GitHubOwnerInput{}, errors.New("invalid GitHub identity")
	}
	providerEmail := strings.TrimSpace(user.Email)
	if len(providerEmail) > 320 {
		providerEmail = ""
	}
	if providerEmail != "" {
		if normalized, err := validate.Email(providerEmail); err == nil {
			providerEmail = normalized
		} else {
			providerEmail = ""
		}
	}
	displayName := strings.TrimSpace(user.Name)
	if displayName == "" {
		displayName = login
	}
	avatarURL := strings.TrimSpace(user.AvatarURL)
	if len(displayName) > 240 || strings.ContainsAny(displayName, "\x00\r\n") || len(avatarURL) > 2048 || strings.ContainsAny(avatarURL, "\x00\r\n") {
		return repository.GitHubOwnerInput{}, errors.New("invalid GitHub identity metadata")
	}
	if avatarURL != "" {
		parsedAvatarURL, err := url.Parse(avatarURL)
		// GitHub commonly appends a cache/version query (for example, ?v=4)
		// to avatar URLs. It is display metadata, not a redirect target, so
		// keep safe HTTPS URLs while rejecting credentials and fragments.
		if err != nil || parsedAvatarURL.Scheme != "https" || parsedAvatarURL.Host == "" || parsedAvatarURL.User != nil || parsedAvatarURL.Fragment != "" {
			return repository.GitHubOwnerInput{}, errors.New("invalid GitHub avatar URL")
		}
	}
	accountID, err := uuid.NewV7()
	if err != nil {
		return repository.GitHubOwnerInput{}, fmt.Errorf("generate owner account ID: %w", err)
	}
	sessionID, err := uuid.NewV7()
	if err != nil {
		return repository.GitHubOwnerInput{}, fmt.Errorf("generate owner session ID: %w", err)
	}
	return repository.GitHubOwnerInput{
		BootstrapSessionID: authorization.SessionID,
		BootstrapCodeHash:  authorization.CodeHash,
		AccountID:          accountID,
		SessionID:          sessionID,
		ProviderUserID:     fmt.Sprintf("%d", user.ID),
		ProviderLogin:      login,
		ProviderEmail:      providerEmail,
		DisplayName:        displayName,
		AvatarURL:          avatarURL,
	}, nil
}
