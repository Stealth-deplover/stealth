package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Stealth-deplover/stealth/internal/apikey"
	"github.com/Stealth-deplover/stealth/internal/database"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrStorageQuotaExceeded = errors.New("storage bucket quota exceeded")
	ErrStorageFileTooLarge  = errors.New("storage file is too large")
)

// StorageActor intentionally aliases the Database actor model. Both services
// have the same four authentication provenance classes, and sharing the type
// keeps it impossible for a Console account to be fabricated for an app user.
type StorageActor = DatabaseActor

const (
	StorageConsoleActor     = DatabaseConsoleActor
	StorageAPIKeyActor      = DatabaseAPIKeyActor
	StorageApplicationActor = DatabaseApplicationActor
	StorageAnonymousActor   = DatabaseAnonymousActor
)

type StorageBucketInput struct {
	Name              string
	FileSecurity      bool
	CreatePermissions []string
	ReadPermissions   []string
	UpdatePermissions []string
	DeletePermissions []string
	MaxFileSizeBytes  int64
	QuotaBytes        int64
}

type StorageBucketPatch struct {
	Name              *string
	FileSecurity      *bool
	CreatePermissions *[]string
	ReadPermissions   *[]string
	UpdatePermissions *[]string
	DeletePermissions *[]string
	MaxFileSizeBytes  *int64
	QuotaBytes        *int64
}

type StorageFileInput struct {
	Name                 string
	MimeType             string
	SizeBytes            int64
	ChecksumSHA256       string
	StoragePath          string
	ReadPermissions      *[]string
	UpdatePermissions    *[]string
	DeletePermissions    *[]string
	CreatorProjectUserID *uuid.UUID
}

type StorageFilePatch struct {
	Name              *string
	ReadPermissions   *[]string
	UpdatePermissions *[]string
	DeletePermissions *[]string
}

const storageBucketProjection = `id,project_id,name,file_security,create_permissions,read_permissions,update_permissions,delete_permissions,max_file_size_bytes,quota_bytes,used_bytes,created_at,updated_at`
const storageFileProjection = `id,bucket_id,project_id,name,mime_type,size_bytes,checksum_sha256,storage_path,read_permissions,update_permissions,delete_permissions,creator_project_user_id,created_at,updated_at`

type storageScanner interface{ Scan(...any) error }

func scanStorageBucket(row storageScanner) (domain.StorageBucket, error) {
	var item domain.StorageBucket
	err := row.Scan(&item.ID, &item.ProjectID, &item.Name, &item.FileSecurity, &item.CreatePermissions, &item.ReadPermissions, &item.UpdatePermissions, &item.DeletePermissions, &item.MaxFileSizeBytes, &item.QuotaBytes, &item.UsedBytes, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanStorageFile(row storageScanner) (domain.StorageFile, string, error) {
	var item domain.StorageFile
	var path string
	var creator *uuid.UUID
	err := row.Scan(&item.ID, &item.BucketID, &item.ProjectID, &item.Name, &item.MimeType, &item.SizeBytes, &item.ChecksumSHA256, &path, &item.ReadPermissions, &item.UpdatePermissions, &item.DeletePermissions, &creator, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return item, "", err
	}
	if creator != nil {
		value := creator.String()
		item.CreatorProjectUserID = &value
	}
	return item, path, nil
}

func (r *Repository) storageBucket(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, projectID, bucketID uuid.UUID, lock bool) (domain.StorageBucket, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	item, err := scanStorageBucket(query.QueryRow(ctx, `SELECT `+storageBucketProjection+` FROM storage_buckets WHERE project_id=$1 AND id=$2`+suffix, projectID, bucketID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StorageBucket{}, ErrNotFound
	}
	return item, err
}

func normalizeStoragePermissions(raw []string) ([]string, error) {
	return database.NormalizePermissions(raw)
}

func storageBucketPermissions(input StorageBucketInput) ([4][]string, error) {
	values := [][]string{input.CreatePermissions, input.ReadPermissions, input.UpdatePermissions, input.DeletePermissions}
	var result [4][]string
	for i, value := range values {
		permissions, err := normalizeStoragePermissions(value)
		if err != nil {
			return result, err
		}
		result[i] = permissions
	}
	return result, nil
}

func storageBucketCanManage(actor StorageActor, role string) bool {
	if actor.Kind == StorageConsoleActor {
		return role == "owner" || role == "admin"
	}
	return actor.Kind == StorageAPIKeyActor && apikey.HasScope(actor.APIKeyScopes, "storage.write")
}

// requireStorageRead authorizes project metadata reads and returns whether the
// actor may manage buckets. Application and anonymous callers are deliberately
// excluded from bucket-management endpoints; they use file data endpoints.
func (r *Repository) requireStorageRead(ctx context.Context, projectID uuid.UUID, actor StorageActor) (bool, error) {
	switch actor.Kind {
	case StorageConsoleActor:
		role, err := r.projectRole(ctx, projectID, actor.AccountID)
		if err != nil {
			return false, err
		}
		return storageBucketCanManage(actor, role), nil
	case StorageAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "storage.read") {
			return false, ErrForbidden
		}
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, projectID).Scan(&exists); err != nil {
			return false, err
		}
		if !exists {
			return false, ErrNotFound
		}
		return apikey.HasScope(actor.APIKeyScopes, "storage.write"), nil
	default:
		return false, ErrForbidden
	}
}

func (r *Repository) requireStorageWriteTx(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor StorageActor) error {
	switch actor.Kind {
	case StorageConsoleActor:
		return requireProjectRoleTx(ctx, tx, projectID, actor.AccountID, "owner", "admin")
	case StorageAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "storage.write") {
			return ErrForbidden
		}
		return requireActiveProjectAPIKeyTx(ctx, tx, projectID, actor.APIKeyID, "storage.write")
	default:
		return ErrForbidden
	}
}

