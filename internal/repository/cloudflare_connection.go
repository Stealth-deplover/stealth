package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/domainname"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrCloudflareConnectionUnavailable = errors.New("Cloudflare connection is unavailable")
	ErrCloudflareConnectionConflict    = errors.New("Cloudflare connection conflicts with the existing tunnel")
	ErrCloudflareIdentityRequired      = errors.New("Cloudflare tunnel identity is required before reconnecting")
)

const cloudflareReconcileLockID int64 = 8_105_202_603

type CloudflareConnectionInput struct {
	AccountID       string
	ConsoleZoneID   string
	ConsoleHostname string
	TunnelID        string
	TunnelName      string
	ConsoleRecordID string
	APIToken        string
}

// TryCloudflareReconcileLock holds one connection-scoped PostgreSQL advisory
// lock across provider HTTP work. A second worker skips rather than racing the
// tunnel configuration or wildcard record state.
func (r *Repository) TryCloudflareReconcileLock(ctx context.Context) (func() error, bool, error) {
	if r == nil || r.pool == nil {
		return nil, false, ErrNotFound
	}
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, cloudflareReconcileLockID).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !acquired {
		conn.Release()
		return nil, false, nil
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			_, releaseErr = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, cloudflareReconcileLockID)
			conn.Release()
		})
		return releaseErr
	}, true, nil
}

// CloudflareConnectionDetails returns only non-secret provider identity and
// the authoritative workload domain. API handlers use it to validate a
// replacement token without loading the existing credential.
func (r *Repository) CloudflareConnectionDetails(ctx context.Context) (domain.CloudflareConnection, error) {
	return r.readCloudflareConnection(ctx, false)
}

// CloudflareReconcileSnapshot is the worker-only credential read. The token
// is decrypted in memory and is excluded from all JSON serialization.
func (r *Repository) CloudflareReconcileSnapshot(ctx context.Context) (domain.CloudflareConnection, error) {
	return r.readCloudflareConnection(ctx, true)
}

