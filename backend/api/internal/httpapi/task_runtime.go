package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
)

// taskRuntime exposes only non-secret task metadata to the queue worker. The
// browser path still uses /secret because the legacy Playwright process needs
// the encrypted Session value; the Go-owned upstream path never receives it.
func (s *Server) taskRuntime(c *gin.Context) {
	var task models.RechargeTask
	if err := s.DB.Preload("Plan").First(&task, "id = ?", c.Param("id")).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			fail(c, http.StatusNotFound, "任务不存在")
			return
		}
		fail(c, http.StatusInternalServerError, "读取任务元数据失败")
		return
	}
	if err := validateTaskLeaseHeaders(c, &task); err != nil {
		if errors.Is(err, errTaskLeaseInput) || errors.Is(err, errTaskLeaseLost) {
			taskLeaseError(c, http.StatusConflict, "task_lease_lost", err)
			return
		}
		publicFail(c, http.StatusInternalServerError, "读取任务租约失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"taskId":            task.ID,
		"jobKey":            task.JobKey,
		"traceId":           task.TraceID,
		"trace_id":          task.TraceID,
		"mode":              task.Mode,
		"planId":            task.PlanID,
		"planCode":          task.Plan.Code,
		"cdkCode":           strings.TrimSpace(task.CDKCode),
		"poolId":            task.PoolID,
		"businessAccountId": task.BusinessAccountID,
		"usageType":         task.UsageType,
		"paymentCardId":     task.PaymentCardID,
		"allocationId":      task.CardAllocationID,
		"cardAllocationId":  task.CardAllocationID,
		"status":            task.Status,
		"progress":          clampProgress(task.Progress),
		"message":           publicTaskMessage(task.Status),
	})
}
