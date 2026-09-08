package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const adminCardActivityLimit = 100

type cardPoolProviderInput struct {
	Provider string `json:"provider"`
	Enabled  *bool  `json:"enabled"`
	Priority *int   `json:"priority"`
	Weight   *int   `json:"weight"`
}

type cardPoolInput struct {
	Name             string                  `json:"name"`
	Type             string                  `json:"type"`
	UsageType        string                  `json:"usageType"`
	CardCreationMode string                  `json:"cardCreationMode"`
	Currency         string                  `json:"currency"`
	FundingCurrency  string                  `json:"fundingCurrency"`
	MerchantCurrency string                  `json:"merchantCurrency"`
	RoutingStrategy  string                  `json:"routingStrategy"`
	DefaultProvider  string                  `json:"defaultProvider"`
	Enabled          *bool                   `json:"enabled"`
	Providers        []cardPoolProviderInput `json:"providers"`
}

func (s *Server) adminCardPools(c *gin.Context) {
	var pools []models.CardPool
	if err := s.DB.Preload("Providers").Order("created_at ASC, id ASC").Find(&pools).Error; err != nil {
		fail(c, http.StatusInternalServerError, "读取银行卡池配置失败")
		return
	}
	providers := make([]gin.H, 0)
	if s.CardPools != nil && s.CardPools.Registry != nil {
		for _, name := range s.CardPools.Registry.Names() {
			provider, err := s.CardPools.Registry.Get(name)
			if err != nil {
				continue
			}
			providers = append(providers, gin.H{
				"provider":  name,
				"enabled":   boolConfigValue(s.configValue(cardProviderEnabledConfigKeys[name], "0")) == "1",
				"canCreate": provider.Supports(cardpool.CapabilityCreateCard),
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"pools": pools, "providers": providers})
}

// legacyKimooxCardBINs exposes only non-sensitive BIN metadata for the
// configuration screen. Provider-specific discovery stays behind the
// optional CardBINLister extension and never leaks into PaymentService.
func (s *Server) legacyKimooxCardBINs(c *gin.Context) {
	if s.CardPools == nil || s.CardPools.Registry == nil {
		fail(c, http.StatusServiceUnavailable, "银行卡 Provider 服务不可用")
		return
	}
	provider, err := s.CardPools.Registry.Get("KIMOOX")
	if err != nil {
		fail(c, http.StatusBadRequest, "KIMOOX Provider 不可用")
		return
	}
	lister, ok := provider.(cardpool.CardBINLister)
	if !ok {
		fail(c, http.StatusNotImplemented, "KIMOOX Provider 不支持 BIN 查询")
		return
	}
	bins, err := lister.ListCardBINs(c.Request.Context())
	if err != nil {
		writeCardProviderValidationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "provider": "KIMOOX", "bins": bins})
}

type adminCreateProviderCardInput struct {
	PoolID            string            `json:"poolId"`
	Provider          string            `json:"provider"`
	BusinessAccountID string            `json:"businessAccountId"`
	PaymentTaskID     string            `json:"paymentTaskId"`
	UsageType         string            `json:"usageType"`
	Amount            float64           `json:"amount"`
	Currency          string            `json:"currency"`
	IdempotencyKey    string            `json:"idempotencyKey"`
	CardholderName    string            `json:"cardholderName"`
	Metadata          map[string]string `json:"metadata"`
}

// adminCreateProviderCard is the admin-only pre-provisioning entry point.
// Provider adapters return a provider-neutral card metadata object; the
// endpoint deliberately never returns PAN, expiry, or CVC.
func (s *Server) adminCreateProviderCard(c *gin.Context) {
	if s.CardPools == nil {
		fail(c, http.StatusServiceUnavailable, "银行卡 Provider 服务不可用")
		return
	}
	var input adminCreateProviderCardInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	input.Provider = strings.ToUpper(strings.TrimSpace(input.Provider))
	if input.Provider == "" {
		input.Provider = strings.ToUpper(strings.TrimSpace(s.configValue("card_pool_default_provider", "")))
	}
	if input.Provider == "" {
		fail(c, http.StatusBadRequest, "请选择已启用的 Provider")
		return
	}
	if reader := s.CardPools.Config; reader != nil && boolConfigValue(reader.Value(cardProviderEnabledConfigKeys[input.Provider], "0")) != "1" {
		fail(c, http.StatusBadRequest, "Provider 未启用，请先在系统配置中启用")
		return
	}
	provider, err := s.CardPools.Registry.Get(input.Provider)
	if err != nil {
		fail(c, http.StatusBadRequest, "Provider 不支持")
		return
	}
	if !provider.Supports(cardpool.CapabilityCreateCard) {
		fail(c, http.StatusBadRequest, "该 Provider 不支持创建虚拟卡")
		return
	}
	if strings.EqualFold(input.Provider, "KIMOOX") && strings.EqualFold(s.configValue("kimoox_card_type", "PREPAID"), "PREPAID") && input.Amount <= 0 {
		fail(c, http.StatusBadRequest, "手动创建虚拟卡必须填写大于 0 的首充金额")
		return
	}
	card, err := s.CardPools.CreateCard(c.Request.Context(), cardpool.CreateCardRequest{
		PoolID: input.PoolID, Provider: input.Provider, BusinessAccountID: strings.TrimSpace(input.BusinessAccountID),
		PaymentTaskID: strings.TrimSpace(input.PaymentTaskID), UsageType: cardpool.UsageType(strings.ToUpper(strings.TrimSpace(input.UsageType))),
		Amount: input.Amount, Currency: strings.ToUpper(strings.TrimSpace(input.Currency)), IdempotencyKey: strings.TrimSpace(input.IdempotencyKey),
		CardholderName: strings.TrimSpace(input.CardholderName), Metadata: input.Metadata,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, cardpool.ErrProvisionInProgress) {
			status = http.StatusConflict
		}
		fail(c, status, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "card": card, "provider": card.Provider, "poolId": card.PoolID})
}

func (s *Server) createCardPool(c *gin.Context) {
	var input cardPoolInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	pool, providers, err := normalizeCardPoolInputWithRegistry(input, db.NewID("pool"), cardPoolRegistry(s))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&pool).Error; err != nil {
			return err
		}
		for _, provider := range providers {
			if err := tx.Create(&provider).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		fail(c, http.StatusConflict, "创建银行卡池失败")
		return
	}
	pool.Providers = providers
	c.JSON(http.StatusCreated, gin.H{"pool": pool})
}

