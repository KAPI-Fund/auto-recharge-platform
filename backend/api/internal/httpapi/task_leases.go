package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errTaskLeaseHeld    = errors.New("task lease is held by another worker")
	errTaskLeaseLost    = errors.New("task worker lease is no longer valid")
	errTaskLeaseInput   = errors.New("task worker lease credentials are required")
	errTaskNotClaimable = errors.New("task is not claimable")
)

type taskLeaseResult struct {
	TaskID         string
	Status         string
	WorkerID       string
	LeaseToken     string
	TraceID        string
	Claimed        bool
	Terminal       bool
	LeaseTimeout   time.Duration
	HeartbeatAt    *time.Time
	LeaseExpiresAt *time.Time
}

func (s *Server) taskQueuedTimeout() time.Duration {
	seconds := s.Cfg.TaskQueuedTimeoutSeconds
	if configured, err := strconv.Atoi(strings.TrimSpace(s.configValue("recharge_queued_timeout_seconds", ""))); err == nil && configured > 0 {
		seconds = configured
	}
	if seconds <= 0 {
		seconds = 600
	}
	return time.Duration(seconds) * time.Second
}

func (s *Server) taskLeaseTimeout() time.Duration {
	seconds := s.Cfg.TaskLeaseTimeoutSeconds
	if configured, err := strconv.Atoi(strings.TrimSpace(s.configValue("recharge_task_lease_timeout_seconds", ""))); err == nil && configured > 0 {
		seconds = configured
	}
	if seconds <= 0 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}

func hasTaskLeaseCredentials(workerID, leaseToken string) bool {
	return strings.TrimSpace(workerID) != "" || strings.TrimSpace(leaseToken) != ""
}

func taskLeaseHeaders(c *gin.Context) (string, string) {
	return strings.TrimSpace(c.GetHeader("X-Worker-ID")), strings.TrimSpace(c.GetHeader("X-Worker-Lease-Token"))
}

func validateTaskLeaseHeaders(c *gin.Context, task *models.RechargeTask) error {
	workerID, leaseToken := taskLeaseHeaders(c)
	if !hasTaskLeaseCredentials(workerID, leaseToken) {
		return nil
	}
	return validateTaskLease(task, workerID, leaseToken, time.Now())
}

func validateTaskLease(task *models.RechargeTask, workerID, leaseToken string, now time.Time) error {
	workerID = strings.TrimSpace(workerID)
	leaseToken = strings.TrimSpace(leaseToken)
	if workerID == "" || leaseToken == "" {
		return errTaskLeaseInput
	}
	if task.WorkerID != workerID || task.WorkerLeaseToken != leaseToken {
		return errTaskLeaseLost
	}
	if task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(now) {
		return errTaskLeaseLost
	}
	if isTerminal(task.Status) {
		return errTaskLeaseLost
	}
	return nil
}

func taskLeasePayload(result taskLeaseResult) gin.H {
	payload := gin.H{
		"ok":       true,
		"taskId":   result.TaskID,
		"status":   result.Status,
		"workerId": result.WorkerID,
		"traceId":  result.TraceID,
		"trace_id": result.TraceID,
		"claimed":  result.Claimed,
		"terminal": result.Terminal,
	}
	if result.LeaseTimeout > 0 {
		payload["leaseTimeoutSeconds"] = int(result.LeaseTimeout / time.Second)
	}
	if result.LeaseToken != "" {
		payload["leaseToken"] = result.LeaseToken
	}
	if result.HeartbeatAt != nil {
		payload["heartbeatAt"] = result.HeartbeatAt
	}
	if result.LeaseExpiresAt != nil {
		payload["leaseExpiresAt"] = result.LeaseExpiresAt
	}
	return payload
}

func taskLeaseError(c *gin.Context, status int, code string, err error) {
	traceID := requestTraceID(c)
	c.Abort()
	c.JSON(status, gin.H{
		"success":  false,
		"ok":       false,
		"code":     code,
		"message":  err.Error(),
		"traceId":  traceID,
		"trace_id": traceID,
	})
}

