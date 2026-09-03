package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/queue"
	"gorm.io/gorm"
)

const maxProductGenerationCount = 100

func (s *Server) generateProducts(c *gin.Context) {
	var input struct {
		Count int `json:"count"`
	}
	if err := c.ShouldBindJSON(&input); err != nil && c.Request.ContentLength != 0 {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	count := input.Count
	if count < 1 {
		count = 1
	}
	if count > maxProductGenerationCount {
		count = maxProductGenerationCount
	}
	if s.configValue("maintenance_mode", "0") == "1" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "系统维护中，请稍后再试"})
		return
	}

	var running int64
	if err := s.DB.Model(&models.ProductGenerationTask{}).
		Where("status IN ?", []string{models.ProductGenerationQueued, models.ProductGenerationRunning}).Count(&running).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取成品生产任务失败")
		return
	}
	if running > 0 {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "已有成品生产任务正在运行，请先等待或停止当前任务"})
		return
	}

	workerCount := minInt(count, maxInt(1, parseConfigInt(s.configValue("max_background_concurrent", "1"), 1)))
	task := models.ProductGenerationTask{
		ID:          db.NewID("product-generation"),
		JobKey:      db.NewID("product-job"),
		TraceID:     requestTraceID(c),
		TargetCount: count,
		WorkerCount: workerCount,
		Status:      models.ProductGenerationQueued,
		Progress:    0,
		Message:     "成品生产任务已排队",
	}
	if err := s.DB.Create(&task).Error; err != nil {
		fail(c, http.StatusInternalServerError, "创建成品生产任务失败")
		return
	}
	if err := s.Q.Enqueue(c.Request.Context(), queue.TaskMessage{TaskID: task.ID, Mode: "product_generation", Kind: "product_generation", TraceID: task.TraceID}); err != nil {
		if updateErr := s.DB.Model(&task).Updates(map[string]any{"status": models.ProductGenerationFailed, "message": "任务入队失败", "finished_at": time.Now()}).Error; updateErr != nil {
			fail(c, http.StatusInternalServerError, "任务入队失败且无法保存失败状态")
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "任务队列暂不可用"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"jobKey":      task.JobKey,
		"workerCount": workerCount,
		"message":     "后台成品生产任务已启动",
		"task":        productGenerationResponse(task),
	})
}

func (s *Server) resumeProducts(c *gin.Context) {
	if s.configValue("maintenance_mode", "0") == "1" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "系统维护中，请稍后再试"})
		return
	}
	var running int64
	if err := s.DB.Model(&models.ProductGenerationTask{}).
		Where("status IN ?", []string{models.ProductGenerationQueued, models.ProductGenerationRunning}).Count(&running).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取成品生产任务失败")
		return
	}
	if running > 0 {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "已有成品生产任务正在运行，请先等待或停止当前任务"})
		return
	}

	var previous models.ProductGenerationTask
	if err := s.DB.Where("status = ? AND completed_count < target_count", models.ProductGenerationFailed).
		Order("updated_at DESC, id DESC").First(&previous).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "当前没有可继续生产的中断任务"})
			return
		}
		fail(c, http.StatusInternalServerError, "读取可续作任务失败")
		return
	}

	remaining := previous.TargetCount - previous.CompletedCount
	workerCount := minInt(remaining, maxInt(1, parseConfigInt(s.configValue("max_background_concurrent", "1"), 1)))
	task := models.ProductGenerationTask{
		ID:                db.NewID("product-generation"),
		JobKey:            db.NewID("product-job"),
		TraceID:           requestTraceID(c),
		TargetCount:       remaining,
		WorkerCount:       workerCount,
		Status:            models.ProductGenerationQueued,
		Message:           "成品续作任务已排队",
		ResumedFromJobKey: previous.JobKey,
	}
	if err := s.DB.Create(&task).Error; err != nil {
		fail(c, http.StatusInternalServerError, "创建成品续作任务失败")
		return
	}
	if err := s.Q.Enqueue(c.Request.Context(), queue.TaskMessage{TaskID: task.ID, Mode: "product_generation", Kind: "product_generation", TraceID: task.TraceID}); err != nil {
		if updateErr := s.DB.Model(&task).Updates(map[string]any{"status": models.ProductGenerationFailed, "message": "任务入队失败", "finished_at": time.Now()}).Error; updateErr != nil {
			fail(c, http.StatusInternalServerError, "任务入队失败且无法保存失败状态")
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "任务队列暂不可用"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"jobKey":       task.JobKey,
		"workerCount":  workerCount,
		"resumedCount": remaining,
		"message":      "已继续生产中断任务",
		"task":         productGenerationResponse(task),
	})
}

func (s *Server) stopProducts(c *gin.Context) {
	var input struct {
		JobKey string `json:"jobKey"`
	}
	if err := c.ShouldBindJSON(&input); err != nil && c.Request.ContentLength != 0 {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	query := s.DB.Model(&models.ProductGenerationTask{}).
		Where("status IN ?", []string{models.ProductGenerationQueued, models.ProductGenerationRunning})
	if strings.TrimSpace(input.JobKey) != "" {
		query = query.Where("job_key = ?", strings.TrimSpace(input.JobKey))
	}
	var tasks []models.ProductGenerationTask
	if err := query.Find(&tasks).Error; err != nil {
		fail(c, http.StatusInternalServerError, "发送停止指令失败")
		return
	}
	stopped := int64(0)
	for _, task := range tasks {
		updates := map[string]any{
			"aborted": true,
			"message": "已发送停止指令：当前步骤完成后停止新的成品生产",
		}
		if task.Status == models.ProductGenerationQueued {
			updates["status"] = models.ProductGenerationFailed
			updates["progress"] = 100
			now := time.Now()
			updates["finished_at"] = now
		}
		result := s.DB.Model(&task).Updates(updates)
		if result.Error != nil {
			fail(c, http.StatusInternalServerError, "发送停止指令失败")
			return
		}
		stopped += result.RowsAffected
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"stopped": stopped,
		"message": map[bool]string{true: "已发送停止指令", false: "未找到运行中的成品生产任务"}[stopped > 0],
	})
}

