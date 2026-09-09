package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nazxf/stealth-api/internal/domain"
)

var ErrInvalidAgent = errors.New("invalid agent")

const agentProjection = `a.id,a.project_id,p.name,a.name,a.description,a.role,a.status,a.branch,a.provider,a.model,a.current_task,a.last_active_at,a.tools,a.instructions,a.created_by_account_id,a.created_at,a.updated_at`

var agentRoles = map[string]struct{}{
	"General":       {},
	"Frontend":      {},
	"Reviewer":      {},
	"Documentation": {},
}

var agentTools = map[string]struct{}{
	"Read files":  {},
	"Search code": {},
	"Edit files":  {},
	"Terminal":    {},
	"Run tests":   {},
	"Git diff":    {},
}

var supportedAgentRoles = []string{"General", "Frontend", "Reviewer", "Documentation"}
var supportedAgentTools = []string{"Read files", "Search code", "Edit files", "Terminal", "Run tests", "Git diff"}

// SupportedAgentRoles and SupportedAgentTools expose the same bounded values
// used by persistence validation so clients do not need to duplicate the
// Agent configuration contract.
func SupportedAgentRoles() []string {
	return append([]string(nil), supportedAgentRoles...)
}

func SupportedAgentTools() []string {
	return append([]string(nil), supportedAgentTools...)
}

// AgentInput is the durable configuration accepted when creating an agent.
// Provider credentials are intentionally absent; they will be introduced by
// the encrypted provider-connection control plane rather than this endpoint.
type AgentInput struct {
	ProjectID    uuid.UUID
	Name         string
	Description  string
	Role         string
	Branch       string
	Provider     string
	Model        string
	CurrentTask  *string
	Tools        []string
	Instructions *string
}

// AgentPatch contains only mutable configuration fields. Status and activity
// timestamps are controlled by the future run worker, not by the Console UI.
type AgentPatch struct {
	Name         *string
	Description  *string
	Role         *string
	Branch       *string
	Provider     *string
	Model        *string
	CurrentTask  *string
	Tools        *[]string
	Instructions *string
}