func (s *Server) updateCardPool(c *gin.Context) {
	poolID := strings.TrimSpace(c.Param("id"))
	var input cardPoolInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	var pool models.CardPool
	if err := s.DB.Preload("Providers").Where("id = ?", poolID).First(&pool).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			fail(c, http.StatusNotFound, "银行卡池不存在")
			return
		}
		fail(c, http.StatusInternalServerError, "读取银行卡池失败")
		return
	}
	updated, providers, err := normalizeCardPoolInputWithRegistry(input, pool.ID, cardPoolRegistry(s))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	updates := map[string]any{
		"name": updated.Name, "type": updated.Type, "usage_type": updated.UsageType,
		"card_creation_mode": updated.CardCreationMode,
		"currency":           updated.Currency, "funding_currency": updated.FundingCurrency,
		"merchant_currency": updated.MerchantCurrency, "routing_strategy": updated.RoutingStrategy,
		"default_provider": updated.DefaultProvider,
	}
	if input.Enabled != nil {
		updates["enabled"] = *input.Enabled
	}
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&pool).Updates(updates).Error; err != nil {
			return err
		}
		providerNames := make([]string, 0, len(providers))
		for _, provider := range providers {
			providerNames = append(providerNames, provider.Provider)
		}
		// PATCH is a full pool configuration update from the admin UI. Remove
		// providers omitted by the submitted list so a disabled/removed provider
		// cannot remain eligible in the router after the save succeeds.
		if err := tx.Where("pool_id = ? AND provider NOT IN ?", pool.ID, providerNames).Delete(&models.CardPoolProvider{}).Error; err != nil {
			return err
		}
		for _, provider := range providers {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "pool_id"}, {Name: "provider"}},
				DoUpdates: clause.AssignmentColumns([]string{"enabled", "priority", "weight", "updated_at"}),
			}).Create(&provider).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		fail(c, http.StatusConflict, "保存银行卡池失败")
		return
	}
	s.DB.Preload("Providers").Where("id = ?", pool.ID).First(&pool)
	c.JSON(http.StatusOK, gin.H{"pool": pool})
}

func normalizeCardPoolInput(input cardPoolInput, poolID string) (models.CardPool, []models.CardPoolProvider, error) {
	return normalizeCardPoolInputWithRegistry(input, poolID, nil)
}

