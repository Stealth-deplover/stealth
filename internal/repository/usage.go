package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrInvalidUsageWindow = errors.New("invalid usage window")

// UsageDelta is the write-side metering contract shared by HTTP middleware
// and trusted Function workers. All values are increments; zero-value fields
// are intentionally no-ops.
type UsageDelta struct {
	APIRequestCount         int64
	APIEgressBytes          int64
	FunctionInvocationCount int64
	FunctionFailureCount    int64
	FunctionComputeMS       int64
}

// incrementUsageTx upserts one UTC calendar bucket. It is deliberately kept
// inside the caller's transaction so a request trace or a terminal Function
// transition can never publish usage for a rolled-back operation.
func incrementUsageTx(ctx context.Context, tx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, projectID uuid.UUID, occurredAt time.Time, delta UsageDelta) error {
	if projectID == uuid.Nil {
		return ErrInvalidUsageWindow
	}
	if delta.APIRequestCount < 0 || delta.APIEgressBytes < 0 || delta.FunctionInvocationCount < 0 || delta.FunctionFailureCount < 0 || delta.FunctionComputeMS < 0 {
		return ErrInvalidUsageWindow
	}
	if delta.APIRequestCount == 0 && delta.APIEgressBytes == 0 && delta.FunctionInvocationCount == 0 && delta.FunctionFailureCount == 0 && delta.FunctionComputeMS == 0 {
		return nil
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	usageDate := occurredAt.UTC().Format("2006-01-02")
	_, err := tx.Exec(ctx, `
		INSERT INTO project_usage_daily (project_id,usage_date,api_request_count,api_egress_bytes,function_invocation_count,function_failure_count,function_compute_ms)
		VALUES ($1,$2::date,$3,$4,$5,$6,$7)
		ON CONFLICT (project_id,usage_date) DO UPDATE SET
			api_request_count=project_usage_daily.api_request_count+EXCLUDED.api_request_count,
			api_egress_bytes=project_usage_daily.api_egress_bytes+EXCLUDED.api_egress_bytes,
			function_invocation_count=project_usage_daily.function_invocation_count+EXCLUDED.function_invocation_count,
			function_failure_count=project_usage_daily.function_failure_count+EXCLUDED.function_failure_count,
			function_compute_ms=project_usage_daily.function_compute_ms+EXCLUDED.function_compute_ms,
			updated_at=now()`,
		projectID, usageDate, delta.APIRequestCount, delta.APIEgressBytes, delta.FunctionInvocationCount, delta.FunctionFailureCount, delta.FunctionComputeMS)
	return err
}

// ProjectUsage returns only aggregates for a project the Console account can
// access. Each subsystem is counted independently so an empty subsystem
// contributes zero rather than causing a missing-row error.
func (r *Repository) ProjectUsage(ctx context.Context, projectID, accountID uuid.UUID) (domain.ProjectUsage, error) {
	if err := r.requireProjectAccess(ctx, projectID, accountID); err != nil {
		return domain.ProjectUsage{}, err
	}
	var item domain.ProjectUsage
	err := r.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM project_users WHERE project_id=$1),
			(SELECT count(*) FROM project_databases WHERE project_id=$1),
			(SELECT count(*) FROM database_tables WHERE project_id=$1),
			(SELECT count(*) FROM database_rows WHERE project_id=$1),
			(SELECT count(*) FROM storage_files WHERE project_id=$1),
			COALESCE((SELECT sum(used_bytes) FROM storage_buckets WHERE project_id=$1), 0),
			COALESCE((SELECT sum(quota_bytes) FROM storage_buckets WHERE project_id=$1), 0),
			(SELECT count(*) FROM project_functions WHERE project_id=$1),
			COALESCE((SELECT sum(artifact_used_bytes) FROM project_functions WHERE project_id=$1), 0),
			COALESCE((SELECT sum(artifact_quota_bytes) FROM project_functions WHERE project_id=$1), 0),
			(SELECT count(*) FROM project_sites WHERE project_id=$1),
			COALESCE((SELECT sum(artifact_used_bytes) FROM project_sites WHERE project_id=$1), 0),
			COALESCE((SELECT sum(artifact_reserved_bytes) FROM project_sites WHERE project_id=$1), 0),
			COALESCE((SELECT sum(artifact_quota_bytes) FROM project_sites WHERE project_id=$1), 0),
			(SELECT count(*) FROM webhook_events WHERE project_id=$1 AND expires_at > now()),
			(SELECT count(*) FROM webhook_deliveries d JOIN webhook_events e ON e.id=d.event_id WHERE e.project_id=$1 AND e.created_at >= now() - interval '7 days'),
			COALESCE((SELECT sum(api_request_count) FROM project_usage_daily WHERE project_id=$1 AND usage_date >= CURRENT_DATE - 29), 0),
			COALESCE((SELECT sum(api_egress_bytes) FROM project_usage_daily WHERE project_id=$1 AND usage_date >= CURRENT_DATE - 29), 0),
			COALESCE((SELECT sum(function_invocation_count) FROM project_usage_daily WHERE project_id=$1 AND usage_date >= CURRENT_DATE - 29), 0),
			COALESCE((SELECT sum(function_failure_count) FROM project_usage_daily WHERE project_id=$1 AND usage_date >= CURRENT_DATE - 29), 0),
			COALESCE((SELECT sum(function_compute_ms) FROM project_usage_daily WHERE project_id=$1 AND usage_date >= CURRENT_DATE - 29), 0),
			now()
	`, projectID).Scan(
		&item.ApplicationUsers,
		&item.DatabaseCount,
		&item.DatabaseTableCount,
		&item.DatabaseRowCount,
		&item.StorageFileCount,
		&item.StorageBytes,
		&item.StorageQuotaBytes,
		&item.FunctionCount,
		&item.FunctionArtifactBytes,
		&item.FunctionQuotaBytes,
		&item.SiteCount,
		&item.SiteArtifactBytes,
		&item.SiteReservedBytes,
		&item.SiteQuotaBytes,
		&item.RealtimeEventCount,
		&item.WebhookDeliveryCount7,
		&item.APIRequestCount30D,
		&item.APIEgressBytes30D,
		&item.FunctionInvocationCount30D,
		&item.FunctionFailureCount30D,
		&item.FunctionComputeMS30D,
		&item.CapturedAt,
	)
	item.ProjectID = projectID.String()
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectUsage{}, ErrNotFound
	}
	return item, err
}

// ProjectUsageMetering returns durable daily buckets for an authenticated
// project member. The inclusive window is capped at one year to keep response
// size bounded and to make the contract safe for operator charts.
func (r *Repository) ProjectUsageMetering(ctx context.Context, projectID, accountID uuid.UUID, from, to time.Time) (domain.ProjectUsageMetering, error) {
	fromDate, toDate, err := normalizeUsageWindow(from, to)
	if err != nil {
		return domain.ProjectUsageMetering{}, err
	}
	if err := r.requireProjectAccess(ctx, projectID, accountID); err != nil {
		return domain.ProjectUsageMetering{}, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT to_char(usage_date,'YYYY-MM-DD'),api_request_count,api_egress_bytes,function_invocation_count,function_failure_count,function_compute_ms
		FROM project_usage_daily
		WHERE project_id=$1 AND usage_date BETWEEN $2::date AND $3::date
		ORDER BY usage_date ASC`, projectID, fromDate.Format("2006-01-02"), toDate.Format("2006-01-02"))
	if err != nil {
		return domain.ProjectUsageMetering{}, err
	}
	defer rows.Close()
	result := domain.ProjectUsageMetering{
		ProjectID: projectID.String(),
		From:      fromDate.Format("2006-01-02"),
		To:        toDate.Format("2006-01-02"),
		Days:      make([]domain.ProjectUsageDay, 0),
		Totals:    domain.ProjectUsageDay{},
	}
	for rows.Next() {
		var item domain.ProjectUsageDay
		if err := rows.Scan(&item.Date, &item.APIRequestCount, &item.APIEgressBytes, &item.FunctionInvocationCount, &item.FunctionFailureCount, &item.FunctionComputeMS); err != nil {
			return domain.ProjectUsageMetering{}, err
		}
		result.Days = append(result.Days, item)
		result.Totals.APIRequestCount += item.APIRequestCount
		result.Totals.APIEgressBytes += item.APIEgressBytes
		result.Totals.FunctionInvocationCount += item.FunctionInvocationCount
		result.Totals.FunctionFailureCount += item.FunctionFailureCount
		result.Totals.FunctionComputeMS += item.FunctionComputeMS
	}
	if err := rows.Err(); err != nil {
		return domain.ProjectUsageMetering{}, err
	}
	return result, nil
}

func normalizeUsageWindow(from, to time.Time) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	if from.IsZero() {
		from = now.AddDate(0, 0, -29)
	}
	if to.IsZero() {
		to = now
	}
	from = time.Date(from.UTC().Year(), from.UTC().Month(), from.UTC().Day(), 0, 0, 0, 0, time.UTC)
	to = time.Date(to.UTC().Year(), to.UTC().Month(), to.UTC().Day(), 0, 0, 0, 0, time.UTC)
	if to.Before(from) || to.Sub(from) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: range must be between one and 367 calendar days", ErrInvalidUsageWindow)
	}
	return from, to, nil
}
