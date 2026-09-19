package repository

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const adminErrorGroupMaxBatch = 100

var ErrInvalidAdminErrorGroup = errors.New("invalid admin error group")

type AdminErrorGroupState struct {
	Fingerprint        string
	Status             string
	AcknowledgedAt     *time.Time
	ResolvedAt         *time.Time
	IgnoredAt          *time.Time
	UpdatedByAccountID *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (r *Repository) ListAdminErrorGroupStatuses(ctx context.Context, fingerprints []string) (map[string]string, error) {
	if r == nil || r.pool == nil {
		return nil, ErrInvalidAdminErrorGroup
	}
	normalized := make([]string, 0, len(fingerprints))
	seen := make(map[string]struct{}, len(fingerprints))
	for _, fingerprint := range fingerprints {
		value, err := normalizeAdminErrorFingerprint(fingerprint)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return map[string]string{}, nil
	}
	if len(normalized) > adminErrorGroupMaxBatch {
		return nil, fmt.Errorf("%w: too many fingerprints", ErrInvalidAdminErrorGroup)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT fingerprint,status
		FROM admin_error_group_states
		WHERE fingerprint = ANY($1::text[])`, normalized)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]string, len(normalized))
	for rows.Next() {
		var fingerprint, status string
		if err := rows.Scan(&fingerprint, &status); err != nil {
			return nil, err
		}
		result[fingerprint] = status
	}
	return result, rows.Err()
}

func (r *Repository) UpdateAdminErrorGroupStatus(ctx context.Context, accountID uuid.UUID, fingerprint, status string) (AdminErrorGroupState, error) {
	normalizedFingerprint, err := normalizeAdminErrorFingerprint(fingerprint)
	if err != nil {
		return AdminErrorGroupState{}, err
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "open" && status != "acknowledged" && status != "resolved" && status != "ignored" {
		return AdminErrorGroupState{}, fmt.Errorf("%w: unsupported status", ErrInvalidAdminErrorGroup)
	}
	if r == nil || r.pool == nil || accountID == uuid.Nil {
		return AdminErrorGroupState{}, ErrInvalidAdminErrorGroup
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AdminErrorGroupState{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return AdminErrorGroupState{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO admin_error_group_states
		  (fingerprint,status,acknowledged_at,resolved_at,ignored_at,updated_by_account_id)
		VALUES ($1,$2,
		  CASE WHEN $2='acknowledged' THEN now() ELSE NULL END,
		  CASE WHEN $2='resolved' THEN now() ELSE NULL END,
		  CASE WHEN $2='ignored' THEN now() ELSE NULL END,
		  $3)
		ON CONFLICT (fingerprint) DO UPDATE SET
		  status=EXCLUDED.status,
		  acknowledged_at=EXCLUDED.acknowledged_at,
		  resolved_at=EXCLUDED.resolved_at,
		  ignored_at=EXCLUDED.ignored_at,
		  updated_by_account_id=EXCLUDED.updated_by_account_id,
		  updated_at=now()`, normalizedFingerprint, status, accountID); err != nil {
		return AdminErrorGroupState{}, mapError(err)
	}
	state, err := scanAdminErrorGroupState(tx.QueryRow(ctx, `
		SELECT fingerprint,status,acknowledged_at,resolved_at,ignored_at,
		       updated_by_account_id::text,created_at,updated_at
		FROM admin_error_group_states WHERE fingerprint=$1`, normalizedFingerprint))
	if err != nil {
		return AdminErrorGroupState{}, err
	}
	encoded, err := json.Marshal(map[string]any{
		"fingerprint": normalizedFingerprint,
		"status":      status,
	})
	if err != nil {
		return AdminErrorGroupState{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (id,organization_id,actor_account_id,action,target_type,target_id,metadata)
		VALUES ($1,NULL,$2,$3,$4,NULL,$5)`, uuid.Must(uuid.NewV7()), accountID,
		"admin.error_group.status", "admin_error_group", encoded); err != nil {
		return AdminErrorGroupState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminErrorGroupState{}, err
	}
	return state, nil
}

func normalizeAdminErrorFingerprint(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 64 {
		return "", fmt.Errorf("%w: fingerprint must be a SHA-256 hex value", ErrInvalidAdminErrorGroup)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("%w: fingerprint is invalid", ErrInvalidAdminErrorGroup)
	}
	return value, nil
}

func scanAdminErrorGroupState(row interface{ Scan(...any) error }) (AdminErrorGroupState, error) {
	var state AdminErrorGroupState
	if err := row.Scan(&state.Fingerprint, &state.Status, &state.AcknowledgedAt, &state.ResolvedAt, &state.IgnoredAt, &state.UpdatedByAccountID, &state.CreatedAt, &state.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AdminErrorGroupState{}, ErrNotFound
		}
		return AdminErrorGroupState{}, err
	}
	return state, nil
}
