package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	appdb "github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/queue"
	"gorm.io/gorm"
)

type integrationHTTPResult struct {
	status  int
	data    map[string]any
	err     error
	traceID string
}

func TestRechargeConcurrencyExternalHTTPFlow(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("RECHARGE_TEST_DATABASE_URL"))
	redisURL := strings.TrimSpace(os.Getenv("RECHARGE_TEST_REDIS_URL"))
	if databaseURL == "" || redisURL == "" {
		t.Skip("set RECHARGE_TEST_DATABASE_URL and RECHARGE_TEST_REDIS_URL to run the real PostgreSQL/Redis HTTP flow")
	}

	database, err := appdb.Open(databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	if err := appdb.Migrate(database); err != nil {
		t.Fatalf("migrate integration database: %v", err)
	}

	queueName := "recharge:integration:" + strings.ReplaceAll(uuid.NewString(), "-", "")
	queueClient, err := queue.New(redisURL, queueName)
	if err != nil {
		t.Fatalf("open integration redis: %v", err)
	}
	ctx := context.Background()
	if err := queueClient.Ping(ctx); err != nil {
		_ = queueClient.Close()
		t.Fatalf("ping integration redis: %v", err)
	}
	if err := queueClient.Redis.Del(ctx, queueName, queueName+":admission").Err(); err != nil {
		_ = queueClient.Close()
		t.Fatalf("clear integration redis keys: %v", err)
	}

	server := &Server{
		DB: database,
		Q:  queueClient,
		Cfg: config.Config{
			SessionEncryptionKey:     "integration-session-key",
			WorkerAPIToken:           "integration-worker-token",
			DefaultRechargeMode:      "dry_run",
			TaskQueuedTimeoutSeconds: 600,
			TaskLeaseTimeoutSeconds:  60,
			CORSOrigins:              []string{"*"},
		},
	}
	httpServer := httptest.NewServer(NewRouter(server))
	defer httpServer.Close()
	defer queueClient.Close()
	defer func() {
		sqlDB, dbErr := database.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	}()

	configOriginal, configExisted, configErr := integrationConfigValue(database, "max_concurrent_activations")
	if configErr != nil {
		t.Fatalf("read integration concurrency config: %v", configErr)
	}
	if err := database.Where("key = ?", "max_concurrent_activations").Assign(models.AppConfig{Value: "1"}).FirstOrCreate(&models.AppConfig{Key: "max_concurrent_activations"}).Error; err != nil {
		t.Fatalf("set integration concurrency limit: %v", err)
	}
	defer func() {
		if configExisted {
			_ = database.Model(&models.AppConfig{}).Where("key = ?", "max_concurrent_activations").Update("value", configOriginal).Error
		} else {
			_ = database.Where("key = ?", "max_concurrent_activations").Delete(&models.AppConfig{}).Error
		}
	}()

	plan := models.Plan{}
	if err := database.Where("code = ?", "plus").First(&plan).Error; err != nil {
		t.Fatalf("load integration plan: %v", err)
	}
	cdkIDs := make([]string, 0, 5)
	cdkCodes := make([]string, 0, 5)
	for index := 0; index < 5; index++ {
		id := "integration-cdk-" + strings.ReplaceAll(uuid.NewString(), "-", "")
		code := fmt.Sprintf("E2E-%d-%s", index, strings.ReplaceAll(uuid.NewString(), "-", ""))
		if err := database.Create(&models.CDK{ID: id, Code: code, PlanID: plan.ID, PlanType: plan.Code, Type: models.CDKTypeSelf, Status: models.CDKAvailable}).Error; err != nil {
			t.Fatalf("create integration CDK %d: %v", index, err)
		}
		cdkIDs = append(cdkIDs, id)
		cdkCodes = append(cdkCodes, code)
	}
	taskIDs := make([]string, 0, 4)
	defer func() {
		if len(taskIDs) > 0 {
			_ = database.Where("task_id IN ?", taskIDs).Delete(&models.BillingRecord{}).Error
			_ = database.Where("task_id IN ?", taskIDs).Delete(&models.EmailDelivery{}).Error
			_ = database.Where("job_key IN (SELECT job_key FROM recharge_tasks WHERE id IN ?)", taskIDs).Delete(&models.RuntimeLog{}).Error
			_ = database.Where("id IN ?", taskIDs).Delete(&models.RechargeTask{}).Error
		}
		if len(cdkIDs) > 0 {
			_ = database.Where("id IN ?", cdkIDs).Delete(&models.CDK{}).Error
		}
		_ = queueClient.Redis.Del(ctx, queueName, queueName+":admission").Err()
	}()

	session := integrationAccessToken()
	first := integrationHTTPResult{}
	second := integrationHTTPResult{}
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go func() {
		defer waitGroup.Done()
		first.status, first.data, first.err, first.traceID = integrationJSONRequest(http.MethodPost, httpServer.URL+"/api/v1/recharge/tasks", map[string]any{"code": cdkCodes[0], "session": session, "mode": "dry_run"}, "trace-integration-a", "")
	}()
	go func() {
		defer waitGroup.Done()
		second.status, second.data, second.err, second.traceID = integrationJSONRequest(http.MethodPost, httpServer.URL+"/api/v1/recharge/tasks", map[string]any{"code": cdkCodes[1], "session": session, "mode": "dry_run"}, "trace-integration-b", "")
	}()
	waitGroup.Wait()
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent HTTP request errors: first=%v second=%v", first.err, second.err)
	}
	accepted, rejected := first, second
	if first.status == http.StatusTooManyRequests && second.status == http.StatusAccepted {
		accepted, rejected = second, first
	}
	if accepted.status != http.StatusAccepted || rejected.status != http.StatusTooManyRequests {
		t.Fatalf("concurrent statuses = %d and %d, want one 202 and one 429; first=%#v second=%#v", first.status, second.status, first.data, second.data)
	}
	acceptedTask, ok := accepted.data["task"].(map[string]any)
	if !ok {
		t.Fatalf("accepted response task = %#v", accepted.data)
	}
	acceptedTaskID, ok := acceptedTask["id"].(string)
	if !ok || acceptedTaskID == "" {
		t.Fatalf("accepted task id = %#v", acceptedTask)
	}
	taskIDs = append(taskIDs, acceptedTaskID)
	if rejected.data["success"] != false {
		t.Fatalf("rejected response = %#v", rejected.data)
	}
	if got := queueClient.Redis.ZCard(ctx, queueName+":admission").Val(); got != 1 {
		t.Fatalf("Redis admission slots after first request = %d, want 1", got)
	}

	status, publicTask, err, _ := integrationJSONRequest(http.MethodGet, httpServer.URL+"/api/v1/recharge/tasks/"+acceptedTaskID, nil, "trace-integration-public", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("public task request status=%d err=%v data=%#v", status, err, publicTask)
	}
	publicTaskData, _ := publicTask["task"].(map[string]any)
	if _, exposed := publicTaskData["traceId"]; exposed {
		t.Fatalf("public task exposed traceId: %#v", publicTaskData)
	}

	status, claim, err, _ := integrationJSONRequest(http.MethodPost, httpServer.URL+"/api/v1/internal/tasks/"+acceptedTaskID+"/claim", map[string]any{"workerId": "integration-worker"}, "trace-integration-worker", "integration-worker-token")
	if err != nil || status != http.StatusOK {
		t.Fatalf("claim status=%d err=%v data=%#v", status, err, claim)
	}
	leaseToken, ok := claim["leaseToken"].(string)
	if !ok || leaseToken == "" {
		t.Fatalf("claim lease token = %#v", claim)
	}
	status, _, err, _ = integrationJSONRequest(http.MethodPost, httpServer.URL+"/api/v1/internal/tasks/"+acceptedTaskID+"/heartbeat", map[string]any{"workerId": "integration-worker", "leaseToken": leaseToken, "progress": 35, "message": "integration heartbeat"}, "trace-integration-worker", "integration-worker-token")
	if err != nil || status != http.StatusOK {
		t.Fatalf("heartbeat status=%d err=%v", status, err)
	}
	if got := queueClient.Redis.ZCard(ctx, queueName+":admission").Val(); got != 1 {
		t.Fatalf("Redis admission slots after heartbeat = %d, want 1", got)
	}
	status, _, err, _ = integrationJSONRequest(http.MethodPatch, httpServer.URL+"/api/v1/internal/tasks/"+acceptedTaskID, map[string]any{"status": models.TaskSucceeded, "progress": 100, "message": "integration succeeded", "workerId": "integration-worker", "leaseToken": leaseToken}, "trace-integration-worker", "integration-worker-token")
	if err != nil || status != http.StatusOK {
		t.Fatalf("terminal update status=%d err=%v", status, err)
	}

	var completed models.RechargeTask
	if err := database.First(&completed, "id = ?", acceptedTaskID).Error; err != nil {
		t.Fatalf("load completed task: %v", err)
	}
	if completed.Status != models.TaskSucceeded || completed.AdmissionToken != "" {
		t.Fatalf("completed task state = status=%s admission=%q", completed.Status, completed.AdmissionToken)
	}
	var usedCDK models.CDK
	if err := database.First(&usedCDK, "id = ?", completedCDKID(database, acceptedTaskID)).Error; err != nil {
		// The fallback lookup below keeps the assertion independent of which
		// concurrent request won while still checking the accepted CDK row.
		if err := database.Where("used_by_task_id = ? OR status = ?", acceptedTaskID, models.CDKUsed).First(&usedCDK).Error; err != nil {
			t.Fatalf("load used CDK: %v", err)
		}
	}
	if usedCDK.Status != models.CDKUsed {
		t.Fatalf("used CDK status = %s", usedCDK.Status)
	}
	if got := queueClient.Redis.ZCard(ctx, queueName+":admission").Val(); got != 0 {
		t.Fatalf("Redis admission slots after completion = %d, want 0", got)
	}

	queuedTaskID := createIntegrationTask(t, httpServer.URL, cdkCodes[2], session, "trace-integration-queued")
	taskIDs = append(taskIDs, queuedTaskID)
	past := time.Now().Add(-time.Minute)
	if err := database.Model(&models.RechargeTask{}).Where("id = ?", queuedTaskID).Updates(map[string]any{"queue_deadline_at": past}).Error; err != nil {
		t.Fatalf("expire queued task in test: %v", err)
	}
	report, err := server.RecoverStaleRechargeTasks()
	if err != nil || report.QueuedExpired != 1 {
		t.Fatalf("queued recovery report=%+v err=%v", report, err)
	}
	assertIntegrationTaskState(t, database, queuedTaskID, models.TaskFailed, "queue_timeout")
	if got := queueClient.Redis.ZCard(ctx, queueName+":admission").Val(); got != 0 {
		t.Fatalf("Redis admission slots after queued recovery = %d, want 0", got)
	}

	leaseTaskID := createIntegrationTask(t, httpServer.URL, cdkCodes[3], session, "trace-integration-lease")
	taskIDs = append(taskIDs, leaseTaskID)
	status, leaseClaim, err, _ := integrationJSONRequest(http.MethodPost, httpServer.URL+"/api/v1/internal/tasks/"+leaseTaskID+"/claim", map[string]any{"workerId": "integration-old-worker"}, "trace-integration-lease", "integration-worker-token")
	if err != nil || status != http.StatusOK {
		t.Fatalf("lease claim status=%d err=%v data=%#v", status, err, leaseClaim)
	}
	oldLease, _ := leaseClaim["leaseToken"].(string)
	if err := database.Model(&models.RechargeTask{}).Where("id = ?", leaseTaskID).Updates(map[string]any{"lease_expires_at": past}).Error; err != nil {
		t.Fatalf("expire running task in test: %v", err)
	}
	report, err = server.RecoverStaleRechargeTasks()
	if err != nil || report.RunningExpired != 1 {
		t.Fatalf("lease recovery report=%+v err=%v", report, err)
	}
	assertIntegrationTaskState(t, database, leaseTaskID, models.TaskFailed, "worker_lease_timeout")
	status, staleResponse, err, _ := integrationJSONRequest(http.MethodPatch, httpServer.URL+"/api/v1/internal/tasks/"+leaseTaskID, map[string]any{"status": models.TaskSucceeded, "progress": 100, "workerId": "integration-old-worker", "leaseToken": oldLease}, "trace-integration-lease", "integration-worker-token")
	if err != nil || status != http.StatusConflict || staleResponse["code"] != "task_lease_lost" {
		t.Fatalf("stale worker update status=%d err=%v data=%#v", status, err, staleResponse)
	}
	if got := queueClient.Redis.ZCard(ctx, queueName+":admission").Val(); got != 0 {
		t.Fatalf("Redis admission slots after lease recovery = %d, want 0", got)
	}

	legacyStatus, legacyResponse, err, _ := integrationJSONRequest(http.MethodPost, httpServer.URL+"/api/run-process", map[string]any{"cdk": cdkCodes[4], "session": session, "mode": "dry_run"}, "trace-integration-legacy", "")
	if err != nil || legacyStatus != http.StatusOK || legacyResponse["success"] != true {
		t.Fatalf("legacy create task status=%d err=%v data=%#v", legacyStatus, err, legacyResponse)
	}
	legacyTaskID, _ := legacyResponse["taskId"].(string)
	if legacyTaskID == "" {
		t.Fatalf("legacy task response = %#v", legacyResponse)
	}
	taskIDs = append(taskIDs, legacyTaskID)
	if err := database.Model(&models.RechargeTask{}).Where("id = ?", legacyTaskID).Updates(map[string]any{"queue_deadline_at": past}).Error; err != nil {
		t.Fatalf("expire legacy queued task in test: %v", err)
	}
	report, err = server.RecoverStaleRechargeTasks()
	if err != nil || report.QueuedExpired != 1 {
		t.Fatalf("legacy queued recovery report=%+v err=%v", report, err)
	}
	assertIntegrationTaskState(t, database, legacyTaskID, models.TaskFailed, "queue_timeout")
}

