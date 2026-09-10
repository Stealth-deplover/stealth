package repository

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidOrganizationIncident           = errors.New("invalid organization incident")
	ErrInvalidOrganizationIncidentTransition = errors.New("invalid organization incident transition")
)

const (
	organizationIncidentMaxTitle    = 160
	organizationIncidentMaxMessage  = 4000
	organizationIncidentMaxServices = 16
	organizationIncidentMaxService  = 80
)

type OrganizationIncidentInput struct {
	Title    string
	Severity string
	Status   string
	Services []string
	Message  string
}

type OrganizationIncidentPatch struct {
	Title    *string
	Severity *string
	Status   *string
	Services *[]string
	Message  *string
}

const organizationIncidentProjection = `
	i.id,
	i.organization_id,
	i.created_by_account_id,
	a.email,
	i.title,
	i.severity,
	i.status,
	i.services,
	i.started_at,
	i.resolved_at,
	i.created_at,
	i.updated_at`

type organizationIncidentScanner interface {
	Scan(...any) error
}

func scanOrganizationIncident(row organizationIncidentScanner) (domain.OrganizationIncident, error) {
	var item domain.OrganizationIncident
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.CreatedByAccountID,
		&item.CreatedByEmail,
		&item.Title,
		&item.Severity,
		&item.Status,
		&item.Services,
		&item.StartedAt,
		&item.ResolvedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return domain.OrganizationIncident{}, err
	}
	if item.Services == nil {
		item.Services = []string{}
	}
	item.Updates = []domain.OrganizationIncidentUpdate{}
	return item, nil
}

func normalizeOrganizationIncidentTitle(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > organizationIncidentMaxTitle || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("%w: title must be between 3 and %d characters and cannot contain controls", ErrInvalidOrganizationIncident, organizationIncidentMaxTitle)
	}
	if utf8.RuneCountInString(value) < 3 {
		return "", fmt.Errorf("%w: title must be between 3 and %d characters", ErrInvalidOrganizationIncident, organizationIncidentMaxTitle)
	}
	return value, nil
}

func normalizeOrganizationIncidentSeverity(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value != "critical" && value != "warning" && value != "info" {
		return "", fmt.Errorf("%w: severity must be critical, warning, or info", ErrInvalidOrganizationIncident)
	}
	return value, nil
}

func normalizeOrganizationIncidentStatus(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value != "investigating" && value != "identified" && value != "monitoring" && value != "resolved" {
		return "", fmt.Errorf("%w: status must be investigating, identified, monitoring, or resolved", ErrInvalidOrganizationIncident)
	}
	return value, nil
}

func normalizeOrganizationIncidentServices(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > organizationIncidentMaxServices {
		return nil, fmt.Errorf("%w: services must contain between 1 and %d items", ErrInvalidOrganizationIncident, organizationIncidentMaxServices)
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || utf8.RuneCountInString(value) > organizationIncidentMaxService || strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("%w: each service must be 1 to %d characters and cannot contain controls", ErrInvalidOrganizationIncident, organizationIncidentMaxService)
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: services must be unique", ErrInvalidOrganizationIncident)
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func normalizeOrganizationIncidentMessage(value string, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" && !required {
		return "", nil
	}
	if value == "" || utf8.RuneCountInString(value) > organizationIncidentMaxMessage || strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("%w: message must be between 1 and %d characters", ErrInvalidOrganizationIncident, organizationIncidentMaxMessage)
	}
	return value, nil
}

func normalizeOrganizationIncidentInput(input OrganizationIncidentInput) (OrganizationIncidentInput, error) {
	title, err := normalizeOrganizationIncidentTitle(input.Title)
	if err != nil {
		return OrganizationIncidentInput{}, err
	}
	severity, err := normalizeOrganizationIncidentSeverity(input.Severity)
	if err != nil {
		return OrganizationIncidentInput{}, err
	}
	status := input.Status
	if strings.TrimSpace(status) == "" {
		status = "investigating"
	}
	status, err = normalizeOrganizationIncidentStatus(status)
	if err != nil {
		return OrganizationIncidentInput{}, err
	}
	services, err := normalizeOrganizationIncidentServices(input.Services)
	if err != nil {
		return OrganizationIncidentInput{}, err
	}
	message, err := normalizeOrganizationIncidentMessage(input.Message, false)
	if err != nil {
		return OrganizationIncidentInput{}, err
	}
	if message == "" {
		message = "Incident opened manually from the admin console."
	}
	return OrganizationIncidentInput{Title: title, Severity: severity, Status: status, Services: services, Message: message}, nil
}