func (r *Repository) readCloudflareConnection(ctx context.Context, decrypt bool) (domain.CloudflareConnection, error) {
	if r == nil || r.pool == nil {
		return domain.CloudflareConnection{}, ErrNotFound
	}
	var connection domain.CloudflareConnection
	var ciphertext []byte
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(c.account_id,''),COALESCE(c.console_zone_id,''),COALESCE(c.console_hostname,''),COALESCE(c.tunnel_id,''),COALESCE(c.tunnel_name,''),COALESCE(c.console_record_id,''),
		       c.api_token_ciphertext,COALESCE(c.workload_zone_id,''),COALESCE(c.workload_zone_name,''),COALESCE(c.wildcard_hostname,''),COALESCE(c.wildcard_record_id,''),
		       c.status,c.last_reconciled_at,COALESCE(c.last_error,''),c.configured_at,c.updated_at,d.workload_base_domain
		FROM cloudflare_connections c
	LEFT JOIN instance_domain_settings d ON d.id=TRUE
	WHERE c.id=TRUE`).Scan(
		&connection.AccountID, &connection.ConsoleZoneID, &connection.ConsoleHostname, &connection.TunnelID, &connection.TunnelName, &connection.ConsoleRecordID,
		&ciphertext, &connection.WorkloadZoneID, &connection.WorkloadZoneName, &connection.WildcardHostname, &connection.WildcardRecordID,
		&connection.Status, &connection.LastReconciledAt, &connection.LastError, &connection.ConfiguredAt, &connection.UpdatedAt, &connection.WorkloadBaseDomain)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CloudflareConnection{}, ErrNotFound
	}
	if err != nil {
		return domain.CloudflareConnection{}, err
	}
	if decrypt && len(ciphertext) > 0 {
		if r.cloudflareCipher == nil {
			return domain.CloudflareConnection{}, ErrCloudflareConnectionUnavailable
		}
		plaintext, err := r.cloudflareCipher.Decrypt(ciphertext)
		if err != nil {
			return domain.CloudflareConnection{}, errors.New("Cloudflare credential decryption failed")
		}
		connection.APIToken = string(plaintext)
	}
	return connection, nil
}

func (r *Repository) CloudflareRoutingStatus(ctx context.Context) (domain.CloudflareRoutingStatus, error) {
	connection, err := r.CloudflareConnectionDetails(ctx)
	if err != nil {
		return domain.CloudflareRoutingStatus{}, err
	}
	var configured bool
	if err := r.pool.QueryRow(ctx, `SELECT api_token_ciphertext IS NOT NULL AND octet_length(api_token_ciphertext)>0 FROM cloudflare_connections WHERE id=TRUE`).Scan(&configured); err != nil {
		return domain.CloudflareRoutingStatus{}, err
	}
	status := connection.Status
	if status == "" {
		status = "unconfigured"
	}
	var hostname *string
	if connection.WorkloadBaseDomain != nil {
		value := "*." + *connection.WorkloadBaseDomain
		hostname = &value
	}
	return domain.CloudflareRoutingStatus{
		Configured:       configured,
		Status:           status,
		ConsoleHostname:  connection.ConsoleHostname,
		WorkloadHostname: hostname,
		Zone:             connection.WorkloadZoneName,
		LastReconciledAt: connection.LastReconciledAt,
		LastError:        connection.LastError,
	}, nil
}

// SaveCloudflareConnectionFromSetup persists the setup API's server-held
// plaintext token only after named tunnel and Console DNS provisioning have
// completed. A retry with the same identity is a no-op; setup cannot replace a
// newer production connection.
func (r *Repository) SaveCloudflareConnectionFromSetup(ctx context.Context, input CloudflareConnectionInput) error {
	return r.saveCloudflareConnection(ctx, uuid.Nil, input, false)
}

// ImportCloudflareConnectionOnce bridges a completed encrypted setup state to
// PostgreSQL. It never replaces a row that already has a credential or a
// different identity and can persist an identity without a recoverable token
// so an Instance Owner can reconnect explicitly.
func (r *Repository) ImportCloudflareConnectionOnce(ctx context.Context, input CloudflareConnectionInput, unavailableReason string) (bool, error) {
	if r == nil || r.pool == nil {
		return false, ErrNotFound
	}
	if err := validateCloudflareConnectionInput(input, input.APIToken != ""); err != nil {
		return false, err
	}
	var ciphertext []byte
	if input.APIToken != "" {
		if r.cloudflareCipher == nil {
			return false, ErrCloudflareConnectionUnavailable
		}
		var err error
		ciphertext, err = r.cloudflareCipher.Encrypt([]byte(input.APIToken))
		if err != nil {
			return false, err
		}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var existingToken []byte
	var existingAccount, existingZone, existingHostname, existingTunnel string
	if err := tx.QueryRow(ctx, `SELECT api_token_ciphertext,COALESCE(account_id,''),COALESCE(console_zone_id,''),COALESCE(console_hostname,''),COALESCE(tunnel_id,'') FROM cloudflare_connections WHERE id=TRUE FOR UPDATE`).Scan(&existingToken, &existingAccount, &existingZone, &existingHostname, &existingTunnel); err != nil {
		return false, err
	}
	if len(existingToken) > 0 || existingTunnel != "" {
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	status := "pending"
	lastError := any(nil)
	if len(ciphertext) == 0 {
		status = "error"
		lastError = normalizeCloudflareError(unavailableReason)
	}
	_, err = tx.Exec(ctx, `
		UPDATE cloudflare_connections
		SET account_id=$1,console_zone_id=$2,console_hostname=$3,tunnel_id=$4,tunnel_name=$5,console_record_id=$6,
		    api_token_ciphertext=$7,status=$8,last_error=$9,configured_at=CASE WHEN $7::bytea IS NULL THEN configured_at ELSE now() END,
		    updated_at=now()
		WHERE id=TRUE`, input.AccountID, input.ConsoleZoneID, input.ConsoleHostname, input.TunnelID, input.TunnelName, input.ConsoleRecordID, nullableBytes(ciphertext), status, lastError)
	if err != nil {
		return false, err
	}
	if err := writeSystemCloudflareAuditTx(ctx, tx, "admin.cloudflare.connection.import", map[string]any{
		"configured": len(ciphertext) > 0, "credential_recovered": len(ciphertext) > 0,
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// MarkCloudflareConnectionUnavailable leaves an unconfigured status with an
// actionable, bounded reason when a legacy setup credential cannot be read.
func (r *Repository) MarkCloudflareConnectionUnavailable(ctx context.Context, reason string) error {
	if r == nil || r.pool == nil {
		return ErrNotFound
	}
	_, err := r.pool.Exec(ctx, `UPDATE cloudflare_connections SET status='error',last_error=$1,updated_at=now() WHERE id=TRUE AND api_token_ciphertext IS NULL`, normalizeCloudflareError(reason))
	return err
}

func (r *Repository) saveCloudflareConnection(ctx context.Context, actor uuid.UUID, input CloudflareConnectionInput, requireOwner bool) error {
	if r == nil || r.pool == nil {
		return ErrNotFound
	}
	if err := validateCloudflareConnectionInput(input, true); err != nil {
		return err
	}
	if r.cloudflareCipher == nil {
		return ErrCloudflareConnectionUnavailable
	}
	ciphertext, err := r.cloudflareCipher.Encrypt([]byte(input.APIToken))
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if requireOwner {
		if err := requireInstanceOwnerTx(ctx, tx, actor); err != nil {
			return err
		}
	}
	var existing domain.CloudflareConnection
	var existingToken []byte
	if err := tx.QueryRow(ctx, `SELECT COALESCE(account_id,''),COALESCE(console_zone_id,''),COALESCE(console_hostname,''),COALESCE(tunnel_id,''),COALESCE(tunnel_name,''),COALESCE(console_record_id,''),api_token_ciphertext FROM cloudflare_connections WHERE id=TRUE FOR UPDATE`).Scan(
		&existing.AccountID, &existing.ConsoleZoneID, &existing.ConsoleHostname, &existing.TunnelID, &existing.TunnelName, &existing.ConsoleRecordID, &existingToken); err != nil {
		return err
	}
	if len(existingToken) > 0 {
		if !sameCloudflareIdentity(existing, input) {
			return ErrCloudflareConnectionConflict
		}
		if !requireOwner {
			return tx.Commit(ctx)
		}
		if _, err := tx.Exec(ctx, `UPDATE cloudflare_connections SET api_token_ciphertext=$1,console_record_id=$2,status='pending',last_error=NULL,updated_at=now() WHERE id=TRUE`, ciphertext, input.ConsoleRecordID); err != nil {
			return err
		}
		if err := writeInstanceAuditTx(ctx, tx, actor, "admin.cloudflare.connection.update", "cloudflare_connection", uuid.Nil, map[string]any{"credential_replaced": true}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if existing.TunnelID != "" && !sameCloudflareIdentity(existing, input) {
		return ErrCloudflareConnectionConflict
	}
	_, err = tx.Exec(ctx, `
		UPDATE cloudflare_connections
		SET account_id=$1,console_zone_id=$2,console_hostname=$3,tunnel_id=$4,tunnel_name=$5,console_record_id=$6,
		    api_token_ciphertext=$7,status='pending',last_error=NULL,configured_at=COALESCE(configured_at,now()),updated_at=now()
		WHERE id=TRUE`, input.AccountID, input.ConsoleZoneID, input.ConsoleHostname, input.TunnelID, input.TunnelName, input.ConsoleRecordID, ciphertext)
	if err != nil {
		return err
	}
	if requireOwner {
		if err := writeInstanceAuditTx(ctx, tx, actor, "admin.cloudflare.connection.update", "cloudflare_connection", uuid.Nil, map[string]any{"credential_replaced": false, "configured": true}); err != nil {
			return err
		}
	} else if err := writeSystemCloudflareAuditTx(ctx, tx, "admin.cloudflare.connection.established", map[string]any{"configured": true}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetCloudflareConnectionByOwner establishes or reconnects the existing tunnel
// under live Instance Owner authorization. It never creates provider
// resources; the API adapter validates the identity and token first.
func (r *Repository) SetCloudflareConnectionByOwner(ctx context.Context, actor uuid.UUID, input CloudflareConnectionInput) error {
	return r.saveCloudflareConnection(ctx, actor, input, true)
}

func (r *Repository) CompleteCloudflareReconcile(ctx context.Context, update domain.CloudflareRoutingUpdate) (bool, error) {
	if r == nil || r.pool == nil {
		return false, ErrNotFound
	}
	if err := validateCloudflareObserved(update); err != nil {
		return false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var currentDomain *string
	if err := tx.QueryRow(ctx, `SELECT workload_base_domain FROM instance_domain_settings WHERE id=TRUE`).Scan(&currentDomain); err != nil {
		return false, err
	}
	var accountID, tunnelID, oldZoneID, oldHostname, oldRecordID string
	if err := tx.QueryRow(ctx, `SELECT account_id,tunnel_id,workload_zone_id,wildcard_hostname,wildcard_record_id FROM cloudflare_connections WHERE id=TRUE AND api_token_ciphertext IS NOT NULL FOR UPDATE`).Scan(&accountID, &tunnelID, &oldZoneID, &oldHostname, &oldRecordID); errors.Is(err, pgx.ErrNoRows) {
		return false, ErrCloudflareIdentityRequired
	} else if err != nil {
		return false, err
	}
	if !sameOptionalString(currentDomain, update.ExpectedWorkloadBaseDomain) {
		if update.WildcardRecordID != "" && update.WildcardRecordID != oldRecordID {
			if err := queueRetiringWildcardTx(ctx, tx, update.WorkloadZoneID, update.WildcardHostname, update.WildcardRecordID, tunnelID+".cfargotunnel.com"); err != nil {
				return false, err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE cloudflare_connections SET status='pending',last_error=NULL,updated_at=now() WHERE id=TRUE`); err != nil {
			return false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	newHost := update.WildcardHostname
	if newHost != "" && (oldRecordID != update.WildcardRecordID || oldHostname != newHost || oldZoneID != update.WorkloadZoneID) && oldRecordID != "" {
		if err := queueRetiringWildcardTx(ctx, tx, oldZoneID, oldHostname, oldRecordID, tunnelID+".cfargotunnel.com"); err != nil {
			return false, err
		}
	}
	if newHost == "" && oldRecordID != "" {
		if err := queueRetiringWildcardTx(ctx, tx, oldZoneID, oldHostname, oldRecordID, tunnelID+".cfargotunnel.com"); err != nil {
			return false, err
		}
	}
	_, err = tx.Exec(ctx, `
		UPDATE cloudflare_connections
		SET workload_zone_id=NULLIF($1,''),workload_zone_name=NULLIF($2,''),wildcard_hostname=NULLIF($3,''),wildcard_record_id=NULLIF($4,''),
		    status='ready',last_error=NULL,last_reconciled_at=now(),updated_at=now()
		WHERE id=TRUE`, update.WorkloadZoneID, update.WorkloadZoneName, update.WildcardHostname, update.WildcardRecordID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) ListRetiringCloudflareWildcardDNS(ctx context.Context) ([]domain.CloudflareRetiringWildcardDNS, error) {
	if r == nil || r.pool == nil {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT record_id,zone_id,hostname,target FROM cloudflare_retiring_wildcard_dns ORDER BY queued_at,record_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.CloudflareRetiringWildcardDNS, 0)
	for rows.Next() {
		var item domain.CloudflareRetiringWildcardDNS
		if err := rows.Scan(&item.RecordID, &item.ZoneID, &item.Hostname, &item.Target); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) CompleteCloudflareWildcardRetirement(ctx context.Context, recordID string) error {
	if r == nil || r.pool == nil {
		return ErrNotFound
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM cloudflare_retiring_wildcard_dns WHERE record_id=$1`, recordID)
	return err
}

func (r *Repository) RecordCloudflareReconcileFailure(ctx context.Context, message string) error {
	if r == nil || r.pool == nil {
		return ErrNotFound
	}
	_, err := r.pool.Exec(ctx, `UPDATE cloudflare_connections SET status='error',last_error=$1,updated_at=now() WHERE id=TRUE`, normalizeCloudflareError(message))
	return err
}

func (r *Repository) MarkCloudflareRoutingPending(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return ErrNotFound
	}
	_, err := r.pool.Exec(ctx, `UPDATE cloudflare_connections SET status='pending',last_error=NULL,updated_at=now() WHERE id=TRUE AND api_token_ciphertext IS NOT NULL`)
	return err
}

func validateCloudflareConnectionInput(input CloudflareConnectionInput, requireToken bool) error {
	fields := []struct{ value, label string }{
		{input.AccountID, "account"}, {input.ConsoleZoneID, "console zone"}, {input.TunnelID, "tunnel"},
		{input.TunnelName, "tunnel name"}, {input.ConsoleRecordID, "console DNS record"},
	}
	for _, field := range fields {
		value := strings.TrimSpace(field.value)
		if value == "" || len(value) > 255 || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("Cloudflare %s identity is invalid", field.label)
		}
	}
	hostname, err := domainname.NormalizeHostname(input.ConsoleHostname)
	if err != nil || hostname != strings.ToLower(strings.TrimSuffix(strings.TrimSpace(input.ConsoleHostname), ".")) {
		return errors.New("Cloudflare Console hostname is invalid")
	}
	if requireToken && (strings.TrimSpace(input.APIToken) == "" || len(input.APIToken) > 4096 || strings.ContainsAny(input.APIToken, "\x00\r\n")) {
		return errors.New("Cloudflare API token is invalid")
	}
	return nil
}

func validateCloudflareObserved(update domain.CloudflareRoutingUpdate) error {
	if update.ExpectedWorkloadBaseDomain != nil {
		domain, err := domainname.NormalizeDomain(*update.ExpectedWorkloadBaseDomain)
		if err != nil || domain != *update.ExpectedWorkloadBaseDomain {
			return errors.New("Cloudflare workload domain is invalid")
		}
		expected := "*." + domain
		if update.WildcardHostname != expected || update.WorkloadZoneID == "" || update.WorkloadZoneName == "" || update.WildcardRecordID == "" {
			return errors.New("Cloudflare wildcard observation is incomplete")
		}
	} else if update.WorkloadZoneID != "" || update.WorkloadZoneName != "" || update.WildcardHostname != "" || update.WildcardRecordID != "" {
		return errors.New("Cloudflare clear observation must not contain wildcard resources")
	}
	return nil
}

func sameCloudflareIdentity(existing domain.CloudflareConnection, input CloudflareConnectionInput) bool {
	return existing.AccountID == input.AccountID && existing.ConsoleZoneID == input.ConsoleZoneID && existing.ConsoleHostname == input.ConsoleHostname && existing.TunnelID == input.TunnelID && existing.TunnelName == input.TunnelName
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func queueRetiringWildcardTx(ctx context.Context, tx pgx.Tx, zoneID, hostname, recordID, target string) error {
	if zoneID == "" || hostname == "" || recordID == "" || target == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO cloudflare_retiring_wildcard_dns (record_id,zone_id,hostname,target) VALUES ($1,$2,$3,$4) ON CONFLICT (record_id) DO NOTHING`, recordID, zoneID, hostname, target)
	return err
}

func normalizeCloudflareError(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\x00' || r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, value))
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	if len(value) > 512 {
		value = value[:512]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func writeSystemCloudflareAuditTx(ctx context.Context, tx pgx.Tx, action string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (id,organization_id,actor_account_id,action,target_type,target_id,metadata) VALUES ($1,NULL,NULL,$2,'cloudflare_connection',NULL,$3)`, id, action, encoded); err != nil {
		return err
	}
	return enqueueAdminRealtimeEventTx(ctx, tx, action, "cloudflare_connection", uuid.Nil, nil)
}
