package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
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

const activationGuardReasonNoCardInventory = "NO_AVAILABLE_CARD"

type activationGuardResult struct {
	status           int
	message          string
	reason           string
	poolID           string
	provider         string
	routingStrategy  string
	cardCreationMode string
	providers        []string
}

// checkActivationGuards mirrors the original service's request-time checks.
// The final CDK transition is still performed under a row lock when the task is created.
//
// poolIDs is optional for legacy callers that use the configured default pool.
// The modern recharge endpoint passes its explicit poolId so the guard checks
// the same card source that the Worker will eventually acquire from.
func (s *Server) checkActivationGuards(code, mode, clientIP string, poolIDs ...string) (int, string) {
	result := s.checkActivationGuardsResult(code, mode, clientIP, poolIDs...)
	return result.status, result.message
}

func (s *Server) checkActivationGuardsResult(code, mode, clientIP string, poolIDs ...string) activationGuardResult {
	if s.configValue("maintenance_mode", "0") == "1" {
		return activationGuardResult{status: http.StatusServiceUnavailable, message: "系统维护中，请稍后再试"}
	}
	var cdk models.CDK
	if err := s.DB.Where("code = ?", strings.TrimSpace(code)).First(&cdk).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return activationGuardResult{status: http.StatusForbidden, message: "CDK 无效、已使用或非自助激活码"}
		}
		return activationGuardResult{status: http.StatusInternalServerError, message: "读取 CDK 失败"}
	}
	if cdk.Status == models.CDKProcessing {
		return activationGuardResult{status: http.StatusConflict, message: "该 CDK 正在处理中"}
	}
	if cdk.Status != models.CDKAvailable || (cdk.Type != "" && cdk.Type != models.CDKTypeSelf) {
		if cdk.Status == models.CDKUsed {
			return activationGuardResult{status: http.StatusForbidden, message: "该 CDK 已使用"}
		}
		return activationGuardResult{status: http.StatusForbidden, message: "CDK 无效、已使用或非自助激活码"}
	}
	if cdk.CooldownUntil != nil && cdk.CooldownUntil.After(time.Now()) {
		return activationGuardResult{status: http.StatusForbidden, message: "该卡密连续无资格尝试过多，请稍后再试"}
	}
	if strings.TrimSpace(clientIP) != "" {
		var limit models.ActivationAttemptLimit
		result := s.DB.Where("scope_type = ? AND scope_key = ?", "ip", clientIP).Limit(1).Find(&limit)
		if result.Error != nil {
			return activationGuardResult{status: http.StatusInternalServerError, message: "读取 IP 冷却状态失败"}
		}
		if result.RowsAffected > 0 {
			if limit.CooldownUntil != nil && limit.CooldownUntil.After(time.Now()) {
				return activationGuardResult{status: http.StatusForbidden, message: "当前 IP 连续无资格尝试过多，请稍后再试"}
			}
		}
	}
	if mode == "browser" || mode == "protocol" {
		poolID := ""
		if len(poolIDs) > 0 {
			poolID = strings.TrimSpace(poolIDs[0])
		}
		requiresInventory, err := s.activationRequiresPreexistingCard(poolID)
		if err != nil {
			return activationGuardResult{status: http.StatusInternalServerError, message: "读取卡池失败"}
		}
		if requiresInventory {
			available, err := s.hasActivationCardInventory(poolID)
			if err != nil {
				return activationGuardResult{status: http.StatusInternalServerError, message: "读取卡池失败"}
			}
			if !available {
				return activationGuardResult{
					status:  http.StatusServiceUnavailable,
					message: "银行卡池暂无可用卡片，请先在后台导入银行卡后再试",
					reason:  activationGuardReasonNoCardInventory,
					poolID:  strings.TrimSpace(poolID),
				}
			}
		}
	}
	return activationGuardResult{}
}

// activationRequiresPreexistingCard keeps the old request-time inventory
// guard for LOCAL_TEXT and POOL_ONLY, while allowing an external issuing
// provider to create a card during Worker execution when the pool is
// configured as CREATE_ON_DEMAND.
//
// The decision is intentionally based on the provider route rather than only
// CardPool.Type. The seeded legacy pool is LOCAL for compatibility, but its
// configured default provider may be switched to Kimoox/Airwallex/Stripe.
func (s *Server) activationRequiresPreexistingCard(poolID string) (bool, error) {
	if s == nil || s.DB == nil || s.CardPools == nil {
		return true, nil
	}

	pool, err := s.CardPools.ResolvePool(context.Background(), poolID)
	if err != nil {
		return false, err
	}
	return activationPoolRequiresPreexistingCard(pool), nil
}

