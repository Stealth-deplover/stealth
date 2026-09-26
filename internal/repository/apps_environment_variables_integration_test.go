package repository

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/appsecret"
	"github.com/google/uuid"
)

func TestAppEnvironmentVariablePersistenceGenerationAuthorizationAndLeaseFencingIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	appID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "environment", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	createReadySelectedRuntimeDeployment(t, f, f.projectOneID, appID)
	cipher, err := appsecret.New(bytesOfOnes(appsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	initialValue := "app-env-fake-secret-DO-NOT-LOG"
	variableID := uuid.Must(uuid.NewV7())
	before, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	created, err := f.repo.CreateAppEnvironmentVariable(f.ctx, variableID, f.projectOneID, appID, f.actor, AppEnvironmentVariableInput{
		Key: "API_TOKEN", IsSecret: true, Value: &initialValue, Cipher: cipher,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.HasValue || !created.IsSecret {
		t.Fatalf("created App variable metadata = %+v", created)
	}
	if _, err := f.repo.CreateAppEnvironmentVariable(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, appID, f.actor, AppEnvironmentVariableInput{Key: "OPTIONAL_VALUE"}); err != nil {
		t.Fatalf("create metadata-only App variable: %v", err)
	}
	afterCreate, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if afterCreate.DesiredGeneration != before.DesiredGeneration+1 || afterCreate.RuntimeStatus != "pending" {
		t.Fatalf("configured value did not advance desired runtime generation: before=%+v after=%+v", before, afterCreate)
	}
	duplicateValue := "duplicate-attempt-must-not-persist"
	if _, err := f.repo.CreateAppEnvironmentVariable(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, appID, f.actor, AppEnvironmentVariableInput{
		Key: "API_TOKEN", IsSecret: true, Value: &duplicateValue, Cipher: cipher,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate App variable create = %v, want conflict without a second row", err)
	}
	afterDuplicate, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil || afterDuplicate.DesiredGeneration != afterCreate.DesiredGeneration {
		t.Fatalf("duplicate variable request changed the runtime generation: App=%+v err=%v", afterDuplicate, err)
	}

	items, next, canManage, err := f.repo.ListAppEnvironmentVariables(f.ctx, f.projectOneID, appID, f.actor, 20, nil)
	if err != nil || !canManage || next != "" || len(items) != 2 {
		t.Fatalf("App environment list = %#v next=%q can_manage=%t err=%v", items, next, canManage, err)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), initialValue) || strings.Contains(string(encoded), "ciphertext") || strings.Contains(string(encoded), "nonce") {
		t.Fatalf("App environment metadata exposed value or crypto state: %s", encoded)
	}

	description := "Rotated by operations"
	metadataOnly, err := f.repo.UpdateAppEnvironmentVariable(f.ctx, f.projectOneID, appID, variableID, f.actor, AppEnvironmentVariablePatch{
		SetDescription: true, Description: &description,
	})
	if err != nil || metadataOnly.Description == nil || *metadataOnly.Description != description {
		t.Fatalf("safe metadata update = %+v err=%v", metadataOnly, err)
	}
	metadataApp, err := f.repo.GetApp(f.ctx, f.projectOneID, appID, f.actor)
	if err != nil || metadataApp.DesiredGeneration != afterCreate.DesiredGeneration {
		t.Fatalf("metadata-only edit changed runtime generation: App=%+v err=%v", metadataApp, err)
	}

	job, err := f.repo.ClaimNextAppRuntime(f.ctx, "app-env-runtime", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	values, err := f.repo.ListAppRuntimeEnvironment(f.ctx, job)
	if err != nil || len(values) != 1 {
		t.Fatalf("leased runtime environment = %#v err=%v", values, err)
	}
	decrypted, err := cipher.Decrypt(f.projectOneID, appID, variableID, values[0].Ciphertext)
	if err != nil || string(decrypted) != initialValue {
		t.Fatalf("trusted runtime ciphertext decrypt = %q err=%v", decrypted, err)
	}
	clear(decrypted)

	readKeyID, writeKeyID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO project_api_keys (id,project_id,name,prefix,secret_hash,scopes)
		VALUES ($1,$2,'App env reader','stl_key_read0001',$3,$4),($5,$2,'App env writer','stl_key_write001',$6,$7)`,
		readKeyID, f.projectOneID, bytesOfZeroes(32), []string{"apps.read"}, writeKeyID, bytesOfOnes(32), []string{"apps.write"}); err != nil {
		t.Fatal(err)
	}
	readActor := AppActor{Kind: AppAPIKeyActor, APIKeyID: readKeyID, APIKeyScopes: []string{"apps.read"}}
	readItems, _, readCanManage, err := f.repo.ListAppEnvironmentVariables(f.ctx, f.projectOneID, appID, readActor, 20, nil)
	if err != nil || readCanManage || len(readItems) != 1 {
		t.Fatalf("apps.read access = %#v can_manage=%t err=%v", readItems, readCanManage, err)
	}
	if _, err := f.repo.CreateAppEnvironmentVariable(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, appID, readActor, AppEnvironmentVariableInput{Key: "DENIED"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("apps.read key mutation = %v, want forbidden", err)
	}
	writeActor := AppActor{Kind: AppAPIKeyActor, APIKeyID: writeKeyID, APIKeyScopes: []string{"apps.write"}}
	writeValue := "write-scope-value"
	if _, err := f.repo.CreateAppEnvironmentVariable(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, appID, writeActor, AppEnvironmentVariableInput{Key: "WRITE_SCOPE", Value: &writeValue, Cipher: cipher}); err != nil {
		t.Fatalf("apps.write key could not create an App variable: %v", err)
	}
	if _, _, _, err := f.repo.ListAppEnvironmentVariables(f.ctx, f.projectTwoID, appID, f.actor, 20, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project App variable list = %v, want hidden not-found", err)
	}
	if _, _, _, err := f.repo.ListAppEnvironmentVariables(f.ctx, f.projectTwoID, appID, AppActor{Kind: AppAPIKeyActor, APIKeyID: readKeyID, APIKeyScopes: []string{"apps.read"}}, 20, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-project API key list = %v, want hidden not-found", err)
	}

	replacement := "replacement-fake-secret"
	if _, err := f.repo.UpdateAppEnvironmentVariable(f.ctx, f.projectOneID, appID, variableID, f.actor, AppEnvironmentVariablePatch{Value: &replacement, Cipher: cipher}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ListAppRuntimeEnvironment(f.ctx, job); !errors.Is(err, ErrAppRuntimeStale) {
		t.Fatalf("old runtime lease read after value generation changed = %v, want stale", err)
	}
	if _, err := f.repo.UpdateAppEnvironmentVariable(f.ctx, f.projectOneID, appID, variableID, f.actor, AppEnvironmentVariablePatch{ClearValue: true}); err != nil {
		t.Fatalf("clear configured App value: %v", err)
	}
	emptyJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "app-env-empty-runtime", time.Minute)
	if err != nil {
		t.Fatalf("claim generation with no configured values: %v", err)
	}
	emptyValues, err := f.repo.ListAppRuntimeEnvironment(f.ctx, emptyJob)
	if err != nil || len(emptyValues) != 0 {
		t.Fatalf("current generation with no configured values = %#v err=%v", emptyValues, err)
	}
	var auditText string
	if err := f.pool.QueryRow(f.ctx, `SELECT COALESCE(string_agg(metadata::text,' '),'') FROM audit_events WHERE target_id=$1`, appID.String()).Scan(&auditText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditText, initialValue) || strings.Contains(auditText, replacement) || strings.Contains(auditText, duplicateValue) {
		t.Fatalf("App environment plaintext was recorded in audit metadata: %s", auditText)
	}
}