func (r *Repository) ListAgents(ctx context.Context, accountID uuid.UUID, limit int, cursor *uuid.UUID, projectID *uuid.UUID) ([]domain.Agent, string, bool, error) {
	if limit < 1 || limit > 100 {
		return nil, "", false, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidAgent)
	}
	canManage := false
	if projectID != nil {
		var err error
		canManage, err = r.AgentProjectCanManage(ctx, accountID, *projectID)
		if err != nil {
			return nil, "", false, err
		}
	}
	var cursorArg any
	if cursor != nil {
		cursorArg = *cursor
	}
	var projectArg any
	if projectID != nil {
		projectArg = *projectID
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+agentProjection+`
		FROM project_agents a
		JOIN projects p ON p.id=a.project_id
		JOIN organization_memberships m ON m.organization_id=p.organization_id
		WHERE m.account_id=$1
		  AND ($2::uuid IS NULL OR a.project_id=$2)
		  AND ($3::uuid IS NULL OR a.id>$3)
		ORDER BY a.id
		LIMIT $4`, accountID, projectArg, cursorArg, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.Agent, 0, limit)
	for rows.Next() {
		item, scanErr := scanAgent(rows)
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

// AgentProjectCanManage reports the capability shared by Agent configuration
// mutations and Run enqueue/cancel actions. Read access is intentionally
// broader and is enforced separately by the resource queries.
func (r *Repository) AgentProjectCanManage(ctx context.Context, accountID, projectID uuid.UUID) (bool, error) {
	role, err := r.projectRole(ctx, projectID, accountID)
	if err != nil {
		return false, err
	}
	return role == "owner" || role == "admin", nil
}

func (r *Repository) AgentByID(ctx context.Context, accountID, agentID uuid.UUID) (domain.Agent, error) {
	item, err := scanAgent(r.pool.QueryRow(ctx, `
		SELECT `+agentProjection+`
		FROM project_agents a
		JOIN projects p ON p.id=a.project_id
		JOIN organization_memberships m ON m.organization_id=p.organization_id
		WHERE a.id=$1 AND m.account_id=$2`, agentID, accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Agent{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateAgent(ctx context.Context, id, accountID uuid.UUID, input AgentInput) (domain.Agent, error) {
	normalized, err := normalizeAgentInput(input)
	if err != nil {
		return domain.Agent{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireProjectRoleTx(ctx, tx, normalized.ProjectID, accountID, "owner", "admin"); err != nil {
		return domain.Agent{}, err
	}
	orgID, err := projectOrganizationIDValue(ctx, tx, normalized.ProjectID)
	if err != nil {
		return domain.Agent{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO project_agents (id,project_id,created_by_account_id,name,description,role,branch,provider,model,current_task,tools,instructions)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		id, normalized.ProjectID, accountID, normalized.Name, normalized.Description, normalized.Role,
		normalized.Branch, normalized.Provider, normalized.Model, normalized.CurrentTask, normalized.Tools, normalized.Instructions)
	if err != nil {
		return domain.Agent{}, mapError(err)
	}
	item, err := scanAgent(tx.QueryRow(ctx, `SELECT `+agentProjection+` FROM project_agents a JOIN projects p ON p.id=a.project_id WHERE a.id=$1`, id))
	if err != nil {
		return domain.Agent{}, err
	}
	if err := writeAuditMetadata(ctx, tx, orgID, accountID, "agent.create", "agent", id, map[string]any{
		"project_id": normalized.ProjectID.String(), "name": normalized.Name, "role": normalized.Role,
	}); err != nil {
		return domain.Agent{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, normalized.ProjectID, "agent.create", "agent", id, map[string]any{
		"name": normalized.Name, "role": normalized.Role, "provider": normalized.Provider, "model": normalized.Model,
	}); err != nil {
		return domain.Agent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Agent{}, err
	}
	return item, nil
}

func (r *Repository) UpdateAgent(ctx context.Context, accountID, agentID uuid.UUID, patch AgentPatch) (domain.Agent, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	defer tx.Rollback(ctx)
	var projectID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT project_id FROM project_agents WHERE id=$1 FOR UPDATE`, agentID).Scan(&projectID); errors.Is(err, pgx.ErrNoRows) {
		return domain.Agent{}, ErrNotFound
	} else if err != nil {
		return domain.Agent{}, err
	}
	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner", "admin"); err != nil {
		return domain.Agent{}, err
	}
	current, err := scanAgent(tx.QueryRow(ctx, `SELECT `+agentProjection+` FROM project_agents a JOIN projects p ON p.id=a.project_id WHERE a.id=$1`, agentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Agent{}, ErrNotFound
	}
	if err != nil {
		return domain.Agent{}, err
	}
	updated, changed, err := applyAgentPatch(current, patch)
	if err != nil {
		return domain.Agent{}, err
	}
	if len(changed) == 0 {
		return current, nil
	}
	_, err = tx.Exec(ctx, `
		UPDATE project_agents
		SET name=$2,description=$3,role=$4,branch=$5,provider=$6,model=$7,current_task=$8,tools=$9,instructions=$10,updated_at=now()
		WHERE id=$1`, agentID, updated.Name, updated.Description, updated.Role, updated.Branch, updated.Provider, updated.Model, updated.CurrentTask, updated.Tools, updated.Instructions)
	if err != nil {
		return domain.Agent{}, mapError(err)
	}
	updated, err = scanAgent(tx.QueryRow(ctx, `SELECT `+agentProjection+` FROM project_agents a JOIN projects p ON p.id=a.project_id WHERE a.id=$1`, agentID))
	if err != nil {
		return domain.Agent{}, err
	}
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.Agent{}, err
	}
	if err := writeAuditMetadata(ctx, tx, orgID, accountID, "agent.update", "agent", agentID, map[string]any{"project_id": projectID.String(), "fields": changed}); err != nil {
		return domain.Agent{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "agent.update", "agent", agentID, map[string]any{"fields": changed}); err != nil {
		return domain.Agent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Agent{}, err
	}
	return updated, nil
}

func (r *Repository) DeleteAgent(ctx context.Context, accountID, agentID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var projectID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT project_id FROM project_agents WHERE id=$1 FOR UPDATE`, agentID).Scan(&projectID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner", "admin"); err != nil {
		return err
	}
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_agents WHERE id=$1`, agentID); err != nil {
		return err
	}
	if err := writeAuditMetadata(ctx, tx, orgID, accountID, "agent.delete", "agent", agentID, map[string]any{"project_id": projectID.String()}); err != nil {
		return err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "agent.delete", "agent", agentID, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type agentRow interface {
	Scan(dest ...any) error
}

func scanAgent(row agentRow) (domain.Agent, error) {
	var item domain.Agent
	err := row.Scan(
		&item.ID, &item.ProjectID, &item.ProjectName, &item.Name, &item.Description, &item.Role,
		&item.Status, &item.Branch, &item.Provider, &item.Model, &item.CurrentTask, &item.LastActiveAt,
		&item.Tools, &item.Instructions, &item.CreatedByAccountID, &item.CreatedAt, &item.UpdatedAt,
	)
	if item.Tools == nil {
		item.Tools = []string{}
	}
	return item, err
}

func normalizeAgentInput(input AgentInput) (AgentInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Role = strings.TrimSpace(input.Role)
	input.Branch = strings.TrimSpace(input.Branch)
	input.Provider = strings.TrimSpace(input.Provider)
	input.Model = strings.TrimSpace(input.Model)
	if input.Branch == "" {
		input.Branch = "main"
	}
	if input.ProjectID == uuid.Nil {
		return AgentInput{}, fmt.Errorf("%w: project_id is required", ErrInvalidAgent)
	}
	if !validAgentText(input.Name, 2, 120) {
		return AgentInput{}, fmt.Errorf("%w: name must be between 2 and 120 characters", ErrInvalidAgent)
	}
	if utf8.RuneCountInString(input.Description) > 2000 {
		return AgentInput{}, fmt.Errorf("%w: description must be at most 2000 characters", ErrInvalidAgent)
	}
	if _, ok := agentRoles[input.Role]; !ok {
		return AgentInput{}, fmt.Errorf("%w: role is not supported", ErrInvalidAgent)
	}
	if !validAgentText(input.Branch, 1, 255) {
		return AgentInput{}, fmt.Errorf("%w: branch is invalid", ErrInvalidAgent)
	}
	if !validAgentText(input.Provider, 1, 64) {
		return AgentInput{}, fmt.Errorf("%w: provider is invalid", ErrInvalidAgent)
	}
	if !validAgentText(input.Model, 1, 128) {
		return AgentInput{}, fmt.Errorf("%w: model is invalid", ErrInvalidAgent)
	}
	var err error
	input.Tools, err = normalizeAgentTools(input.Tools)
	if err != nil {
		return AgentInput{}, err
	}
	input.CurrentTask, err = normalizeAgentOptional(input.CurrentTask, 500, "current_task")
	if err != nil {
		return AgentInput{}, err
	}
	input.Instructions, err = normalizeAgentOptional(input.Instructions, 10000, "instructions")
	if err != nil {
		return AgentInput{}, err
	}
	return input, nil
}

func normalizeAgentTools(raw []string) ([]string, error) {
	if len(raw) > len(agentTools) {
		return nil, fmt.Errorf("%w: at most %d tools are supported", ErrInvalidAgent, len(agentTools))
	}
	seen := make(map[string]struct{}, len(raw))
	result := make([]string, 0, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		if _, ok := agentTools[value]; !ok {
			return nil, fmt.Errorf("%w: unsupported tool %q", ErrInvalidAgent, value)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("%w: duplicate tool %q", ErrInvalidAgent, value)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeAgentOptional(value *string, max int, field string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(trimmed) > max {
		return nil, fmt.Errorf("%w: %s must be at most %d characters", ErrInvalidAgent, field, max)
	}
	return &trimmed, nil
}

func validAgentText(value string, min, max int) bool {
	if utf8.RuneCountInString(value) < min || utf8.RuneCountInString(value) > max || value != strings.TrimSpace(value) {
		return false
	}
	return !strings.ContainsAny(value, "\x00\r\n\t")
}

func applyAgentPatch(current domain.Agent, patch AgentPatch) (domain.Agent, []string, error) {
	updated := current
	changed := make([]string, 0, 9)
	if patch.Name != nil {
		value := strings.TrimSpace(*patch.Name)
		if !validAgentText(value, 2, 120) {
			return domain.Agent{}, nil, fmt.Errorf("%w: name must be between 2 and 120 characters", ErrInvalidAgent)
		}
		updated.Name = value
		changed = append(changed, "name")
	}
	if patch.Description != nil {
		value := strings.TrimSpace(*patch.Description)
		if utf8.RuneCountInString(value) > 2000 {
			return domain.Agent{}, nil, fmt.Errorf("%w: description must be at most 2000 characters", ErrInvalidAgent)
		}
		updated.Description = value
		changed = append(changed, "description")
	}
	if patch.Role != nil {
		value := strings.TrimSpace(*patch.Role)
		if _, ok := agentRoles[value]; !ok {
			return domain.Agent{}, nil, fmt.Errorf("%w: role is not supported", ErrInvalidAgent)
		}
		updated.Role = value
		changed = append(changed, "role")
	}
	if patch.Branch != nil {
		value := strings.TrimSpace(*patch.Branch)
		if !validAgentText(value, 1, 255) {
			return domain.Agent{}, nil, fmt.Errorf("%w: branch is invalid", ErrInvalidAgent)
		}
		updated.Branch = value
		changed = append(changed, "branch")
	}
	if patch.Provider != nil {
		value := strings.TrimSpace(*patch.Provider)
		if !validAgentText(value, 1, 64) {
			return domain.Agent{}, nil, fmt.Errorf("%w: provider is invalid", ErrInvalidAgent)
		}
		updated.Provider = value
		changed = append(changed, "provider")
	}
	if patch.Model != nil {
		value := strings.TrimSpace(*patch.Model)
		if !validAgentText(value, 1, 128) {
			return domain.Agent{}, nil, fmt.Errorf("%w: model is invalid", ErrInvalidAgent)
		}
		updated.Model = value
		changed = append(changed, "model")
	}
	if patch.CurrentTask != nil {
		value, err := normalizeAgentOptional(patch.CurrentTask, 500, "current_task")
		if err != nil {
			return domain.Agent{}, nil, err
		}
		updated.CurrentTask = value
		changed = append(changed, "current_task")
	}
	if patch.Tools != nil {
		value, err := normalizeAgentTools(*patch.Tools)
		if err != nil {
			return domain.Agent{}, nil, err
		}
		updated.Tools = value
		changed = append(changed, "tools")
	}
	if patch.Instructions != nil {
		value, err := normalizeAgentOptional(patch.Instructions, 10000, "instructions")
		if err != nil {
			return domain.Agent{}, nil, err
		}
		updated.Instructions = value
		changed = append(changed, "instructions")
	}
	return updated, changed, nil
}
