package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStorageBinaryQuotaPermissionsAndAPIKeyRevocationIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	// Resource cleanup callbacks are registered later and run first (LIFO), so
	// keep the pool open until every tenant fixture has been removed.
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	storageRoot := t.TempDir()
	if base := strings.TrimSpace(os.Getenv("TEST_STORAGE_ROOT")); base != "" {
		if err := os.MkdirAll(base, 0o700); err != nil {
			t.Fatalf("create TEST_STORAGE_ROOT %q: %v", base, err)
		}
		configuredRoot, err := os.MkdirTemp(base, "storage-integration-*")
		if err != nil {
			t.Fatalf("create storage test root under TEST_STORAGE_ROOT: %v", err)
		}
		storageRoot = configuredRoot
		t.Cleanup(func() { _ = os.RemoveAll(configuredRoot) })
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Keep the readiness-failure sentinel outside the object root. Blob
	// accounting assertions below should count only files owned by Storage,
	// not test fixtures used to make initialization fail.
	invalidRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(invalidRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	notReadyServer := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		StorageRoot:        invalidRoot,
		StorageMaxFileSize: 16,
		SessionCookieName:  "stealth_session",
		SessionTTL:         time.Hour,
		AppSessionTTL:      time.Hour,
	}, repository.New(pool), logger, httpapi.Dependencies{AuthLimiter: integrationLimiter(t, ctx)}))
	readinessResponse, err := notReadyServer.Client().Get(notReadyServer.URL + "/readyz")
	if err != nil {
		notReadyServer.Close()
		t.Fatal(err)
	}
	_ = readinessResponse.Body.Close()
	notReadyServer.Close()
	if readinessResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readiness with failed storage initialization = %d, want 503", readinessResponse.StatusCode)
	}

	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	ownerRegistration := struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}{}
	server := httptestNewStorageServer(t, storageRoot, pool, logger)
	defer server.Close()

	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    "storage-owner-" + ownerID.String() + "@example.test",
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &ownerRegistration)
	project := struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+ownerRegistration.Organization.ID+"/projects", map[string]string{
		"name": "storage-" + ownerID.String()[:12],
	}, http.StatusCreated, &project)
	projectURL := server.URL + "/v1/projects/" + project.Project.ID
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, ownerRegistration.Organization.ID); err != nil {
			t.Errorf("clean storage integration organization: %v", err)
		}
		if _, err := pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, ownerRegistration.Account.ID); err != nil {
			t.Errorf("clean storage integration account: %v", err)
		}
	})

	appUser := struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/users", map[string]string{
		"email":    "storage-app-" + ownerID.String() + "@example.test",
		"password": "application-user-password-1",
	}, http.StatusCreated, &appUser)
	bucket := struct {
		Bucket struct {
			ID      string `json:"id"`
			Used    int64  `json:"used_bytes"`
			MaxFile int64  `json:"max_file_size_bytes"`
			Quota   int64  `json:"quota_bytes"`
		} `json:"bucket"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/storage/buckets", map[string]any{
		"name":                "private-files",
		"file_security":       true,
		"create_permissions":  []string{},
		"read_permissions":    []string{},
		"update_permissions":  []string{},
		"delete_permissions":  []string{},
		"max_file_size_bytes": 5,
		"quota_bytes":         8,
	}, http.StatusCreated, &bucket)
	if bucket.Bucket.MaxFile != 5 || bucket.Bucket.Quota != 8 || bucket.Bucket.Used != 0 {
		t.Fatalf("unexpected bucket limits: %+v", bucket.Bucket)
	}
	bucketURL := projectURL + "/storage/buckets/" + bucket.Bucket.ID
	filesURL := bucketURL + "/files"

	content := []byte("q9@Z!")
	fileBody := uploadStorageMultipart(t, ownerClient, filesURL, "document.bin", content, map[string]string{
		"read_permissions":   fmt.Sprintf(`["user:%s"]`, appUser.User.ID),
		"update_permissions": fmt.Sprintf(`["user:%s"]`, appUser.User.ID),
		"delete_permissions": fmt.Sprintf(`["user:%s"]`, appUser.User.ID),
	}, http.StatusCreated)
	var fileResponse struct {
		File struct {
			ID       string   `json:"id"`
			Name     string   `json:"name"`
			MimeType string   `json:"mime_type"`
			Size     int64    `json:"size_bytes"`
			Checksum string   `json:"checksum_sha256"`
			Read     []string `json:"read_permissions"`
		} `json:"file"`
	}
	if err := json.Unmarshal(fileBody, &fileResponse); err != nil {
		t.Fatal(err)
	}
	wantDigest := sha256.Sum256(content)
	if fileResponse.File.Name != "document.bin" || fileResponse.File.MimeType != "text/plain" || fileResponse.File.Size != int64(len(content)) || fileResponse.File.Checksum != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("unexpected file metadata: %s", fileBody)
	}
	if len(fileResponse.File.Read) != 1 || fileResponse.File.Read[0] != "user:"+appUser.User.ID {
		t.Fatalf("unexpected file read grant: %#v", fileResponse.File.Read)
	}
	fileID := fileResponse.File.ID
	blobPath := filepath.Join(storageRoot, project.Project.ID, bucket.Bucket.ID, fileID)
	if _, err := os.Stat(blobPath); err != nil {
		t.Fatalf("published blob missing at UUID path %q: %v", blobPath, err)
	}

	var ownerFiles struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	requestJSON(t, ownerClient, http.MethodGet, filesURL, nil, http.StatusOK, &ownerFiles)
	if len(ownerFiles.Files) != 1 || ownerFiles.Files[0].ID != fileID {
		t.Fatalf("owner file list = %#v", ownerFiles.Files)
	}
	downloadRequest, err := http.NewRequest(http.MethodGet, filesURL+"/"+fileID+"/download", nil)
	if err != nil {
		t.Fatal(err)
	}
	downloadResponse, err := ownerClient.Do(downloadRequest)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, readErr := io.ReadAll(downloadResponse.Body)
	closeErr := downloadResponse.Body.Close()
	if downloadResponse.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || !bytes.Equal(downloaded, content) {
		t.Fatalf("download status=%d bytes=%q read=%v close=%v", downloadResponse.StatusCode, downloaded, readErr, closeErr)
	}
	if downloadResponse.Header.Get("Content-Disposition") == "" || downloadResponse.Header.Get("X-Content-Type-Options") != "nosniff" || downloadResponse.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("unsafe download headers: %+v", downloadResponse.Header)
	}

	appClient := newIntegrationClient(t)
	requestJSON(t, appClient, http.MethodPost, projectURL+"/sessions/email-password", map[string]string{
		"email":    "storage-app-" + ownerID.String() + "@example.test",
		"password": "application-user-password-1",
	}, http.StatusNoContent, nil)
	var appFiles struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	requestJSON(t, appClient, http.MethodGet, filesURL, nil, http.StatusOK, &appFiles)
	if len(appFiles.Files) != 1 || appFiles.Files[0].ID != fileID {
		t.Fatalf("file grant did not authorize app list: %#v", appFiles.Files)
	}
	requestJSON(t, appClient, http.MethodPatch, filesURL+"/"+fileID, map[string]string{"name": "renamed.txt"}, http.StatusOK, nil)
	requestJSON(t, appClient, http.MethodPatch, filesURL+"/"+fileID, map[string]any{"read_permissions": []string{"any"}}, http.StatusForbidden, nil)

	tooLarge := uploadStorageMultipart(t, ownerClient, filesURL, "six.txt", []byte("123456"), nil, http.StatusRequestEntityTooLarge)
	if !bytes.Contains(tooLarge, []byte("payload_too_large")) {
		t.Fatalf("max-file response = %s", tooLarge)
	}
	quotaExceeded := uploadStorageMultipart(t, ownerClient, filesURL, "four.txt", []byte("1234"), nil, http.StatusRequestEntityTooLarge)
	if !bytes.Contains(quotaExceeded, []byte("storage_quota_exceeded")) {
		t.Fatalf("quota response = %s", quotaExceeded)
	}
	if got := countStorageBlobFiles(t, storageRoot); got != 1 {
		t.Fatalf("blob count after rejected uploads = %d, want one committed blob", got)
	}
	requestJSON(t, ownerClient, http.MethodGet, bucketURL, nil, http.StatusOK, &bucket)
	if bucket.Bucket.Used != int64(len(content)) {
		t.Fatalf("bucket used_bytes after rejected uploads = %d, want %d", bucket.Bucket.Used, len(content))
	}

	// Disabling file security deliberately makes bucket grants authoritative.
	publicBucket := struct {
		Bucket struct {
			ID string `json:"id"`
		} `json:"bucket"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/storage/buckets", map[string]any{
		"name":                "public-files",
		"file_security":       false,
		"create_permissions":  []string{},
		"read_permissions":    []string{"user:" + appUser.User.ID},
		"update_permissions":  []string{},
		"delete_permissions":  []string{},
		"max_file_size_bytes": 5,
		"quota_bytes":         8,
	}, http.StatusCreated, &publicBucket)
	publicFilesURL := projectURL + "/storage/buckets/" + publicBucket.Bucket.ID + "/files"
	publicFileBody := uploadStorageMultipart(t, ownerClient, publicFilesURL, "public.bin", []byte("open"), map[string]string{
		"read_permissions":   `[]`,
		"update_permissions": `[]`,
		"delete_permissions": `[]`,
	}, http.StatusCreated)
	var publicFile struct {
		File struct {
			ID string `json:"id"`
		} `json:"file"`
	}
	if err := json.Unmarshal(publicFileBody, &publicFile); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, appClient, http.MethodGet, publicFilesURL, nil, http.StatusOK, &appFiles)
	if len(appFiles.Files) != 1 || appFiles.Files[0].ID != publicFile.File.ID {
		t.Fatalf("bucket-only grant did not authorize app list: %#v", appFiles.Files)
	}

	// The creator foreign keys use PostgreSQL's column-list SET NULL action:
	// deleting an application identity must clear only the creator column and
	// must not try to null the tenant's NOT NULL project_id.
	creatorBucket := struct {
		Bucket struct {
			ID string `json:"id"`
		} `json:"bucket"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/storage/buckets", map[string]any{
		"name":                "creator-files",
		"file_security":       true,
		"create_permissions":  []string{"user:" + appUser.User.ID},
		"read_permissions":    []string{"any"},
		"update_permissions":  []string{},
		"delete_permissions":  []string{},
		"max_file_size_bytes": 5,
		"quota_bytes":         8,
	}, http.StatusCreated, &creatorBucket)
	creatorFilesURL := projectURL + "/storage/buckets/" + creatorBucket.Bucket.ID + "/files"
	creatorFileBody := uploadStorageMultipart(t, appClient, creatorFilesURL, "creator.bin", []byte("ownr"), nil, http.StatusCreated)
	var creatorFile struct {
		File struct {
			ID string `json:"id"`
		} `json:"file"`
	}
	if err := json.Unmarshal(creatorFileBody, &creatorFile); err != nil {
		t.Fatal(err)
	}
	databaseID := uuid.Must(uuid.NewV7())
	tableID := uuid.Must(uuid.NewV7())
	rowID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO project_databases (id,project_id,name) VALUES ($1,$2,$3)`, databaseID, project.Project.ID, "fk-regression-"+ownerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO database_tables (id,database_id,project_id,name,create_permissions,read_permissions,update_permissions,delete_permissions) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, tableID, databaseID, project.Project.ID, "fk-table-"+ownerID.String()[:8], []string{}, []string{}, []string{}, []string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO database_rows (id,table_id,project_id,data,creator_project_user_id) VALUES ($1,$2,$3,'{}'::jsonb,$4)`, rowID, tableID, project.Project.ID, appUser.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM project_users WHERE project_id=$1 AND id=$2`, project.Project.ID, appUser.User.ID); err != nil {
		t.Fatal(err)
	}
	var storageProjectID, databaseProjectID uuid.UUID
	var storageCreator, databaseCreator *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT project_id,creator_project_user_id FROM storage_files WHERE id=$1`, creatorFile.File.ID).Scan(&storageProjectID, &storageCreator); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT project_id,creator_project_user_id FROM database_rows WHERE id=$1`, rowID).Scan(&databaseProjectID, &databaseCreator); err != nil {
		t.Fatal(err)
	}
	if storageProjectID.String() != project.Project.ID || databaseProjectID.String() != project.Project.ID || storageCreator != nil || databaseCreator != nil {
		t.Fatalf("creator FK delete changed tenant or retained creator: storage project=%s creator=%v database project=%s creator=%v", storageProjectID, storageCreator, databaseProjectID, databaseCreator)
	}

	// Anonymous writes are available only when the bucket allows create and
	// all three file grants are explicitly present in the multipart request.
	anonymousBucket := struct {
		Bucket struct {
			ID string `json:"id"`
		} `json:"bucket"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/storage/buckets", map[string]any{
		"name":                "anonymous-files",
		"file_security":       true,
		"create_permissions":  []string{"any"},
		"read_permissions":    []string{"any"},
		"update_permissions":  []string{"any"},
		"delete_permissions":  []string{"any"},
		"max_file_size_bytes": 5,
		"quota_bytes":         8,
	}, http.StatusCreated, &anonymousBucket)
	anonymousFilesURL := projectURL + "/storage/buckets/" + anonymousBucket.Bucket.ID + "/files"
	anonymousClient := newIntegrationClient(t)
	missingAnonymousGrants := uploadStorageMultipart(t, anonymousClient, anonymousFilesURL, "anonymous.bin", []byte("anon"), nil, http.StatusUnprocessableEntity)
	if !bytes.Contains(missingAnonymousGrants, []byte("anonymous uploads")) {
		t.Fatalf("anonymous missing-grants response = %s", missingAnonymousGrants)
	}
	uploadStorageMultipart(t, anonymousClient, anonymousFilesURL, "anonymous.bin", []byte("anon"), map[string]string{
		"read_permissions":   `["any"]`,
		"update_permissions": `["any"]`,
		"delete_permissions": `["any"]`,
	}, http.StatusCreated)

	readOnlyKeyBody := requestJSONRaw(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{
		"name":   "storage read-only integration key",
		"scopes": []string{"storage.read"},
	}, http.StatusCreated)
	var readOnlyKey struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(readOnlyKeyBody, &readOnlyKey); err != nil {
		t.Fatal(err)
	}
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/storage/buckets", nil, http.StatusOK, map[string]string{"X-Stealth-Key": readOnlyKey.Secret})
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPost, projectURL+"/storage/buckets", map[string]any{"name": "read-only-cannot-write"}, http.StatusForbidden, map[string]string{"X-Stealth-Key": readOnlyKey.Secret})

	writeOnlyKeyBody := requestJSONRaw(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{
		"name":   "storage write-only integration key",
		"scopes": []string{"storage.write"},
	}, http.StatusCreated)
	var writeOnlyKey struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(writeOnlyKeyBody, &writeOnlyKey); err != nil {
		t.Fatal(err)
	}
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/storage/buckets", nil, http.StatusForbidden, map[string]string{"X-Stealth-Key": writeOnlyKey.Secret})
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPost, projectURL+"/storage/buckets", map[string]any{"name": "write-only-bucket"}, http.StatusCreated, map[string]string{"X-Stealth-Key": writeOnlyKey.Secret})

	keyBody := requestJSONRaw(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{
		"name":   "storage integration key",
		"scopes": []string{"storage.read", "storage.write"},
	}, http.StatusCreated)
	var createdKey struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(keyBody, &createdKey); err != nil {
		t.Fatal(err)
	}
	keyClient := newIntegrationClient(t)
	keyHeaders := map[string]string{"X-Stealth-Key": createdKey.Secret}
	requestJSONWithHeaders(t, keyClient, http.MethodGet, projectURL+"/storage/buckets", nil, http.StatusOK, keyHeaders)
	requestJSON(t, ownerClient, http.MethodDelete, projectURL+"/storage/buckets/"+bucket.Bucket.ID+"/files/"+fileID, nil, http.StatusNoContent, nil)
	if _, err := os.Stat(blobPath); !os.IsNotExist(err) {
		t.Fatalf("deleted blob stat error = %v, want not exist", err)
	}
	requestJSON(t, ownerClient, http.MethodGet, bucketURL, nil, http.StatusOK, &bucket)
	if bucket.Bucket.Used != 0 {
		t.Fatalf("bucket used_bytes after delete = %d, want 0", bucket.Bucket.Used)
	}
	// Console/API-key uploads may omit file grants. Omission must persist as
	// non-null empty arrays (default deny), never as SQL NULL.
	defaultGrantBody := uploadStorageMultipart(t, ownerClient, filesURL, "default-deny.bin", []byte("x"), nil, http.StatusCreated)
	var defaultGrantFile struct {
		File struct {
			ID     string   `json:"id"`
			Read   []string `json:"read_permissions"`
			Update []string `json:"update_permissions"`
			Delete []string `json:"delete_permissions"`
		} `json:"file"`
	}
	if err := json.Unmarshal(defaultGrantBody, &defaultGrantFile); err != nil {
		t.Fatal(err)
	}
	if defaultGrantFile.File.Read == nil || defaultGrantFile.File.Update == nil || defaultGrantFile.File.Delete == nil || len(defaultGrantFile.File.Read) != 0 || len(defaultGrantFile.File.Update) != 0 || len(defaultGrantFile.File.Delete) != 0 {
		t.Fatalf("omitted management grants must be non-null empty arrays: %+v", defaultGrantFile.File)
	}
	requestJSON(t, ownerClient, http.MethodDelete, filesURL+"/"+defaultGrantFile.File.ID, nil, http.StatusNoContent, nil)
	requestJSONWithHeaders(t, keyClient, http.MethodGet, projectURL+"/storage/buckets", nil, http.StatusOK, keyHeaders)
	requestJSON(t, ownerClient, http.MethodDelete, projectURL+"/api-keys/"+createdKey.Key.ID, nil, http.StatusNoContent, nil)
	requestJSONWithHeaders(t, keyClient, http.MethodGet, projectURL+"/storage/buckets", nil, http.StatusUnauthorized, keyHeaders)

	otherProject := struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+ownerRegistration.Organization.ID+"/projects", map[string]string{
		"name": "storage-other-" + ownerID.String()[:12],
	}, http.StatusCreated, &otherProject)
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/projects/"+otherProject.Project.ID+"/storage/buckets/"+bucket.Bucket.ID, nil, http.StatusNotFound, nil)

	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='storage_file.create' AND target_id=$1`, fileID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("storage create audit count = %d, want 1", auditCount)
	}
	var auditMetadata string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM audit_events WHERE action='storage_file.create' AND target_id=$1 ORDER BY created_at DESC LIMIT 1`, fileID).Scan(&auditMetadata); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditMetadata, string(content)) {
		t.Fatalf("file content leaked into audit metadata: %s", auditMetadata)
	}
}

// Keep the integration server's local store isolated from the host default.
// The production constructor receives the root from config.Load; tests must
// never create /var/lib/stealth/storage as a side effect.
func httptestNewStorageServer(t *testing.T, root string, pool *pgxpool.Pool, logger *slog.Logger) *httptest.Server {
	t.Helper()
	return httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		StorageRoot:              root,
		StorageMaxFileSize:       16,
		StorageDefaultQuotaBytes: 32,
		SessionCookieName:        "stealth_session",
		SessionTTL:               time.Hour,
		AppSessionTTL:            time.Hour,
	}, repository.New(pool), logger, httpapi.Dependencies{AuthLimiter: integrationLimiter(t, context.Background())}))
}

func uploadStorageMultipart(t *testing.T, client *http.Client, url, filename string, content []byte, fields map[string]string, expectedStatus int) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	result, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != expectedStatus {
		t.Fatalf("multipart upload expected %d, got %d: %s", expectedStatus, response.StatusCode, result)
	}
	return result
}

func countStorageBlobFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Type().IsRegular() {
			count++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count
}
