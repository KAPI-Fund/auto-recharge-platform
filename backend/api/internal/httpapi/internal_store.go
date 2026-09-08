package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const assetLockStaleAfter = 15 * time.Minute

func (s *Server) configValue(key, fallback string) string {
	if s == nil || s.DB == nil {
		return fallback
	}
	var value models.AppConfig
	result := s.DB.Where("key = ?", key).Limit(1).Find(&value)
	if result.Error != nil || result.RowsAffected == 0 || strings.TrimSpace(value.Value) == "" {
		return fallback
	}
	if value.IsSecret && strings.TrimSpace(s.Cfg.SessionEncryptionKey) != "" {
		if decrypted, decryptErr := s.decryptSessionValue(value.Value); decryptErr == nil {
			return decrypted
		}
		return fallback
	}
	return value.Value
}

func (s *Server) appendRuntimeLog(c *gin.Context) {
	var input struct {
		TaskID     string `json:"taskId"`
		JobKey     string `json:"jobKey"`
		TraceID    string `json:"traceId"`
		Level      string `json:"level"`
		Source     string `json:"source"`
		Text       string `json:"text"`
		WorkerID   string `json:"workerId"`
		LeaseToken string `json:"leaseToken"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.Text) == "" {
		fail(c, http.StatusBadRequest, "运行日志内容不能为空")
		return
	}
	level := strings.TrimSpace(input.Level)
	if level == "" {
		level = "stdout"
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = "worker"
	}
	text := strings.TrimSpace(input.Text)
	if len(text) > 32768 {
		text = text[:32768]
	}
	var row models.RuntimeLog
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		jobKey := strings.TrimSpace(input.JobKey)
		traceID := firstNonEmpty(strings.TrimSpace(input.TraceID), requestTraceID(c))
		if taskID := strings.TrimSpace(input.TaskID); taskID != "" {
			var task models.RechargeTask
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, "id = ?", taskID).Error; err != nil {
				return err
			}
			if err := validateTaskLease(&task, input.WorkerID, input.LeaseToken, time.Now()); err != nil {
				return err
			}
			jobKey = task.JobKey
			traceID = firstNonEmpty(task.TraceID, traceID)
		}
		row = models.RuntimeLog{
			ID: db.NewID("runtime"), JobKey: jobKey, TraceID: traceID,
			Level: level, Source: source, Text: text,
		}
		return tx.Create(&row).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, "任务不存在")
			return
		}
		if errors.Is(err, errTaskLeaseInput) {
			taskLeaseError(c, http.StatusBadRequest, "task_lease_required", err)
			return
		}
		if errors.Is(err, errTaskLeaseLost) {
			taskLeaseError(c, http.StatusConflict, "task_lease_lost", err)
			return
		}
		fail(c, http.StatusInternalServerError, "保存运行日志失败")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true, "traceId": row.TraceID, "trace_id": row.TraceID, "log": row})
}

func (s *Server) internalStore(c *gin.Context) {
	action := c.Param("action")
	var input map[string]any
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, "请求格式不正确")
			return
		}
	}
	if input == nil {
		input = map[string]any{}
	}

	var (
		payload any
		err     error
	)
	switch action {
	case "getPaymentRegion":
		payload = gin.H{"region": s.configValue("payment_region", "PH")}
	case "getAppConfigValue":
		key := stringValue(input, "key")
		payload = gin.H{"value": s.configValue(key, stringValue(input, "fallback"))}
	case "setAppConfigValue":
		key := strings.TrimSpace(stringValue(input, "key"))
		if key == "" || len(key) > 80 {
			err = errInvalid("配置键不能为空")
			break
		}
		err = s.upsertConfig(key, stringValue(input, "value"))
		payload = gin.H{"ok": err == nil}
	case "verifyCdkDetails":
		payload, err = s.internalVerifyCDK(stringValue(input, "code"))
	case "getActiveProxy":
		payload, err = s.claimActiveProxyPayload(c.Request.Context(), input)
	case "releaseProxy":
		err = s.unlockProxyAsset(firstNonEmpty(stringValue(input, "id"), stringValue(input, "proxyId"), stringValue(input, "proxyAssetId")))
		payload = gin.H{"ok": err == nil}
	case "recordProxyAttempt":
		err = s.recordProxyAttempt(firstNonEmpty(stringValue(input, "id"), stringValue(input, "proxyId")), firstNonEmpty(stringValue(input, "outcome"), stringValue(input, "result")))
		payload = gin.H{"ok": err == nil}
	case "hasAvailableCard":
		err = reclaimStaleCardLocks(s.DB)
		var count int64
		if err == nil {
			err = s.DB.Model(&models.CardAsset{}).
				Where("active = ? AND in_use = ?", true, false).
				Where("status NOT IN ?", []string{"报废", "已报废", "disabled"}).
				Where("cooldown_until IS NULL OR cooldown_until < ?", time.Now()).Count(&count).Error
		}
		payload = gin.H{"available": count > 0, "count": count}
	case "reserveCard":
		payload, err = s.internalReserveCardContext(c.Request.Context(), cardReservationInput{
			OwnerKey: stringValue(input, "ownerKey"), TaskID: stringValue(input, "taskId"), JobKey: stringValue(input, "jobKey"),
			PoolID: stringValue(input, "poolId"), BusinessAccountID: stringValue(input, "businessAccountId"),
			UsageType: stringValue(input, "usageType"), IdempotencyKey: stringValue(input, "idempotencyKey"),
			WorkerID: stringValue(input, "workerId"), LeaseToken: stringValue(input, "leaseToken"),
		})
	case "releaseCard":
		payload, err = s.releaseCardContext(c.Request.Context(), stringValue(input, "cardId"), stringValue(input, "allocationId"), stringValue(input, "taskId"), stringValue(input, "workerId"), stringValue(input, "leaseToken"))
	case "markCardExhausted":
		payload, err = s.markCardExhaustedContext(c.Request.Context(), stringValue(input, "cardId"), stringValue(input, "allocationId"), stringValue(input, "taskId"), stringValue(input, "workerId"), stringValue(input, "leaseToken"), stringValue(input, "failureCode"), stringValue(input, "failureMessage"))
	case "recordCardFailure":
		payload, err = s.recordCardFailureContext(c.Request.Context(), stringValue(input, "cardId"), stringValue(input, "allocationId"), stringValue(input, "taskId"), stringValue(input, "workerId"), stringValue(input, "leaseToken"), stringValue(input, "failureCode"), stringValue(input, "failureMessage"))
	case "recordCardUsage":
		payload, err = s.recordCardUsageContext(c.Request.Context(), stringValue(input, "cardId"), stringValue(input, "allocationId"), stringValue(input, "taskId"), stringValue(input, "workerId"), stringValue(input, "leaseToken"))
	case "settleCard":
		payload, err = s.settleCardContext(c.Request.Context(), stringValue(input, "cardId"), stringValue(input, "allocationId"), stringValue(input, "taskId"), stringValue(input, "workerId"), stringValue(input, "leaseToken"), stringValue(input, "failureCode"), stringValue(input, "failureMessage"))
	case "bindCardPaymentProfile":
		payload, err = s.internalBindCard(input)
	case "createBillingRecord":
		if _, ok := input["trace_id"]; !ok {
			input["trace_id"] = firstNonEmpty(stringValue(input, "traceId"), requestTraceID(c))
		}
		payload, err = s.internalCreateBilling(input)
	case "listTaxFreeAddresses":
		payload, err = s.internalListAddresses(stringValue(input, "region"), false)
	case "pickableTaxFreeAddresses":
		payload, err = s.internalListAddresses(stringValue(input, "region"), true)
	case "createTaxFreeAddress":
		payload, err = s.internalCreateAddress(input)
	case "getTaxFreeAddress":
		payload, err = s.internalGetAddress(stringValue(input, "id"))
	case "updateTaxFreeAddress":
		payload, err = s.internalUpdateAddress(input)
	case "deleteTaxFreeAddress":
		payload, err = s.internalDeleteAddress(stringValue(input, "id"))
	case "bindTaxFreeAddress":
		payload, err = s.internalBindAddress(stringValue(input, "id"), stringValue(input, "cardId"))
	case "clearUnboundTaxFreeAddresses":
		payload, err = s.internalClearAddresses(stringValue(input, "region"))
	case "getMaxBackgroundConcurrent":
		payload = gin.H{"value": maxInt(1, parseConfigInt(s.configValue("max_background_concurrent", "1"), 1))}
	case "getMaintenanceModeState":
		payload = gin.H{"enabled": s.configValue("maintenance_mode", "0") == "1", "drain": s.configValue("maintenance_mode_drain", "0") == "1"}
	case "reservePoolEmail":
		payload, err = s.internalReservePoolEmail(stringValue(input, "ownerKey"))
	case "releasePoolEmailReservation":
		payload, err = s.internalReleasePoolEmailReservation(stringValue(input, "id"))
	case "markPoolEmailRegistered":
		payload, err = s.internalMarkPoolEmailRegistered(stringValue(input, "id"))
	case "reserveRuntimeAssets":
		payload, err = s.internalReserveRuntimeAssets(c.Request.Context(), stringValue(input, "ownerKey"))
	case "releaseRuntimeAssets":
		payload, err = s.internalReleaseRuntimeAssets(stringValue(input, "phoneAssetId"), stringValue(input, "cardAssetId"), firstNonEmpty(stringValue(input, "proxyAssetId"), stringValue(input, "proxyId")))
	case "deletePhoneAsset":
		payload, err = s.internalDeletePhoneAsset(stringValue(input, "phone"))
	case "deleteCardAsset":
		payload, err = s.internalDeleteCardAsset(stringValue(input, "cardNumber"))
	case "incrementAssetSuccessCount":
		payload, err = s.internalIncrementAssetSuccessCount(stringValue(input, "phone"), stringValue(input, "cardNumber"))
	case "upsertPendingProduct":
		payload, err = s.internalUpsertPendingProduct(stringValue(input, "email"), stringValue(input, "token"))
	case "markProductReadyByEmail":
		payload, err = s.internalMarkProductReady(stringValue(input, "email"), stringValue(input, "filePath"), stringValue(input, "imapKey"))
	case "addProduct":
		payload, err = s.internalAddProduct(input)
	default:
		err = errInvalid("未知 Worker 存储操作")
	}
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, payload)
}

func (s *Server) internalRecordCardUsage(cardID string) (gin.H, error) {
	if strings.TrimSpace(cardID) == "" {
		return gin.H{"ok": false, "dailyUsageCount": 0, "cooledDown": false}, nil
	}
	var result gin.H
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var card models.CardAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&card, "id = ?", cardID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				result = gin.H{"dailyUsageCount": 0, "cooledDown": false}
				return nil
			}
			return err
		}
		now := time.Now()
		dailyCount := card.DailyUsageCount + 1
		resetAt := card.DailyUsageResetAt
		if resetAt == nil || resetAt.Before(now.Add(-24*time.Hour)) {
			dailyCount = 1
			resetAt = &now
		}
		cooledDown := dailyCount >= 3
		updates := map[string]any{
			"usage_count":          gorm.Expr("usage_count + 1"),
			"daily_usage_count":    dailyCount,
			"daily_usage_reset_at": resetAt,
			"last_used_at":         now,
			"in_use":               false,
			"locked_at":            nil,
			"locked_by":            "",
		}
		if cooledDown {
			updates["cooldown_until"] = now.Add(24 * time.Hour)
		}
		if err := tx.Model(&card).Updates(updates).Error; err != nil {
			return err
		}
		result = gin.H{"ok": true, "dailyUsageCount": dailyCount, "cooledDown": cooledDown}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func errInvalid(message string) error { return &storeError{message: message} }

type storeError struct{ message string }

func (e *storeError) Error() string { return e.message }

func stringValue(input map[string]any, key string) string {
	if value, ok := input[key]; ok && value != nil {
		return strings.TrimSpace(toString(value))
	}
	return ""
}

func validateAddressFields(input map[string]any, requireRegion bool) []string {
	errors := make([]string, 0, 6)
	validateString := func(key, emptyMessage string, maxLength int) {
		value, ok := input[key]
		text, textOK := value.(string)
		if !ok || !textOK || strings.TrimSpace(text) == "" {
			errors = append(errors, emptyMessage)
			return
		}
		if len(text) > maxLength {
			errors = append(errors, fmt.Sprintf("%s 长度不能超过 %d 字符", key, maxLength))
		}
	}

	validateString("line1", "line1 不能为空", 200)
	validateString("city", "city 不能为空", 100)
	validateString("state", "state 不能为空", 100)
	validateString("postal_code", "postal_code 不能为空", 20)

	country, countryOK := input["country"].(string)
	if !countryOK || strings.TrimSpace(country) == "" {
		errors = append(errors, "country 不能为空")
	} else if !isUpperAlpha2(country) {
		errors = append(errors, "country 必须是恰好 2 位大写字母 (ISO 3166-1 alpha-2)")
	}

	if requireRegion {
		region, regionOK := input["region"].(string)
		if !regionOK || strings.TrimSpace(region) == "" {
			errors = append(errors, "region 不能为空")
		}
	}
	return errors
}

var taxFreeUSStateNames = map[string]string{
	"oregon":        "Oregon",
	"or":            "Oregon",
	"delaware":      "Delaware",
	"de":            "Delaware",
	"montana":       "Montana",
	"mt":            "Montana",
	"new hampshire": "New Hampshire",
	"nh":            "New Hampshire",
	"alaska":        "Alaska",
	"ak":            "Alaska",
}

// normalizeAddressInput is the server-side equivalent of the legacy form
// normalizer. The browser submits raw form values; persistence and worker
// consumers receive one canonical representation from Go.
func normalizeAddressInput(input map[string]any) map[string]any {
	normalized := make(map[string]any, len(input))
	for key, value := range input {
		normalized[key] = value
	}
	for _, key := range []string{"line1", "city", "state", "postal_code", "country", "region"} {
		if _, ok := normalized[key]; ok {
			normalized[key] = stringValue(normalized, key)
		}
	}
	if country := stringValue(normalized, "country"); country != "" {
		normalized["country"] = strings.ToUpper(country)
	}
	if region := stringValue(normalized, "region"); region != "" {
		normalized["region"] = strings.ToUpper(region)
	}
	if state := strings.ToLower(stringValue(normalized, "state")); state != "" {
		if canonical, ok := taxFreeUSStateNames[state]; ok {
			normalized["state"] = canonical
		}
	}
	return normalized
}

func isUpperAlpha2(value string) bool {
	if len(value) != 2 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func toString(value any) string {
	switch item := value.(type) {
	case string:
		return item
	case float64:
		return strconv.FormatFloat(item, 'f', -1, 64)
	case int:
		return strconv.Itoa(item)
	case bool:
		return strconv.FormatBool(item)
	default:
		return ""
	}
}

func (s *Server) upsertConfig(key, value string) error {
	if s == nil || s.DB == nil {
		return errors.New("配置数据库不可用")
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		return s.upsertConfigTx(tx, key, value)
	})
}

func (s *Server) upsertConfigBatch(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	if s == nil || s.DB == nil {
		return errors.New("配置数据库不可用")
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := s.upsertConfigTx(tx, key, values[key]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Server) upsertConfigTx(tx *gorm.DB, key, value string) error {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	secret := isSensitiveConfigKey(key)
	storedValue := value
	if secret && strings.TrimSpace(s.Cfg.SessionEncryptionKey) != "" {
		ciphertext, err := security.Encrypt(value, s.Cfg.SessionEncryptionKey)
		if err != nil {
			return err
		}
		storedValue = ciphertext
	}
	updateValues := map[string]any{
		"value":      storedValue,
		"updated_at": time.Now(),
	}
	if secret && strings.TrimSpace(s.Cfg.SessionEncryptionKey) != "" {
		updateValues["is_secret"] = true
	}
	config := &models.AppConfig{Key: key, Value: storedValue, IsSecret: secret && strings.TrimSpace(s.Cfg.SessionEncryptionKey) != ""}
	query := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(updateValues),
	})
	// Keep the legacy SQL shape for isolated tests and installations that have
	// not configured an encryption key yet. Production config always has one.
	if !config.IsSecret {
		return query.Omit("is_secret").Create(config).Error
	}
	return query.Create(config).Error
}

func (s *Server) internalVerifyCDK(code string) (gin.H, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, errInvalid("缺少 CDK")
	}
	var cdk models.CDK
	if err := s.DB.Preload("Plan").Where("code = ?", code).First(&cdk).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return gin.H{"cdk": nil}, nil
		}
		return nil, err
	}
	return gin.H{"cdk": gin.H{
		"id": cdk.ID, "cdk_code": cdk.Code, "code": cdk.Code, "type": cdk.Type,
		"plan_type": cdk.PlanType, "used_at": cdk.UsedAt, "cooldown_until": cdk.CooldownUntil,
		"fail_count": cdk.FailCount, "is_active": cdk.Status != models.CDKDisabled,
		"country": cdk.Plan.Country, "payment_region": cdk.Plan.Country,
		"currency": regionCurrency(cdk.Plan.Country),
	}}, nil
}

type cardReservationInput struct {
	OwnerKey          string
	TaskID            string
	JobKey            string
	PoolID            string
	BusinessAccountID string
	UsageType         string
	IdempotencyKey    string
	WorkerID          string
	LeaseToken        string
}

func (s *Server) internalReserveCard(ownerKey string) (gin.H, error) {
	return s.internalReserveCardContext(context.Background(), cardReservationInput{OwnerKey: ownerKey})
}

func (s *Server) internalReserveCardContext(ctx context.Context, input cardReservationInput) (gin.H, error) {
	ownerKey := strings.TrimSpace(input.OwnerKey)
	if ownerKey == "" {
		ownerKey = "worker"
	}
	if s.CardPools == nil {
		return s.internalReserveCardLegacy(ownerKey)
	}

	task, err := s.findCardReservationTask(ctx, input)
	if err != nil {
		return nil, err
	}
	request := cardpool.AcquireCardRequest{
		OwnerKey:          ownerKey,
		PoolID:            strings.TrimSpace(input.PoolID),
		BusinessAccountID: strings.TrimSpace(input.BusinessAccountID),
		UsageType:         cardpool.UsageType(strings.ToUpper(strings.TrimSpace(input.UsageType))),
		IdempotencyKey:    strings.TrimSpace(input.IdempotencyKey),
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = "owner:" + ownerKey
	}
	if task != nil {
		// Task-owned values are authoritative. A Worker cannot change its card
		// pool or recurring account by sending a different bridge payload.
		request.PaymentTaskID = task.ID
		request.PoolID = task.PoolID
		request.BusinessAccountID = task.BusinessAccountID
		request.UsageType = cardpool.UsageType(task.UsageType)
		if strings.TrimSpace(input.IdempotencyKey) == "" {
			request.IdempotencyKey = "task:" + task.ID
		}
		if amount, currency := s.prepaidAmountForTask(ctx, task); amount > 0 {
			request.Amount = amount
			request.Currency = currency
		}
	}

	result, err := s.CardPools.AcquireCard(ctx, request)
	if errors.Is(err, cardpool.ErrNoAvailableCard) {
		return gin.H{"card": nil}, nil
	}
	if err != nil {
		return nil, err
	}
	if task != nil {
		if err := s.attachCardAllocationToTask(ctx, task.ID, result, input.WorkerID, input.LeaseToken); err != nil {
			_ = s.CardPools.ReleaseCardForAllocation(ctx, result.Card.InternalCardID, result.AllocationID)
			return nil, err
		}
	}
	details, err := s.CardPools.GetSensitiveCardDetails(ctx, result.Card.InternalCardID)
	if err != nil {
		_ = s.CardPools.ReleaseCardForAllocation(ctx, result.Card.InternalCardID, result.AllocationID)
		return nil, err
	}
	expiry := fmt.Sprintf("%02d/%02d", details.ExpiryMonth, details.ExpiryYear%100)
	// Keep the legacy sensitive-card field names, but also carry the
	// provider-neutral allocation metadata in the card object. The original
	// Worker consumes result.card directly; if these fields exist only on the
	// outer bridge response, billing records lose provider/pool attribution.
	card := gin.H{
		"id": result.Card.InternalCardID, "card_number": details.CardNumber, "card_expiry": expiry,
		"card_cvc": details.CVC, "card_holder": details.CardholderName, "last4": result.Card.Last4,
		"provider": result.Card.Provider, "provider_card_id": result.Card.ProviderCardID,
		"providerCardId": result.Card.ProviderCardID, "pool_id": result.Card.PoolID, "poolId": result.Card.PoolID,
		"allocationId": result.AllocationID, "allocation_id": result.AllocationID,
	}
	return gin.H{
		"card":     card,
		"provider": result.Provider, "provider_card_id": result.Card.ProviderCardID,
		"providerCardId": result.Card.ProviderCardID, "pool_id": result.Card.PoolID, "poolId": result.Card.PoolID,
		"allocationId": result.AllocationID, "allocation_id": result.AllocationID,
	}, nil
}

func (s *Server) prepaidAmountForTask(ctx context.Context, task *models.RechargeTask) (float64, string) {
	if s == nil || task == nil {
		return 0, ""
	}
	mode := strings.ToUpper(strings.TrimSpace(s.configValue("kimoox_prepaid_amount_mode", "PLAN_PLUS_5")))
	if mode == "FIXED" {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(s.configValue("kimoox_prepaid_recharge_amount", "")), 64)
		if err != nil || parsed <= 0 {
			return 0, ""
		}
		return parsed, "USD"
	}
	if s.DB == nil || strings.TrimSpace(task.PlanID) == "" {
		return 0, ""
	}
	var plan models.Plan
	if err := s.DB.WithContext(ctx).Where("id = ?", task.PlanID).First(&plan).Error; err != nil {
		return 0, ""
	}
	amount, ok := db.PrepaidRechargeUSDForPlan(plan)
	if !ok {
		return 0, ""
	}
	return amount, "USD"
}

func (s *Server) findCardReservationTask(ctx context.Context, input cardReservationInput) (*models.RechargeTask, error) {
	taskID := strings.TrimSpace(input.TaskID)
	if taskID != "" {
		var task models.RechargeTask
		if err := s.DB.WithContext(ctx).Where("id = ?", taskID).First(&task).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("任务不存在")
			}
			return nil, err
		}
		if strings.TrimSpace(input.WorkerID) == "" || strings.TrimSpace(input.LeaseToken) == "" {
			return nil, errTaskLeaseInput
		}
		if err := validateTaskLease(&task, input.WorkerID, input.LeaseToken, time.Now()); err != nil {
			return nil, err
		}
		if jobKey := strings.TrimSpace(input.JobKey); jobKey != "" && jobKey != task.JobKey {
			return nil, fmt.Errorf("任务 JobKey 不匹配")
		}
		return &task, nil
	}

	jobKeys := []string{strings.TrimSpace(input.JobKey), strings.TrimSpace(input.OwnerKey), strings.TrimPrefix(strings.TrimSpace(input.OwnerKey), "gptapi_")}
	for _, jobKey := range jobKeys {
		if jobKey == "" {
			continue
		}
		var task models.RechargeTask
		if result := s.DB.WithContext(ctx).Where("job_key = ?", jobKey).First(&task); result.Error == nil {
			return &task, nil
		}
	}
	return nil, nil
}

func (s *Server) attachCardAllocationToTask(ctx context.Context, taskID string, result cardpool.AllocationResult, workerID, leaseToken string) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task models.RechargeTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", taskID).First(&task).Error; err != nil {
			return err
		}
		if strings.TrimSpace(workerID) != "" || strings.TrimSpace(leaseToken) != "" {
			if err := validateTaskLease(&task, workerID, leaseToken, time.Now()); err != nil {
				return err
			}
		}
		return tx.Model(&task).Updates(map[string]any{
			"pool_id": result.Card.PoolID, "payment_card_id": result.Card.InternalCardID,
			"card_allocation_id": result.AllocationID, "card_last4": result.Card.Last4,
			"card_provider": result.Card.Provider, "card_provider_card_id": result.Card.ProviderCardID,
		}).Error
	})
}

func (s *Server) internalReserveCardLegacy(ownerKey string) (gin.H, error) {
	if ownerKey == "" {
		ownerKey = "worker"
	}
	var card models.CardAsset
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := reclaimStaleCardLocks(tx); err != nil {
			return err
		}
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("active = ? AND status = ?", true, "正常").
			Where("in_use = ? OR locked_at IS NULL OR locked_at < ?", false, time.Now().Add(-assetLockStaleAfter)).
			Where("cooldown_until IS NULL OR cooldown_until < ?", time.Now()).
			Order("usage_count ASC, COALESCE(last_used_at, '1970-01-01') ASC, id ASC").First(&card)
		if query.Error != nil {
			return query.Error
		}
		now := time.Now()
		return tx.Model(&card).Updates(map[string]any{"in_use": true, "locked_at": now, "locked_by": ownerKey}).Error
	})
	if err == gorm.ErrRecordNotFound {
		return gin.H{"card": nil}, nil
	}
	if err != nil {
		return nil, err
	}
	number, err := s.decryptSessionValue(card.CardNumberCiphertext)
	if err != nil {
		_ = s.DB.Model(&models.CardAsset{}).Where("id = ?", card.ID).Updates(map[string]any{"in_use": false, "locked_at": nil, "locked_by": ""}).Error
		return nil, err
	}
	expiry, err := s.decryptSessionValue(card.ExpiryCiphertext)
	if err != nil {
		_ = s.DB.Model(&models.CardAsset{}).Where("id = ?", card.ID).Updates(map[string]any{"in_use": false, "locked_at": nil, "locked_by": ""}).Error
		return nil, err
	}
	cvc, err := s.decryptSessionValue(card.CVVCiphertext)
	if err != nil {
		_ = s.DB.Model(&models.CardAsset{}).Where("id = ?", card.ID).Updates(map[string]any{"in_use": false, "locked_at": nil, "locked_by": ""}).Error
		return nil, err
	}
	return gin.H{"card": gin.H{"id": card.ID, "card_number": number, "card_expiry": expiry, "card_cvc": cvc, "card_holder": card.Holder, "last4": card.Last4}}, nil
}

func (s *Server) releaseCardContext(ctx context.Context, cardID, allocationID, taskID, workerID, leaseToken string) (gin.H, error) {
	if s.CardPools != nil {
		if err := s.validateCardReservationTask(ctx, taskID, workerID, leaseToken); err != nil {
			return nil, err
		}
		var card models.PaymentCard
		if result := s.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(cardID)).First(&card); result.Error == nil {
			err := s.CardPools.ReleaseCardForAllocation(ctx, card.ID, allocationID)
			return gin.H{"ok": err == nil}, err
		}
	}
	result := s.DB.Model(&models.CardAsset{}).Where("id = ?", cardID).Updates(map[string]any{"in_use": false, "locked_at": nil, "locked_by": ""})
	return gin.H{"ok": result.Error == nil && result.RowsAffected > 0}, result.Error
}

func (s *Server) markCardExhaustedContext(ctx context.Context, cardID, allocationID, taskID, workerID, leaseToken, failureCode, failureMessage string) (gin.H, error) {
	if s.CardPools != nil {
		if err := s.validateCardReservationTask(ctx, taskID, workerID, leaseToken); err != nil {
			return nil, err
		}
		var card models.PaymentCard
		if result := s.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(cardID)).First(&card); result.Error == nil {
			err := s.CardPools.MarkCardExhaustedForAllocation(ctx, card.ID, allocationID)
			failureErr := s.CardPools.RecordCardFailure(ctx, card.ID, allocationID, taskID, failureCode, failureMessage)
			return gin.H{"ok": err == nil && failureErr == nil}, errors.Join(err, failureErr)
		}
	}
	result := s.DB.Model(&models.CardAsset{}).Where("id = ?", cardID).Updates(map[string]any{"status": "报废", "active": false, "in_use": false, "locked_at": nil, "locked_by": ""})
	return gin.H{"ok": result.Error == nil && result.RowsAffected > 0}, result.Error
}

func (s *Server) recordCardFailureContext(ctx context.Context, cardID, allocationID, taskID, workerID, leaseToken, failureCode, failureMessage string) (gin.H, error) {
	if s.CardPools != nil {
		if err := s.validateCardReservationTask(ctx, taskID, workerID, leaseToken); err != nil {
			return nil, err
		}
		var card models.PaymentCard
		if result := s.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(cardID)).First(&card); result.Error == nil {
			err := s.CardPools.RecordCardFailure(ctx, card.ID, allocationID, taskID, failureCode, failureMessage)
			return gin.H{"ok": err == nil}, err
		}
	}
	return gin.H{"ok": true}, nil
}

func (s *Server) recordCardUsageContext(ctx context.Context, cardID, allocationID, taskID, workerID, leaseToken string) (gin.H, error) {
	if s.CardPools != nil {
		if err := s.validateCardReservationTask(ctx, taskID, workerID, leaseToken); err != nil {
			return nil, err
		}
		var card models.PaymentCard
		if result := s.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(cardID)).First(&card); result.Error == nil {
			stats, err := s.CardPools.RecordUsageForAllocation(ctx, card.ID, allocationID)
			return gin.H{"ok": err == nil, "dailyUsageCount": stats.DailyUsageCount, "cooledDown": stats.CooledDown}, err
		}
	}
	return s.internalRecordCardUsage(cardID)
}

// settleCardContext is the single Worker-facing closeout command for a
// recharge card. It records an optional failure, consumes the card exactly
// once, and always attempts release/cancellation afterward. The card remains
// unavailable when Provider cancellation fails because the card-pool service
// persists a pending compensation row for retry.
func (s *Server) settleCardContext(ctx context.Context, cardID, allocationID, taskID, workerID, leaseToken, failureCode, failureMessage string) (gin.H, error) {
	if s.CardPools != nil {
		if err := s.validateCardReservationTask(ctx, taskID, workerID, leaseToken); err != nil {
			return nil, err
		}
		var card models.PaymentCard
		if result := s.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(cardID)).First(&card); result.Error == nil {
			settled, err := s.CardPools.SettleCardForAllocation(ctx, card.ID, allocationID, taskID, failureCode, failureMessage)
			payload := gin.H{
				"ok":       err == nil,
				"cardId":   settled.InternalCardID,
				"status":   string(settled.Status),
				"provider": settled.Provider,
				"poolId":   settled.PoolID,
			}
			if err != nil {
				// Settlement failures are operationally recoverable: the card has
				// already been made unavailable and Provider cancellation is queued
				// for retry. Return HTTP 200 with an explicit result so the Worker
				// does not turn a successful recharge into a false task failure.
				payload["error"] = err.Error()
				return payload, nil
			}
			return payload, nil
		}
	}
	// Legacy CardAsset fallback is kept for old installations that have not
	// initialized the normalized provider-neutral card tables yet.
	if strings.TrimSpace(failureCode) != "" || strings.TrimSpace(failureMessage) != "" {
		// The legacy table has no separate allocation/failure ledger. The
		// terminal status is the authoritative closeout marker in this branch.
		_ = s.DB.Model(&models.CardAsset{}).Where("id = ?", cardID).Updates(map[string]any{"status": "已报废", "active": false})
	}
	result := s.DB.Model(&models.CardAsset{}).Where("id = ?", cardID).Updates(map[string]any{"status": "已报废", "active": false, "in_use": false, "locked_at": nil, "locked_by": ""})
	return gin.H{"ok": result.Error == nil && result.RowsAffected > 0, "status": "已报废"}, result.Error
}

func (s *Server) validateCardReservationTask(ctx context.Context, taskID, workerID, leaseToken string) error {
	if strings.TrimSpace(taskID) == "" {
		return nil
	}
	var task models.RechargeTask
	if err := s.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(taskID)).First(&task).Error; err != nil {
		return err
	}
	return validateTaskLease(&task, workerID, leaseToken, time.Now())
}

func reclaimStaleCardLocks(query *gorm.DB) error {
	_, err := releaseCardAssetLocks(query, true)
	return err
}

func (s *Server) internalBindCard(input map[string]any) (gin.H, error) {
	profile, _ := input["profile"].(map[string]any)
	address, _ := profile["address"].(map[string]any)
	updates := map[string]any{
		"payment_holder_name":    stringValue(profile, "holderName"),
		"payment_address_line1":  stringValue(address, "line1"),
		"payment_address_city":   stringValue(address, "city"),
		"payment_address_state":  stringValue(address, "state"),
		"payment_address_postal": stringValue(address, "postal_code"),
		"payment_address_id":     stringValue(address, "id"),
	}
	result := s.DB.Model(&models.CardAsset{}).Where("id = ?", stringValue(input, "cardId")).Updates(updates)
	return gin.H{"ok": result.Error == nil && result.RowsAffected > 0}, result.Error
}

func (s *Server) internalCreateBilling(input map[string]any) (gin.H, error) {
	data, _ := input["data"].(map[string]any)
	amount := 0.0
	if value, ok := data["amount"].(float64); ok {
		amount = value
	} else if value, ok := data["amount"].(string); ok {
		amount, _ = strconv.ParseFloat(strings.TrimSpace(value), 64)
	}
	cardNumber := stringValue(data, "card_number")
	cardLast4 := stringValue(data, "card_last4")
	if cardLast4 == "" && len(cardNumber) >= 4 {
		cardLast4 = cardNumber[len(cardNumber)-4:]
	}
	planType := normalizePlanType(stringValue(data, "plan_type"))
	var plan models.Plan
	if err := s.DB.Where("code = ? AND active = ?", planType, true).First(&plan).Error; err != nil {
		return nil, fmt.Errorf("套餐不存在: %w", err)
	}
	paymentTime := time.Now()
	if rawPaymentTime := stringValue(data, "payment_time"); rawPaymentTime != "" {
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
			if parsed, parseErr := time.Parse(layout, rawPaymentTime); parseErr == nil {
				paymentTime = parsed
				break
			}
		}
	}
	currency := stringValue(data, "currency")
	if currency == "" {
		currency = "USD"
	}
	record := models.BillingRecord{
		ID: db.NewID("billing"), TaskID: stringValue(data, "task_id"), TraceID: stringValue(data, "trace_id"), PaymentTime: paymentTime, CardLast4: cardLast4,
		Amount: amount, Currency: currency, PlanID: plan.ID,
		PlanType: db.PlanTypeForPlan(plan), StripeSessionID: stringValue(data, "stripe_session_id"), CDKCode: stringValue(data, "cdk_code"),
		Email: stringValue(data, "email"), Status: stringValue(data, "status"), ErrorCode: stringValue(data, "error_code"),
		ErrorMessage: stringValue(data, "error_message"), UpstreamOrderID: stringValue(data, "upstream_order_id"),
	}
	if record.Status == "" {
		record.Status = "success"
	}
	if err := s.DB.Create(&record).Error; err != nil {
		return nil, err
	}
	return gin.H{"ok": true, "id": record.ID}, nil
}

func (s *Server) internalListAddresses(region string, pickable bool) (gin.H, error) {
	query := s.DB.Where("region = ? AND active = ?", strings.ToUpper(strings.TrimSpace(region)), true)
	if pickable {
		query = query.Where("bound_card_id = '' OR bound_card_id IS NULL")
	}
	var addresses []models.TaxFreeAddress
	if err := query.Order("bound_card_id ASC, id DESC").Find(&addresses).Error; err != nil {
		return nil, err
	}
	result := make([]gin.H, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, addressResponse(address))
	}
	return gin.H{"addresses": result, "summary": addressSummary(addresses)}, nil
}

func addressResponse(address models.TaxFreeAddress) gin.H {
	isBound := strings.TrimSpace(address.BoundCardID) != ""
	return gin.H{"id": address.ID, "region": address.Region, "line1": address.Line1, "city": address.City, "state": address.State, "postal_code": address.PostalCode, "country": address.Country, "is_active": address.Active, "is_bound": isBound, "can_edit": !isBound, "can_delete": !isBound, "status_label": map[bool]string{true: "已绑定", false: "未绑定"}[isBound], "status_tone": map[bool]string{true: "success", false: "neutral"}[isBound], "bound_card_id": address.BoundCardID, "bound_at": address.BoundAt, "created_at": address.CreatedAt, "updated_at": address.UpdatedAt}
}

func addressSummary(addresses []models.TaxFreeAddress) gin.H {
	bound := 0
	for _, address := range addresses {
		if strings.TrimSpace(address.BoundCardID) != "" {
			bound++
		}
	}
	return gin.H{"total": len(addresses), "bound": bound, "unbound": len(addresses) - bound}
}

func (s *Server) internalCreateAddress(input map[string]any) (gin.H, error) {
	input = normalizeAddressInput(input)
	region := strings.TrimSpace(stringValue(input, "region"))
	if errors := validateAddressFields(input, true); len(errors) > 0 {
		return addressValidationFailure(errors), nil
	}
	address := models.TaxFreeAddress{ID: db.NewID("address"), Region: strings.ToUpper(region), Line1: stringValue(input, "line1"), City: stringValue(input, "city"), State: stringValue(input, "state"), PostalCode: stringValue(input, "postal_code"), Country: stringValue(input, "country"), Active: true}
	if err := s.DB.Create(&address).Error; err != nil {
		return nil, err
	}
	return gin.H{"success": true, "id": address.ID, "address": addressResponse(address)}, nil
}

func (s *Server) internalGetAddress(id string) (gin.H, error) {
	var address models.TaxFreeAddress
	if err := s.DB.Where("id = ? AND active = ?", id, true).First(&address).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return gin.H{"address": nil}, nil
		}
		return nil, err
	}
	return gin.H{"address": addressResponse(address)}, nil
}

func (s *Server) internalUpdateAddress(input map[string]any) (gin.H, error) {
	var address models.TaxFreeAddress
	result := s.DB.Where("id = ? AND active = ?", stringValue(input, "id"), true).First(&address)
	if result.Error == gorm.ErrRecordNotFound {
		return gin.H{"success": false, "error": "地址模板不存在"}, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	if strings.TrimSpace(address.BoundCardID) != "" {
		return addressMutationFailure("编辑"), nil
	}

	merged := map[string]any{
		"line1":       address.Line1,
		"city":        address.City,
		"state":       address.State,
		"postal_code": address.PostalCode,
		"country":     address.Country,
	}
	for _, key := range []string{"line1", "city", "state", "postal_code", "country"} {
		if _, ok := input[key]; ok {
			merged[key] = input[key]
		}
	}
	merged = normalizeAddressInput(merged)
	if errors := validateAddressFields(merged, false); len(errors) > 0 {
		return addressValidationFailure(errors), nil
	}

	updates := map[string]any{
		"line1":       stringValue(merged, "line1"),
		"city":        stringValue(merged, "city"),
		"state":       stringValue(merged, "state"),
		"postal_code": stringValue(merged, "postal_code"),
		"country":     stringValue(merged, "country"),
	}
	result = s.DB.Model(&models.TaxFreeAddress{}).Where("id = ? AND active = ?", address.ID, true).Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return gin.H{"success": false, "error": "地址模板不存在"}, nil
	}
	return gin.H{"success": true}, nil
}

func addressValidationFailure(details []string) gin.H {
	message := "字段校验失败"
	if len(details) > 0 {
		message += "：" + strings.Join(details, "；")
	}
	return gin.H{"success": false, "error": message, "message": message, "details": details}
}

func addressMutationFailure(action string) gin.H {
	message := "已绑定地址不可" + action
	return gin.H{"success": false, "error": message, "message": message}
}

func (s *Server) internalDeleteAddress(id string) (gin.H, error) {
	var address models.TaxFreeAddress
	result := s.DB.Where("id = ? AND active = ?", id, true).First(&address)
	if result.Error == gorm.ErrRecordNotFound {
		return gin.H{"success": false, "error": "地址模板不存在"}, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	if strings.TrimSpace(address.BoundCardID) != "" {
		return addressMutationFailure("删除"), nil
	}
	result = s.DB.Model(&models.TaxFreeAddress{}).Where("id = ? AND active = ? AND (bound_card_id = '' OR bound_card_id IS NULL)", id, true).Update("active", false)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return gin.H{"success": false, "error": "地址模板不存在"}, nil
	}
	return gin.H{"success": true}, nil
}

func (s *Server) internalBindAddress(id, cardID string) (gin.H, error) {
	cardID = strings.TrimSpace(cardID)
	if cardID == "" {
		return gin.H{"success": false, "error": "卡片 ID 不能为空"}, nil
	}
	result := s.DB.Model(&models.TaxFreeAddress{}).Where("id = ? AND active = ?", id, true).Updates(map[string]any{"bound_card_id": cardID, "bound_at": time.Now()})
	if result.Error != nil {
		return nil, result.Error
	}
	return gin.H{"success": result.RowsAffected > 0}, nil
}

func (s *Server) internalClearAddresses(region string) (gin.H, error) {
	result := s.DB.Model(&models.TaxFreeAddress{}).Where("region = ? AND active = ? AND (bound_card_id = '' OR bound_card_id IS NULL)", strings.ToUpper(region), true).Update("active", false)
	return gin.H{"success": result.Error == nil, "count": result.RowsAffected}, result.Error
}

func hashProxy(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func normalizeProxy(value string) (string, string, string, string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return "", "", "", "", errInvalid("代理 URL 无效")
	}
	return value, strings.ToLower(parsed.Scheme), parsed.Host, hashProxy(value), nil
}