func integrationAccessToken() string {
	encode := func(value any) string {
		raw, _ := json.Marshal(value)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	header := map[string]any{"typ": "JWT", "alg": "RS256"}
	payload := map[string]any{
		"iss":                         "https://auth.openai.com",
		"aud":                         []string{"https://api.openai.com/v1"},
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "integration-account", "chatgpt_user_id": "integration-user"},
		"scp":                         []string{"model.request"},
		"exp":                         time.Now().Add(time.Hour).Unix(),
	}
	return encode(header) + "." + encode(payload) + ".integration-signature"
}

func integrationJSONRequest(method, url string, body any, traceID, workerToken string) (int, map[string]any, error, string) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err, traceID
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		return 0, nil, err, traceID
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Trace-ID", traceID)
	if workerToken != "" {
		request.Header.Set("X-Worker-Token", workerToken)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return 0, nil, err, traceID
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, nil, err, response.Header.Get("X-Trace-ID")
	}
	data := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &data); err != nil {
			return response.StatusCode, nil, fmt.Errorf("decode response %s: %w", raw, err), response.Header.Get("X-Trace-ID")
		}
	}
	return response.StatusCode, data, nil, response.Header.Get("X-Trace-ID")
}

func createIntegrationTask(t *testing.T, baseURL, cdkCode, session, traceID string) string {
	t.Helper()
	status, data, err, _ := integrationJSONRequest(http.MethodPost, baseURL+"/api/v1/recharge/tasks", map[string]any{"code": cdkCode, "session": session, "mode": "dry_run"}, traceID, "")
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("create integration task status=%d err=%v data=%#v", status, err, data)
	}
	task, _ := data["task"].(map[string]any)
	taskID, _ := task["id"].(string)
	if taskID == "" {
		t.Fatalf("create integration task response = %#v", data)
	}
	return taskID
}