func activationPoolRequiresPreexistingCard(pool models.CardPool) bool {
	if strings.ToUpper(strings.TrimSpace(pool.CardCreationMode)) != "CREATE_ON_DEMAND" {
		return true
	}

	defaultProvider := strings.ToUpper(strings.TrimSpace(pool.DefaultProvider))
	providers := make([]models.CardPoolProvider, 0, len(pool.Providers))
	for _, route := range pool.Providers {
		if route.Enabled && strings.TrimSpace(route.Provider) != "" {
			providers = append(providers, route)
		}
	}
	sort.SliceStable(providers, func(i, j int) bool {
		if providers[i].Priority != providers[j].Priority {
			return providers[i].Priority < providers[j].Priority
		}
		return strings.ToUpper(providers[i].Provider) < strings.ToUpper(providers[j].Provider)
	})

	// The seeded default pool can receive a configured provider without a
	// matching CardPoolProvider row. Treat the configured default as the route
	// selected by FIXED (and as the fallback when no route exists).
	primary := defaultProvider
	if primary == "" && len(providers) > 0 {
		primary = strings.ToUpper(strings.TrimSpace(providers[0].Provider))
	}
	switch strings.ToUpper(strings.TrimSpace(pool.RoutingStrategy)) {
	case "FAILOVER", "WEIGHTED":
		// An external route can issue a fresh card. For FAILOVER this also
		// prevents a local backup without CardAsset inventory from blocking an
		// otherwise valid external primary.
		for _, route := range providers {
			if strings.ToUpper(strings.TrimSpace(route.Provider)) != "LOCAL_TEXT" {
				return false
			}
		}
		if primary != "" && primary != "LOCAL_TEXT" {
			return false
		}
		return true
	default:
		return primary == "" || primary == "LOCAL_TEXT"
	}
}

