package repository

import (
	"context"
	"errors"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/appsecret"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	AppEnvironmentVariableMaxValueBytes       = 64 * 1024
	AppEnvironmentVariableMaxDescriptionBytes = 2000
	AppEnvironmentVariableMaxCount            = 128
)

var (
	ErrInvalidAppEnvironmentVariable = errors.New("invalid App environment variable")
	ErrAppSecretUnavailable          = errors.New("App environment encryption is unavailable")
	ErrAppEnvironmentVariableLimit   = errors.New("App environment variable limit reached")
	appEnvironmentVariableKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,119}$`)
)

type AppEnvironmentVariableInput struct {
	Key         string
	IsSecret    bool
	Value       *string
	Description *string
	Cipher      *appsecret.Cipher
}

type AppEnvironmentVariablePatch struct {
	Key            *string
	IsSecret       *bool
	Value          *string
	ClearValue     bool
	Description    *string
	SetDescription bool
	Cipher         *appsecret.Cipher
}

// AppRuntimeEnvironmentCiphertext is private worker input. It contains only
// the ciphertext and authenticated record identity, never a public API value.
type AppRuntimeEnvironmentCiphertext struct {
	ID         uuid.UUID
	Key        string
	Ciphertext []byte
}

const appEnvironmentVariableProjection = `id::text,app_id::text,project_id::text,key,is_secret,(value_ciphertext IS NOT NULL),description,created_at,updated_at`

type appEnvironmentVariableScanner interface{ Scan(...any) error }

func scanAppEnvironmentVariable(row appEnvironmentVariableScanner) (domain.AppEnvironmentVariable, error) {
	var item domain.AppEnvironmentVariable
	return item, row.Scan(&item.ID, &item.AppID, &item.ProjectID, &item.Key, &item.IsSecret, &item.HasValue, &item.Description, &item.CreatedAt, &item.UpdatedAt)
}

func validAppEnvironmentVariableInput(key string, value *string, description *string) bool {
	if !appEnvironmentVariableKeyPattern.MatchString(key) {
		return false
	}
	if value != nil && (len(*value) > AppEnvironmentVariableMaxValueBytes || strings.ContainsAny(*value, "\x00\r\n")) {
		return false
	}
	return description == nil || (len(*description) <= AppEnvironmentVariableMaxDescriptionBytes && !strings.ContainsRune(*description, '\x00'))
}

func (r *Repository) ListAppEnvironmentVariables(ctx context.Context, projectID, appID uuid.UUID, actor AppActor, limit int, cursor *uuid.UUID) ([]domain.AppEnvironmentVariable, string, bool, error) {
	canManage, err := r.requireAppRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	if _, err := appByID(ctx, r.pool, projectID, appID, false); err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+appEnvironmentVariableProjection+`
		FROM app_environment_variables
		WHERE project_id=$1 AND app_id=$2 AND ($3::uuid IS NULL OR id>$3)
		ORDER BY id LIMIT $4`, projectID, appID, cursor, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.AppEnvironmentVariable, 0, limit)
	for rows.Next() {
		item, scanErr := scanAppEnvironmentVariable(rows)
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

func (r *Repository) CreateAppEnvironmentVariable(ctx context.Context, id, projectID, appID uuid.UUID, actor AppActor, input AppEnvironmentVariableInput) (domain.AppEnvironmentVariable, error) {
	if id == uuid.Nil || projectID == uuid.Nil || appID == uuid.Nil || !validAppEnvironmentVariableInput(input.Key, input.Value, input.Description) {
		return domain.AppEnvironmentVariable{}, ErrInvalidAppEnvironmentVariable
	}
	if input.Value != nil && input.Cipher == nil {
		return domain.AppEnvironmentVariable{}, ErrAppSecretUnavailable
	}
	var ciphertext []byte
	if input.Value != nil {
		plaintext := []byte(*input.Value)
		var err error
		ciphertext, err = input.Cipher.Encrypt(projectID, appID, id, plaintext)
		clear(plaintext)
		if err != nil {
			return domain.AppEnvironmentVariable{}, ErrAppSecretUnavailable
		}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireAppWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	app, err := appByID(ctx, tx, projectID, appID, true)
	if err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM app_environment_variables WHERE project_id=$1 AND app_id=$2`, projectID, appID).Scan(&count); err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	if count >= AppEnvironmentVariableMaxCount {
		return domain.AppEnvironmentVariable{}, ErrAppEnvironmentVariableLimit
	}
	item, err := scanAppEnvironmentVariable(tx.QueryRow(ctx, `
		INSERT INTO app_environment_variables (id,app_id,project_id,key,is_secret,value_ciphertext,description)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+appEnvironmentVariableProjection,
		id, appID, projectID, input.Key, input.IsSecret, ciphertext, input.Description))
	if err != nil {
		return domain.AppEnvironmentVariable{}, mapError(err)
	}
	runtimeChanged := input.Value != nil
	if runtimeChanged {
		if err := advanceAppEnvironmentGenerationTx(ctx, tx, projectID, appID, app); err != nil {
			return domain.AppEnvironmentVariable{}, err
		}
	}
	metadata := appEnvironmentAuditMetadata(item, []string{"create"}, runtimeChanged)
	if err := r.auditApp(ctx, tx, projectID, actor, "app_environment_variable.create", appID, metadata); err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, projectID, "app.environment_variable.updated", "app", appID, map[string]any{
		"variable_id": item.ID, "key": item.Key, "has_value": item.HasValue,
	}); err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	return item, nil
}

func (r *Repository) UpdateAppEnvironmentVariable(ctx context.Context, projectID, appID, variableID uuid.UUID, actor AppActor, patch AppEnvironmentVariablePatch) (domain.AppEnvironmentVariable, error) {
	if patch.ClearValue && patch.Value != nil {
		return domain.AppEnvironmentVariable{}, ErrInvalidAppEnvironmentVariable
	}
	if patch.Value != nil && patch.Cipher == nil {
		return domain.AppEnvironmentVariable{}, ErrAppSecretUnavailable
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireAppWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	app, err := appByID(ctx, tx, projectID, appID, true)
	if err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	var existing domain.AppEnvironmentVariable
	var oldCiphertext []byte
	err = tx.QueryRow(ctx, `SELECT `+appEnvironmentVariableProjection+`,value_ciphertext FROM app_environment_variables
		WHERE project_id=$1 AND app_id=$2 AND id=$3 FOR UPDATE`, projectID, appID, variableID).Scan(
		&existing.ID, &existing.AppID, &existing.ProjectID, &existing.Key, &existing.IsSecret, &existing.HasValue, &existing.Description, &existing.CreatedAt, &existing.UpdatedAt, &oldCiphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AppEnvironmentVariable{}, ErrNotFound
	}
	if err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	key := existing.Key
	if patch.Key != nil {
		key = *patch.Key
	}
	description := existing.Description
	if patch.SetDescription {
		description = patch.Description
	}
	isSecret := existing.IsSecret
	if patch.IsSecret != nil {
		isSecret = *patch.IsSecret
	}
	value := oldCiphertext
	if patch.ClearValue {
		value = nil
	}
	if !validAppEnvironmentVariableInput(key, patch.Value, description) {
		return domain.AppEnvironmentVariable{}, ErrInvalidAppEnvironmentVariable
	}
	if patch.Value != nil {
		plaintext := []byte(*patch.Value)
		value, err = patch.Cipher.Encrypt(projectID, appID, variableID, plaintext)
		clear(plaintext)
		if err != nil {
			return domain.AppEnvironmentVariable{}, ErrAppSecretUnavailable
		}
	}
	if patch.Key == nil && patch.IsSecret == nil && !patch.SetDescription && patch.Value == nil && !patch.ClearValue {
		return domain.AppEnvironmentVariable{}, ErrInvalidAppEnvironmentVariable
	}
	item, err := scanAppEnvironmentVariable(tx.QueryRow(ctx, `UPDATE app_environment_variables
		SET key=$4,is_secret=$5,value_ciphertext=$6,description=$7,updated_at=now()
		WHERE project_id=$1 AND app_id=$2 AND id=$3 RETURNING `+appEnvironmentVariableProjection,
		projectID, appID, variableID, key, isSecret, value, description))
	if err != nil {
		return domain.AppEnvironmentVariable{}, mapError(err)
	}
	runtimeChanged := patch.Value != nil || (patch.ClearValue && existing.HasValue) || (key != existing.Key && existing.HasValue)
	if runtimeChanged {
		if err := advanceAppEnvironmentGenerationTx(ctx, tx, projectID, appID, app); err != nil {
			return domain.AppEnvironmentVariable{}, err
		}
	}
	fields := make([]string, 0, 4)
	if key != existing.Key {
		fields = append(fields, "key")
	}
	if isSecret != existing.IsSecret {
		fields = append(fields, "is_secret")
	}
	if patch.SetDescription {
		fields = append(fields, "description")
	}
	if patch.Value != nil || patch.ClearValue {
		fields = append(fields, "value")
	}
	sort.Strings(fields)
	if err := r.auditApp(ctx, tx, projectID, actor, "app_environment_variable.update", appID, appEnvironmentAuditMetadata(item, fields, runtimeChanged)); err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, projectID, "app.environment_variable.updated", "app", appID, map[string]any{
		"variable_id": item.ID, "key": item.Key, "has_value": item.HasValue,
	}); err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AppEnvironmentVariable{}, err
	}
	return item, nil
}

func (r *Repository) DeleteAppEnvironmentVariable(ctx context.Context, projectID, appID, variableID uuid.UUID, actor AppActor) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := r.requireAppWriteTx(ctx, tx, projectID, actor); err != nil {
		return err
	}
	app, err := appByID(ctx, tx, projectID, appID, true)
	if err != nil {
		return err
	}
	var key string
	var isSecret, hasValue bool
	err = tx.QueryRow(ctx, `SELECT key,is_secret,value_ciphertext IS NOT NULL FROM app_environment_variables
		WHERE project_id=$1 AND app_id=$2 AND id=$3 FOR UPDATE`, projectID, appID, variableID).Scan(&key, &isSecret, &hasValue)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app_environment_variables WHERE project_id=$1 AND app_id=$2 AND id=$3`, projectID, appID, variableID); err != nil {
		return err
	}
	if hasValue {
		if err := advanceAppEnvironmentGenerationTx(ctx, tx, projectID, appID, app); err != nil {
			return err
		}
	}
	metadata := map[string]any{"key": key, "is_secret": isSecret, "has_value": hasValue, "runtime_changed": hasValue}
	if err := r.auditApp(ctx, tx, projectID, actor, "app_environment_variable.delete", appID, metadata); err != nil {
		return err
	}
	if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, projectID, "app.environment_variable.updated", "app", appID, map[string]any{
		"variable_id": variableID.String(), "key": key,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func appEnvironmentAuditMetadata(item domain.AppEnvironmentVariable, changedFields []string, runtimeChanged bool) map[string]any {
	return map[string]any{
		"variable_id": item.ID, "key": item.Key, "is_secret": item.IsSecret,
		"has_value": item.HasValue, "changed_fields": changedFields, "runtime_changed": runtimeChanged,
	}
}

func advanceAppEnvironmentGenerationTx(ctx context.Context, tx pgx.Tx, projectID, appID uuid.UUID, app domain.App) error {
	if app.DesiredGeneration == math.MaxInt64 {
		return ErrInvalidAppSettings
	}
	status := app.RuntimeStatus
	if !app.Enabled || app.DesiredDeploymentID != nil {
		status = "pending"
	} else {
		status = "not_deployed"
	}
	if _, err := tx.Exec(ctx, `UPDATE project_apps SET desired_generation=desired_generation+1,runtime_status=$3,runtime_error=NULL,updated_at=now()
		WHERE project_id=$1 AND id=$2`, projectID, appID, status); err != nil {
		return err
	}
	if err := resetAppRuntimeRetryTx(ctx, tx, appID); err != nil {
		return err
	}
	return nil
}

// ListAppRuntimeEnvironment returns only current leased job ciphertext. The
// worker decrypts values just before creating the replacement container.
func (r *Repository) ListAppRuntimeEnvironment(ctx context.Context, job AppRuntimeJob) ([]AppRuntimeEnvironmentCiphertext, error) {
	appID, appErr := uuid.Parse(job.App.ID)
	projectID, projectErr := uuid.Parse(job.App.ProjectID)
	if r == nil || r.pool == nil || appErr != nil || projectErr != nil || appID == uuid.Nil || projectID == uuid.Nil || job.LeaseToken == uuid.Nil || job.WorkerID == "" || job.App.DesiredDeploymentID == nil {
		return nil, ErrInvalidAppRuntimeJob
	}
	// Check the lease and read the values in one statement so an environment
	// update cannot commit between a current-job check and the value query.
	// The left join returns one NULL row when this current generation has no
	// configured values.
	rows, err := r.pool.Query(ctx, `SELECT variable.id::text,variable.key,variable.value_ciphertext
		FROM project_apps app
		JOIN app_runtime_state runtime ON runtime.app_id=app.id AND runtime.project_id=app.project_id
		LEFT JOIN app_environment_variables variable ON variable.project_id=app.project_id
		  AND variable.app_id=app.id AND variable.value_ciphertext IS NOT NULL
		WHERE app.id=$1 AND app.project_id=$2 AND app.enabled=TRUE AND app.desired_generation=$3
		  AND app.desired_deployment_id=$4 AND app.workload_spec_sha256=$5
		  AND runtime.worker_id=$6 AND runtime.lease_token=$7 AND runtime.lease_expires_at>now()
		  AND runtime.route_identity=$8 AND runtime.container_name=$9
	ORDER BY variable.key LIMIT $10`,
		appID, projectID, job.App.DesiredGeneration, *job.App.DesiredDeploymentID, job.App.WorkloadSpecSHA256,
		job.WorkerID, job.LeaseToken, job.RouteIdentity, job.ContainerName, AppEnvironmentVariableMaxCount+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]AppRuntimeEnvironmentCiphertext, 0, 16)
	current := false
	for rows.Next() {
		current = true
		var item AppRuntimeEnvironmentCiphertext
		var id *string
		var key *string
		if err := rows.Scan(&id, &key, &item.Ciphertext); err != nil {
			return nil, err
		}
		if id == nil {
			if key != nil || len(item.Ciphertext) != 0 || len(values) != 0 {
				return nil, ErrInvalidAppEnvironmentVariable
			}
			continue
		}
		if key == nil {
			return nil, ErrInvalidAppEnvironmentVariable
		}
		item.ID, err = uuid.Parse(*id)
		if err != nil || item.ID == uuid.Nil {
			return nil, ErrInvalidAppEnvironmentVariable
		}
		item.Key = *key
		values = append(values, item)
		if len(values) > AppEnvironmentVariableMaxCount {
			return nil, ErrInvalidAppEnvironmentVariable
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !current {
		return nil, ErrAppRuntimeStale
	}
	return values, nil
}
