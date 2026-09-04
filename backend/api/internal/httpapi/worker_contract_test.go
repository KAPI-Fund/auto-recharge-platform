package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"
)

func TestWorkerTaskSecretReturnsJobKeyAndTraceContractWithoutCDK(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	secret, err := security.Encrypt(`{"access_token":"tok_worker_contract"}`, "session-contract-key")
	if err != nil {
		t.Fatalf("encrypt session: %v", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "recharge_tasks"`)+`.*`).
		WithArgs("task_contract", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "job_key", "trace_id", "session_ciphertext", "mode", "cdk_id", "plan_id", "cdk_code"}).
			AddRow("task_contract", "job_contract", "trace_contract", secret, "browser", nil, "", "[checkout-debug]"))

	gin.SetMode(gin.TestMode)
	server := &Server{DB: db, Cfg: config.Config{SessionEncryptionKey: "session-contract-key"}}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/internal/tasks/task_contract/secret", nil)
	context.Params = gin.Params{{Key: "id", Value: "task_contract"}}
	server.taskSecret(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["jobKey"] != "job_contract" || response["traceId"] != "trace_contract" || response["trace_id"] != "trace_contract" {
		t.Fatalf("worker identity contract = %#v", response)
	}
	if response["token"] != "tok_worker_contract" {
		t.Fatalf("access token extraction = %#v", response["token"])
	}
	if response["cdkCode"] != "[checkout-debug]" || response["planType"] != "plus" || response["planName"] != "chatgptplusplan" {
		t.Fatalf("checkout debug secret contract = %#v", response)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestWorkerTaskSecretNormalizesLegacyPlanNameOverride(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	secret, err := security.Encrypt(`{"access_token":"tok_worker_plan_alias"}`, "session-contract-key")
	if err != nil {
		t.Fatalf("encrypt session: %v", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "recharge_tasks"`)+`.*`).
		WithArgs("task_plan_alias", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "job_key", "trace_id", "session_ciphertext", "mode", "cdk_id", "plan_id", "cdk_code", "plan_name_override"}).
			AddRow("task_plan_alias", "job_plan_alias", "trace_plan_alias", secret, "browser", nil, "", "[checkout-debug]", "pro_20x"))

	gin.SetMode(gin.TestMode)
	server := &Server{DB: db, Cfg: config.Config{SessionEncryptionKey: "session-contract-key"}}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/internal/tasks/task_plan_alias/secret", nil)
	context.Params = gin.Params{{Key: "id", Value: "task_plan_alias"}}
	server.taskSecret(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["planName"] != "chatgptpro" {
		t.Fatalf("normalized plan name = %#v, want chatgptpro", response["planName"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestWorkerTaskSecretReturnsIndiaGoContract(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	secret, err := security.Encrypt(`{"access_token":"tok_worker_go"}`, "session-contract-key")
	if err != nil {
		t.Fatalf("encrypt session: %v", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "recharge_tasks"`)+`.*`).
		WithArgs("task_go", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "job_key", "trace_id", "session_ciphertext", "mode", "cdk_id", "plan_id", "cdk_code", "payment_region"}).
			AddRow("task_go", "job_go", "trace_go", secret, "browser", nil, "plan_go", "[go-checkout]", "IN"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "plans"`)+`.*`).
		WithArgs("plan_go", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "code", "provider_plan_name", "country", "currency"}).
			AddRow("plan_go", "go", "chatgptgoplan", "IN", "CNY"))

	gin.SetMode(gin.TestMode)
	server := &Server{DB: db, Cfg: config.Config{SessionEncryptionKey: "session-contract-key"}}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/internal/tasks/task_go/secret", nil)
	context.Params = gin.Params{{Key: "id", Value: "task_go"}}
	server.taskSecret(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for key, want := range map[string]any{
		"planType":      "go",
		"planName":      "chatgptgoplan",
		"region":        "IN",
		"paymentRegion": "IN",
		"currency":      "INR",
	} {
		if response[key] != want {
			t.Fatalf("%s = %#v, want %#v; response=%#v", key, response[key], want, response)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
