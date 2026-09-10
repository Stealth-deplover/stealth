package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidHTTPTrace = errors.New("invalid HTTP trace")

type HTTPTraceInput struct {
	TraceID        string
	SpanID         string
	OrganizationID *uuid.UUID
	ProjectID      *uuid.UUID
	AccountID      *uuid.UUID
	Method         string
	Route          string
	Status         int
	Duration       time.Duration
	ResponseBytes  int64
	StartedAt      time.Time
	FinishedAt     time.Time
}

const httpTraceProjection = `
	t.id::text,
	t.trace_id,
	t.span_id,
	COALESCE(t.organization_id,p.organization_id),
	t.project_id,
	COALESCE(o.name,''),
	COALESCE(p.name,''),
	'api',
	t.method,
	t.route,
	t.status,
	t.duration_ms,
	t.response_bytes,
	t.started_at,
	t.finished_at,
	t.created_at`

type httpTraceScanner interface {
	Scan(...any) error
}

func scanHTTPTrace(row httpTraceScanner) (domain.HTTPTrace, error) {
	var item domain.HTTPTrace
	var organizationID, projectID *uuid.UUID
	if err := row.Scan(
		&item.ID,
		&item.TraceID,
		&item.SpanID,
		&organizationID,
		&projectID,
		&item.OrganizationName,
		&item.ProjectName,
		&item.Service,
		&item.Method,
		&item.Route,
		&item.Status,
		&item.DurationMS,
		&item.ResponseBytes,
		&item.StartedAt,
		&item.FinishedAt,
		&item.CreatedAt,
	); err != nil {
		return domain.HTTPTrace{}, err
	}
	if organizationID != nil {
		value := organizationID.String()
		item.OrganizationID = &value
	}
	if projectID != nil {
		value := projectID.String()
		item.ProjectID = &value
	}
	return item, nil
}

// RecordHTTPTrace persists only a tenant-scoped root request. It is called by
// the API middleware after a response has been produced; recorder failures
// must be logged by the caller and never change the already selected status.
func (r *Repository) RecordHTTPTrace(ctx context.Context, id uuid.UUID, input HTTPTraceInput) error {
	if id == uuid.Nil || !validHTTPTraceID(input.TraceID, 16, 64) || (input.SpanID != "" && !validHTTPTraceID(input.SpanID, 8, 32)) {
		return ErrInvalidHTTPTrace
	}
	method := strings.TrimSpace(input.Method)
	route := strings.TrimSpace(input.Route)
	if method == "" || utf8.RuneCountInString(method) > 16 || strings.ContainsAny(method, "\x00\r\n") || route == "" || utf8.RuneCountInString(route) > 240 || strings.ContainsRune(route, '\x00') {
		return ErrInvalidHTTPTrace
	}
	if input.Status < 100 || input.Status > 599 || input.Duration < 0 || input.ResponseBytes < 0 || input.FinishedAt.Before(input.StartedAt) || input.StartedAt.IsZero() || input.FinishedAt.IsZero() {
		return ErrInvalidHTTPTrace
	}
	if input.OrganizationID == nil && input.ProjectID == nil {
		return ErrInvalidHTTPTrace
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO http_traces (id,trace_id,span_id,organization_id,project_id,account_id,method,route,status,duration_ms,response_bytes,started_at,finished_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		id,
		strings.ToLower(strings.TrimSpace(input.TraceID)),
		nullableHTTPTraceString(input.SpanID),
		input.OrganizationID,
		input.ProjectID,
		input.AccountID,
		method,
		route,
		input.Status,
		input.Duration.Milliseconds(),
		input.ResponseBytes,
		input.StartedAt,
		input.FinishedAt,
	)
	if err != nil {
		return err
	}
	if input.ProjectID != nil {
		if err := incrementUsageTx(ctx, tx, *input.ProjectID, input.StartedAt, UsageDelta{
			APIRequestCount: 1,
			APIEgressBytes:  input.ResponseBytes,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListOrganizationHTTPTraces(ctx context.Context, organizationID, accountID uuid.UUID, limit int, cursor string) ([]domain.HTTPTrace, string, error) {
	if limit < 1 || limit > 100 {
		return nil, "", fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidHTTPTrace)
	}
	if err := r.requireMembership(ctx, organizationID, accountID); err != nil {
		return nil, "", err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+httpTraceProjection+`
		FROM http_traces t
		LEFT JOIN projects p ON p.id=t.project_id
		LEFT JOIN organizations o ON o.id=COALESCE(t.organization_id,p.organization_id)
		WHERE COALESCE(t.organization_id,p.organization_id)=$1
		  AND ($2='' OR t.id::text < $2)
		ORDER BY t.id DESC
		LIMIT $3`, organizationID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.HTTPTrace, 0, limit)
	for rows.Next() {
		item, scanErr := scanHTTPTrace(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}

// ListProjectHTTPTraces returns the newest durable root requests recorded for
// one project. Project membership is checked before querying so a caller can
// never use a guessed project ID to inspect another tenant's traffic.
func (r *Repository) ListProjectHTTPTraces(ctx context.Context, projectID, accountID uuid.UUID, limit int, cursor string) ([]domain.HTTPTrace, string, error) {
	if limit < 1 || limit > 100 {
		return nil, "", fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidHTTPTrace)
	}
	var organizationID uuid.UUID
	if err := r.pool.QueryRow(ctx, `SELECT organization_id FROM projects WHERE id=$1`, projectID).Scan(&organizationID); errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	} else if err != nil {
		return nil, "", err
	}
	if err := r.requireMembership(ctx, organizationID, accountID); err != nil {
		return nil, "", err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+httpTraceProjection+`
		FROM http_traces t
		LEFT JOIN projects p ON p.id=t.project_id
		LEFT JOIN organizations o ON o.id=COALESCE(t.organization_id,p.organization_id)
		WHERE t.project_id=$1 AND ($2='' OR t.id::text<$2)
		ORDER BY t.id DESC
		LIMIT $3`, projectID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.HTTPTrace, 0, limit)
	for rows.Next() {
		item, scanErr := scanHTTPTrace(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}

func validHTTPTraceID(value string, min, max int) bool {
	value = strings.TrimSpace(value)
	if len(value) < min || len(value) > max {
		return false
	}
	for _, character := range value {
		if (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F') {
			continue
		}
		return false
	}
	return true
}

func nullableHTTPTraceString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}
