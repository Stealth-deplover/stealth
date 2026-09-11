package repository

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type SignupInput struct {
	AccountID, OrganizationID, SessionID                    uuid.UUID
	Email, PasswordHash, OrganizationName, OrganizationSlug string
	TokenHash                                               []byte
	SessionExpiresAt                                        time.Time
}

func (r *Repository) Signup(ctx context.Context, input SignupInput) (domain.Account, domain.Organization, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireBootstrapSealedTx(ctx, tx); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	account := domain.Account{ID: input.AccountID.String(), Email: input.Email, EmailVerified: false}
	organization := domain.Organization{ID: input.OrganizationID.String(), Name: input.OrganizationName, Slug: input.OrganizationSlug}
	if err := tx.QueryRow(ctx, `INSERT INTO accounts (id,email,password_hash) VALUES ($1,$2,$3) RETURNING created_at`, input.AccountID, input.Email, input.PasswordHash).Scan(&account.CreatedAt); err != nil {
		return domain.Account{}, domain.Organization{}, mapError(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3) RETURNING created_at`, input.OrganizationID, input.OrganizationName, input.OrganizationSlug).Scan(&organization.CreatedAt); err != nil {
		return domain.Account{}, domain.Organization{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_plans (organization_id) VALUES ($1)`, input.OrganizationID); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'owner')`, input.OrganizationID, input.AccountID); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sessions (id,account_id,token_hash,expires_at) VALUES ($1,$2,$3,$4)`, input.SessionID, input.AccountID, input.TokenHash, input.SessionExpiresAt); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	if err := writeAudit(ctx, tx, uuid.Nil, input.AccountID, "account.signup", "account", input.AccountID); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	if err := writeAudit(ctx, tx, input.OrganizationID, input.AccountID, "organization.create", "organization", input.OrganizationID); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	return account, organization, nil
}

func (r *Repository) AccountBySession(ctx context.Context, tokenHash []byte) (domain.Account, uuid.UUID, error) {
	var account domain.Account
	var sessionID uuid.UUID
	var email, instanceRole, provider, providerUserID, providerLogin, displayName, avatarURL sql.NullString
	err := r.pool.QueryRow(ctx, `
		SELECT a.id,a.email,a.email_verified,ir.role,
		       ai.provider,ai.provider_user_id,ai.provider_login,ai.display_name,ai.avatar_url,
		       a.created_at,s.id
		FROM sessions s
		JOIN accounts a ON a.id=s.account_id
		LEFT JOIN instance_roles ir ON ir.account_id=a.id
		LEFT JOIN account_identities ai ON ai.account_id=a.id AND ai.provider='github'
		WHERE s.token_hash=$1 AND s.expires_at > now()`, tokenHash).Scan(
		&account.ID, &email, &account.EmailVerified, &instanceRole,
		&provider, &providerUserID, &providerLogin, &displayName, &avatarURL,
		&account.CreatedAt, &sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, uuid.Nil, ErrNotFound
	}
	if email.Valid {
		account.Email = email.String
	}
	if instanceRole.Valid {
		account.InstanceRole = instanceRole.String
	}
	populateGitHubIdentity(&account, provider, providerUserID, providerLogin, displayName, avatarURL)
	return account, sessionID, err
}
func (r *Repository) AccountPassword(ctx context.Context, email string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var hash sql.NullString
	err := r.pool.QueryRow(ctx, `SELECT id,password_hash FROM accounts WHERE email=$1`, email).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", ErrNotFound
	}
	if err != nil {
		return uuid.Nil, "", err
	}
	// GitHub-only first owners intentionally have no local password. Treat
	// password login for those accounts like an unknown credential rather than
	// surfacing a database NULL-scan error or inventing a password hash.
	if !hash.Valid {
		return uuid.Nil, "", ErrNotFound
	}
	return id, hash.String, nil
}

