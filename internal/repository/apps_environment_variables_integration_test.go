package repository

import (
	"bytes"
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
	if err != nil || readCanManage || len(readItems) != 2 {
		t.Fatalf("apps.read access = %#v can_manage=%t err=%v", readItems, readCanManage, err)
	}
	if _, err := f.repo.CreateAppEnvironmentVariable(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, appID, readActor, AppEnvironmentVariableInput{Key: "DENIED"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("apps.read key mutation = %v, want forbidden", err)
	}
	writeActor := AppActor{Kind: AppAPIKeyActor, APIKeyID: writeKeyID, APIKeyScopes: []string{"apps.write"}}
	writeValue := "write-scope-value"
	writeVariableID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateAppEnvironmentVariable(f.ctx, writeVariableID, f.projectOneID, appID, writeActor, AppEnvironmentVariableInput{Key: "WRITE_SCOPE", Value: &writeValue, Cipher: cipher}); err != nil {
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
	if err := f.repo.ReleaseAppRuntimeJob(f.ctx, job); err != nil {
		t.Fatalf("release stale runtime lease before claiming current generation: %v", err)
	}
	replacementJob, err := f.repo.ClaimNextAppRuntime(f.ctx, "app-env-replacement-runtime", time.Minute)
	if err != nil {
		t.Fatalf("claim runtime job after value replacement: %v", err)
	}
	replacementValues, err := f.repo.ListAppRuntimeEnvironment(f.ctx, replacementJob)
	if err != nil || len(replacementValues) != 2 {
		t.Fatalf("replacement generation runtime environment = %#v err=%v", replacementValues, err)
	}
	secretMatches := false
	unchangedConfiguredValueMatches := false
	for _, item := range replacementValues {
		plaintext, decryptErr := cipher.Decrypt(f.projectOneID, appID, item.ID, item.Ciphertext)
		if decryptErr != nil {
			t.Fatalf("decrypt replacement generation value: %v", decryptErr)
		}
		switch item.Key {
		case "API_TOKEN":
			secretMatches = string(plaintext) == replacement
		case "WRITE_SCOPE":
			unchangedConfiguredValueMatches = string(plaintext) == writeValue
		}
		clear(plaintext)
	}
	if !secretMatches || !unchangedConfiguredValueMatches {
		t.Fatal("current runtime generation did not read the replaced secret and unchanged configured values")
	}
	if err := f.repo.ReleaseAppRuntimeJob(f.ctx, replacementJob); err != nil {
		t.Fatalf("release replacement runtime lease before clearing values: %v", err)
	}
	if _, err := f.repo.UpdateAppEnvironmentVariable(f.ctx, f.projectOneID, appID, variableID, f.actor, AppEnvironmentVariablePatch{ClearValue: true}); err != nil {
		t.Fatalf("clear configured App value: %v", err)
	}
	if _, err := f.repo.UpdateAppEnvironmentVariable(f.ctx, f.projectOneID, appID, writeVariableID, f.actor, AppEnvironmentVariablePatch{ClearValue: true}); err != nil {
		t.Fatalf("clear apps.write variable value: %v", err)
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

func TestAppEnvironmentAggregateValueLimitIsTransactionalIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	cipher, err := appsecret.New(bytesOfOnes(appsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}

	createApp := func(name string) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		if _, err := f.repo.CreateApp(f.ctx, id, f.projectOneID, f.actor, AppInput{Name: name, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	createValue := func(appID uuid.UUID, key, value string) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		if _, err := f.repo.CreateAppEnvironmentVariable(f.ctx, id, f.projectOneID, appID, f.actor, AppEnvironmentVariableInput{
			Key: key, Value: &value, Cipher: cipher,
		}); err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
		return id
	}

	// Eight maximum-sized values fill the public total exactly. A rejected
	// ninth value must roll back without advancing desired runtime state.
	limitAppID := createApp("environment-total-at-limit")
	fullValue := strings.Repeat("v", AppEnvironmentVariableMaxValueBytes)
	var firstID uuid.UUID
	for index := 0; index < 8; index++ {
		id := createValue(limitAppID, "LIMIT_"+string(rune('A'+index)), fullValue)
		if index == 0 {
			firstID = id
		}
	}
	atLimit, err := f.repo.GetApp(f.ctx, f.projectOneID, limitAppID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	tooMuch := "x"
	if _, err := f.repo.CreateAppEnvironmentVariable(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, limitAppID, f.actor, AppEnvironmentVariableInput{
		Key: "OVER_LIMIT", Value: &tooMuch, Cipher: cipher,
	}); !errors.Is(err, ErrAppEnvironmentTotalSizeLimit) {
		t.Fatalf("over-limit create = %v, want total size limit", err)
	}
	afterRejectedCreate, err := f.repo.GetApp(f.ctx, f.projectOneID, limitAppID, f.actor)
	if err != nil || afterRejectedCreate.DesiredGeneration != atLimit.DesiredGeneration {
		t.Fatalf("rejected create changed desired generation: before=%d after=%d err=%v", atLimit.DesiredGeneration, afterRejectedCreate.DesiredGeneration, err)
	}
	items, _, _, err := f.repo.ListAppEnvironmentVariables(f.ctx, f.projectOneID, limitAppID, f.actor, 20, nil)
	if err != nil || len(items) != 8 {
		t.Fatalf("rejected create persisted a row: rows=%d err=%v", len(items), err)
	}
	if err := f.repo.DeleteAppEnvironmentVariable(f.ctx, f.projectOneID, limitAppID, firstID, f.actor); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.CreateAppEnvironmentVariable(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, limitAppID, f.actor, AppEnvironmentVariableInput{
		Key: "FREED_CAPACITY", Value: &fullValue, Cipher: cipher,
	}); err != nil {
		t.Fatalf("create at limit after delete freed capacity: %v", err)
	}

	// Exercise replacement accounting with a configured old value, while a
	// distinct metadata-only value can be assigned only if its new size fits.
	replaceAppID := createApp("environment-total-replacement")
	for index := 0; index < 7; index++ {
		createValue(replaceAppID, "FULL_"+string(rune('A'+index)), fullValue)
	}
	partial := strings.Repeat("p", 41248)
	createValue(replaceAppID, "PARTIAL", partial) // Other values total 500000 bytes.
	old := "o"
	targetID := createValue(replaceAppID, "TARGET", old)
	within := strings.Repeat("w", 10000)
	if _, err := f.repo.UpdateAppEnvironmentVariable(f.ctx, f.projectOneID, replaceAppID, targetID, f.actor, AppEnvironmentVariablePatch{Value: &within, Cipher: cipher}); err != nil {
		t.Fatalf("replacement within aggregate limit: %v", err)
	}
	beforeOverReplace, err := f.repo.GetApp(f.ctx, f.projectOneID, replaceAppID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	var beforeCiphertext []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT value_ciphertext FROM app_environment_variables WHERE id=$1`, targetID).Scan(&beforeCiphertext); err != nil {
		t.Fatal(err)
	}
	replacement := fullValue
	if _, err := f.repo.UpdateAppEnvironmentVariable(f.ctx, f.projectOneID, replaceAppID, targetID, f.actor, AppEnvironmentVariablePatch{Value: &replacement, Cipher: cipher}); !errors.Is(err, ErrAppEnvironmentTotalSizeLimit) {
		t.Fatalf("over-limit replacement = %v, want total size limit", err)
	}
	var afterCiphertext []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT value_ciphertext FROM app_environment_variables WHERE id=$1`, targetID).Scan(&afterCiphertext); err != nil {
		t.Fatal(err)
	}
	afterOverReplace, err := f.repo.GetApp(f.ctx, f.projectOneID, replaceAppID, f.actor)
	if err != nil || afterOverReplace.DesiredGeneration != beforeOverReplace.DesiredGeneration || !bytes.Equal(beforeCiphertext, afterCiphertext) {
		t.Fatalf("rejected replacement changed ciphertext or generation: generation %d→%d ciphertextPreserved=%t err=%v", beforeOverReplace.DesiredGeneration, afterOverReplace.DesiredGeneration, bytes.Equal(beforeCiphertext, afterCiphertext), err)
	}
	description := "metadata only"
	metadataApp, err := f.repo.UpdateAppEnvironmentVariable(f.ctx, f.projectOneID, replaceAppID, targetID, f.actor, AppEnvironmentVariablePatch{
		SetDescription: true, Description: &description,
	})
	if err != nil || metadataApp.Description == nil || *metadataApp.Description != description {
		t.Fatalf("metadata-only edit at near-limit total = %+v err=%v", metadataApp, err)
	}
	metaGeneration, err := f.repo.GetApp(f.ctx, f.projectOneID, replaceAppID, f.actor)
	if err != nil || metaGeneration.DesiredGeneration != beforeOverReplace.DesiredGeneration {
		t.Fatalf("metadata-only edit changed generation: before=%d after=%d err=%v", beforeOverReplace.DesiredGeneration, metaGeneration.DesiredGeneration, err)
	}
	if _, err := f.repo.UpdateAppEnvironmentVariable(f.ctx, f.projectOneID, replaceAppID, targetID, f.actor, AppEnvironmentVariablePatch{ClearValue: true}); err != nil {
		t.Fatalf("clear value to free aggregate capacity: %v", err)
	}
	isSecret := true
	renamed := "TARGET_RENAMED"
	beforeMetadata, err := f.repo.GetApp(f.ctx, f.projectOneID, replaceAppID, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.UpdateAppEnvironmentVariable(f.ctx, f.projectOneID, replaceAppID, targetID, f.actor, AppEnvironmentVariablePatch{Key: &renamed, IsSecret: &isSecret}); err != nil {
		t.Fatalf("rename/classify cleared metadata: %v", err)
	}
	var totalBytes int64
	if err := f.pool.QueryRow(f.ctx, `SELECT COALESCE(sum(value_plaintext_bytes),0)::bigint FROM app_environment_variables WHERE app_id=$1 AND project_id=$2`, replaceAppID, f.projectOneID).Scan(&totalBytes); err != nil {
		t.Fatal(err)
	}
	afterMetadata, err := f.repo.GetApp(f.ctx, f.projectOneID, replaceAppID, f.actor)
	if err != nil || totalBytes != 500000 || afterMetadata.DesiredGeneration != beforeMetadata.DesiredGeneration {
		t.Fatalf("rename/classification changed configured usage or generation: bytes=%d generation %d→%d err=%v", totalBytes, beforeMetadata.DesiredGeneration, afterMetadata.DesiredGeneration, err)
	}
	freed := strings.Repeat("f", 24000)
	freedID := createValue(replaceAppID, "CLEARED_CAPACITY", freed)
	if err := f.repo.DeleteAppEnvironmentVariable(f.ctx, f.projectOneID, replaceAppID, freedID, f.actor); err != nil {
		t.Fatalf("delete configured value to free aggregate capacity: %v", err)
	}
	if _, err := f.repo.CreateAppEnvironmentVariable(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, replaceAppID, f.actor, AppEnvironmentVariableInput{
		Key: "DELETED_CAPACITY", Value: &freed, Cipher: cipher,
	}); err != nil {
		t.Fatalf("create after delete freed aggregate capacity: %v", err)
	}
}
