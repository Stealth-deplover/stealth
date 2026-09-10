package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nazxf/stealth-api/internal/domain"
)

var (
	ErrInvalidAgentRun           = errors.New("invalid agent run")
	ErrInvalidAgentRunTransition = errors.New("invalid agent run transition")
	ErrAgentRunNotAvailable      = errors.New("agent run is not available")
	ErrNoAgentRunJob             = errors.New("no agent run job available")
)

const (
	agentRunMaxPrompt     = 20_000
	agentRunMaxOutput     = 100_000
	agentRunMaxError      = 4_000
	agentRunMaxSteps      = 256
	agentRunMaxChanges    = 256
	agentRunMaxLogBytes   = 16_000
	agentRunMaxWorkerID   = 128
	agentRunMaxStepLabel  = 256
	agentRunMaxStepTarget = 2_000
	agentRunMaxChangePath = 1_000
)

const agentRunProjection = `r.id,r.agent_id,r.project_id,r.created_by_account_id,r.prompt,r.status,r.output_text,r.error_message,r.steps,r.changes,r.queued_at,r.started_at,r.finished_at,r.created_at,r.updated_at`
const agentRunReturningProjection = `id,agent_id,project_id,created_by_account_id,prompt,status,output_text,error_message,steps,changes,queued_at,started_at,finished_at,created_at,updated_at`
const agentRunLogProjection = `l.id,l.run_id,l.project_id,l.sequence,l.level,l.message,l.created_at`

type agentRunScanner interface {
	Scan(...any) error
}

type AgentRunInput struct {
	Prompt string
}

// AgentRunResult is only accepted by a trusted worker. Provider credentials,
// source contents, and tool execution details never travel through the
// Console API.
type AgentRunResult struct {
	Status       string
	OutputText   *string
	ErrorMessage *string
	Steps        []domain.AgentRunStep
	Changes      []domain.AgentRunChange
}

type AgentRunJob struct {
	Run   domain.AgentRun
	Agent domain.Agent
}

func scanAgentRun(row agentRunScanner) (domain.AgentRun, error) {
	var item domain.AgentRun
	var stepsJSON, changesJSON []byte
	err := row.Scan(
		&item.ID, &item.AgentID, &item.ProjectID, &item.CreatedByAccountID, &item.Prompt,
		&item.Status, &item.OutputText, &item.ErrorMessage, &stepsJSON, &changesJSON,
		&item.QueuedAt, &item.StartedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return domain.AgentRun{}, err
	}
	if len(stepsJSON) == 0 {
		item.Steps = []domain.AgentRunStep{}
	} else if err := json.Unmarshal(stepsJSON, &item.Steps); err != nil {
		return domain.AgentRun{}, fmt.Errorf("decode agent run steps: %w", err)
	}
	if len(changesJSON) == 0 {
		item.Changes = []domain.AgentRunChange{}
	} else if err := json.Unmarshal(changesJSON, &item.Changes); err != nil {
		return domain.AgentRun{}, fmt.Errorf("decode agent run changes: %w", err)
	}
	if item.Steps == nil {
		item.Steps = []domain.AgentRunStep{}
	}
	if item.Changes == nil {
		item.Changes = []domain.AgentRunChange{}
	}
	return item, nil
}

func scanAgentRunLog(row agentRunScanner) (domain.AgentRunLog, error) {
	var item domain.AgentRunLog
	return item, row.Scan(&item.ID, &item.RunID, &item.ProjectID, &item.Sequence, &item.Level, &item.Message, &item.CreatedAt)
}

func normalizeAgentRunPrompt(prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("%w: prompt is required", ErrInvalidAgentRun)
	}
	if utf8.RuneCountInString(prompt) > agentRunMaxPrompt {
		return "", fmt.Errorf("%w: prompt must be at most %d characters", ErrInvalidAgentRun, agentRunMaxPrompt)
	}
	if strings.ContainsRune(prompt, '\x00') {
		return "", fmt.Errorf("%w: prompt cannot contain NUL", ErrInvalidAgentRun)
	}
	return prompt, nil
}