func normalizeCardPoolInputWithRegistry(input cardPoolInput, poolID string, registry *cardpool.ProviderRegistry) (models.CardPool, []models.CardPoolProvider, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 96 {
		return models.CardPool{}, nil, errInvalid("银行卡池名称不能为空且不能超过 96 个字符")
	}
	poolType := strings.ToUpper(strings.TrimSpace(input.Type))
	if poolType == "" {
		poolType = string(cardpool.PoolTypeLocal)
	}
	if poolType != string(cardpool.PoolTypeLocal) && poolType != string(cardpool.PoolTypeExternalAPI) {
		return models.CardPool{}, nil, errInvalid("银行卡池类型不支持")
	}
	usage := strings.ToUpper(strings.TrimSpace(input.UsageType))
	if usage == "" {
		usage = string(cardpool.UsageOneTime)
	}
	if usage != string(cardpool.UsageOneTime) && usage != string(cardpool.UsageRecurring) && usage != string(cardpool.UsageMultiUse) {
		return models.CardPool{}, nil, errInvalid("银行卡池用途不支持")
	}
	routing := strings.ToUpper(strings.TrimSpace(input.RoutingStrategy))
	if routing == "" {
		routing = string(cardpool.RoutingFixed)
	}
	if routing != string(cardpool.RoutingFixed) && routing != string(cardpool.RoutingPriority) && routing != string(cardpool.RoutingWeighted) && routing != string(cardpool.RoutingFailover) {
		return models.CardPool{}, nil, errInvalid("银行卡池路由策略不支持")
	}
	creationMode := cardpool.CardCreationMode(strings.ToUpper(strings.TrimSpace(input.CardCreationMode)))
	if creationMode == "" {
		creationMode = cardpool.CardCreationOnDemand
	}
	if creationMode != cardpool.CardCreationPoolOnly && creationMode != cardpool.CardCreationOnDemand {
		return models.CardPool{}, nil, errInvalid("银行卡池发卡模式不支持")
	}
	defaultProvider := strings.ToUpper(strings.TrimSpace(input.DefaultProvider))
	providers := make([]models.CardPoolProvider, 0, len(input.Providers))
	seenProviders := make(map[string]struct{}, len(input.Providers))
	enabledProviders := make(map[string]bool, len(input.Providers))
	for index, item := range input.Providers {
		provider := strings.ToUpper(strings.TrimSpace(item.Provider))
		if !cardProviderAvailable(registry, provider) {
			return models.CardPool{}, nil, errInvalid("银行卡池 Provider 不支持: " + provider)
		}
		if _, exists := seenProviders[provider]; exists {
			return models.CardPool{}, nil, errInvalid("银行卡池 Provider 不能重复: " + provider)
		}
		seenProviders[provider] = struct{}{}
		enabled, priority, weight := true, index+1, 100
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		if item.Priority != nil {
			priority = *item.Priority
		}
		if item.Weight != nil {
			weight = *item.Weight
		}
		if priority < 1 || weight < 0 {
			return models.CardPool{}, nil, errInvalid("Provider priority/weight 无效")
		}
		providers = append(providers, models.CardPoolProvider{ID: db.NewID("pool_provider"), PoolID: poolID, Provider: provider, Enabled: enabled, Priority: priority, Weight: weight})
		enabledProviders[provider] = enabled
		if defaultProvider == "" && enabled {
			defaultProvider = provider
		}
	}
	if defaultProvider == "" {
		defaultProvider = "LOCAL_TEXT"
	}
	if !cardProviderAvailable(registry, defaultProvider) {
		return models.CardPool{}, nil, errInvalid("默认 Provider 不支持: " + defaultProvider)
	}
	if len(providers) == 0 {
		providers = append(providers, models.CardPoolProvider{ID: db.NewID("pool_provider"), PoolID: poolID, Provider: defaultProvider, Enabled: true, Priority: 1, Weight: 100})
		enabledProviders[defaultProvider] = true
	} else {
		enabled := enabledProviders[defaultProvider]
		if _, exists := seenProviders[defaultProvider]; !exists {
			return models.CardPool{}, nil, errInvalid("默认 Provider 必须包含在 Provider 列表中")
		}
		if !enabled {
			return models.CardPool{}, nil, errInvalid("默认 Provider 必须处于启用状态")
		}
	}
	return models.CardPool{ID: poolID, Name: name, Type: poolType, UsageType: usage,
		Currency: strings.ToUpper(strings.TrimSpace(input.Currency)), FundingCurrency: strings.ToUpper(strings.TrimSpace(input.FundingCurrency)),
		MerchantCurrency: strings.ToUpper(strings.TrimSpace(input.MerchantCurrency)), RoutingStrategy: routing, DefaultProvider: defaultProvider,
		CardCreationMode: string(creationMode),
		Enabled:          input.Enabled == nil || *input.Enabled}, providers, nil
}