// hasActivationCardInventory checks only the inventory belonging to a route
// the card-pool service can actually select. It is only called for pools whose
// configured route requires a pre-existing card. This avoids allowing a fixed
// external Provider merely because an unrelated LOCAL_TEXT card exists in the
// same pool.
func (s *Server) hasActivationCardInventory(poolID string) (bool, error) {
	if s == nil || s.DB == nil {
		return false, nil
	}
	effectivePoolID := firstNonEmpty(strings.TrimSpace(poolID), s.configValue("card_pool_default_id", "pool_legacy"))
	if s.CardPools == nil {
		return s.hasLegacyActivationCardInventory(effectivePoolID)
	}
	pool, err := s.CardPools.ResolvePool(context.Background(), effectivePoolID)
	if err != nil {
		return false, err
	}
	now := time.Now()

	for _, provider := range activationInventoryProviders(pool) {
		if strings.EqualFold(provider, "LOCAL_TEXT") {
			var localCount int64
			query := s.DB.Model(&models.CardAsset{}).
				Where("active = ? AND in_use = ?", true, false).
				Where("status = ?", "正常").
				Where("cooldown_until IS NULL OR cooldown_until < ?", now).
				Where("provider = ? OR provider = '' OR provider IS NULL", "LOCAL_TEXT")
			if effectivePoolID == "" || effectivePoolID == "pool_legacy" {
				query = query.Where("pool_id = ? OR pool_id = '' OR pool_id IS NULL", effectivePoolID)
			} else {
				query = query.Where("pool_id = ?", effectivePoolID)
			}
			if err := query.Count(&localCount).Error; err != nil {
				return false, err
			}
			if localCount > 0 {
				return true, nil
			}
			continue
		}

		var providerCount int64
		if err := s.DB.Model(&models.PaymentCard{}).
			Where("pool_id = ? AND provider = ?", effectivePoolID, strings.ToUpper(strings.TrimSpace(provider))).
			Where("status IN ? AND in_use = ?", []string{"ACTIVE", "ASSIGNED"}, false).
			Where("usage_count = 0").
			Where("cooldown_until IS NULL OR cooldown_until < ?", now).
			Count(&providerCount).Error; err != nil {
			return false, err
		}
		if providerCount > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) hasLegacyActivationCardInventory(poolID string) (bool, error) {
	now := time.Now()
	var localCount int64
	if err := s.DB.Model(&models.CardAsset{}).
		Where("active = ? AND in_use = ?", true, false).
		Where("status = ?", "正常").
		Where("cooldown_until IS NULL OR cooldown_until < ?", now).
		Count(&localCount).Error; err != nil {
		return false, err
	}
	if localCount > 0 {
		return true, nil
	}

	var providerCount int64
	if err := s.DB.Model(&models.PaymentCard{}).
		Where("status IN ? AND in_use = ?", []string{"ACTIVE", "ASSIGNED"}, false).
		Where("usage_count = 0").
		Where("cooldown_until IS NULL OR cooldown_until < ?", now).
		Count(&providerCount).Error; err != nil {
		return false, err
	}
	return providerCount > 0, nil
}

// activationInventoryProviders mirrors the selection point in AcquireCard.
// FIXED and PRIORITY select one provider before looking for a reusable card;
// WEIGHTED may select any enabled route, so the guard accepts inventory from
// any of them. FAILOVER still starts with the first priority route, and the
// existing allocation code only performs the POOL_ONLY inventory lookup for
// that selected route.
func activationInventoryProviders(pool models.CardPool) []string {
	providers := make([]models.CardPoolProvider, 0, len(pool.Providers))
	for _, route := range pool.Providers {
		if route.Enabled && strings.TrimSpace(route.Provider) != "" {
			providers = append(providers, route)
		}
	}
	sort.SliceStable(providers, func(i, j int) bool {
		if providers[i].Priority != providers[j].Priority {
			return providers[i].Priority < providers[j].Priority
		}
		return strings.ToUpper(providers[i].Provider) < strings.ToUpper(providers[j].Provider)
	})
	if len(providers) == 0 {
		if provider := strings.TrimSpace(pool.DefaultProvider); provider != "" {
			return []string{provider}
		}
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(pool.RoutingStrategy), "WEIGHTED") {
		result := make([]string, 0, len(providers))
		for _, provider := range providers {
			result = append(result, provider.Provider)
		}
		return result
	}
	if strings.EqualFold(strings.TrimSpace(pool.RoutingStrategy), "FIXED") {
		if provider := strings.TrimSpace(pool.DefaultProvider); provider != "" {
			return []string{provider}
		}
	}
	return []string{providers[0].Provider}
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
	emailValue := ""
	if len(email) > 0 {
		emailValue = email[0]
	}
	return s.guardOrAbortForPool(c, code, mode, clientIP, "", emailValue)
}

func (s *Server) guardOrAbortForPool(c *gin.Context, code, mode, clientIP, poolID, email string) bool {
	result := s.checkActivationGuardsResult(code, mode, clientIP, poolID)
	if result.status == 0 {
		return true
	}
	if result.reason == activationGuardReasonNoCardInventory {
		s.recordActivationAdmissionFailure(c, mode, result)
		payload := map[string]string{"cdk": strings.TrimSpace(code), "ip": strings.TrimSpace(clientIP), "message": result.message}
		if strings.TrimSpace(email) != "" {
			payload["email"] = strings.TrimSpace(email)
		}
		go s.notifyTelegramEvent("card_pool_empty", payload)
	}
	if result.status >= http.StatusInternalServerError {
		publicFail(c, result.status, result.message)
		return false
	}
	c.JSON(result.status, gin.H{"success": false, "message": result.message})
	return false
}

// recordActivationAdmissionFailure writes a safe, task-independent runtime log
// for failures that happen before a RechargeTask exists. This is intentionally
// synchronous: the request must not return a 503 while silently losing the
// diagnostic record in a goroutine.
func (s *Server) recordActivationAdmissionFailure(c *gin.Context, mode string, result activationGuardResult) {
	if result.reason != activationGuardReasonNoCardInventory {
		return
	}

	traceID := requestTraceID(c)
	if result.poolID == "" || result.provider == "" || result.routingStrategy == "" || result.cardCreationMode == "" || len(result.providers) == 0 {
		admissionContext := s.activationAdmissionContext(result.poolID)
		if result.poolID == "" {
			result.poolID = admissionContext.poolID
		}
		if result.provider == "" {
			result.provider = admissionContext.provider
		}
		if result.routingStrategy == "" {
			result.routingStrategy = admissionContext.routingStrategy
		}
		if result.cardCreationMode == "" {
			result.cardCreationMode = admissionContext.cardCreationMode
		}
		if len(result.providers) == 0 {
			result.providers = admissionContext.providers
		}
	}

	text := formatActivationAdmissionFailureLog(traceID, mode, result)
	log.Printf("%s", text)
	if s == nil || s.DB == nil {
		return
	}
	if err := s.DB.Create(&models.RuntimeLog{
		ID:      db.NewID("runtime"),
		JobKey:  truncate("admission:"+traceID, 96),
		TraceID: traceID,
		Level:   "warn",
		Source:  "activation-guard",
		Text:    text,
	}).Error; err != nil {
		log.Printf("[activation-guard] trace_id=%s runtime log persist failed: %v", traceID, err)
	}
}

type activationAdmissionContext struct {
	poolID           string
	provider         string
	routingStrategy  string
	cardCreationMode string
	providers        []string
}

func (s *Server) activationAdmissionContext(poolID string) activationAdmissionContext {
	result := activationAdmissionContext{}
	if s != nil && s.CardPools != nil {
		// ResolvePool already applies the effective admin configuration for the
		// default pool. Reuse that result before falling back to individual
		// config reads, keeping the failure path cheap and internally consistent.
		if pool, err := s.CardPools.ResolvePool(context.Background(), strings.TrimSpace(poolID)); err == nil {
			result.poolID = pool.ID
			result.provider = strings.ToUpper(strings.TrimSpace(pool.DefaultProvider))
			result.routingStrategy = strings.ToUpper(strings.TrimSpace(pool.RoutingStrategy))
			result.cardCreationMode = strings.ToUpper(strings.TrimSpace(pool.CardCreationMode))
			for _, route := range pool.Providers {
				if route.Enabled && strings.TrimSpace(route.Provider) != "" {
					result.providers = append(result.providers, strings.ToUpper(strings.TrimSpace(route.Provider)))
				}
			}
		}
	}
	if result.poolID == "" {
		result.poolID = firstNonEmpty(strings.TrimSpace(poolID), s.configValue("card_pool_default_id", "pool_legacy"))
	}
	if result.provider == "" {
		result.provider = strings.ToUpper(strings.TrimSpace(s.configValue("card_pool_default_provider", "LOCAL_TEXT")))
	}
	if result.routingStrategy == "" {
		result.routingStrategy = strings.ToUpper(strings.TrimSpace(s.configValue("card_pool_routing", "FIXED")))
	}
	if result.cardCreationMode == "" {
		result.cardCreationMode = strings.ToUpper(strings.TrimSpace(s.configValue("card_pool_card_creation_mode", "POOL_ONLY")))
	}
	if result.poolID == "" {
		result.poolID = "pool_legacy"
	}
	if result.provider == "" {
		result.provider = "LOCAL_TEXT"
	}
	if result.routingStrategy == "" {
		result.routingStrategy = "FIXED"
	}
	if result.cardCreationMode == "" {
		result.cardCreationMode = "POOL_ONLY"
	}

	if len(result.providers) == 0 && result.provider != "" {
		result.providers = []string{result.provider}
	}
	return result
}

func formatActivationAdmissionFailureLog(traceID, mode string, result activationGuardResult) string {
	poolID := sanitizeLogValue(firstNonEmpty(result.poolID, "unknown"))
	provider := sanitizeLogValue(firstNonEmpty(result.provider, "unknown"))
	routing := sanitizeLogValue(firstNonEmpty(result.routingStrategy, "unknown"))
	creationMode := sanitizeLogValue(firstNonEmpty(result.cardCreationMode, "unknown"))
	mode = sanitizeLogValue(firstNonEmpty(strings.TrimSpace(mode), "unknown"))
	providers := make([]string, 0, len(result.providers))
	for _, item := range result.providers {
		item = sanitizeLogValue(strings.ToUpper(strings.TrimSpace(item)))
		if item != "" {
			providers = append(providers, item)
		}
	}
	checkedProviders := strings.Join(providers, ",")
	if checkedProviders == "" {
		checkedProviders = "unknown"
	}
	return fmt.Sprintf("[充值准入] 任务创建失败：银行卡池暂无可用卡片 trace_id=%s pool_id=%s provider=%s checked_providers=%s routing=%s creation_mode=%s reason=%s available_cards=0 mode=%s", sanitizeLogValue(traceID), poolID, provider, checkedProviders, routing, creationMode, activationGuardReasonNoCardInventory, mode)
}

func sanitizeLogValue(value string) string {
	return strings.NewReplacer("\r", "", "\n", "", "\t", " ").Replace(strings.TrimSpace(value))
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