func normalizeAgentRunWorkerID(workerID string) (string, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || len(workerID) > agentRunMaxWorkerID || strings.ContainsAny(workerID, "\x00\r\n\t") {
		return "", fmt.Errorf("%w: worker id is invalid", ErrInvalidAgentRun)
	}
	return workerID, nil
}

func normalizeAgentRunResult(result AgentRunResult) (AgentRunResult, []byte, []byte, error) {
	if result.Status != "completed" && result.Status != "failed" && result.Status != "cancelled" {
		return AgentRunResult{}, nil, nil, fmt.Errorf("%w: terminal status is invalid", ErrInvalidAgentRunTransition)
	}
	if result.OutputText != nil {
		value := *result.OutputText
		if len(value) > agentRunMaxOutput || strings.ContainsRune(value, '\x00') {
			return AgentRunResult{}, nil, nil, fmt.Errorf("%w: output_text is too large or contains NUL", ErrInvalidAgentRun)
		}
		result.OutputText = &value
	}
	if result.ErrorMessage != nil {
		value := strings.TrimSpace(*result.ErrorMessage)
		if len(value) > agentRunMaxError || strings.ContainsRune(value, '\x00') {
			return AgentRunResult{}, nil, nil, fmt.Errorf("%w: error_message is too large or contains NUL", ErrInvalidAgentRun)
		}
		if value == "" {
			result.ErrorMessage = nil
		} else {
			result.ErrorMessage = &value
		}
	}
	if len(result.Steps) > agentRunMaxSteps || len(result.Changes) > agentRunMaxChanges {
		return AgentRunResult{}, nil, nil, fmt.Errorf("%w: result contains too many steps or changes", ErrInvalidAgentRun)
	}
	if result.Steps == nil {
		result.Steps = []domain.AgentRunStep{}
	}
	if result.Changes == nil {
		result.Changes = []domain.AgentRunChange{}
	}
	for index := range result.Steps {
		step := &result.Steps[index]
		step.ID = strings.TrimSpace(step.ID)
		step.Type = strings.TrimSpace(step.Type)
		step.Label = strings.TrimSpace(step.Label)
		step.Target = strings.TrimSpace(step.Target)
		step.Status = strings.TrimSpace(step.Status)
		if !validAgentRunStep(*step) {
			return AgentRunResult{}, nil, nil, fmt.Errorf("%w: step %d is invalid", ErrInvalidAgentRun, index)
		}
	}
	for index := range result.Changes {
		change := &result.Changes[index]
		change.Path = strings.TrimSpace(change.Path)
		change.Status = strings.TrimSpace(change.Status)
		if change.Path == "" || len(change.Path) > agentRunMaxChangePath || strings.ContainsAny(change.Path, "\x00\r\n") || (change.Status != "added" && change.Status != "modified") || change.Additions < 0 || change.Deletions < 0 {
			return AgentRunResult{}, nil, nil, fmt.Errorf("%w: change %d is invalid", ErrInvalidAgentRun, index)
		}
	}
	stepsJSON, err := json.Marshal(result.Steps)
	if err != nil {
		return AgentRunResult{}, nil, nil, err
	}
	changesJSON, err := json.Marshal(result.Changes)
	if err != nil {
		return AgentRunResult{}, nil, nil, err
	}
	return result, stepsJSON, changesJSON, nil
}

func validAgentRunStep(step domain.AgentRunStep) bool {
	if step.ID == "" || len(step.ID) > 128 || strings.ContainsAny(step.ID, "\x00\r\n\t") {
		return false
	}
	if step.Type != "read" && step.Type != "edit" && step.Type != "search" && step.Type != "command" && step.Type != "check" {
		return false
	}
	if step.Label == "" || utf8.RuneCountInString(step.Label) > agentRunMaxStepLabel || strings.ContainsAny(step.Label, "\x00\r\n") {
		return false
	}
	if step.Target == "" || utf8.RuneCountInString(step.Target) > agentRunMaxStepTarget || strings.ContainsAny(step.Target, "\x00\r\n") {
		return false
	}
	return step.Status == "pending" || step.Status == "done"
}