func normalizeOrganizationIncidentPatch(patch OrganizationIncidentPatch) (OrganizationIncidentPatch, error) {
	if patch.Title != nil {
		value, err := normalizeOrganizationIncidentTitle(*patch.Title)
		if err != nil {
			return OrganizationIncidentPatch{}, err
		}
		patch.Title = &value
	}
	if patch.Severity != nil {
		value, err := normalizeOrganizationIncidentSeverity(*patch.Severity)
		if err != nil {
			return OrganizationIncidentPatch{}, err
		}
		patch.Severity = &value
	}
	if patch.Status != nil {
		value, err := normalizeOrganizationIncidentStatus(*patch.Status)
		if err != nil {
			return OrganizationIncidentPatch{}, err
		}
		patch.Status = &value
	}
	if patch.Services != nil {
		value, err := normalizeOrganizationIncidentServices(*patch.Services)
		if err != nil {
			return OrganizationIncidentPatch{}, err
		}
		patch.Services = &value
	}
	if patch.Message != nil {
		value, err := normalizeOrganizationIncidentMessage(*patch.Message, false)
		if err != nil {
			return OrganizationIncidentPatch{}, err
		}
		patch.Message = &value
	}
	if patch.Title == nil && patch.Severity == nil && patch.Status == nil && patch.Services == nil && patch.Message == nil {
		return OrganizationIncidentPatch{}, fmt.Errorf("%w: at least one field is required", ErrInvalidOrganizationIncident)
	}
	return patch, nil
}

func incidentStatusTransitionAllowed(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case "investigating":
		return to == "identified" || to == "monitoring" || to == "resolved"
	case "identified":
		return to == "investigating" || to == "monitoring" || to == "resolved"
	case "monitoring":
		return to == "investigating" || to == "identified" || to == "resolved"
	case "resolved":
		return to == "investigating"
	default:
		return false
	}
}

func loadOrganizationIncidentUpdates(ctx context.Context, tx pgx.Tx, item *domain.OrganizationIncident) error {
	rows, err := tx.Query(ctx, `
		SELECT u.id,u.incident_id,u.author_account_id,a.email,u.status,u.message,u.created_at
		FROM organization_incident_updates u
		LEFT JOIN accounts a ON a.id=u.author_account_id
		WHERE u.incident_id=$1
		ORDER BY u.created_at ASC,u.id ASC`, uuid.MustParse(item.ID))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var update domain.OrganizationIncidentUpdate
		if err := rows.Scan(&update.ID, &update.IncidentID, &update.AuthorAccountID, &update.AuthorEmail, &update.Status, &update.Message, &update.CreatedAt); err != nil {
			return err
		}
		item.Updates = append(item.Updates, update)
	}
	return rows.Err()
}

