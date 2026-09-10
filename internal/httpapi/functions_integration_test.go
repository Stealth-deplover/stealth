package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/ratelimit"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFunctionsControlPlaneIntegration(t *testing.T) {
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
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	storageRoot := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		StorageRoot:                storageRoot,
		StorageMaxFileSize:         64,
		StorageDefaultQuotaBytes:   128,
		FunctionsMaxArtifactSize:   32,
		FunctionsDefaultQuotaBytes: 64,
		FunctionsSecretKey:         bytes.Repeat([]byte{0x2a}, 32),
		SessionCookieName:          "stealth_session",
		SessionTTL:                 time.Hour,
		AppSessionTTL:              time.Hour,
	}, repository.New(pool), logger, httpapi.Dependencies{AuthLimiter: ratelimit.NewMemoryLimiter()}))
	t.Cleanup(server.Close)
	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	var registration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("functions-owner-%s@example.test", ownerID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id=$1 OR actor_account_id=$2`, registration.Organization.ID, registration.Account.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})
	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{
		"name": "functions-" + ownerID.String()[:8],
	}, http.StatusCreated, &project)
	projectURL := server.URL + "/v1/projects/" + project.Project.ID

	createKey := func(name string, scopes []string) (string, string) {
		t.Helper()
		var response struct {
			Key struct {
				ID string `json:"id"`
			} `json:"key"`
			Secret string `json:"secret"`
		}
		requestJSON(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{"name": name, "scopes": scopes}, http.StatusCreated, &response)
		if response.Key.ID == "" || response.Secret == "" {
			t.Fatalf("API key response is incomplete: %+v", response)
		}
		return response.Key.ID, response.Secret
	}
	_, readSecret := createKey("Functions read key", []string{"functions.read"})
	_, writeSecret := createKey("Functions write key", []string{"functions.write"})
	readHeaders := map[string]string{"X-Stealth-Key": readSecret}
	writeHeaders := map[string]string{"X-Stealth-Key": writeSecret}

	var functionResponse struct {
		Function struct {
			ID                 string  `json:"id"`
			Runtime            string  `json:"runtime"`
			Entrypoint         string  `json:"entrypoint"`
			ActiveDeploymentID *string `json:"active_deployment_id"`
		} `json:"function"`
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/functions", map[string]any{
		"name":       "hello-functions",
		"runtime":    "node-22",
		"entrypoint": "src/main.js",
		"commands":   "npm test",
	}, http.StatusCreated, &functionResponse)
	functionID := functionResponse.Function.ID
	if functionID == "" || functionResponse.Function.Runtime != "node-22" || functionResponse.Function.Entrypoint != "src/main.js" {
		t.Fatalf("unexpected function response: %+v", functionResponse.Function)
	}
	functionsURL := projectURL + "/functions/" + functionID

	listBody := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/functions", nil, http.StatusOK, readHeaders)
	if !bytes.Contains(listBody, []byte(`"can_manage":false`)) || !bytes.Contains(listBody, []byte(functionID)) {
		t.Fatalf("function read response did not include safe capability metadata: %s", listBody)
	}
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/functions/"+functionID, nil, http.StatusForbidden, writeHeaders)

	variableBody := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodPost, functionsURL+"/variables", map[string]any{
		"key":         "API_TOKEN",
		"value":       "super-secret-function-value",
		"kind":        "secret",
		"is_secret":   true,
		"description": "deployment token",
	}, http.StatusCreated, writeHeaders)
	if bytes.Contains(variableBody, []byte("super-secret-function-value")) || bytes.Contains(variableBody, []byte("ciphertext")) || bytes.Contains(variableBody, []byte(`"secret":`)) {
		t.Fatalf("variable response leaked secret material or a compatibility alias: %s", variableBody)
	}
	var variableResponse struct {
		Variable struct {
			ID       string `json:"id"`
			IsSecret bool   `json:"is_secret"`
			HasValue bool   `json:"has_value"`
		} `json:"variable"`
	}
	if err := json.Unmarshal(variableBody, &variableResponse); err != nil {
		t.Fatal(err)
	}
	if variableResponse.Variable.ID == "" || !variableResponse.Variable.IsSecret || !variableResponse.Variable.HasValue {
		t.Fatalf("unexpected variable metadata: %+v", variableResponse.Variable)
	}
	var ciphertext []byte
	if err := pool.QueryRow(ctx, `SELECT value_ciphertext FROM function_variables WHERE id=$1`, variableResponse.Variable.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) == 0 || bytes.Equal(ciphertext, []byte("super-secret-function-value")) {
		t.Fatalf("variable was not encrypted at rest: %x", ciphertext)
	}
	variableListBody := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodGet, functionsURL+"/variables", nil, http.StatusOK, readHeaders)
	if bytes.Contains(variableListBody, []byte("super-secret-function-value")) || bytes.Contains(variableListBody, []byte("ciphertext")) || !bytes.Contains(variableListBody, []byte(`"has_value":true`)) {
		t.Fatalf("variable list exposed write-only material: %s", variableListBody)
	}
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPatch, functionsURL+"/variables/"+variableResponse.Variable.ID, map[string]any{"kind": "variable", "is_secret": false}, http.StatusBadRequest, writeHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPatch, functionsURL+"/variables/"+variableResponse.Variable.ID, map[string]string{"value": "updated-secret"}, http.StatusOK, writeHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodDelete, functionsURL+"/variables/"+variableResponse.Variable.ID, nil, http.StatusNoContent, writeHeaders)

	archive := []byte("function-source-opaque-bytes")
	deploymentBody := uploadFunctionMultipart(t, newIntegrationClient(t), functionsURL+"/deployments", "source.zip", archive, writeHeaders, http.StatusCreated)
	if bytes.Contains(deploymentBody, []byte("source_path")) || !bytes.Contains(deploymentBody, []byte(`"source_name":"source.zip"`)) {
		t.Fatalf("deployment response exposed private path or omitted source metadata: %s", deploymentBody)
	}
	var deploymentResponse struct {
		Deployment struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"deployment"`
	}
	if err := json.Unmarshal(deploymentBody, &deploymentResponse); err != nil {
		t.Fatal(err)
	}
	if deploymentResponse.Deployment.ID == "" || deploymentResponse.Deployment.Status != "ready" {
		t.Fatalf("unexpected deployment metadata: %+v", deploymentResponse.Deployment)
	}
	deploymentID := deploymentResponse.Deployment.ID
	deploymentListBody := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodGet, functionsURL+"/deployments", nil, http.StatusOK, readHeaders)
	if bytes.Contains(deploymentListBody, []byte("source_path")) || !bytes.Contains(deploymentListBody, []byte(deploymentID)) {
		t.Fatalf("deployment list was not a safe metadata projection: %s", deploymentListBody)
	}
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPost, functionsURL+"/deployments/"+deploymentID+"/activate", nil, http.StatusForbidden, readHeaders)
	activationBody := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodPost, functionsURL+"/deployments/"+deploymentID+"/activate", nil, http.StatusOK, writeHeaders)
	if !bytes.Contains(activationBody, []byte(`"function"`)) || !bytes.Contains(activationBody, []byte(`"deployment"`)) {
		t.Fatalf("activation response did not contain both canonical resources: %s", activationBody)
	}
	functionRepository := repository.New(pool)
	if _, err := functionRepository.AppendFunctionBuildLog(ctx, uuid.Must(uuid.Parse(project.Project.ID)), uuid.Must(uuid.Parse(functionID)), uuid.Must(uuid.Parse(deploymentID)), uuid.Must(uuid.NewV7()), 0, "info", "worker build completed"); err != nil {
		t.Fatal(err)
	}
	var buildLogs struct {
		Logs []struct {
			Sequence int64  `json:"sequence"`
			Level    string `json:"level"`
			Message  string `json:"message"`
		} `json:"logs"`
	}
	requestJSON(t, ownerClient, http.MethodGet, functionsURL+"/deployments/"+deploymentID+"/logs?limit=100", nil, http.StatusOK, &buildLogs)
	if len(buildLogs.Logs) != 1 || buildLogs.Logs[0].Sequence != 1 || buildLogs.Logs[0].Level != "info" || buildLogs.Logs[0].Message != "worker build completed" {
		t.Fatalf("unexpected build logs: %+v", buildLogs.Logs)
	}
	executionBody := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodPost, functionsURL+"/executions", map[string]any{"trigger": "manual", "input": map[string]any{"hello": "world"}}, http.StatusAccepted, writeHeaders)
	if !bytes.Contains(executionBody, []byte(`"status":"accepted"`)) || !bytes.Contains(executionBody, []byte(`"input_json":{"hello":"world"}`)) {
		t.Fatalf("execution enqueue response was unexpected: %s", executionBody)
	}
	var executionResponse struct {
		Execution struct {
			ID string `json:"id"`
		} `json:"execution"`
	}
	if err := json.Unmarshal(executionBody, &executionResponse); err != nil {
		t.Fatal(err)
	}
	if executionResponse.Execution.ID == "" {
		t.Fatalf("execution response did not include an id: %s", executionBody)
	}
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPatch, functionsURL, map[string]any{"execute_permissions": []string{"any"}}, http.StatusOK, writeHeaders)
	anonymousExecution := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodPost, functionsURL+"/executions", map[string]any{"trigger": "public", "input": map[string]any{"anonymous": true}}, http.StatusAccepted, nil)
	if !bytes.Contains(anonymousExecution, []byte(`"status":"accepted"`)) {
		t.Fatalf("anonymous execution was not accepted with any permission: %s", anonymousExecution)
	}
	var anonymousExecutionResponse struct {
		Execution struct {
			ID string `json:"id"`
		} `json:"execution"`
	}
	if err := json.Unmarshal(anonymousExecution, &anonymousExecutionResponse); err != nil {
		t.Fatal(err)
	}
	if _, err := functionRepository.TransitionFunctionExecution(ctx, uuid.Must(uuid.Parse(project.Project.ID)), uuid.Must(uuid.Parse(functionID)), uuid.Must(uuid.Parse(executionResponse.Execution.ID)), "running", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := functionRepository.TransitionFunctionExecutionResult(ctx, uuid.Must(uuid.Parse(project.Project.ID)), uuid.Must(uuid.Parse(functionID)), uuid.Must(uuid.Parse(executionResponse.Execution.ID)), "succeeded", "", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := functionRepository.AppendFunctionExecutionLog(ctx, uuid.Must(uuid.Parse(project.Project.ID)), uuid.Must(uuid.Parse(functionID)), uuid.Must(uuid.Parse(executionResponse.Execution.ID)), uuid.Must(uuid.NewV7()), 0, "info", "worker execution completed"); err != nil {
		t.Fatal(err)
	}
	var executionLogs struct {
		Logs []struct {
			Sequence int64  `json:"sequence"`
			Level    string `json:"level"`
			Message  string `json:"message"`
		} `json:"logs"`
	}
	requestJSON(t, ownerClient, http.MethodGet, functionsURL+"/executions/"+executionResponse.Execution.ID+"/logs?limit=100", nil, http.StatusOK, &executionLogs)
	if len(executionLogs.Logs) != 1 || executionLogs.Logs[0].Sequence != 1 || executionLogs.Logs[0].Level != "info" || executionLogs.Logs[0].Message != "worker execution completed" {
		t.Fatalf("unexpected execution logs: %+v", executionLogs.Logs)
	}
	if _, err := functionRepository.TransitionFunctionExecution(ctx, uuid.Must(uuid.Parse(project.Project.ID)), uuid.Must(uuid.Parse(functionID)), uuid.Must(uuid.Parse(anonymousExecutionResponse.Execution.ID)), "failed", "test failure"); err != nil {
		t.Fatal(err)
	}
	var usage struct {
		Invocations int64
		Failures    int64
	}
	if err := pool.QueryRow(ctx, `SELECT function_invocation_count,function_failure_count FROM project_usage_daily WHERE project_id=$1 AND usage_date=CURRENT_DATE`, project.Project.ID).Scan(&usage.Invocations, &usage.Failures); err != nil {
		t.Fatal(err)
	}
	if usage.Invocations < 2 || usage.Failures < 1 {
		t.Fatalf("function usage counters = %+v, want at least two invocations and one failure", usage)
	}
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodDelete, functionsURL+"/deployments/"+deploymentID, nil, http.StatusConflict, writeHeaders)

	oversized := bytes.Repeat([]byte("x"), 33)
	uploadFunctionMultipart(t, newIntegrationClient(t), functionsURL+"/deployments", "too-large.zip", oversized, writeHeaders, http.StatusRequestEntityTooLarge)
	var deploymentRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM function_deployments WHERE function_id=$1`, functionID).Scan(&deploymentRows); err != nil {
		t.Fatal(err)
	}
	if deploymentRows != 1 {
		t.Fatalf("oversized upload created %d deployment rows, want 1", deploymentRows)
	}

	outsiderClient := newIntegrationClient(t)
	outsiderID := uuid.Must(uuid.NewV7())
	var outsider struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, outsiderClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("functions-outsider-%s@example.test", outsiderID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &outsider)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id=$1 OR actor_account_id=$2`, outsider.Organization.ID, outsider.Account.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, outsider.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, outsider.Account.ID)
	})
	requestJSON(t, outsiderClient, http.MethodGet, functionsURL, nil, http.StatusNotFound, nil)

	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodDelete, functionsURL, nil, http.StatusNoContent, writeHeaders)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, functionsURL, nil, http.StatusNotFound, readHeaders)
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organization_id=$1 AND action IN ('function.create','function_deployment.create','function_deployment.activate','function.delete')`, registration.Organization.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 4 {
		t.Fatalf("function audit count = %d, want 4", auditCount)
	}

	var files []string
	if err := filepath.Walk(storageRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("function delete left artifact files: %v", files)
	}
}

func uploadFunctionMultipart(t *testing.T, client *http.Client, url, filename string, content []byte, headers map[string]string, expectedStatus int) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("source", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	for name, value := range headers {
		request.Header.Set(name, value)
	}
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
		t.Fatalf("function multipart upload expected %d, got %d: %s", expectedStatus, response.StatusCode, result)
	}
	return result
}