func cardPoolRegistry(s *Server) *cardpool.ProviderRegistry {
	if s == nil || s.CardPools == nil {
		return nil
	}
	return s.CardPools.Registry
}

func cardProviderAvailable(registry *cardpool.ProviderRegistry, value string) bool {
	if registry != nil {
		return registry.Has(value)
	}
	// Keep the package-level normalizer useful in isolated unit tests. Runtime
	// HTTP validation always uses the composition-root registry above, so adding
	// a provider does not require changing business code here.
	return knownCardProvider(value)
}

type adminCardRef struct {
	ID             string
	Provider       string
	ProviderCardID string
	PaymentCardID  string
	Last4          string
	Status         string
	PoolID         string
	UsageType      string
	Local          bool
}

func (s *Server) adminCardActivity(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		fail(c, http.StatusBadRequest, "缺少卡片 ID")
		return
	}
	if s.DB == nil {
		fail(c, http.StatusServiceUnavailable, "数据库不可用")
		return
	}
	card, found, err := s.lookupAdminCardRef(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, "读取卡片失败")
		return
	}
	if !found {
		fail(c, http.StatusNotFound, "卡片不存在")
		return
	}

	events := make([]gin.H, 0)
	transactions := make([]gin.H, 0)
	if card.PaymentCardID != "" || strings.TrimSpace(card.ProviderCardID) != "" {
		var records []models.CardProviderEvent
		if err := cardActivityScope(s.DB, card).Order("created_at DESC").Limit(adminCardActivityLimit).Find(&records).Error; err != nil {
			fail(c, http.StatusInternalServerError, "读取卡片事件失败")
			return
		}
		for _, record := range records {
			events = append(events, gin.H{
				"id":              record.ID,
				"eventType":       record.EventType,
				"eventTypeLabel":  cardEventTypeLabel(record.EventType),
				"status":          record.Status,
				"providerCardId":  record.ProviderCardID,
				"occurredAt":      record.OccurredAt,
				"occurredAtText":  legacyOptionalTimeString(record.OccurredAt),
				"processedAt":     record.ProcessedAt,
				"processedAtText": legacyOptionalTimeString(record.ProcessedAt),
			})
		}
		var rows []models.CardTransaction
		if err := cardActivityScope(s.DB, card).Order("created_at DESC").Limit(adminCardActivityLimit).Find(&rows).Error; err != nil {
			fail(c, http.StatusInternalServerError, "读取卡片交易失败")
			return
		}
		for _, row := range rows {
			transactions = append(transactions, gin.H{
				"id":                    row.ID,
				"providerTransactionId": row.ProviderTransactionID,
				"amount":                row.Amount,
				"currency":              row.Currency,
				"status":                row.Status,
				"statusLabel":           cardTransactionStatusLabel(row.Status),
				"type":                  row.Type,
				"merchantName":          row.MerchantName,
				"failureCode":           row.FailureCode,
				"occurredAt":            row.OccurredAt,
				"occurredAtText":        legacyOptionalTimeString(row.OccurredAt),
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"card": gin.H{
			"id":             card.ID,
			"provider":       card.Provider,
			"providerCardId": card.ProviderCardID,
			"last4":          card.Last4,
			"status":         card.Status,
			"poolId":         card.PoolID,
			"usageType":      card.UsageType,
			"local":          card.Local,
		},
		"events":       events,
		"transactions": transactions,
	})
}

func (s *Server) lookupAdminCardRef(id string) (adminCardRef, bool, error) {
	var payment models.PaymentCard
	err := s.DB.Where("id = ?", id).First(&payment).Error
	if err == nil {
		return adminCardRef{
			ID: payment.ID, Provider: payment.Provider, ProviderCardID: payment.ProviderCardID,
			PaymentCardID: payment.ID, Last4: payment.Last4, Status: payment.Status,
			PoolID: payment.PoolID, UsageType: payment.UsageType,
			Local: strings.EqualFold(payment.Provider, "LOCAL_TEXT") || strings.TrimSpace(payment.LocalCardAssetID) != "",
		}, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return adminCardRef{}, false, err
	}
	var asset models.CardAsset
	err = s.DB.Where("id = ?", id).First(&asset).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return adminCardRef{}, false, nil
	}
	if err != nil {
		return adminCardRef{}, false, err
	}
	ref := adminCardRef{
		ID: asset.ID, Provider: firstNonEmpty(asset.Provider, "LOCAL_TEXT"),
		Last4: asset.Last4, Status: asset.Status, PoolID: asset.PoolID, Local: true,
	}
	var linked models.PaymentCard
	linkErr := s.DB.Where("local_card_asset_id = ?", asset.ID).First(&linked).Error
	if linkErr == nil {
		ref.PaymentCardID = linked.ID
		ref.Provider = linked.Provider
		ref.ProviderCardID = linked.ProviderCardID
		if strings.TrimSpace(ref.Status) == "" {
			ref.Status = linked.Status
		}
		if strings.TrimSpace(ref.UsageType) == "" {
			ref.UsageType = linked.UsageType
		}
	} else if !errors.Is(linkErr, gorm.ErrRecordNotFound) {
		return adminCardRef{}, false, linkErr
	}
	return ref, true, nil
}