func storageBucketPermission(bucket domain.StorageBucket, actor StorageActor, operation string) bool {
	var permissions []string
	switch operation {
	case "create":
		permissions = bucket.CreatePermissions
	case "read":
		permissions = bucket.ReadPermissions
	case "update":
		permissions = bucket.UpdatePermissions
	case "delete":
		permissions = bucket.DeletePermissions
	default:
		return false
	}
	return tablePermission(permissions, actor)
}

func requireStorageBucketPermission(bucket domain.StorageBucket, actor StorageActor, operation string) error {
	switch actor.Kind {
	case StorageConsoleActor, StorageAPIKeyActor:
		return nil
	case StorageApplicationActor, StorageAnonymousActor:
		if storageBucketPermission(bucket, actor, operation) {
			return nil
		}
		return ErrForbidden
	default:
		return ErrForbidden
	}
}

func (r *Repository) AuthorizeStorageBucket(ctx context.Context, projectID, bucketID uuid.UUID, actor StorageActor, operation string) (domain.StorageBucket, error) {
	if actor.IsManagement() {
		if operation == "read" {
			if _, err := r.requireStorageRead(ctx, projectID, actor); err != nil {
				return domain.StorageBucket{}, err
			}
		} else {
			tx, err := r.pool.Begin(ctx)
			if err != nil {
				return domain.StorageBucket{}, err
			}
			defer tx.Rollback(ctx)
			if err := r.requireStorageWriteTx(ctx, tx, projectID, actor); err != nil {
				return domain.StorageBucket{}, err
			}
			item, err := r.storageBucket(ctx, tx, projectID, bucketID, false)
			if err != nil {
				return domain.StorageBucket{}, err
			}
			return item, nil
		}
		return r.storageBucket(ctx, r.pool, projectID, bucketID, false)
	}
	item, err := r.storageBucket(ctx, r.pool, projectID, bucketID, false)
	if err != nil {
		return domain.StorageBucket{}, err
	}
	if err := requireStorageBucketPermission(item, actor, operation); err != nil {
		return domain.StorageBucket{}, err
	}
	return item, nil
}