func (s *Server) claimTask(c *gin.Context) {
	var input struct {
		WorkerID string `json:"workerId"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.WorkerID) == "" {
		fail(c, http.StatusBadRequest, "Worker ID 不能为空")
		return
	}
	workerID := strings.TrimSpace(input.WorkerID)
	now := time.Now()
	result := taskLeaseResult{TaskID: strings.TrimSpace(c.Param("id")), WorkerID: workerID, LeaseTimeout: s.taskLeaseTimeout()}
	admissionToken := ""
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var task models.RechargeTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, "id = ?", result.TaskID).Error; err != nil {
			return err
		}
		result.Status = task.Status
		result.TraceID = task.TraceID
		admissionToken = strings.TrimSpace(task.AdmissionToken)
		if isTerminal(task.Status) {
			result.Terminal = true
			return nil
		}
		if task.Status == models.TaskQueued && task.QueueDeadlineAt != nil && !task.QueueDeadlineAt.After(now) {
			_, err := s.expireRechargeTaskTx(tx, &task, "queue_timeout", "任务排队超时，请稍后重试", now)
			if err != nil {
				return err
			}
			result.Status = models.TaskFailed
			result.Terminal = true
			return nil
		}
		if task.Status == models.TaskRunning && taskLeaseActive(task, now) {
			if task.WorkerID != workerID || strings.TrimSpace(task.WorkerLeaseToken) == "" {
				return errTaskLeaseHeld
			}
			// A retried claim from the same worker is idempotent. Renewing here
			// also covers a response lost after the first successful claim.
			expires := now.Add(s.taskLeaseTimeout())
			if err := tx.Model(&task).Updates(map[string]any{"heartbeat_at": now, "lease_expires_at": expires}).Error; err != nil {
				return err
			}
			task.HeartbeatAt = &now
			task.LeaseExpiresAt = &expires
			result.Status = task.Status
			result.Claimed = true
			result.LeaseToken = task.WorkerLeaseToken
			result.HeartbeatAt = &now
			result.LeaseExpiresAt = &expires
			return nil
		}
		if task.Status != models.TaskQueued && task.Status != models.TaskRunning {
			return errTaskNotClaimable
		}

		leaseToken := db.NewID("lease")
		expires := now.Add(s.taskLeaseTimeout())
		updates := map[string]any{
			"status":             models.TaskRunning,
			"worker_id":          workerID,
			"worker_lease_token": leaseToken,
			"heartbeat_at":       now,
			"lease_expires_at":   expires,
			"progress":           maxInt(5, task.Progress),
			"message":            "Worker 已接管任务",
		}
		if task.StartedAt == nil {
			updates["started_at"] = now
		}
		if err := tx.Model(&task).Updates(updates).Error; err != nil {
			return err
		}
		task.Status = models.TaskRunning
		task.WorkerID = workerID
		task.WorkerLeaseToken = leaseToken
		task.HeartbeatAt = &now
		task.LeaseExpiresAt = &expires
		task.Progress = maxInt(5, task.Progress)
		task.Message = "Worker 已接管任务"
		result.Status = task.Status
		result.Claimed = true
		result.LeaseToken = leaseToken
		result.HeartbeatAt = &now
		result.LeaseExpiresAt = &expires
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			fail(c, http.StatusNotFound, "任务不存在")
		case errors.Is(err, errTaskLeaseHeld):
			taskLeaseError(c, http.StatusConflict, "task_lease_held", err)
		case errors.Is(err, errTaskLeaseInput), errors.Is(err, errTaskLeaseLost):
			taskLeaseError(c, http.StatusConflict, "task_lease_lost", err)
		case errors.Is(err, errTaskNotClaimable):
			taskLeaseError(c, http.StatusConflict, "task_not_claimable", err)
		default:
			publicFail(c, http.StatusInternalServerError, "任务租约创建失败")
		}
		return
	}
	if result.Terminal && admissionToken != "" {
		s.releaseAdmission(admissionToken)
	}
	c.JSON(http.StatusOK, taskLeasePayload(result))
}

func taskLeaseActive(task models.RechargeTask, now time.Time) bool {
	return task.LeaseExpiresAt != nil && task.LeaseExpiresAt.After(now)
}

func (s *Server) heartbeatTask(c *gin.Context) {
	var input struct {
		WorkerID   string  `json:"workerId"`
		LeaseToken string  `json:"leaseToken"`
		Progress   *int    `json:"progress"`
		Message    *string `json:"message"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	workerID := strings.TrimSpace(input.WorkerID)
	leaseToken := strings.TrimSpace(input.LeaseToken)
	if workerID == "" || leaseToken == "" {
		taskLeaseError(c, http.StatusBadRequest, "task_lease_required", errTaskLeaseInput)
		return
	}
	now := time.Now()
	result := taskLeaseResult{TaskID: strings.TrimSpace(c.Param("id")), WorkerID: workerID, LeaseTimeout: s.taskLeaseTimeout()}
	admissionToken := ""
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var task models.RechargeTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, "id = ?", result.TaskID).Error; err != nil {
			return err
		}
		result.Status = task.Status
		result.TraceID = task.TraceID
		admissionToken = strings.TrimSpace(task.AdmissionToken)
		if isTerminal(task.Status) {
			result.Terminal = true
			return nil
		}
		if err := validateTaskLease(&task, workerID, leaseToken, now); err != nil {
			return err
		}
		expires := now.Add(s.taskLeaseTimeout())
		updates := map[string]any{"heartbeat_at": now, "lease_expires_at": expires}
		if input.Progress != nil {
			progress := clampProgress(*input.Progress)
			if progress > task.Progress {
				updates["progress"] = progress
			}
		}
		if input.Message != nil && strings.TrimSpace(*input.Message) != "" {
			updates["message"] = strings.TrimSpace(*input.Message)
		}
		if err := tx.Model(&task).Updates(updates).Error; err != nil {
			return err
		}
		result.HeartbeatAt = &now
		result.LeaseExpiresAt = &expires
		result.LeaseToken = leaseToken
		result.Claimed = true
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			fail(c, http.StatusNotFound, "任务不存在")
		case errors.Is(err, errTaskLeaseInput):
			taskLeaseError(c, http.StatusBadRequest, "task_lease_required", err)
		case errors.Is(err, errTaskLeaseLost):
			taskLeaseError(c, http.StatusConflict, "task_lease_lost", err)
		default:
			publicFail(c, http.StatusInternalServerError, "任务心跳续期失败")
		}
		return
	}
	if !result.Terminal {
		s.refreshAdmission(admissionToken)
	}
	c.JSON(http.StatusOK, taskLeasePayload(result))
}