func cardActivityScope(database *gorm.DB, card adminCardRef) *gorm.DB {
	paymentID := strings.TrimSpace(card.PaymentCardID)
	provider := strings.TrimSpace(card.Provider)
	providerCardID := strings.TrimSpace(card.ProviderCardID)
	switch {
	case paymentID != "" && provider != "" && providerCardID != "":
		return database.Where("payment_card_id = ? OR (provider = ? AND provider_card_id = ?)", paymentID, provider, providerCardID)
	case paymentID != "":
		return database.Where("payment_card_id = ?", paymentID)
	case provider != "" && providerCardID != "":
		return database.Where("provider = ? AND provider_card_id = ?", provider, providerCardID)
	default:
		return database.Where("1 = 0")
	}
}

func cardEventTypeLabel(eventType string) string {
	switch strings.ToUpper(strings.TrimSpace(eventType)) {
	case "CARD_ISSUE.SUCCESS":
		return "开卡成功"
	case "CARD_ISSUE.FAILED":
		return "开卡失败"
	case "CARD_OPERATION.FREEZE_SUCCESS":
		return "冻结成功"
	case "CARD_OPERATION.UNFREEZE_SUCCESS":
		return "解冻成功"
	case "CARD_OPERATION.CANCEL_SUCCESS":
		return "销卡成功"
	case "CARD.RISK_CANCELLED":
		return "风控销卡"
	case "CARD_OPERATION.RECHARGE_SUCCESS":
		return "充值成功"
	case "CARD_OPERATION.WITHDRAW_SUCCESS":
		return "余额转出"
	case "CARD_OPERATION.LIMIT_CHANGE_SUCCESS":
		return "限额变更"
	case "CARD_OPERATION.REMARK_UPDATE_SUCCESS":
		return "备注更新"
	case "CARD_TRANSACTION.PROCESSING":
		return "交易处理中"
	case "CARD_TRANSACTION.AUTH_SUCCESS":
		return "授权成功"
	case "CARD_TRANSACTION.AUTH_FAILED":
		return "授权失败"
	case "CARD_TRANSACTION.SETTLED":
		return "已清算"
	case "CARD_TRANSACTION.REFUND_SUCCESS":
		return "退款成功"
	case "CARD_TRANSACTION.REVERSE_SUCCESS":
		return "撤销成功"
	case "CARD_TRANSACTION.CORRECTION":
		return "订单修正"
	case "CARD_3DS.OTP_RECEIVED":
		return "3DS 验证码"
	case "WEBHOOK_TEST":
		return "测试推送"
	default:
		if eventType = strings.TrimSpace(eventType); eventType != "" {
			return eventType
		}
		return "未知事件"
	}
}

func cardTransactionStatusLabel(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PROCESSING":
		return "处理中"
	case "AUTH_SUCCESS":
		return "授权成功"
	case "AUTH_FAILED":
		return "授权失败"
	case "SETTLED":
		return "已清算"
	case "REFUND_SUCCESS":
		return "退款成功"
	case "REVERSE_SUCCESS":
		return "撤销成功"
	case "CORRECTION":
		return "订单修正"
	default:
		if status = strings.TrimSpace(status); status != "" {
			return status
		}
		return "-"
	}
}

func knownCardProvider(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "LOCAL_TEXT", "AIRWALLEX", "STRIPE_ISSUING", "PHOTONPAY", "DOGPAY", "KIMOOX":
		return true
	default:
		return false
	}
}
