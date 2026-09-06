package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/domain"
)

func (r *Repository) CreateSession(ctx context.Context, sessionID, accountID uuid.UUID, tokenHash []byte, expires time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO sessions (id,account_id,token_hash,expires_at) VALUES ($1,$2,$3,$4)`, sessionID, accountID, tokenHash, expires); err != nil {
		return err
	}
	if err = writeAudit(ctx, tx, uuid.Nil, accountID, "session.login", "session", sessionID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *Repository) DeleteSession(ctx context.Context, sessionID uuid.UUID, accountID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE id=$1 AND account_id=$2`, sessionID, accountID); err != nil {
		return err
	}
	if err = writeAudit(ctx, tx, uuid.Nil, accountID, "session.logout", "session", sessionID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListConsoleSessions returns active Console sessions for an account. Only
// safe metadata is projected; bearer tokens remain write-only secrets.
func (r *Repository) ListConsoleSessions(ctx context.Context, accountID, currentSessionID uuid.UUID) ([]domain.ConsoleSession, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,(id=$2),expires_at,created_at FROM sessions WHERE account_id=$1 AND expires_at > now() ORDER BY created_at DESC`, accountID, currentSessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.ConsoleSession, 0)
	for rows.Next() {
		var item domain.ConsoleSession
		if err := rows.Scan(&item.ID, &item.IsCurrent, &item.ExpiresAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// RevokeConsoleSession removes one session owned by accountID. A missing
// session is reported as ErrNotFound so the HTTP layer does not claim success
// for another account's session ID.
func (r *Repository) RevokeConsoleSession(ctx context.Context, accountID, sessionID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `DELETE FROM sessions WHERE id=$1 AND account_id=$2`, sessionID, accountID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := writeAudit(ctx, tx, uuid.Nil, accountID, "session.revoke", "session", sessionID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RevokeOtherConsoleSessions revokes every active and expired session except
// the one currently being used. Keeping the current session alive lets a user
// safely sign out old devices without losing the page that performed the
// action.
func (r *Repository) RevokeOtherConsoleSessions(ctx context.Context, accountID, currentSessionID uuid.UUID) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `DELETE FROM sessions WHERE account_id=$1 AND id<>$2`, accountID, currentSessionID)
	if err != nil {
		return 0, err
	}
	if result.RowsAffected() > 0 {
		if err := writeAudit(ctx, tx, uuid.Nil, accountID, "session.revoke_others", "account", accountID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// UpdateAccountPassword changes the password and revokes every other Console
// session in one transaction. The caller's session remains valid so the
// account can continue using the Settings page after a successful update.