// RecoverStaleRechargeTasks is the API-owned crash recovery pass. It handles
// both rows that never reached a Worker and rows whose Worker lease expired.
// The task row is locked before the CDK is released, so an old Worker cannot
// race recovery and return the same CDK to the wrong task.
type RechargeTaskRecoveryReport struct {
	QueuedExpired            int64
	RunningExpired           int64
	ProviderReleaseAttempted int64
	ProviderReleaseSucceeded int64
	ProviderReleaseFailed    int64
}

func (s *Server) RecoverStaleRechargeTasks() (RechargeTaskRecoveryReport, error) {
	now := time.Now()
	queueCutoff := now.Add(-s.taskQueuedTimeout())
	leaseCutoff := now.Add(-s.taskLeaseTimeout())
	report := RechargeTaskRecoveryReport{}
	admissionTokens := make([]string, 0)
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var queued []models.RechargeTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("status = ? AND ((queue_deadline_at IS NOT NULL AND queue_deadline_at <= ?) OR (queue_deadline_at IS NULL AND created_at <= ?))", models.TaskQueued, now, queueCutoff).
			Limit(500).Find(&queued).Error; err != nil {
			return err
		}
		for index := range queued {
			admissionToken := strings.TrimSpace(queued[index].AdmissionToken)
			changed, err := s.expireRechargeTaskTx(tx, &queued[index], "queue_timeout", "任务排队超时，请稍后重试", now)
			if err != nil {
				return err
			}
			if changed {
				report.QueuedExpired++
				if admissionToken != "" {
					admissionTokens = append(admissionTokens, admissionToken)
				}
			}
		}

		var running []models.RechargeTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("status = ? AND ((lease_expires_at IS NOT NULL AND lease_expires_at <= ?) OR (lease_expires_at IS NULL AND updated_at <= ?))", models.TaskRunning, now, leaseCutoff).
			Limit(500).Find(&running).Error; err != nil {
			return err
		}
		for index := range running {
			admissionToken := strings.TrimSpace(running[index].AdmissionToken)
			changed, err := s.expireRechargeTaskTx(tx, &running[index], "worker_lease_timeout", "Worker 租约已超时，任务已自动回收", now)
			if err != nil {
				return err
			}
			if changed {
				report.RunningExpired++
				if admissionToken != "" {
					admissionTokens = append(admissionTokens, admissionToken)
				}
			}
		}
		return nil
	})
	if err == nil && s.CardPools != nil {
		releaseReport, releaseErr := s.CardPools.RetryPendingProviderReleases(context.Background(), 500)
		if releaseErr != nil {
			return report, releaseErr
		}
		report.ProviderReleaseAttempted = releaseReport.Attempted
		report.ProviderReleaseSucceeded = releaseReport.Succeeded
		report.ProviderReleaseFailed = releaseReport.Failed
	}
	if err == nil {
		for _, token := range admissionTokens {
			s.releaseAdmission(token)
		}
	}
	return report, err
}