func requireBootstrapSealedTx(ctx context.Context, tx pgx.Tx) error {
	var sealed bool
	err := tx.QueryRow(ctx, `
		SELECT (b.sealed_at IS NOT NULL OR EXISTS (
			SELECT 1 FROM instance_roles WHERE role = 'instance_owner'
		))
		FROM instance_bootstrap b
		WHERE b.id = TRUE
		FOR UPDATE`).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !sealed {
		return ErrBootstrapRequired
	}
	return nil
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

// BootstrapSession stores only the durable portion of a one-time setup
// session. The plaintext code is generated and returned by the HTTP layer but
// is never passed to this repository.
func (r *Repository) CreateBootstrapSession(ctx context.Context, input BootstrapSessionInput) error {
	if input.ID == uuid.Nil {
		return ErrInvalidBootstrapCode
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var sealed bool
	err = tx.QueryRow(ctx, `
		SELECT (b.sealed_at IS NOT NULL OR EXISTS (
			SELECT 1 FROM instance_roles WHERE role = 'instance_owner'
		))
		FROM instance_bootstrap b
		WHERE b.id = TRUE
		FOR UPDATE`).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if sealed {
		return ErrBootstrapSealed
	}
	if len(input.CodeHash) != 32 || input.ExpiresAt.Before(time.Now().UTC()) {
		return ErrInvalidBootstrapCode
	}
	if _, err := tx.Exec(ctx, `
		UPDATE bootstrap_sessions
		SET invalidated_at = now(),github_status='none',github_device_code_ciphertext=NULL,
		    github_user_code=NULL,github_verification_uri=NULL,github_expires_at=NULL,
		    github_interval_seconds=NULL,github_next_poll_at=NULL
		WHERE used_at IS NULL AND invalidated_at IS NULL`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bootstrap_sessions (id,code_hash,expires_at) VALUES ($1,$2,$3)`, input.ID, input.CodeHash, input.ExpiresAt); err != nil {
		return mapError(err)
	}
	if err := writeAuditMetadata(ctx, tx, uuid.Nil, uuid.Nil, "instance.bootstrap.session_created", "bootstrap_session", input.ID, map[string]any{"expires_at": input.ExpiresAt}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) BootstrapStatus(ctx context.Context) (BootstrapStatus, error) {
	var setupRequired bool
	err := r.pool.QueryRow(ctx, `
		SELECT b.sealed_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM instance_roles WHERE role = 'instance_owner')
		FROM instance_bootstrap b
		WHERE b.id = TRUE`).Scan(&setupRequired)
	if errors.Is(err, pgx.ErrNoRows) {
		return BootstrapStatus{}, ErrNotFound
	}
	return BootstrapStatus{SetupRequired: setupRequired}, err
}

// VerifyBootstrapCode checks the operator-provided code and returns only the
// opaque database session ID needed for the subsequent GitHub Device Flow.
// The setup code itself never leaves this request boundary in a response.
func (r *Repository) VerifyBootstrapCode(ctx context.Context, codeHash []byte) (BootstrapVerification, error) {
	if len(codeHash) != 32 {
		return BootstrapVerification{}, ErrInvalidBootstrapCode
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BootstrapVerification{}, err
	}
	defer tx.Rollback(ctx)

	var sealed bool
	err = tx.QueryRow(ctx, `
		SELECT (b.sealed_at IS NOT NULL OR EXISTS (
			SELECT 1 FROM instance_roles WHERE role = 'instance_owner'
		))
		FROM instance_bootstrap b
		WHERE b.id = TRUE
		FOR UPDATE`).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return BootstrapVerification{}, ErrNotFound
	}
	if err != nil {
		return BootstrapVerification{}, err
	}
	if sealed {
		return BootstrapVerification{}, ErrBootstrapSealed
	}

	var verification BootstrapVerification
	err = tx.QueryRow(ctx, `
		SELECT id,expires_at
		FROM bootstrap_sessions
		WHERE code_hash=$1 AND used_at IS NULL AND invalidated_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC
		LIMIT 1
		FOR UPDATE`, codeHash).Scan(&verification.ID, &verification.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return BootstrapVerification{}, ErrInvalidBootstrapCode
	}
	if err != nil {
		return BootstrapVerification{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BootstrapVerification{}, err
	}
	return verification, nil
}

// StartGitHubDeviceFlow persists only encrypted GitHub device state. A
// repeated click for the same verified setup session reuses the current
// pending device flow instead of creating multiple GitHub authorizations.
func (r *Repository) StartGitHubDeviceFlow(ctx context.Context, input GitHubDeviceFlowInput) (GitHubDeviceFlow, error) {
	requestedIntervalSeconds := int(input.Interval / time.Second)
	if len(input.CodeHash) != 32 || len(input.DeviceCodeCiphertext) == 0 || input.ID == uuid.Nil || input.ExpiresAt.Before(time.Now().UTC()) || input.GitHubExpiresAt.Before(time.Now().UTC()) || input.GitHubExpiresAt.After(input.ExpiresAt) || requestedIntervalSeconds < 1 || requestedIntervalSeconds > 300 {
		return GitHubDeviceFlow{}, ErrBootstrapDevice
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return GitHubDeviceFlow{}, err
	}
	defer tx.Rollback(ctx)
	var sealed bool
	if err := tx.QueryRow(ctx, `SELECT (sealed_at IS NOT NULL OR EXISTS (SELECT 1 FROM instance_roles WHERE role='instance_owner')) FROM instance_bootstrap WHERE id=TRUE FOR UPDATE`).Scan(&sealed); errors.Is(err, pgx.ErrNoRows) {
		return GitHubDeviceFlow{}, ErrNotFound
	} else if err != nil {
		return GitHubDeviceFlow{}, err
	} else if sealed {
		return GitHubDeviceFlow{}, ErrBootstrapSealed
	}
	var existing GitHubDeviceFlow
	var intervalSeconds int
	var githubExpiresAt, nextPollAt sql.NullTime
	var userCode, verificationURI sql.NullString
	err = tx.QueryRow(ctx, `
		SELECT id,code_hash,github_device_code_ciphertext,github_user_code,github_verification_uri,
		       expires_at,github_expires_at,COALESCE(github_interval_seconds,0),github_next_poll_at,github_status
		FROM bootstrap_sessions
		WHERE id=$1 AND code_hash=$2 AND used_at IS NULL AND invalidated_at IS NULL AND expires_at > now()
		FOR UPDATE`, input.ID, input.CodeHash).Scan(
		&existing.ID, &existing.CodeHash, &existing.DeviceCodeCiphertext, &userCode,
		&verificationURI, &existing.ExpiresAt, &githubExpiresAt,
		&intervalSeconds, &nextPollAt, &existing.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return GitHubDeviceFlow{}, ErrInvalidBootstrapCode
	}
	if err != nil {
		return GitHubDeviceFlow{}, err
	}
	if userCode.Valid {
		existing.UserCode = userCode.String
	}
	if verificationURI.Valid {
		existing.VerificationURI = verificationURI.String
	}
	if githubExpiresAt.Valid {
		existing.GitHubExpiresAt = githubExpiresAt.Time
	}
	if nextPollAt.Valid {
		existing.NextPollAt = nextPollAt.Time
	}
	existing.Interval = time.Duration(intervalSeconds) * time.Second
	if existing.Status == "pending" && existing.GitHubExpiresAt.After(time.Now().UTC()) {
		if err := tx.Commit(ctx); err != nil {
			return GitHubDeviceFlow{}, err
		}
		return existing, nil
	}
	pollAt := time.Now().UTC().Add(input.Interval)
	if _, err := tx.Exec(ctx, `
		UPDATE bootstrap_sessions
		SET github_device_code_ciphertext=$2,github_user_code=$3,github_verification_uri=$4,
		    github_expires_at=$5,github_interval_seconds=$6,github_next_poll_at=$7,github_status='pending'
		WHERE id=$1`, input.ID, input.DeviceCodeCiphertext, input.UserCode, input.VerificationURI,
		input.GitHubExpiresAt, requestedIntervalSeconds, pollAt); err != nil {
		return GitHubDeviceFlow{}, err
	}
	existing.CodeHash = append([]byte(nil), input.CodeHash...)
	existing.DeviceCodeCiphertext = append([]byte(nil), input.DeviceCodeCiphertext...)
	existing.UserCode = input.UserCode
	existing.VerificationURI = input.VerificationURI
	existing.GitHubExpiresAt = input.GitHubExpiresAt
	existing.Interval = input.Interval
	existing.NextPollAt = pollAt
	existing.Status = "pending"
	if err := writeAuditMetadata(ctx, tx, uuid.Nil, uuid.Nil, "instance.bootstrap.github.started", "bootstrap_session", input.ID, map[string]any{
		"expires_at":        input.ExpiresAt,
		"github_expires_at": input.GitHubExpiresAt,
	}); err != nil {
		return GitHubDeviceFlow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GitHubDeviceFlow{}, err
	}
	return existing, nil
}

// ClaimGitHubDevicePoll atomically reserves one provider poll. The caller
// must wait until allowed is true; this prevents a fast browser from bypassing
// GitHub's interval and causing slow_down responses.
func (r *Repository) ClaimGitHubDevicePoll(ctx context.Context, id uuid.UUID) (GitHubDeviceFlow, bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return GitHubDeviceFlow{}, false, err
	}
	defer tx.Rollback(ctx)
	var sealed bool
	if err := tx.QueryRow(ctx, `SELECT (sealed_at IS NOT NULL OR EXISTS (SELECT 1 FROM instance_roles WHERE role='instance_owner')) FROM instance_bootstrap WHERE id=TRUE FOR UPDATE`).Scan(&sealed); errors.Is(err, pgx.ErrNoRows) {
		return GitHubDeviceFlow{}, false, ErrNotFound
	} else if err != nil {
		return GitHubDeviceFlow{}, false, err
	} else if sealed {
		return GitHubDeviceFlow{}, false, ErrBootstrapSealed
	}
	var flow GitHubDeviceFlow
	var intervalSeconds int
	var githubExpiresAt, nextPollAt sql.NullTime
	var userCode, verificationURI sql.NullString
	err = tx.QueryRow(ctx, `
		SELECT id,code_hash,github_device_code_ciphertext,github_user_code,github_verification_uri,
		       expires_at,github_expires_at,COALESCE(github_interval_seconds,0),github_next_poll_at,github_status
		FROM bootstrap_sessions
		WHERE id=$1 AND used_at IS NULL AND invalidated_at IS NULL
		FOR UPDATE`, id).Scan(
		&flow.ID, &flow.CodeHash, &flow.DeviceCodeCiphertext, &userCode,
		&verificationURI, &flow.ExpiresAt, &githubExpiresAt,
		&intervalSeconds, &nextPollAt, &flow.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return GitHubDeviceFlow{}, false, ErrInvalidBootstrapCode
	}
	if err != nil {
		return GitHubDeviceFlow{}, false, err
	}
	if userCode.Valid {
		flow.UserCode = userCode.String
	}
	if verificationURI.Valid {
		flow.VerificationURI = verificationURI.String
	}
	if githubExpiresAt.Valid {
		flow.GitHubExpiresAt = githubExpiresAt.Time
	}
	if nextPollAt.Valid {
		flow.NextPollAt = nextPollAt.Time
	}
	flow.Interval = time.Duration(intervalSeconds) * time.Second
	now := time.Now().UTC()
	if flow.Status != "pending" || !flow.ExpiresAt.After(now) || !flow.GitHubExpiresAt.After(now) {
		if !flow.ExpiresAt.After(now) || !flow.GitHubExpiresAt.After(now) {
			flow.Status = "expired"
			if _, err := tx.Exec(ctx, `UPDATE bootstrap_sessions SET github_status='expired' WHERE id=$1`, id); err != nil {
				return GitHubDeviceFlow{}, false, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return GitHubDeviceFlow{}, false, err
		}
		return flow, false, nil
	}
	if now.Before(flow.NextPollAt) {
		if err := tx.Commit(ctx); err != nil {
			return GitHubDeviceFlow{}, false, err
		}
		return flow, false, nil
	}
	flow.NextPollAt = now.Add(flow.Interval)
	if _, err := tx.Exec(ctx, `UPDATE bootstrap_sessions SET github_next_poll_at=$2 WHERE id=$1`, id, flow.NextPollAt); err != nil {
		return GitHubDeviceFlow{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GitHubDeviceFlow{}, false, err
	}
	return flow, true, nil
}

func (r *Repository) UpdateGitHubDeviceFlow(ctx context.Context, id uuid.UUID, status string, interval time.Duration, nextPollAt time.Time) error {
	intervalSeconds := int(interval / time.Second)
	if id == uuid.Nil || intervalSeconds < 1 || intervalSeconds > 300 || nextPollAt.IsZero() {
		return ErrBootstrapDevice
	}
	if status != "pending" && status != "denied" && status != "expired" && status != "failed" {
		return ErrBootstrapDevice
	}
	_, err := r.pool.Exec(ctx, `UPDATE bootstrap_sessions SET github_status=$2,github_interval_seconds=$3,github_next_poll_at=$4 WHERE id=$1 AND used_at IS NULL AND invalidated_at IS NULL`, id, status, intervalSeconds, nextPollAt)
	return err
}

// CreateGitHubInstanceOwner creates the account, identity, instance role and
// normal Stealth session atomically. The singleton row and active bootstrap
// session are locked before any insert, so only one authorization can win.
func (r *Repository) CreateGitHubInstanceOwner(ctx context.Context, input GitHubOwnerInput) (domain.Account, error) {
	if input.BootstrapSessionID == uuid.Nil || input.AccountID == uuid.Nil || input.SessionID == uuid.Nil || len(input.BootstrapCodeHash) != 32 || len(input.TokenHash) != 32 || input.ProviderUserID == "" || input.ProviderLogin == "" || !input.SessionExpiresAt.After(time.Now().UTC()) {
		return domain.Account{}, ErrGitHubIdentity
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Account{}, err
	}
	defer tx.Rollback(ctx)
	var sealed bool
	err = tx.QueryRow(ctx, `SELECT (sealed_at IS NOT NULL OR EXISTS (SELECT 1 FROM instance_roles WHERE role='instance_owner')) FROM instance_bootstrap WHERE id=TRUE FOR UPDATE`).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, ErrNotFound
	}
	if err != nil {
		return domain.Account{}, err
	}
	if sealed {
		return domain.Account{}, ErrBootstrapSealed
	}
	var storedHash []byte
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT code_hash,expires_at FROM bootstrap_sessions WHERE id=$1 AND used_at IS NULL AND invalidated_at IS NULL AND expires_at > now() FOR UPDATE`, input.BootstrapSessionID).Scan(&storedHash, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, ErrInvalidBootstrapCode
	}
	if err != nil {
		return domain.Account{}, err
	}
	if subtle.ConstantTimeCompare(input.BootstrapCodeHash, storedHash) != 1 {
		return domain.Account{}, ErrInvalidBootstrapCode
	}
	account := domain.Account{
		ID:             input.AccountID.String(),
		EmailVerified:  false,
		InstanceRole:   "instance_owner",
		Provider:       "github",
		ProviderUserID: input.ProviderUserID,
		ProviderLogin:  input.ProviderLogin,
		DisplayName:    input.DisplayName,
		AvatarURL:      input.AvatarURL,
	}
	if err := tx.QueryRow(ctx, `INSERT INTO accounts (id,email,password_hash) VALUES ($1,NULL,NULL) RETURNING created_at`, input.AccountID).Scan(&account.CreatedAt); err != nil {
		return domain.Account{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO account_identities (account_id,provider,provider_user_id,provider_login,provider_email,display_name,avatar_url) VALUES ($1,'github',$2,$3,$4,$5,$6)`, input.AccountID, input.ProviderUserID, input.ProviderLogin, nullableString(input.ProviderEmail), nullableString(input.DisplayName), nullableString(input.AvatarURL)); err != nil {
		return domain.Account{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO instance_roles (account_id,role) VALUES ($1,'instance_owner')`, input.AccountID); err != nil {
		return domain.Account{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sessions (id,account_id,token_hash,expires_at) VALUES ($1,$2,$3,$4)`, input.SessionID, input.AccountID, input.TokenHash, input.SessionExpiresAt); err != nil {
		return domain.Account{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE bootstrap_sessions SET used_at=now(),github_status='none',github_device_code_ciphertext=NULL,github_user_code=NULL,github_verification_uri=NULL,github_expires_at=NULL,github_interval_seconds=NULL,github_next_poll_at=NULL WHERE id=$1`, input.BootstrapSessionID); err != nil {
		return domain.Account{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE bootstrap_sessions SET invalidated_at=now(),github_status='none',github_device_code_ciphertext=NULL,github_user_code=NULL,github_verification_uri=NULL,github_expires_at=NULL,github_interval_seconds=NULL,github_next_poll_at=NULL WHERE used_at IS NULL AND invalidated_at IS NULL`); err != nil {
		return domain.Account{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE instance_bootstrap SET sealed_at=now(),sealed_reason='instance_owner_created' WHERE id=TRUE`); err != nil {
		return domain.Account{}, err
	}
	identityMetadata := map[string]any{"provider": "github", "provider_user_id": input.ProviderUserID, "provider_login": input.ProviderLogin}
	if err := writeAuditMetadata(ctx, tx, uuid.Nil, input.AccountID, "bootstrap.github.authorized", "account", input.AccountID, identityMetadata); err != nil {
		return domain.Account{}, err
	}
	if err := writeAuditMetadata(ctx, tx, uuid.Nil, input.AccountID, "instance.owner.created", "instance_owner", input.AccountID, map[string]any{"role": "instance_owner", "provider": "github"}); err != nil {
		return domain.Account{}, err
	}
	if err := writeAuditMetadata(ctx, tx, uuid.Nil, input.AccountID, "instance.bootstrap.sealed", "instance_bootstrap", input.AccountID, map[string]any{"reason": "instance_owner_created"}); err != nil {
		return domain.Account{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Account{}, err
	}
	return account, nil
}

func (r *Repository) ListBootstrapAdoptionAccounts(ctx context.Context) ([]BootstrapAdoptionAccount, error) {
	var sealedReason sql.NullString
	var sealedAt sql.NullTime
	if err := r.pool.QueryRow(ctx, `SELECT sealed_at,sealed_reason FROM instance_bootstrap WHERE id=TRUE`).Scan(&sealedAt, &sealedReason); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !sealedAt.Valid || sealedReason.String != "legacy_installation" {
		return nil, ErrBootstrapSealed
	}
	rows, err := r.pool.Query(ctx, `SELECT a.id,a.email,ai.provider,ai.provider_login,a.created_at FROM accounts a LEFT JOIN account_identities ai ON ai.account_id=a.id AND ai.provider='github' ORDER BY a.created_at ASC,a.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]BootstrapAdoptionAccount, 0)
	for rows.Next() {
		var account BootstrapAdoptionAccount
		var email, provider, login sql.NullString
		if err := rows.Scan(&account.ID, &email, &provider, &login, &account.CreatedAt); err != nil {
			return nil, err
		}
		account.Email = email.String
		account.Provider = provider.String
		account.ProviderLogin = login.String
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (r *Repository) AdoptInstanceOwner(ctx context.Context, accountID uuid.UUID) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var sealedAt sql.NullTime
	var reason sql.NullString
	if err := tx.QueryRow(ctx, `SELECT sealed_at,sealed_reason FROM instance_bootstrap WHERE id=TRUE FOR UPDATE`).Scan(&sealedAt, &reason); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if reason.String != "legacy_installation" {
		return ErrBootstrapSealed
	}
	var ownerExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM instance_roles WHERE role='instance_owner')`).Scan(&ownerExists); err != nil {
		return err
	}
	if ownerExists {
		return ErrBootstrapSealed
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id=$1)`, accountID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `INSERT INTO instance_roles (account_id,role) VALUES ($1,'instance_owner')`, accountID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE instance_bootstrap SET sealed_at=COALESCE(sealed_at,now()),sealed_reason='instance_owner_adopted' WHERE id=TRUE`); err != nil {
		return err
	}
	if err := writeAuditMetadata(ctx, tx, uuid.Nil, accountID, "instance.owner.adopted", "instance_owner", accountID, map[string]any{"role": "instance_owner", "reason": "operator_adoption"}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func populateGitHubIdentity(account *domain.Account, provider, providerUserID, providerLogin, displayName, avatarURL sql.NullString) {
	if provider.Valid {
		account.Provider = provider.String
	}
	if providerUserID.Valid {
		account.ProviderUserID = providerUserID.String
	}
	if providerLogin.Valid {
		account.ProviderLogin = providerLogin.String
	}
	if displayName.Valid {
		account.DisplayName = displayName.String
	}
	if avatarURL.Valid {
		account.AvatarURL = avatarURL.String
	}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func (r *Repository) UpdateAccountPassword(ctx context.Context, accountID, currentSessionID uuid.UUID, passwordHash string) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var lockedAccountID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&lockedAccountID); errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET password_hash=$2,updated_at=now() WHERE id=$1`, accountID, passwordHash); err != nil {
		return 0, err
	}
	result, err := tx.Exec(ctx, `DELETE FROM sessions WHERE account_id=$1 AND id<>$2`, accountID, currentSessionID)
	if err != nil {
		return 0, err
	}
	if err := writeAudit(ctx, tx, uuid.Nil, accountID, "account.password_update", "account", accountID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