func assertIntegrationTaskState(t *testing.T, database *gorm.DB, taskID, status, errorCode string) {
	t.Helper()
	var task models.RechargeTask
	if err := database.First(&task, "id = ?", taskID).Error; err != nil {
		t.Fatalf("load integration task %s: %v", taskID, err)
	}
	if task.Status != status || task.ErrorCode != errorCode || task.AdmissionToken != "" {
		t.Fatalf("task %s state = status=%s error=%s admission=%q", taskID, task.Status, task.ErrorCode, task.AdmissionToken)
	}
	if task.CDKID == nil {
		t.Fatalf("task %s has no CDK", taskID)
	}
	var cdk models.CDK
	if err := database.First(&cdk, "id = ?", *task.CDKID).Error; err != nil {
		t.Fatalf("load integration CDK: %v", err)
	}
	if cdk.Status != models.CDKAvailable {
		t.Fatalf("recovered CDK status = %s, want available", cdk.Status)
	}
}

func integrationConfigValue(database *gorm.DB, key string) (string, bool, error) {
	var row models.AppConfig
	err := database.Where("key = ?", key).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return "", false, nil
	}
	return row.Value, err == nil, err
}

func completedCDKID(database *gorm.DB, taskID string) string {
	var task models.RechargeTask
	if err := database.Select("cdk_id").First(&task, "id = ?", taskID).Error; err != nil || task.CDKID == nil {
		return ""
	}
	return *task.CDKID
}
