package httpapi

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errActivationCapacity = errors.New("recharge activation capacity reached")

const activationAdmissionLockSQL = "SELECT pg_advisory_xact_lock(2147483647, 424242)"

// checkActivationGuards mirrors the original service's request-time checks.
// The final CDK transition is still performed under a row lock when the task is created.
func (s *Server) checkActivationGuards(code, mode, clientIP string) (int, string) {
	if s.configValue("maintenance_mode", "0") == "1" {
		return http.StatusServiceUnavailable, "系统维护中，请稍后再试"
	}
	var cdk models.CDK
	if err := s.DB.Where("code = ?", strings.TrimSpace(code)).First(&cdk).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return http.StatusForbidden, "CDK 无效、已使用或非自助激活码"
		}
		return http.StatusInternalServerError, "读取 CDK 失败"
	}
	if cdk.Status == models.CDKProcessing {
		return http.StatusConflict, "该 CDK 正在处理中"
	}
	if cdk.Status != models.CDKAvailable || (cdk.Type != "" && cdk.Type != models.CDKTypeSelf) {
		if cdk.Status == models.CDKUsed {
			return http.StatusForbidden, "该 CDK 已使用"
		}
		return http.StatusForbidden, "CDK 无效、已使用或非自助激活码"
	}
	if cdk.CooldownUntil != nil && cdk.CooldownUntil.After(time.Now()) {
		return http.StatusForbidden, "该卡密连续无资格尝试过多，请稍后再试"
	}
	if strings.TrimSpace(clientIP) != "" {
		var limit models.ActivationAttemptLimit
		if err := s.DB.Where("scope_type = ? AND scope_key = ?", "ip", clientIP).First(&limit).Error; err == nil {
			if limit.CooldownUntil != nil && limit.CooldownUntil.After(time.Now()) {
				return http.StatusForbidden, "当前 IP 连续无资格尝试过多，请稍后再试"
			}
		} else if err != gorm.ErrRecordNotFound {
			return http.StatusInternalServerError, "读取 IP 冷却状态失败"
		}
	}
	if mode == "browser" || mode == "protocol" {
		var available int64
		if err := s.DB.Model(&models.CardAsset{}).
			Where("active = ? AND in_use = ?", true, false).
			Where("status = ?", "正常").
			Where("cooldown_until IS NULL OR cooldown_until < ?", time.Now()).
			Count(&available).Error; err != nil {
			return http.StatusInternalServerError, "读取卡池失败"
		}
		if available == 0 {
			return http.StatusServiceUnavailable, "银行卡池暂无可用卡片，请先在后台导入银行卡后再试"
		}
	}
	return 0, ""
}

// checkActivationCapacityTx serializes admission decisions across API
// instances. The advisory lock is transaction-scoped and releases on commit
// or rollback, so the count and task creation form one atomic decision.
func (s *Server) checkActivationCapacityTx(tx *gorm.DB) error {
	if err := tx.Exec(activationAdmissionLockSQL).Error; err != nil {
		return err
	}
	var active int64
	if err := tx.Model(&models.RechargeTask{}).
		Where("status IN ?", []string{models.TaskQueued, models.TaskRunning}).
		Count(&active).Error; err != nil {
		return err
	}
	if active >= int64(s.maxConcurrentActivations()) {
		return errActivationCapacity
	}
	return nil
}

func (s *Server) maxConcurrentActivations() int {
	return maxInt(1, parseConfigInt(s.configValue("max_concurrent_activations", "1"), 1))
}

// admissionLeaseTimeout is deliberately longer than the queue deadline. A
// queued task therefore keeps its Redis slot until the API recovery pass can
// release it, while running tasks renew the same slot with their Worker
// heartbeat. The database transaction remains the final source of truth.
func (s *Server) admissionLeaseTimeout() time.Duration {
	ttl := s.taskQueuedTimeout() + s.taskLeaseTimeout()
	if ttl < time.Minute {
		return time.Minute
	}
	return ttl
}

func (s *Server) acquireAdmission(ctx context.Context) (string, error) {
	if s == nil || s.Q == nil || s.Q.Redis == nil {
		return "", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	token := db.NewID("admission")
	acquired, err := s.Q.TryAcquireAdmission(ctx, token, s.maxConcurrentActivations(), s.admissionLeaseTimeout())
	if err != nil {
		// Redis is an optimization in front of the PostgreSQL arbiter. Failing
		// open here preserves correctness while letting the DB enforce the limit.
		log.Printf("[admission] Redis gate unavailable, falling back to PostgreSQL: %v", err)
		return "", nil
	}
	if !acquired {
		return "", errActivationCapacity
	}
	return token, nil
}

func (s *Server) releaseAdmission(token string) {
	if s == nil || s.Q == nil || s.Q.Redis == nil || strings.TrimSpace(token) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Q.ReleaseAdmission(ctx, token); err != nil {
		log.Printf("[admission] Redis release failed: %v", err)
	}
}

func (s *Server) refreshAdmission(token string) {
	if s == nil || s.Q == nil || s.Q.Redis == nil || strings.TrimSpace(token) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.Q.RefreshAdmission(ctx, token, s.admissionLeaseTimeout()); err != nil {
		log.Printf("[admission] Redis refresh failed: %v", err)
	}
}

func (s *Server) guardOrAbort(c *gin.Context, code, mode, clientIP string, email ...string) bool {
	status, message := s.checkActivationGuards(code, mode, clientIP)
	if status == 0 {
		return true
	}
	if status == http.StatusServiceUnavailable && strings.Contains(message, "银行卡池") {
		payload := map[string]string{"cdk": strings.TrimSpace(code), "ip": strings.TrimSpace(clientIP), "message": message}
		if len(email) > 0 {
			payload["email"] = strings.TrimSpace(email[0])
		}
		go s.notifyTelegramEvent("card_pool_empty", payload)
	}
	if status >= http.StatusInternalServerError {
		publicFail(c, status, message)
		return false
	}
	c.JSON(status, gin.H{"success": false, "message": message})
	return false
}

func recordAttemptFailureTx(tx *gorm.DB, scopeType, scopeKey string, now time.Time) (bool, error) {
	if strings.TrimSpace(scopeKey) == "" {
		return false, nil
	}
	var row models.ActivationAttemptLimit
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("scope_type = ? AND scope_key = ?", scopeType, scopeKey).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		row = models.ActivationAttemptLimit{ID: db.NewID("attempt"), ScopeType: scopeType, ScopeKey: scopeKey, FailCount: 1}
		return false, tx.Create(&row).Error
	}
	if err != nil {
		return false, err
	}
	failCount := row.FailCount + 1
	updates := map[string]any{"fail_count": failCount}
	cooled := false
	if failCount >= 3 {
		updates["fail_count"] = 0
		updates["cooldown_until"] = now.Add(10 * time.Minute)
		cooled = true
	}
	return cooled, tx.Model(&row).Updates(updates).Error
}

func resetAttemptFailureTx(tx *gorm.DB, scopeType, scopeKey string) error {
	if strings.TrimSpace(scopeKey) == "" {
		return nil
	}
	return tx.Model(&models.ActivationAttemptLimit{}).
		Where("scope_type = ? AND scope_key = ?", scopeType, scopeKey).
		Updates(map[string]any{"fail_count": 0, "cooldown_until": nil}).Error
}