func (r *Repository) ListOrganizationIncidents(ctx context.Context, organizationID, accountID uuid.UUID, limit int, cursor string) ([]domain.OrganizationIncident, string, bool, error) {
	if limit < 1 || limit > 100 {
		return nil, "", false, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidOrganizationIncident)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, "", false, err
	}
	defer tx.Rollback(ctx)
	role, err := r.organizationRoleTx(ctx, tx, organizationID, accountID)
	if errors.Is(err, ErrNotFound) {
		return nil, "", false, ErrForbidden
	}
	if err != nil {
		return nil, "", false, err
	}
	rows, err := tx.Query(ctx, `
		SELECT `+organizationIncidentProjection+`
		FROM organization_incidents i
		LEFT JOIN accounts a ON a.id=i.created_by_account_id
		WHERE i.organization_id=$1 AND ($2='' OR i.id::text < $2)
		ORDER BY i.id DESC
		LIMIT $3`, organizationID, cursor, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	items := make([]domain.OrganizationIncident, 0, limit)
	for rows.Next() {
		item, scanErr := scanOrganizationIncident(rows)
		if scanErr != nil {
			rows.Close()
			return nil, "", false, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, "", false, err
	}
	rows.Close()
	for index := range items {
		if err := loadOrganizationIncidentUpdates(ctx, tx, &items[index]); err != nil {
			return nil, "", false, err
		}
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", false, err
	}
	return items, next, role == "owner" || role == "admin", nil
}

func (r *Repository) GetOrganizationIncident(ctx context.Context, organizationID, accountID, incidentID uuid.UUID) (domain.OrganizationIncident, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.OrganizationIncident{}, false, err
	}
	defer tx.Rollback(ctx)
	role, err := r.organizationRoleTx(ctx, tx, organizationID, accountID)
	if errors.Is(err, ErrNotFound) {
		return domain.OrganizationIncident{}, false, ErrForbidden
	}
	if err != nil {
		return domain.OrganizationIncident{}, false, err
	}
	item, err := scanOrganizationIncident(tx.QueryRow(ctx, `
		SELECT `+organizationIncidentProjection+`
		FROM organization_incidents i
		LEFT JOIN accounts a ON a.id=i.created_by_account_id
		WHERE i.organization_id=$1 AND i.id=$2`, organizationID, incidentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OrganizationIncident{}, false, ErrNotFound
	}
	if err != nil {
		return domain.OrganizationIncident{}, false, err
	}
	if err := loadOrganizationIncidentUpdates(ctx, tx, &item); err != nil {
		return domain.OrganizationIncident{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OrganizationIncident{}, false, err
	}
	return item, role == "owner" || role == "admin", nil
}

func (r *Repository) CreateOrganizationIncident(ctx context.Context, id, organizationID, accountID uuid.UUID, input OrganizationIncidentInput) (domain.OrganizationIncident, error) {
	normalized, err := normalizeOrganizationIncidentInput(input)
	if err != nil {
		return domain.OrganizationIncident{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.OrganizationIncident{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := r.organizationManagerRoleTx(ctx, tx, organizationID, accountID); err != nil {
		return domain.OrganizationIncident{}, err
	}
	var resolvedAt any
	if normalized.Status == "resolved" {
		resolvedAt = time.Now().UTC()
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_incidents (id,organization_id,created_by_account_id,title,severity,status,services,resolved_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, id, organizationID, accountID, normalized.Title, normalized.Severity, normalized.Status, normalized.Services, resolvedAt); err != nil {
		return domain.OrganizationIncident{}, mapError(err)
	}
	updateID := uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_incident_updates (id,incident_id,author_account_id,status,message)
		VALUES ($1,$2,$3,$4,$5)`, updateID, id, accountID, normalized.Status, normalized.Message); err != nil {
		return domain.OrganizationIncident{}, err
	}
	if err := writeAuditMetadata(ctx, tx, organizationID, accountID, "organization.incident.create", "organization_incident", id, map[string]any{
		"title": normalized.Title, "severity": normalized.Severity, "status": normalized.Status, "services": normalized.Services,
	}); err != nil {
		return domain.OrganizationIncident{}, err
	}
	item, err := scanOrganizationIncident(tx.QueryRow(ctx, `
		SELECT `+organizationIncidentProjection+`
		FROM organization_incidents i
		LEFT JOIN accounts a ON a.id=i.created_by_account_id
		WHERE i.id=$1`, id))
	if err != nil {
		return domain.OrganizationIncident{}, err
	}
	if err := loadOrganizationIncidentUpdates(ctx, tx, &item); err != nil {
		return domain.OrganizationIncident{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OrganizationIncident{}, err
	}
	return item, nil
}

func (r *Repository) UpdateOrganizationIncident(ctx context.Context, organizationID, incidentID, accountID uuid.UUID, patch OrganizationIncidentPatch) (domain.OrganizationIncident, error) {
	normalized, err := normalizeOrganizationIncidentPatch(patch)
	if err != nil {
		return domain.OrganizationIncident{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.OrganizationIncident{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := r.organizationManagerRoleTx(ctx, tx, organizationID, accountID); err != nil {
		return domain.OrganizationIncident{}, err
	}
	item, err := scanOrganizationIncident(tx.QueryRow(ctx, `
		SELECT `+organizationIncidentProjection+`
		FROM organization_incidents i
		LEFT JOIN accounts a ON a.id=i.created_by_account_id
		WHERE i.organization_id=$1 AND i.id=$2
		FOR UPDATE OF i`, organizationID, incidentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OrganizationIncident{}, ErrNotFound
	}
	if err != nil {
		return domain.OrganizationIncident{}, err
	}
	previous := item
	nextTitle, nextSeverity, nextStatus, nextServices := item.Title, item.Severity, item.Status, item.Services
	if normalized.Title != nil {
		nextTitle = *normalized.Title
	}
	if normalized.Severity != nil {
		nextSeverity = *normalized.Severity
	}
	if normalized.Status != nil {
		nextStatus = *normalized.Status
	}
	if normalized.Services != nil {
		nextServices = *normalized.Services
	}
	if !incidentStatusTransitionAllowed(item.Status, nextStatus) {
		return domain.OrganizationIncident{}, ErrInvalidOrganizationIncidentTransition
	}
	if nextStatus == "resolved" && item.ResolvedAt == nil {
		now := time.Now().UTC()
		item.ResolvedAt = &now
	} else if nextStatus != "resolved" {
		item.ResolvedAt = nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE organization_incidents
		SET title=$3,severity=$4,status=$5,services=$6,resolved_at=$7,updated_at=now()
		WHERE organization_id=$1 AND id=$2`, organizationID, incidentID, nextTitle, nextSeverity, nextStatus, nextServices, item.ResolvedAt); err != nil {
		return domain.OrganizationIncident{}, mapError(err)
	}
	changed := map[string]any{}
	if previous.Title != nextTitle {
		changed["title"] = map[string]string{"from": previous.Title, "to": nextTitle}
	}
	if previous.Severity != nextSeverity {
		changed["severity"] = map[string]string{"from": previous.Severity, "to": nextSeverity}
	}
	if previous.Status != nextStatus {
		changed["status"] = map[string]string{"from": previous.Status, "to": nextStatus}
	}
	if !slices.Equal(previous.Services, nextServices) {
		changed["services"] = map[string]any{"from": previous.Services, "to": nextServices}
	}
	message := ""
	if normalized.Message != nil {
		message = *normalized.Message
	}
	if message == "" {
		switch {
		case previous.Status != nextStatus && nextStatus == "resolved":
			message = "Incident resolved from the admin console."
		case previous.Status != nextStatus:
			message = fmt.Sprintf("Incident status changed to %s.", nextStatus)
		default:
			message = "Incident details updated from the admin console."
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_incident_updates (id,incident_id,author_account_id,status,message)
		VALUES ($1,$2,$3,$4,$5)`, uuid.Must(uuid.NewV7()), incidentID, accountID, nextStatus, message); err != nil {
		return domain.OrganizationIncident{}, err
	}
	if err := writeAuditMetadata(ctx, tx, organizationID, accountID, "organization.incident.update", "organization_incident", incidentID, map[string]any{"changes": changed}); err != nil {
		return domain.OrganizationIncident{}, err
	}
	item, err = scanOrganizationIncident(tx.QueryRow(ctx, `
		SELECT `+organizationIncidentProjection+`
		FROM organization_incidents i
		LEFT JOIN accounts a ON a.id=i.created_by_account_id
		WHERE i.id=$1`, incidentID))
	if err != nil {
		return domain.OrganizationIncident{}, err
	}
	if err := loadOrganizationIncidentUpdates(ctx, tx, &item); err != nil {
		return domain.OrganizationIncident{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OrganizationIncident{}, err
	}
	return item, nil
}