func (r *Repository) ListAgentRuns(ctx context.Context, accountID, agentID uuid.UUID, limit int, cursor *uuid.UUID) ([]domain.AgentRun, string, bool, error) {
	if limit < 1 || limit > 100 {
		return nil, "", false, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidAgentRun)
	}
	agent, err := r.AgentByID(ctx, accountID, agentID)
	if err != nil {
		return nil, "", false, err
	}
	projectID, err := uuid.Parse(agent.ProjectID)
	if err != nil {
		return nil, "", false, fmt.Errorf("parse Agent project id: %w", err)
	}
	canManage, err := r.AgentProjectCanManage(ctx, accountID, projectID)
	if err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+agentRunProjection+`
		FROM agent_runs r
		JOIN project_agents a ON a.id=r.agent_id AND a.project_id=r.project_id
		JOIN projects p ON p.id=r.project_id
		JOIN organization_memberships m ON m.organization_id=p.organization_id
		WHERE r.agent_id=$1 AND m.account_id=$2 AND ($3::uuid IS NULL OR r.id<$3)
		ORDER BY r.id DESC
		LIMIT $4`, agentID, accountID, cursor, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.AgentRun, 0, limit)
	for rows.Next() {
		item, scanErr := scanAgentRun(rows)
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

func (r *Repository) AgentRunByID(ctx context.Context, accountID, agentID, runID uuid.UUID) (domain.AgentRun, error) {
	item, err := scanAgentRun(r.pool.QueryRow(ctx, `
		SELECT `+agentRunProjection+`
		FROM agent_runs r
		JOIN project_agents a ON a.id=r.agent_id AND a.project_id=r.project_id
		JOIN projects p ON p.id=r.project_id
		JOIN organization_memberships m ON m.organization_id=p.organization_id
		WHERE r.agent_id=$1 AND r.id=$2 AND m.account_id=$3`, agentID, runID, accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentRun{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateAgentRun(ctx context.Context, id, accountID, agentID uuid.UUID, input AgentRunInput) (domain.AgentRun, error) {
	prompt, err := normalizeAgentRunPrompt(input.Prompt)
	if err != nil {
		return domain.AgentRun{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AgentRun{}, err
	}
	defer tx.Rollback(ctx)
	var projectID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT project_id FROM project_agents WHERE id=$1 FOR UPDATE`, agentID).Scan(&projectID); errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentRun{}, ErrNotFound
	} else if err != nil {
		return domain.AgentRun{}, err
	}
	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner", "admin"); err != nil {
		return domain.AgentRun{}, err
	}
	item, err := scanAgentRun(tx.QueryRow(ctx, `
		INSERT INTO agent_runs (id,agent_id,project_id,created_by_account_id,prompt)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING `+agentRunReturningProjection, id, agentID, projectID, accountID, prompt))
	if err != nil {
		return domain.AgentRun{}, mapError(err)
	}
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.AgentRun{}, err
	}
	metadata := map[string]any{"project_id": projectID.String(), "agent_id": agentID.String(), "status": item.Status}
	if err := writeAuditMetadata(ctx, tx, orgID, accountID, "agent.run.accepted", "agent_run", id, metadata); err != nil {
		return domain.AgentRun{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "agent.run.accepted", "agent_run", id, metadata); err != nil {
		return domain.AgentRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AgentRun{}, err
	}
	return item, nil
}

func (r *Repository) CancelAgentRun(ctx context.Context, accountID, agentID, runID uuid.UUID) (domain.AgentRun, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AgentRun{}, err
	}
	defer tx.Rollback(ctx)
	var projectID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT project_id FROM agent_runs WHERE agent_id=$1 AND id=$2 FOR UPDATE`, agentID, runID).Scan(&projectID); errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentRun{}, ErrNotFound
	} else if err != nil {
		return domain.AgentRun{}, err
	}
	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner", "admin"); err != nil {
		return domain.AgentRun{}, err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM agent_runs WHERE agent_id=$1 AND id=$2`, agentID, runID).Scan(&status); err != nil {
		return domain.AgentRun{}, err
	}
	if status != "queued" && status != "running" {
		return domain.AgentRun{}, ErrInvalidAgentRunTransition
	}
	item, err := scanAgentRun(tx.QueryRow(ctx, `
		UPDATE agent_runs
		SET status='cancelled',finished_at=now(),claimed_at=NULL,worker_id=NULL,updated_at=now()
		WHERE agent_id=$1 AND id=$2
		RETURNING `+agentRunReturningProjection, agentID, runID))
	if err != nil {
		return domain.AgentRun{}, err
	}
	if err := r.refreshAgentStatusTx(ctx, tx, agentID, projectID); err != nil {
		return domain.AgentRun{}, err
	}
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.AgentRun{}, err
	}
	metadata := map[string]any{"project_id": projectID.String(), "agent_id": agentID.String(), "status": item.Status}
	if err := writeAuditMetadata(ctx, tx, orgID, accountID, "agent.run.cancelled", "agent_run", runID, metadata); err != nil {
		return domain.AgentRun{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "agent.run.cancelled", "agent_run", runID, metadata); err != nil {
		return domain.AgentRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AgentRun{}, err
	}
	return item, nil
}

// ClaimNextAgentRun atomically leases one queued run. It is intentionally
// separate from the HTTP API so a future provider worker can use the same
// queue without exposing worker credentials or filesystem details.
func (r *Repository) ClaimNextAgentRun(ctx context.Context, workerID string) (AgentRunJob, error) {
	return r.ClaimNextAgentRunForProviders(ctx, workerID, nil)
}

// ClaimNextAgentRunForProviders atomically leases one queued run whose
// provider is present in allowedProviders. A nil provider list means all
// providers (the legacy control-plane primitive); an empty list claims
// nothing. Comparing normalized lowercase values keeps existing agent rows
// written with display-case provider names compatible with workers.
func (r *Repository) ClaimNextAgentRunForProviders(ctx context.Context, workerID string, allowedProviders []string) (AgentRunJob, error) {
	workerID, err := normalizeAgentRunWorkerID(workerID)
	if err != nil {
		return AgentRunJob{}, err
	}
	var providerArg any
	if allowedProviders != nil {
		normalizedProviders := make([]string, 0, len(allowedProviders))
		seen := make(map[string]struct{}, len(allowedProviders))
		for _, provider := range allowedProviders {
			provider = strings.ToLower(strings.TrimSpace(provider))
			if provider == "" || strings.ContainsAny(provider, "\x00\r\n\t") {
				return AgentRunJob{}, fmt.Errorf("%w: provider filter is invalid", ErrInvalidAgentRun)
			}
			if _, exists := seen[provider]; exists {
				continue
			}
			seen[provider] = struct{}{}
			normalizedProviders = append(normalizedProviders, provider)
		}
		providerArg = normalizedProviders
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AgentRunJob{}, err
	}
	defer tx.Rollback(ctx)
	var runID, agentID, projectID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT r.id,r.agent_id,r.project_id
		FROM agent_runs r
		JOIN project_agents a ON a.id=r.agent_id AND a.project_id=r.project_id
		WHERE r.status='queued'
		  AND ($1::text[] IS NULL OR lower(a.provider)=ANY($1::text[]))
		ORDER BY r.queued_at,r.id
		FOR UPDATE OF r SKIP LOCKED
		LIMIT 1`, providerArg).Scan(&runID, &agentID, &projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRunJob{}, ErrNoAgentRunJob
	}
	if err != nil {
		return AgentRunJob{}, err
	}
	run, err := scanAgentRun(tx.QueryRow(ctx, `
		UPDATE agent_runs
		SET status='running',started_at=COALESCE(started_at,now()),claimed_at=now(),worker_id=$4,updated_at=now()
		WHERE id=$1 AND agent_id=$2 AND project_id=$3 AND status='queued'
		RETURNING `+agentRunReturningProjection, runID, agentID, projectID, workerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRunJob{}, ErrNoAgentRunJob
	}
	if err != nil {
		return AgentRunJob{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "agent.run.running", "agent_run", runID, map[string]any{"agent_id": agentID.String(), "status": run.Status}); err != nil {
		return AgentRunJob{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_agents SET status='running',last_active_at=now(),updated_at=now() WHERE id=$1 AND project_id=$2`, agentID, projectID); err != nil {
		return AgentRunJob{}, err
	}
	agent, err := scanAgent(tx.QueryRow(ctx, `SELECT `+agentProjection+` FROM project_agents a JOIN projects p ON p.id=a.project_id WHERE a.id=$1 AND a.project_id=$2`, agentID, projectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRunJob{}, ErrAgentRunNotAvailable
	}
	if err != nil {
		return AgentRunJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentRunJob{}, err
	}
	return AgentRunJob{Run: run, Agent: agent}, nil
}

// TransitionAgentRun persists a worker result while fencing stale workers by
// worker_id. Only a claimed running run may become terminal.
func (r *Repository) TransitionAgentRun(ctx context.Context, projectID, agentID, runID uuid.UUID, workerID string, result AgentRunResult) (domain.AgentRun, error) {
	workerID, err := normalizeAgentRunWorkerID(workerID)
	if err != nil {
		return domain.AgentRun{}, err
	}
	result, stepsJSON, changesJSON, err := normalizeAgentRunResult(result)
	if err != nil {
		return domain.AgentRun{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AgentRun{}, err
	}
	defer tx.Rollback(ctx)
	var currentStatus, currentWorker string
	err = tx.QueryRow(ctx, `SELECT status,COALESCE(worker_id,'') FROM agent_runs WHERE project_id=$1 AND agent_id=$2 AND id=$3 FOR UPDATE`, projectID, agentID, runID).Scan(&currentStatus, &currentWorker)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentRun{}, ErrNotFound
	}
	if err != nil {
		return domain.AgentRun{}, err
	}
	if currentStatus != "running" || currentWorker != workerID {
		return domain.AgentRun{}, ErrAgentRunNotAvailable
	}
	run, err := scanAgentRun(tx.QueryRow(ctx, `
		UPDATE agent_runs
		SET status=$4,output_text=$5,error_message=$6,steps=$7,changes=$8,finished_at=now(),claimed_at=NULL,worker_id=NULL,updated_at=now()
		WHERE project_id=$1 AND agent_id=$2 AND id=$3 AND status='running'
		RETURNING `+agentRunReturningProjection, projectID, agentID, runID, result.Status, result.OutputText, result.ErrorMessage, stepsJSON, changesJSON))
	if err != nil {
		return domain.AgentRun{}, err
	}
	if err := r.refreshAgentStatusTx(ctx, tx, agentID, projectID); err != nil {
		return domain.AgentRun{}, err
	}
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.AgentRun{}, err
	}
	metadata := map[string]any{"project_id": projectID.String(), "agent_id": agentID.String(), "status": run.Status}
	if err := writeAuditMetadata(ctx, tx, orgID, uuid.Nil, "agent.run."+run.Status, "agent_run", runID, metadata); err != nil {
		return domain.AgentRun{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "agent.run."+run.Status, "agent_run", runID, metadata); err != nil {
		return domain.AgentRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AgentRun{}, err
	}
	return run, nil
}

func (r *Repository) AppendAgentRunLog(ctx context.Context, projectID, agentID, runID uuid.UUID, workerID string, id uuid.UUID, sequence int64, level, message string) (domain.AgentRunLog, error) {
	workerID, err := normalizeAgentRunWorkerID(workerID)
	if err != nil {
		return domain.AgentRunLog{}, err
	}
	level = strings.ToLower(strings.TrimSpace(level))
	message = strings.TrimSpace(message)
	if level != "debug" && level != "info" && level != "warn" && level != "error" {
		return domain.AgentRunLog{}, fmt.Errorf("%w: log level is invalid", ErrInvalidAgentRun)
	}
	if message == "" || len(message) > agentRunMaxLogBytes || strings.ContainsRune(message, '\x00') {
		return domain.AgentRunLog{}, fmt.Errorf("%w: log message is invalid", ErrInvalidAgentRun)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AgentRunLog{}, err
	}
	defer tx.Rollback(ctx)
	var currentWorker string
	err = tx.QueryRow(ctx, `SELECT COALESCE(worker_id,'') FROM agent_runs WHERE project_id=$1 AND agent_id=$2 AND id=$3 AND status='running' FOR UPDATE`, projectID, agentID, runID).Scan(&currentWorker)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentRunLog{}, ErrAgentRunNotAvailable
	}
	if err != nil {
		return domain.AgentRunLog{}, err
	}
	if currentWorker != workerID {
		return domain.AgentRunLog{}, ErrAgentRunNotAvailable
	}
	if sequence <= 0 {
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM agent_run_logs WHERE project_id=$1 AND run_id=$2`, projectID, runID).Scan(&sequence); err != nil {
			return domain.AgentRunLog{}, err
		}
	}
	item, err := scanAgentRunLog(tx.QueryRow(ctx, `
		INSERT INTO agent_run_logs (id,run_id,project_id,sequence,level,message)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id,run_id,project_id,sequence,level,message,created_at`, id, runID, projectID, sequence, level, message))
	if err != nil {
		return domain.AgentRunLog{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AgentRunLog{}, err
	}
	return item, nil
}

func (r *Repository) ListAgentRunLogs(ctx context.Context, accountID, agentID, runID uuid.UUID, limit int, after int64) ([]domain.AgentRunLog, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidAgentRun)
	}
	if after < 0 {
		return nil, fmt.Errorf("%w: after must be non-negative", ErrInvalidAgentRun)
	}
	if _, err := r.AgentRunByID(ctx, accountID, agentID, runID); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+agentRunLogProjection+` FROM agent_run_logs l WHERE l.project_id=(SELECT project_id FROM agent_runs WHERE id=$1 AND agent_id=$2) AND l.run_id=$1 AND l.sequence>$3 ORDER BY l.sequence LIMIT $4`, runID, agentID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.AgentRunLog, 0, limit)
	for rows.Next() {
		item, scanErr := scanAgentRunLog(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *Repository) RequeueStaleAgentRuns(ctx context.Context, maxAge time.Duration) (int64, error) {
	if maxAge <= 0 {
		return 0, fmt.Errorf("%w: max age must be positive", ErrInvalidAgentRun)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `UPDATE agent_runs SET status='queued',started_at=NULL,claimed_at=NULL,worker_id=NULL,updated_at=now() WHERE status='running' AND claimed_at IS NOT NULL AND claimed_at < now() - ($1::double precision * interval '1 second') RETURNING agent_id,project_id`, maxAge.Seconds())
	if err != nil {
		return 0, err
	}
	type agentProject struct{ agentID, projectID uuid.UUID }
	changed := make([]agentProject, 0)
	for rows.Next() {
		var value agentProject
		if err := rows.Scan(&value.agentID, &value.projectID); err != nil {
			rows.Close()
			return 0, err
		}
		changed = append(changed, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	seen := make(map[uuid.UUID]struct{}, len(changed))
	for _, value := range changed {
		if _, ok := seen[value.agentID]; ok {
			continue
		}
		seen[value.agentID] = struct{}{}
		if err := r.refreshAgentStatusTx(ctx, tx, value.agentID, value.projectID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int64(len(changed)), nil
}

func (r *Repository) refreshAgentStatusTx(ctx context.Context, tx pgx.Tx, agentID, projectID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE project_agents
		SET status=CASE
			WHEN EXISTS (SELECT 1 FROM agent_runs WHERE agent_id=$1 AND project_id=$2 AND status='running') THEN 'running'
			WHEN EXISTS (SELECT 1 FROM agent_runs WHERE agent_id=$1 AND project_id=$2 AND status='queued') THEN 'active'
			ELSE 'idle'
		END,
		last_active_at=now(),updated_at=now()
		WHERE id=$1 AND project_id=$2`, agentID, projectID)
	return err
}