func (r *Repository) ListStorageBuckets(ctx context.Context, projectID uuid.UUID, actor StorageActor, limit int, cursor *uuid.UUID) ([]domain.StorageBucket, string, bool, error) {
	canManage, err := r.requireStorageRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+storageBucketProjection+` FROM storage_buckets WHERE project_id=$1 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $2`, projectID, limit+1, cursor)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.StorageBucket, 0, limit)
	for rows.Next() {
		item, scanErr := scanStorageBucket(rows)
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

func (r *Repository) GetStorageBucket(ctx context.Context, projectID, bucketID uuid.UUID, actor StorageActor) (domain.StorageBucket, error) {
	if _, err := r.requireStorageRead(ctx, projectID, actor); err != nil {
		return domain.StorageBucket{}, err
	}
	return r.storageBucket(ctx, r.pool, projectID, bucketID, false)
}

func (r *Repository) CreateStorageBucket(ctx context.Context, id, projectID uuid.UUID, actor StorageActor, input StorageBucketInput) (domain.StorageBucket, error) {
	permissions, err := storageBucketPermissions(input)
	if err != nil {
		return domain.StorageBucket{}, err
	}
	if input.QuotaBytes <= 0 || input.MaxFileSizeBytes <= 0 || input.MaxFileSizeBytes > input.QuotaBytes {
		return domain.StorageBucket{}, ErrStorageQuotaExceeded
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.StorageBucket{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireStorageWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.StorageBucket{}, err
	}
	organizationID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.StorageBucket{}, err
	}
	if err := r.enforceOrganizationLimitTx(ctx, tx, organizationID, "storage_buckets"); err != nil {
		return domain.StorageBucket{}, err
	}
	if err := lockStorageNamespace(ctx, tx, projectID); err != nil {
		return domain.StorageBucket{}, err
	}
	item, err := scanStorageBucket(tx.QueryRow(ctx, `INSERT INTO storage_buckets (id,project_id,name,file_security,create_permissions,read_permissions,update_permissions,delete_permissions,max_file_size_bytes,quota_bytes) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+storageBucketProjection, id, projectID, input.Name, input.FileSecurity, permissions[0], permissions[1], permissions[2], permissions[3], input.MaxFileSizeBytes, input.QuotaBytes))
	if err != nil {
		return domain.StorageBucket{}, mapError(err)
	}
	if err := r.auditStorage(ctx, tx, projectID, actor, "storage_bucket.create", "storage_bucket", id, map[string]any{"name": input.Name, "quota_bytes": input.QuotaBytes}); err != nil {
		return domain.StorageBucket{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StorageBucket{}, err
	}
	return item, nil
}

func (r *Repository) UpdateStorageBucket(ctx context.Context, projectID, bucketID uuid.UUID, actor StorageActor, patch StorageBucketPatch) (domain.StorageBucket, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.StorageBucket{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireStorageWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.StorageBucket{}, err
	}
	if err := lockStorageNamespace(ctx, tx, projectID); err != nil {
		return domain.StorageBucket{}, err
	}
	item, err := r.storageBucket(ctx, tx, projectID, bucketID, true)
	if err != nil {
		return domain.StorageBucket{}, err
	}
	name := item.Name
	if patch.Name != nil {
		name = *patch.Name
	}
	fileSecurity := item.FileSecurity
	if patch.FileSecurity != nil {
		fileSecurity = *patch.FileSecurity
	}
	createPermissions := item.CreatePermissions
	if patch.CreatePermissions != nil {
		createPermissions, err = normalizeStoragePermissions(*patch.CreatePermissions)
		if err != nil {
			return domain.StorageBucket{}, err
		}
	}
	readPermissions := item.ReadPermissions
	if patch.ReadPermissions != nil {
		readPermissions, err = normalizeStoragePermissions(*patch.ReadPermissions)
		if err != nil {
			return domain.StorageBucket{}, err
		}
	}
	updatePermissions := item.UpdatePermissions
	if patch.UpdatePermissions != nil {
		updatePermissions, err = normalizeStoragePermissions(*patch.UpdatePermissions)
		if err != nil {
			return domain.StorageBucket{}, err
		}
	}
	deletePermissions := item.DeletePermissions
	if patch.DeletePermissions != nil {
		deletePermissions, err = normalizeStoragePermissions(*patch.DeletePermissions)
		if err != nil {
			return domain.StorageBucket{}, err
		}
	}
	quota := item.QuotaBytes
	if patch.QuotaBytes != nil {
		quota = *patch.QuotaBytes
	}
	maxFileSize := item.MaxFileSizeBytes
	if patch.MaxFileSizeBytes != nil {
		maxFileSize = *patch.MaxFileSizeBytes
	}
	if quota <= 0 || quota < item.UsedBytes || maxFileSize <= 0 || maxFileSize > quota {
		return domain.StorageBucket{}, ErrStorageQuotaExceeded
	}
	item, err = scanStorageBucket(tx.QueryRow(ctx, `UPDATE storage_buckets SET name=$3,file_security=$4,create_permissions=$5,read_permissions=$6,update_permissions=$7,delete_permissions=$8,max_file_size_bytes=$9,quota_bytes=$10,updated_at=now() WHERE project_id=$1 AND id=$2 RETURNING `+storageBucketProjection, projectID, bucketID, name, fileSecurity, createPermissions, readPermissions, updatePermissions, deletePermissions, maxFileSize, quota))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StorageBucket{}, ErrNotFound
	}
	if err != nil {
		return domain.StorageBucket{}, mapError(err)
	}
	if err := r.auditStorage(ctx, tx, projectID, actor, "storage_bucket.update", "storage_bucket", bucketID, map[string]any{"changed_fields": storageBucketChangedFields(patch)}); err != nil {
		return domain.StorageBucket{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StorageBucket{}, err
	}
	return item, nil
}

func storageBucketChangedFields(patch StorageBucketPatch) []string {
	fields := make([]string, 0, 6)
	if patch.Name != nil {
		fields = append(fields, "name")
	}
	if patch.FileSecurity != nil {
		fields = append(fields, "file_security")
	}
	if patch.CreatePermissions != nil {
		fields = append(fields, "create_permissions")
	}
	if patch.ReadPermissions != nil {
		fields = append(fields, "read_permissions")
	}
	if patch.UpdatePermissions != nil {
		fields = append(fields, "update_permissions")
	}
	if patch.DeletePermissions != nil {
		fields = append(fields, "delete_permissions")
	}
	if patch.QuotaBytes != nil {
		fields = append(fields, "quota_bytes")
	}
	if patch.MaxFileSizeBytes != nil {
		fields = append(fields, "max_file_size_bytes")
	}
	sort.Strings(fields)
	return fields
}

// DeleteStorageBucket removes metadata/accounting in one transaction and
// returns only UUID-derived relative paths for post-commit filesystem cleanup.
func (r *Repository) DeleteStorageBucket(ctx context.Context, projectID, bucketID uuid.UUID, actor StorageActor) ([]string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireStorageWriteTx(ctx, tx, projectID, actor); err != nil {
		return nil, err
	}
	if err := lockStorageNamespace(ctx, tx, projectID); err != nil {
		return nil, err
	}
	if _, err := r.storageBucket(ctx, tx, projectID, bucketID, true); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT storage_path FROM storage_files WHERE project_id=$1 AND bucket_id=$2 FOR UPDATE`, projectID, bucketID)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return nil, err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if _, err := tx.Exec(ctx, `DELETE FROM storage_buckets WHERE project_id=$1 AND id=$2`, projectID, bucketID); err != nil {
		return nil, err
	}
	if err := r.auditStorage(ctx, tx, projectID, actor, "storage_bucket.delete", "storage_bucket", bucketID, map[string]any{"file_count": len(paths)}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return paths, nil
}

func storageFilePermissionSQL(column string, actor StorageActor, args *[]any) string {
	return rowPermissionSQL(column, actor, args)
}

func (r *Repository) ListStorageFiles(ctx context.Context, projectID, bucketID uuid.UUID, actor StorageActor, limit int, cursor *uuid.UUID) ([]domain.StorageFile, string, bool, error) {
	bucket, err := r.storageBucket(ctx, r.pool, projectID, bucketID, false)
	if err != nil {
		return nil, "", false, err
	}
	if actor.IsManagement() {
		if _, err := r.requireStorageRead(ctx, projectID, actor); err != nil {
			return nil, "", false, err
		}
	} else if !bucket.FileSecurity && !storageBucketPermission(bucket, actor, "read") {
		return nil, "", false, ErrForbidden
	}
	args := []any{projectID, bucketID}
	where := `f.project_id=$1 AND f.bucket_id=$2`
	if actor.IsApplication() && bucket.FileSecurity && !storageBucketPermission(bucket, actor, "read") {
		where += ` AND ` + storageFilePermissionSQL("f.read_permissions", actor, &args)
	}
	cursorArg := len(args) + 1
	limitArg := cursorArg + 1
	args = append(args, cursor, limit+1)
	rows, err := r.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM storage_files f WHERE %s AND ($%d::uuid IS NULL OR f.id>$%d) ORDER BY f.id LIMIT $%d`, storageFileProjection, where, cursorArg, cursorArg, limitArg), args...)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.StorageFile, 0, limit)
	for rows.Next() {
		item, _, scanErr := scanStorageFile(rows)
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
	canManage := actor.Kind == StorageConsoleActor || (actor.Kind == StorageAPIKeyActor && apikey.HasScope(actor.APIKeyScopes, "storage.write"))
	return items, next, canManage, nil
}

func (r *Repository) storageFile(ctx context.Context, projectID, bucketID, fileID uuid.UUID, actor StorageActor, lock bool) (domain.StorageFile, string, error) {
	bucket, err := r.storageBucket(ctx, r.pool, projectID, bucketID, false)
	if err != nil {
		return domain.StorageFile{}, "", err
	}
	if actor.IsManagement() {
		if _, err := r.requireStorageRead(ctx, projectID, actor); err != nil {
			return domain.StorageFile{}, "", err
		}
	} else if !bucket.FileSecurity && !storageBucketPermission(bucket, actor, "read") {
		return domain.StorageFile{}, "", ErrForbidden
	}
	args := []any{projectID, bucketID, fileID}
	where := `f.project_id=$1 AND f.bucket_id=$2 AND f.id=$3`
	if actor.IsApplication() && bucket.FileSecurity && !storageBucketPermission(bucket, actor, "read") {
		where += ` AND ` + storageFilePermissionSQL("f.read_permissions", actor, &args)
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	item, path, err := scanStorageFile(r.pool.QueryRow(ctx, `SELECT `+storageFileProjection+` FROM storage_files f WHERE `+where+suffix, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StorageFile{}, "", ErrRowHidden
	}
	return item, path, err
}

func (r *Repository) GetStorageFile(ctx context.Context, projectID, bucketID, fileID uuid.UUID, actor StorageActor) (domain.StorageFile, error) {
	item, _, err := r.storageFile(ctx, projectID, bucketID, fileID, actor, false)
	return item, err
}

func (r *Repository) StorageFileForDownload(ctx context.Context, projectID, bucketID, fileID uuid.UUID, actor StorageActor) (domain.StorageFile, string, error) {
	return r.storageFile(ctx, projectID, bucketID, fileID, actor, false)
}

// UpdateStorageFile changes metadata and/or ACLs without ever touching the
// immutable blob. Management actors require storage.write; application actors
// may rename only when the effective update grant allows it, and cannot change
// ACLs (the same anti-escalation rule used by Database rows).
func (r *Repository) UpdateStorageFile(ctx context.Context, projectID, bucketID, fileID uuid.UUID, actor StorageActor, patch StorageFilePatch) (domain.StorageFile, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.StorageFile{}, err
	}
	defer tx.Rollback(ctx)
	bucket, err := r.storageBucket(ctx, tx, projectID, bucketID, true)
	if err != nil {
		return domain.StorageFile{}, err
	}
	if actor.IsManagement() {
		if err := r.requireStorageWriteTx(ctx, tx, projectID, actor); err != nil {
			return domain.StorageFile{}, err
		}
	} else {
		if !bucket.FileSecurity {
			if err := requireStorageBucketPermission(bucket, actor, "update"); err != nil {
				return domain.StorageFile{}, err
			}
		} else if !storageBucketPermission(bucket, actor, "update") {
			// The row predicate below supplies the file-level grant when the
			// bucket update grant did not already authorize the operation.
		}
		if patch.ReadPermissions != nil || patch.UpdatePermissions != nil || patch.DeletePermissions != nil {
			return domain.StorageFile{}, ErrForbidden
		}
	}
	if patch.Name != nil {
		if err := storage.ValidateFilename(*patch.Name); err != nil {
			return domain.StorageFile{}, err
		}
	}
	readPermissions := patch.ReadPermissions
	updatePermissions := patch.UpdatePermissions
	deletePermissions := patch.DeletePermissions
	var existing domain.StorageFile
	var path string
	selectSQL := `SELECT ` + storageFileProjection + ` FROM storage_files f WHERE f.project_id=$1 AND f.bucket_id=$2 AND f.id=$3`
	selectArgs := []any{projectID, bucketID, fileID}
	if actor.IsApplication() && bucket.FileSecurity && !storageBucketPermission(bucket, actor, "update") {
		selectSQL += ` AND ` + storageFilePermissionSQL("f.update_permissions", actor, &selectArgs)
	}
	existing, path, err = scanStorageFile(tx.QueryRow(ctx, selectSQL+` FOR UPDATE`, selectArgs...))
	if errors.Is(err, pgx.ErrNoRows) {
		if actor.IsApplication() && bucket.FileSecurity {
			return domain.StorageFile{}, ErrRowHidden
		}
		return domain.StorageFile{}, ErrNotFound
	}
	if err != nil {
		return domain.StorageFile{}, err
	}
	name := existing.Name
	if patch.Name != nil {
		name = *patch.Name
	}
	if readPermissions == nil {
		readPermissions = &existing.ReadPermissions
	} else {
		permissions, normalizeErr := normalizeStoragePermissions(*readPermissions)
		if normalizeErr != nil {
			return domain.StorageFile{}, normalizeErr
		}
		readPermissions = &permissions
	}
	if updatePermissions == nil {
		updatePermissions = &existing.UpdatePermissions
	} else {
		permissions, normalizeErr := normalizeStoragePermissions(*updatePermissions)
		if normalizeErr != nil {
			return domain.StorageFile{}, normalizeErr
		}
		updatePermissions = &permissions
	}
	if deletePermissions == nil {
		deletePermissions = &existing.DeletePermissions
	} else {
		permissions, normalizeErr := normalizeStoragePermissions(*deletePermissions)
		if normalizeErr != nil {
			return domain.StorageFile{}, normalizeErr
		}
		deletePermissions = &permissions
	}
	item, _, err := scanStorageFile(tx.QueryRow(ctx, `UPDATE storage_files SET name=$4,read_permissions=$5,update_permissions=$6,delete_permissions=$7,updated_at=now() WHERE project_id=$1 AND bucket_id=$2 AND id=$3 RETURNING `+storageFileProjection, projectID, bucketID, fileID, name, *readPermissions, *updatePermissions, *deletePermissions))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StorageFile{}, ErrNotFound
	}
	if err != nil {
		return domain.StorageFile{}, mapError(err)
	}
	changed := make([]string, 0, 4)
	if patch.Name != nil {
		changed = append(changed, "name")
	}
	if patch.ReadPermissions != nil {
		changed = append(changed, "read_permissions")
	}
	if patch.UpdatePermissions != nil {
		changed = append(changed, "update_permissions")
	}
	if patch.DeletePermissions != nil {
		changed = append(changed, "delete_permissions")
	}
	sort.Strings(changed)
	if err := r.auditStorage(ctx, tx, projectID, actor, "storage_file.update", "storage_file", fileID, map[string]any{"changed_fields": changed, "name": name}); err != nil {
		return domain.StorageFile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StorageFile{}, err
	}
	_ = path
	return item, nil
}

func storageFilePermissions(bucket domain.StorageBucket, actor StorageActor, input StorageFileInput) ([3][]string, error) {
	// PostgreSQL permission columns are NOT NULL arrays. Management uploads on
	// file-security buckets intentionally default to deny, so represent omitted
	// grants as non-nil empty arrays instead of driver-encoded SQL NULL values.
	values := [3][]string{{}, {}, {}}
	if input.ReadPermissions == nil {
		if !bucket.FileSecurity {
			values[0] = append([]string(nil), bucket.ReadPermissions...)
		} else if actor.Kind == StorageApplicationActor {
			values[0] = []string{"user:" + actor.ProjectUserID.String()}
		}
	} else {
		permissions, err := normalizeStoragePermissions(*input.ReadPermissions)
		if err != nil {
			return values, err
		}
		values[0] = permissions
	}
	if input.UpdatePermissions == nil {
		if !bucket.FileSecurity {
			values[1] = append([]string(nil), bucket.UpdatePermissions...)
		} else if actor.Kind == StorageApplicationActor {
			values[1] = []string{"user:" + actor.ProjectUserID.String()}
		}
	} else {
		permissions, err := normalizeStoragePermissions(*input.UpdatePermissions)
		if err != nil {
			return values, err
		}
		values[1] = permissions
	}
	if input.DeletePermissions == nil {
		if !bucket.FileSecurity {
			values[2] = append([]string(nil), bucket.DeletePermissions...)
		} else if actor.Kind == StorageApplicationActor {
			values[2] = []string{"user:" + actor.ProjectUserID.String()}
		}
	} else {
		permissions, err := normalizeStoragePermissions(*input.DeletePermissions)
		if err != nil {
			return values, err
		}
		values[2] = permissions
	}
	if actor.Kind == StorageAnonymousActor {
		for _, group := range values {
			for _, permission := range group {
				if permission == "users" {
					return values, fmt.Errorf("%w: anonymous files cannot grant users", database.ErrInvalidPermissions)
				}
			}
		}
	}
	return values, nil
}

// CreateStorageFile reserves quota and inserts metadata in one transaction.
// The caller publishes the blob before invoking this method and removes it if
// this transaction fails; a failed insert never leaves visible metadata.
func (r *Repository) CreateStorageFile(ctx context.Context, id, projectID, bucketID uuid.UUID, actor StorageActor, input StorageFileInput) (domain.StorageFile, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.StorageFile{}, err
	}
	defer tx.Rollback(ctx)
	bucket, err := r.storageBucket(ctx, tx, projectID, bucketID, true)
	if err != nil {
		return domain.StorageFile{}, err
	}
	if actor.Kind == StorageConsoleActor || actor.Kind == StorageAPIKeyActor {
		if err := r.requireStorageWriteTx(ctx, tx, projectID, actor); err != nil {
			return domain.StorageFile{}, err
		}
	} else if err := requireStorageBucketPermission(bucket, actor, "create"); err != nil {
		return domain.StorageFile{}, err
	}
	if actor.Kind == StorageAnonymousActor && (input.ReadPermissions == nil || input.UpdatePermissions == nil || input.DeletePermissions == nil) {
		return domain.StorageFile{}, fmt.Errorf("%w: anonymous uploads must specify read, update, and delete permissions", database.ErrInvalidPermissions)
	}
	if input.SizeBytes < 0 {
		return domain.StorageFile{}, ErrStorageFileTooLarge
	}
	permissions, err := storageFilePermissions(bucket, actor, input)
	if err != nil {
		return domain.StorageFile{}, err
	}
	if input.SizeBytes > bucket.MaxFileSizeBytes {
		return domain.StorageFile{}, ErrStorageFileTooLarge
	}
	if input.SizeBytes > bucket.QuotaBytes-bucket.UsedBytes {
		return domain.StorageFile{}, ErrStorageQuotaExceeded
	}
	var creator any
	if input.CreatorProjectUserID != nil {
		creator = *input.CreatorProjectUserID
	}
	item, _, err := scanStorageFile(tx.QueryRow(ctx, `INSERT INTO storage_files (id,bucket_id,project_id,name,mime_type,size_bytes,checksum_sha256,storage_path,read_permissions,update_permissions,delete_permissions,creator_project_user_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING `+storageFileProjection, id, bucketID, projectID, input.Name, input.MimeType, input.SizeBytes, input.ChecksumSHA256, input.StoragePath, permissions[0], permissions[1], permissions[2], creator))
	if err != nil {
		return domain.StorageFile{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE storage_buckets SET used_bytes=used_bytes+$3,updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, bucketID, input.SizeBytes); err != nil {
		return domain.StorageFile{}, err
	}
	if err := r.auditStorage(ctx, tx, projectID, actor, "storage_file.create", "storage_file", id, map[string]any{"name": input.Name, "mime_type": input.MimeType, "size_bytes": input.SizeBytes, "checksum_sha256": input.ChecksumSHA256}); err != nil {
		return domain.StorageFile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StorageFile{}, err
	}
	return item, nil
}

// RemoveStorageFileMetadata is used only when publishing a committed blob
// fails after metadata/quota commit. It is deliberately not exposed by HTTP.
func (r *Repository) RemoveStorageFileMetadata(ctx context.Context, projectID, bucketID, fileID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var size int64
	if err := tx.QueryRow(ctx, `SELECT size_bytes FROM storage_files WHERE project_id=$1 AND bucket_id=$2 AND id=$3 FOR UPDATE`, projectID, bucketID, fileID).Scan(&size); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM storage_files WHERE project_id=$1 AND bucket_id=$2 AND id=$3`, projectID, bucketID, fileID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE storage_buckets SET used_bytes=GREATEST(0,used_bytes-$3),updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, bucketID, size); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) DeleteStorageFile(ctx context.Context, projectID, bucketID, fileID uuid.UUID, actor StorageActor) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	bucket, err := r.storageBucket(ctx, tx, projectID, bucketID, true)
	if err != nil {
		return "", err
	}
	if actor.Kind == StorageConsoleActor || actor.Kind == StorageAPIKeyActor {
		if err := r.requireStorageWriteTx(ctx, tx, projectID, actor); err != nil {
			return "", err
		}
	}
	args := []any{projectID, bucketID, fileID}
	where := `project_id=$1 AND bucket_id=$2 AND id=$3`
	if actor.IsApplication() {
		if !bucket.FileSecurity {
			if err := requireStorageBucketPermission(bucket, actor, "delete"); err != nil {
				return "", err
			}
		} else {
			if !storageBucketPermission(bucket, actor, "delete") {
				where += ` AND ` + storageFilePermissionSQL("delete_permissions", actor, &args)
			}
		}
	}
	var item domain.StorageFile
	var path string
	var creator *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT `+storageFileProjection+` FROM storage_files WHERE `+where+` FOR UPDATE`, args...).Scan(&item.ID, &item.BucketID, &item.ProjectID, &item.Name, &item.MimeType, &item.SizeBytes, &item.ChecksumSHA256, &path, &item.ReadPermissions, &item.UpdatePermissions, &item.DeletePermissions, &creator, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if actor.IsApplication() && bucket.FileSecurity {
			return "", ErrRowHidden
		}
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM storage_files WHERE project_id=$1 AND bucket_id=$2 AND id=$3`, projectID, bucketID, fileID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE storage_buckets SET used_bytes=GREATEST(0,used_bytes-$3),updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, bucketID, item.SizeBytes); err != nil {
		return "", err
	}
	if err := r.auditStorage(ctx, tx, projectID, actor, "storage_file.delete", "storage_file", fileID, map[string]any{"name": item.Name, "size_bytes": item.SizeBytes, "checksum_sha256": item.ChecksumSHA256}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return path, nil
}

func lockStorageNamespace(ctx context.Context, tx pgx.Tx, projectID uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "storage:"+projectID.String())
	return err
}

func (r *Repository) auditStorage(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor StorageActor, action, targetType string, target uuid.UUID, metadata map[string]any) error {
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["project_id"] = projectID.String()
	switch actor.Kind {
	case StorageAPIKeyActor:
		metadata["actor"] = "api_key"
		metadata["api_key_id"] = actor.APIKeyID.String()
	case StorageApplicationActor:
		metadata["actor"] = "project_user"
		metadata["project_user_id"] = actor.ProjectUserID.String()
		metadata["source"] = "application"
	case StorageAnonymousActor:
		metadata["actor"] = "anonymous"
		metadata["source"] = "application"
	}
	actorID := uuid.Nil
	if actor.Kind == StorageConsoleActor {
		actorID = actor.AccountID
	}
	if err := writeAuditMetadata(ctx, tx, orgID, actorID, action, targetType, target, metadata); err != nil {
		return err
	}
	return r.enqueueWebhookEventTx(ctx, tx, projectID, action, targetType, target, metadata)
}
