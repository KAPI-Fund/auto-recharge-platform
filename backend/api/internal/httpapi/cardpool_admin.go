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
	if input.Amount < 0 {
		fail(c, http.StatusBadRequest, "首充金额不能小于 0")
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

func knownCardProvider(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "LOCAL_TEXT", "AIRWALLEX", "STRIPE_ISSUING", "PHOTONPAY", "DOGPAY", "KIMOOX":
		return true
	default:
		return false
	}
}
