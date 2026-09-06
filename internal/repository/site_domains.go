package repository

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nazxf/stealth-api/internal/domain"
)

var (
	ErrInvalidSiteDomain            = errors.New("invalid site domain")
	ErrSiteDomainVerificationFailed = errors.New("site domain verification failed")
)

// SiteTXTResolver is kept injectable so DNS verification can be tested with a
// deterministic resolver and production can supply a resolver with its own
// timeout/cache policy. A nil resolver uses net.DefaultResolver.
type SiteTXTResolver interface {
	LookupTXT(context.Context, string) ([]string, error)
}

type SiteDomainInput struct {
	Hostname string
}

const siteDomainProjection = `id,project_id,site_id,hostname,status,verification_token,verified_at,tls_status,created_at,updated_at`

func scanSiteDomain(row siteScanner) (domain.SiteDomain, error) {
	var item domain.SiteDomain
	err := row.Scan(&item.ID, &item.ProjectID, &item.SiteID, &item.Hostname, &item.Status, &item.VerificationToken, &item.VerifiedAt, &item.TLSStatus, &item.CreatedAt, &item.UpdatedAt)
	if err == nil {
		item.VerificationRecordName = "_stealth-verification." + item.Hostname
		item.VerificationRecordType = "TXT"
		item.VerificationRecordValue = item.VerificationToken
	}
	return item, err
}

// NormalizeSiteHostname canonicalizes a DNS hostname for uniqueness and Host
// lookup. Internationalized names are intentionally rejected until an IDNA
// policy is added; accepting ASCII DNS names only keeps verification and
// routing deterministic.
func NormalizeSiteHostname(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".")
	if len(value) < 4 || len(value) > 253 || strings.ContainsAny(value, "\x00\r\n /\\:@") || net.ParseIP(value) != nil {
		return "", ErrInvalidSiteDomain
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return "", ErrInvalidSiteDomain
	}
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalidSiteDomain
		}
		for _, char := range label {
			if !(char == '-' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
				return "", ErrInvalidSiteDomain
			}
		}
	}
	return value, nil
}

func newSiteDomainVerificationToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func (r *Repository) ListSiteDomains(ctx context.Context, projectID, siteID uuid.UUID, actor SiteActor, limit int, cursor *uuid.UUID) ([]domain.SiteDomain, string, bool, error) {
	canManage, err := r.requireSiteRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	if _, err := r.siteByID(ctx, r.pool, projectID, siteID, false); err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+siteDomainProjection+` FROM site_domains WHERE project_id=$1 AND site_id=$2 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $4`, projectID, siteID, cursor, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.SiteDomain, 0, limit)
	for rows.Next() {
		item, scanErr := scanSiteDomain(rows)
		if scanErr != nil {
			return nil, "", false, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", false, err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, canManage, nil
}

func (r *Repository) GetSiteDomain(ctx context.Context, projectID, siteID, domainID uuid.UUID, actor SiteActor) (domain.SiteDomain, error) {
	if _, err := r.requireSiteRead(ctx, projectID, actor); err != nil {
		return domain.SiteDomain{}, err
	}
	if _, err := r.siteByID(ctx, r.pool, projectID, siteID, false); err != nil {
		return domain.SiteDomain{}, err
	}
	return r.siteDomainByID(ctx, r.pool, projectID, siteID, domainID, false)
}

// siteDomainForWrite reads the challenge after authenticating the mutation
// scope. A sites.write key must be sufficient to verify its own DNS proof;
// read and write scopes remain independent for metadata reads.
func (r *Repository) siteDomainForWrite(ctx context.Context, projectID, siteID, domainID uuid.UUID, actor SiteActor) (domain.SiteDomain, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.SiteDomain{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireSiteWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.SiteDomain{}, err
	}
	if _, err := r.siteByID(ctx, tx, projectID, siteID, false); err != nil {
		return domain.SiteDomain{}, err
	}
	item, err := r.siteDomainByID(ctx, tx, projectID, siteID, domainID, false)
	if err != nil {
		return domain.SiteDomain{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SiteDomain{}, err
	}
	return item, nil
}

func (r *Repository) siteDomainByID(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, projectID, siteID, domainID uuid.UUID, lock bool) (domain.SiteDomain, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	item, err := scanSiteDomain(query.QueryRow(ctx, `SELECT `+siteDomainProjection+` FROM site_domains WHERE project_id=$1 AND site_id=$2 AND id=$3`+suffix, projectID, siteID, domainID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SiteDomain{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateSiteDomain(ctx context.Context, id, projectID, siteID uuid.UUID, actor SiteActor, input SiteDomainInput) (domain.SiteDomain, error) {
	hostname, err := NormalizeSiteHostname(input.Hostname)
	if err != nil {
		return domain.SiteDomain{}, err
	}
	token, err := newSiteDomainVerificationToken()
	if err != nil {
		return domain.SiteDomain{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.SiteDomain{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireSiteWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.SiteDomain{}, err
	}
	if _, err := r.siteByID(ctx, tx, projectID, siteID, true); err != nil {
		return domain.SiteDomain{}, err
	}
	item, err := scanSiteDomain(tx.QueryRow(ctx, `INSERT INTO site_domains (id,project_id,site_id,hostname,verification_token) VALUES ($1,$2,$3,$4,$5) RETURNING `+siteDomainProjection, id, projectID, siteID, hostname, token))
	if err != nil {
		return domain.SiteDomain{}, mapError(err)
	}
	if err := r.auditSite(ctx, tx, projectID, actor, "site_domain.create", "site_domain", id, map[string]any{"hostname": hostname}); err != nil {
		return domain.SiteDomain{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SiteDomain{}, err
	}
	return item, nil
}

func (r *Repository) DeleteSiteDomain(ctx context.Context, projectID, siteID, domainID uuid.UUID, actor SiteActor) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := r.requireSiteWriteTx(ctx, tx, projectID, actor); err != nil {
		return err
	}
	if _, err := r.siteByID(ctx, tx, projectID, siteID, true); err != nil {
		return err
	}
	item, err := r.siteDomainByID(ctx, tx, projectID, siteID, domainID, true)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM site_domains WHERE project_id=$1 AND site_id=$2 AND id=$3`, projectID, siteID, domainID); err != nil {
		return err
	}
	if err := r.auditSite(ctx, tx, projectID, actor, "site_domain.delete", "site_domain", domainID, map[string]any{"hostname": item.Hostname}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// VerifySiteDomain checks the DNS TXT proof at
// _stealth-verification.<hostname>. Certificate issuance, when enabled, is
// performed later by the ACME manager after this verified state is recorded.
func (r *Repository) VerifySiteDomain(ctx context.Context, projectID, siteID, domainID uuid.UUID, actor SiteActor) (domain.SiteDomain, error) {
	item, err := r.siteDomainForWrite(ctx, projectID, siteID, domainID, actor)
	if err != nil {
		return domain.SiteDomain{}, err
	}
	if item.Status == "verified" {
		return item, nil
	}
	resolver := r.txtResolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	records, lookupErr := resolver.LookupTXT(ctx, item.VerificationRecordName)
	if lookupErr != nil || !containsSiteDomainToken(records, item.VerificationToken) {
		return domain.SiteDomain{}, ErrSiteDomainVerificationFailed
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.SiteDomain{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireSiteWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.SiteDomain{}, err
	}
	if _, err := r.siteByID(ctx, tx, projectID, siteID, true); err != nil {
		return domain.SiteDomain{}, err
	}
	locked, err := r.siteDomainByID(ctx, tx, projectID, siteID, domainID, true)
	if err != nil {
		return domain.SiteDomain{}, err
	}
	if locked.Status == "verified" {
		if err := tx.Commit(ctx); err != nil {
			return domain.SiteDomain{}, err
		}
		return locked, nil
	}
	verified, err := scanSiteDomain(tx.QueryRow(ctx, `UPDATE site_domains SET status='verified',verified_at=now(),updated_at=now() WHERE project_id=$1 AND site_id=$2 AND id=$3 RETURNING `+siteDomainProjection, projectID, siteID, domainID))
	if err != nil {
		return domain.SiteDomain{}, err
	}
	if err := r.auditSite(ctx, tx, projectID, actor, "site_domain.verify", "site_domain", domainID, map[string]any{"hostname": verified.Hostname, "method": "dns_txt"}); err != nil {
		return domain.SiteDomain{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SiteDomain{}, err
	}
	return verified, nil
}

func containsSiteDomainToken(records []string, token string) bool {
	for _, record := range records {
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(record)), []byte(token)) == 1 {
			return true
		}
	}
	return false
}

// IsVerifiedSiteHostname is the host policy used by the in-process ACME
// manager. Invalid or unverified names are a normal false result; database
// failures are returned so autocert fails closed instead of requesting a
// certificate for an unknown tenant.
func (r *Repository) IsVerifiedSiteHostname(ctx context.Context, hostname string) (bool, error) {
	hostname, err := NormalizeSiteHostname(hostname)
	if err != nil {
		return false, nil
	}
	var verified bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM site_domains WHERE hostname=$1 AND status='verified')`, hostname).Scan(&verified); err != nil {
		return false, err
	}
	return verified, nil
}

// SetVerifiedSiteDomainTLSStatus records certificate acquisition failures and
// successes without allowing an unverified or deleted hostname to be marked
// active. An already-active certificate is never downgraded by a transient
// renewal error, so clients can continue using the last valid certificate.
func (r *Repository) SetVerifiedSiteDomainTLSStatus(ctx context.Context, hostname, status string) error {
	hostname, err := NormalizeSiteHostname(hostname)
	if err != nil {
		return ErrInvalidSiteDomain
	}
	switch status {
	case "pending", "active", "failed":
	default:
		return ErrInvalidSiteDomain
	}
	_, err = r.pool.Exec(ctx, `UPDATE site_domains SET tls_status=$2,updated_at=now() WHERE hostname=$1 AND status='verified' AND ($2='active' OR tls_status <> 'active')`, hostname, status)
	return err
}

// GetActiveSiteArtifactByHostname resolves a verified custom hostname to the
// same immutable active artifact used by the site-ID public route.
func (r *Repository) GetActiveSiteArtifactByHostname(ctx context.Context, hostname string) (SitePublicArtifact, error) {
	hostname, err := NormalizeSiteHostname(hostname)
	if err != nil {
		return SitePublicArtifact{}, ErrNotFound
	}
	var siteID uuid.UUID
	if err := r.pool.QueryRow(ctx, `SELECT site_id FROM site_domains WHERE hostname=$1 AND status='verified'`, hostname).Scan(&siteID); errors.Is(err, pgx.ErrNoRows) {
		return SitePublicArtifact{}, ErrNotFound
	} else if err != nil {
		return SitePublicArtifact{}, err
	}
	return r.GetActiveSiteArtifact(ctx, siteID)
}
