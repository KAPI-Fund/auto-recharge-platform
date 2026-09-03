package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
)

func TestValidateTaskLeaseRejectsStaleOrForeignWorker(t *testing.T) {
	now := time.Now()
	expires := now.Add(time.Minute)
	task := models.RechargeTask{
		Status:           models.TaskRunning,
		WorkerID:         "worker-a",
		WorkerLeaseToken: "lease-a",
		LeaseExpiresAt:   &expires,
	}
	if err := validateTaskLease(&task, "worker-a", "lease-a", now); err != nil {
		t.Fatalf("valid lease rejected: %v", err)
	}
	if err := validateTaskLease(&task, "worker-b", "lease-a", now); !errors.Is(err, errTaskLeaseLost) {
		t.Fatalf("foreign worker error = %v, want lease lost", err)
	}
	if err := validateTaskLease(&task, "worker-a", "lease-a", expires); !errors.Is(err, errTaskLeaseLost) {
		t.Fatalf("expired lease error = %v, want lease lost", err)
	}
	task.Status = models.TaskFailed
	if err := validateTaskLease(&task, "worker-a", "lease-a", now); !errors.Is(err, errTaskLeaseLost) {
		t.Fatalf("terminal lease error = %v, want lease lost", err)
	}
}

func TestClaimTaskAtomicallyAssignsLease(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	leaseTimeout := "5"
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "app_configs"`)+`.*`).
		WithArgs("recharge_task_lease_timeout_seconds", 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).AddRow("recharge_task_lease_timeout_seconds", leaseTimeout))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "recharge_tasks"`)+`.*FOR UPDATE`).
		WithArgs("task-claim", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status", "trace_id", "progress", "worker_id", "worker_lease_token"}).
			AddRow("task-claim", models.TaskQueued, "trace-claim", 1, "", ""))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "recharge_tasks"`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	gin.SetMode(gin.TestMode)
	server := &Server{DB: database, Cfg: config.Config{TaskLeaseTimeoutSeconds: 5}}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/internal/tasks/task-claim/claim", bytes.NewBufferString(`{"workerId":"worker-a"}`))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Params = gin.Params{{Key: "id", Value: "task-claim"}}
	server.claimTask(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["claimed"] != true || response["status"] != models.TaskRunning || response["workerId"] != "worker-a" {
		t.Fatalf("claim response = %#v", response)
	}
	if token, ok := response["leaseToken"].(string); !ok || token == "" {
		t.Fatalf("claim response missing lease token: %#v", response)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestApplyTaskUpdateRejectsOldLeaseBeforeWriting(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	expires := time.Now().Add(time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "recharge_tasks"`)+`.*FOR UPDATE`).
		WithArgs("task-stale", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status", "worker_id", "worker_lease_token", "lease_expires_at", "progress"}).
			AddRow("task-stale", models.TaskRunning, "worker-new", "lease-new", expires, 40))
	mock.ExpectRollback()

	workerID := "worker-old"
	leaseToken := "lease-old"
	_, _, err := (&Server{DB: database}).applyTaskUpdate("task-stale", taskUpdateInput{
		Status:     models.TaskSucceeded,
		WorkerID:   &workerID,
		LeaseToken: &leaseToken,
	})
	if !errors.Is(err, errTaskLeaseLost) {
		t.Fatalf("stale update error = %v, want lease lost", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestHeartbeatTaskRenewsOnlyTheCurrentLease(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "app_configs"`)+`.*`).
		WithArgs("recharge_task_lease_timeout_seconds", 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).AddRow("recharge_task_lease_timeout_seconds", "5"))
	mock.ExpectBegin()
	expires := time.Now().Add(time.Minute)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "recharge_tasks"`)+`.*FOR UPDATE`).
		WithArgs("task-heartbeat", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status", "trace_id", "progress", "worker_id", "worker_lease_token", "lease_expires_at"}).
			AddRow("task-heartbeat", models.TaskRunning, "trace-heartbeat", 25, "worker-a", "lease-a", expires))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "recharge_tasks"`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	gin.SetMode(gin.TestMode)
	server := &Server{DB: database, Cfg: config.Config{TaskLeaseTimeoutSeconds: 5}}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/internal/tasks/task-heartbeat/heartbeat", bytes.NewBufferString(`{"workerId":"worker-a","leaseToken":"lease-a","progress":35,"message":"继续执行"}`))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Params = gin.Params{{Key: "id", Value: "task-heartbeat"}}
	server.heartbeatTask(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["ok"] != true || response["claimed"] != true || response["traceId"] != "trace-heartbeat" {
		t.Fatalf("heartbeat response = %#v", response)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestExpireRechargeTaskReleasesOwnedCDKAndWritesTraceLog(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	cdkID := "cdk-recovery"
	task := &models.RechargeTask{ID: "task-recovery", JobKey: "job-recovery", TraceID: "trace-recovery", CDKID: &cdkID, Status: models.TaskQueued}
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "recharge_tasks"`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "card_allocations"`)+`.*WHERE payment_task_id = \$\d+`).
		WithArgs("task-recovery", "CREATING", "ASSIGNED", "IN_USE", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "cdks"`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO "runtime_logs"`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	changed := false
	err := database.Transaction(func(tx *gorm.DB) error {
		var err error
		changed, err = (&Server{}).expireRechargeTaskTx(tx, task, "queue_timeout", "任务排队超时，请稍后重试", time.Now())
		return err
	})
	if err != nil || !changed {
		t.Fatalf("expire task changed=%t err=%v", changed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