func (s *Server) getProductGeneration(c *gin.Context) {
	var task models.ProductGenerationTask
	if err := s.DB.Where("job_key = ?", c.Param("jobKey")).First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "成品生产任务不存在"})
			return
		}
		fail(c, http.StatusInternalServerError, "读取成品生产任务失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "task": productGenerationResponse(task)})
}

func (s *Server) productGenerationSecret(c *gin.Context) {
	var task models.ProductGenerationTask
	if err := s.DB.First(&task, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "成品生产任务不存在")
		return
	}
	c.JSON(http.StatusOK, productGenerationResponse(task))
}

func (s *Server) updateProductGeneration(c *gin.Context) {
	var input struct {
		Status         string `json:"status"`
		Progress       *int   `json:"progress"`
		Message        string `json:"message"`
		RawOutput      string `json:"rawOutput"`
		CompletedCount *int   `json:"completedCount"`
		SuccessCount   *int   `json:"successCount"`
		FailedCount    *int   `json:"failedCount"`
		Aborted        *bool  `json:"aborted"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	var task models.ProductGenerationTask
	if err := s.DB.First(&task, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "成品生产任务不存在")
		return
	}
	if isProductGenerationTerminal(task.Status) && input.Status == models.ProductGenerationRunning {
		c.JSON(http.StatusOK, gin.H{"success": true, "task": productGenerationResponse(task)})
		return
	}
	updates := map[string]any{}
	if input.Status != "" {
		updates["status"] = normalizeProductGenerationStatus(input.Status)
	}
	if input.Message != "" {
		updates["message"] = input.Message
	}
	if input.RawOutput != "" {
		updates["raw_output"] = input.RawOutput
	}
	// The stop endpoint is authoritative. Worker heartbeats may carry false
	// while an admin stop races with an update, so only ever set this flag here.
	if input.Aborted != nil && *input.Aborted {
		updates["aborted"] = *input.Aborted
	}
	if input.Progress != nil && *input.Progress > task.Progress {
		updates["progress"] = clampProgress(*input.Progress)
	}
	if input.CompletedCount != nil && *input.CompletedCount > task.CompletedCount {
		updates["completed_count"] = *input.CompletedCount
	}
	if input.SuccessCount != nil && *input.SuccessCount > task.SuccessCount {
		updates["success_count"] = *input.SuccessCount
	}
	if input.FailedCount != nil && *input.FailedCount > task.FailedCount {
		updates["failed_count"] = *input.FailedCount
	}
	if input.Status == models.ProductGenerationRunning && task.StartedAt == nil {
		now := time.Now()
		updates["started_at"] = now
	}
	status := normalizeProductGenerationStatus(input.Status)
	if isProductGenerationTerminal(status) {
		now := time.Now()
		updates["finished_at"] = now
		updates["progress"] = 100
	}
	if len(updates) > 0 {
		if err := s.DB.Model(&task).Updates(updates).Error; err != nil {
			fail(c, http.StatusInternalServerError, "更新成品生产任务失败")
			return
		}
	}
	if err := s.DB.First(&task, "id = ?", task.ID).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取更新后的成品生产任务失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "task": productGenerationResponse(task)})
}

func productGenerationResponse(task models.ProductGenerationTask) gin.H {
	status := legacyTaskStatus(task.Status)
	progress := clampProgress(task.Progress)
	terminal := isProductGenerationTerminal(task.Status)
	stopEnabled := !task.Aborted && !terminal
	return gin.H{
		"id": task.ID, "jobKey": task.JobKey, "job_key": task.JobKey, "traceId": task.TraceID, "trace_id": task.TraceID,
		"targetCount": task.TargetCount, "target_count": task.TargetCount,
		"completedCount": task.CompletedCount, "completed_count": task.CompletedCount,
		"successCount": task.SuccessCount, "success_count": task.SuccessCount,
		"failedCount": task.FailedCount, "failed_count": task.FailedCount,
		"workerCount": task.WorkerCount, "worker_count": task.WorkerCount,
		"status": status, "status_label": legacyTaskStatusLabel(status), "status_tone": legacyTaskStatusTone(status),
		"terminal": terminal, "stop_enabled": stopEnabled, "progress": progress, "progress_text": fmt.Sprintf("%d%%", progress), "message": task.Message,
		"rawOutput": task.RawOutput, "raw_output": task.RawOutput, "aborted": task.Aborted,
		"resumedFromJobKey": task.ResumedFromJobKey, "resumed_from_job_key": task.ResumedFromJobKey,
		"startedAt": task.StartedAt, "finishedAt": task.FinishedAt,
		"createdAt": task.CreatedAt, "updatedAt": task.UpdatedAt,
	}
}

func normalizeProductGenerationStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case models.ProductGenerationQueued:
		return models.ProductGenerationQueued
	case models.ProductGenerationRunning:
		return models.ProductGenerationRunning
	case models.ProductGenerationSucceeded:
		return models.ProductGenerationSucceeded
	case models.ProductGenerationFailed, "aborted":
		return models.ProductGenerationFailed
	default:
		return models.ProductGenerationRunning
	}
}

func isProductGenerationTerminal(status string) bool {
	return status == models.ProductGenerationSucceeded || status == models.ProductGenerationFailed
}