func (s *Server) expireRechargeTaskTx(tx *gorm.DB, task *models.RechargeTask, errorCode, message string, now time.Time) (bool, error) {
	if task == nil || isTerminal(task.Status) {
		return false, nil
	}
	updates := map[string]any{
		"status":             models.TaskFailed,
		"progress":           0,
		"message":            message,
		"error_code":         errorCode,
		"error_message":      message,
		"finished_at":        now,
		"worker_lease_token": "",
		"admission_token":    "",
		"heartbeat_at":       nil,
		"lease_expires_at":   nil,
	}
	if err := tx.Model(task).Updates(updates).Error; err != nil {
		return false, err
	}
	if err := releaseTaskCardAllocationTx(tx, task.ID, task.CardAllocationID, errorCode, message, now); err != nil {
		return false, err
	}
	if task.CDKID != nil {
		cdkID := strings.TrimSpace(*task.CDKID)
		if cdkID != "" {
			if err := tx.Model(&models.CDK{}).
				Where("id = ? AND used_by_task_id = ?", cdkID, task.ID).
				Updates(map[string]any{"status": models.CDKAvailable, "used_by_task_id": "", "used_at": nil}).Error; err != nil {
				return false, err
			}
		}
	}
	if strings.TrimSpace(task.JobKey) != "" {
		if err := tx.Create(&models.RuntimeLog{
			ID:      db.NewID("runtime"),
			JobKey:  task.JobKey,
			TraceID: firstNonEmpty(task.TraceID, db.NewID("trace")),
			Level:   "error",
			Source:  "task-recovery",
			Text:    message,
		}).Error; err != nil {
			return false, err
		}
	}
	return true, nil
}

// releaseTaskCardAllocationTx is the database-side crash recovery for a
// Worker lease that expired before the Worker could call the bridge. It keeps
// the task's allocation isolated and releases the underlying LOCAL_TEXT asset
// only when no other recurring allocation still owns the normalized card.
func releaseTaskCardAllocationTx(tx *gorm.DB, taskID, allocationID, failureCode, failureMessage string, now time.Time) error {
	var allocation models.CardAllocation
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"})
	if strings.TrimSpace(allocationID) != "" {
		query = query.Where("id = ?", strings.TrimSpace(allocationID))
	} else {
		query = query.Where("payment_task_id = ? AND status IN ?", taskID, []string{"CREATING", "ASSIGNED", "IN_USE"}).Order("created_at DESC, id DESC")
	}
	if err := query.First(&allocation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if !isActiveAllocationStatusForHTTPAPI(allocation.Status) {
		return nil
	}
	if err := tx.Model(&allocation).Updates(map[string]any{
		"status": string(cardpool.AllocationFailed), "released_at": now,
		"failure_code": failureCode, "failure_message": failureMessage,
	}).Error; err != nil {
		return err
	}
	if strings.TrimSpace(allocation.PaymentCardID) == "" {
		return nil
	}
	var remaining int64
	if err := tx.Model(&models.CardAllocation{}).
		Where("payment_card_id = ? AND status IN ?", allocation.PaymentCardID, []string{"CREATING", "ASSIGNED", "IN_USE"}).
		Count(&remaining).Error; err != nil {
		return err
	}
	if remaining > 0 {
		return nil
	}
	var card models.PaymentCard
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", allocation.PaymentCardID).First(&card).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	status := card.Status
	if status == string(cardpool.CardInUse) || status == string(cardpool.CardAssigned) || status == string(cardpool.CardCreating) {
		status = string(cardpool.CardActive)
	}
	cardUpdates := map[string]any{"in_use": false, "status": status}
	if allocation.UsageType == string(cardpool.UsageOneTime) {
		// A one-time card exists only for this task. Mark it unavailable until
		// the post-transaction compensation pass successfully cancels it at the
		// Provider. This also applies to LOCAL_TEXT: its adapter's CancelCard
		// retires the underlying imported asset instead of merely unlocking it.
		cardUpdates["status"] = string(cardpool.CardFailed)
		if err := tx.Model(&allocation).Updates(map[string]any{
			"provider_release_pending": true, "provider_release_action": cardpool.ProviderReleaseActionCancel,
		}).Error; err != nil {
			return err
		}
	}
	if err := tx.Model(&card).Updates(cardUpdates).Error; err != nil {
		return err
	}
	if strings.TrimSpace(card.LocalCardAssetID) != "" {
		return tx.Model(&models.CardAsset{}).Where("id = ? AND in_use = ?", card.LocalCardAssetID, true).
			Updates(map[string]any{"in_use": false, "locked_at": nil, "locked_by": ""}).Error
	}
	return nil
}

func isActiveAllocationStatusForHTTPAPI(status string) bool {
	return status == "CREATING" || status == "ASSIGNED" || status == "IN_USE"
}
