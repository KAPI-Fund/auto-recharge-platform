package cardpool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	DB       *gorm.DB
	Registry *ProviderRegistry
	Router   *CardProviderRouter
	Config   ConfigReader
	Now      func() time.Time
}

func NewService(database *gorm.DB, registry *ProviderRegistry, config ConfigReader) *Service {
	if registry == nil {
		registry = NewProviderRegistry()
	}
	return &Service{
		DB: database, Registry: registry, Router: NewCardProviderRouter(registry), Config: config,
		Now: time.Now,
	}
}

// ValidateConfiguration checks enabled providers at startup. Provider health
// remains a separate, cached network concern and is not called here.
func (s *Service) ValidateConfiguration() error {
	if s == nil || s.Registry == nil {
		return nil
	}
	for _, name := range s.Registry.Names() {
		provider, err := s.Registry.Get(name)
		if err != nil {
			return err
		}
		if validator, ok := provider.(ConfigValidator); ok {
			if err := validator.ValidateConfiguration(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func normalizeUsage(value UsageType, fallback string) UsageType {
	if value != "" {
		return value
	}
	switch strings.ToUpper(strings.TrimSpace(fallback)) {
	case string(UsageOneTime):
		return UsageOneTime
	case string(UsageRecurring):
		return UsageRecurring
	default:
		return UsageMultiUse
	}
}

func normalizeCardCreationMode(value CardCreationMode, fallback string) CardCreationMode {
	if value == CardCreationPoolOnly || value == CardCreationOnDemand {
		return value
	}
	switch strings.ToUpper(strings.TrimSpace(fallback)) {
	case string(CardCreationOnDemand):
		return CardCreationOnDemand
	default:
		return CardCreationPoolOnly
	}
}

// normalizeRechargeUsage is the platform invariant for recharge cards:
// every card is allowed to participate in exactly one recharge attempt.
//
// The provider-neutral model still keeps RECURRING/MULTI_USE for backwards
// compatibility with old records and future non-recharge workflows, but the
// recharge allocation path must never create or reuse a card with either of
// those policies.
func normalizeRechargeUsage(_ UsageType, _ string) UsageType {
	return UsageOneTime
}

func (s *Service) defaultPoolID() string {
	if s != nil && s.Config != nil {
		if value := strings.TrimSpace(s.Config.Value("card_pool_default_id", "")); value != "" {
			return value
		}
	}
	return "pool_legacy"
}

func (s *Service) loadPool(tx *gorm.DB, poolID string) (models.CardPool, error) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		poolID = s.defaultPoolID()
	}
	var pool models.CardPool
	query := tx.Preload("Providers")
	if poolID != "" {
		query = query.Where("id = ?", poolID)
	}
	if err := query.First(&pool).Error; err != nil {
		return pool, err
	}
	if !pool.Enabled {
		return pool, fmt.Errorf("card pool %s is disabled", pool.ID)
	}
	if len(pool.Providers) == 0 && pool.DefaultProvider != "" {
		pool.Providers = []models.CardPoolProvider{{Provider: pool.DefaultProvider, Enabled: true, Priority: 1, Weight: 100}}
	}
	if pool.ID == s.defaultPoolID() && s.Config != nil {
		if configured := strings.ToUpper(strings.TrimSpace(s.Config.Value("card_pool_routing", ""))); configured != "" {
			pool.RoutingStrategy = configured
		}
		if configured := strings.ToUpper(strings.TrimSpace(s.Config.Value("card_pool_default_provider", ""))); configured != "" {
			pool.DefaultProvider = configured
			found := false
			for _, provider := range pool.Providers {
				if strings.EqualFold(provider.Provider, configured) {
					found = true
					break
				}
			}
			if !found {
				pool.Providers = append(pool.Providers, models.CardPoolProvider{Provider: configured, Enabled: true, Priority: len(pool.Providers) + 1, Weight: 100})
			}
		}
		pool.CardCreationMode = string(normalizeCardCreationMode(CardCreationMode(pool.CardCreationMode), s.Config.Value("card_pool_card_creation_mode", string(CardCreationPoolOnly))))
		// The legacy default pool is configured from the admin Provider switches.
		// Reflect those switches into the runtime routes so enabling a Provider
		// makes it immediately eligible without requiring a duplicate pool edit.
		known := make(map[string]bool, len(pool.Providers))
		for index := range pool.Providers {
			provider := strings.ToUpper(strings.TrimSpace(pool.Providers[index].Provider))
			known[provider] = true
			if value := strings.TrimSpace(s.Config.Value(providerEnabledConfigKey(provider), "")); value != "" {
				pool.Providers[index].Enabled = configBoolValue(value, pool.Providers[index].Enabled)
			}
		}
		for _, provider := range s.Registry.Names() {
			provider = strings.ToUpper(strings.TrimSpace(provider))
			if known[provider] || !configBoolValue(s.Config.Value(providerEnabledConfigKey(provider), "0"), false) {
				continue
			}
			pool.Providers = append(pool.Providers, models.CardPoolProvider{Provider: provider, Enabled: true, Priority: len(pool.Providers) + 1, Weight: 100})
		}
	} else {
		pool.CardCreationMode = string(normalizeCardCreationMode(CardCreationMode(pool.CardCreationMode), string(CardCreationPoolOnly)))
	}
	return pool, nil
}

// ResolvePool returns the same runtime-effective pool definition used by
// card allocation. In particular, the configured default pool may receive
// its routing strategy, default provider, provider enable switches, and card
// creation mode from AppConfig rather than from the persisted CardPool row.
// Keeping this lookup on the card-pool service prevents request admission
// checks from drifting away from the Worker allocation path.
func (s *Service) ResolvePool(ctx context.Context, poolID string) (models.CardPool, error) {
	if s == nil || s.DB == nil {
		return models.CardPool{}, errors.New("card pool database is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.loadPool(s.DB.WithContext(ctx), poolID)
}

func providerEnabledConfigKey(provider string) string {
	return "card_provider_" + strings.ToLower(strings.TrimSpace(provider)) + "_enabled"
}

func configBoolValue(value string, fallback bool) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func allocationKey(request AcquireCardRequest) string {
	if value := strings.TrimSpace(request.IdempotencyKey); value != "" {
		return value
	}
	if value := strings.TrimSpace(request.PaymentTaskID); value != "" {
		return "task:" + value
	}
	if value := strings.TrimSpace(request.OwnerKey); value != "" {
		return "owner:" + value
	}
	return "allocation:" + uuid.NewString()
}

func providerRouteEnabled(pool models.CardPool, provider string) bool {
	provider = strings.ToUpper(strings.TrimSpace(provider))
	for _, route := range pool.Providers {
		if strings.EqualFold(strings.TrimSpace(route.Provider), provider) {
			return route.Enabled
		}
	}
	return false
}

func (s *Service) selectProviderForPool(pool models.CardPool, requested string) (CardProvider, error) {
	requested = strings.ToUpper(strings.TrimSpace(requested))
	if requested != "" {
		if !providerRouteEnabled(pool, requested) {
			return nil, NewProviderError(requested, "select_provider", CategoryProviderUnavailable, false, false, fmt.Errorf("provider %s is not enabled for pool %s", requested, pool.ID))
		}
		return s.Registry.Get(requested)
	}
	return s.Router.Select(RoutingStrategy(pool.RoutingStrategy), pool.DefaultProvider, routeModels(pool))
}

// CreateCard issues one provider card and stores only normalized metadata. It
// is used by the admin card-pool screen to pre-create cards that later payment
// tasks can reuse. The provider-specific sensitive response is never written
// to the database or returned by this method.
func (s *Service) CreateCard(ctx context.Context, request CreateCardRequest) (PaymentCard, error) {
	if s == nil || s.DB == nil || s.Registry == nil {
		return PaymentCard{}, errors.New("card pool service is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request.PoolID = strings.TrimSpace(request.PoolID)
	request.Provider = strings.ToUpper(strings.TrimSpace(request.Provider))
	request.BusinessAccountID = strings.TrimSpace(request.BusinessAccountID)
	request.PaymentTaskID = strings.TrimSpace(request.PaymentTaskID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.CardholderName = strings.TrimSpace(request.CardholderName)
	request.Currency = strings.ToUpper(strings.TrimSpace(request.Currency))

	var pool models.CardPool
	var provision models.CardProvisionRequest
	duplicateCardID := ""
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		pool, err = s.loadPool(tx.Clauses(clause.Locking{Strength: "UPDATE"}), request.PoolID)
		if err != nil {
			return err
		}
		request.PoolID = pool.ID
		request.UsageType = normalizeRechargeUsage(request.UsageType, pool.UsageType)
		if request.Currency == "" {
			request.Currency = firstNonEmpty(pool.Currency, pool.MerchantCurrency)
		}
		provider, err := s.selectProviderForPool(pool, request.Provider)
		if err != nil {
			return err
		}
		request.Provider = provider.ProviderName()
		if request.IdempotencyKey == "" {
			request.IdempotencyKey = "manual-create:" + pool.ID + ":" + request.Provider + ":" + uuid.NewString()
		}
		var current models.CardProvisionRequest
		lookup := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("idempotency_key = ?", request.IdempotencyKey).First(&current)
		if lookup.Error == nil {
			provision = current
			switch current.Status {
			case ProvisionSucceeded:
				if current.PaymentCardID == "" {
					return ErrProvisionClosed
				}
				duplicateCardID = current.PaymentCardID
				return nil
			case ProvisionCreating:
				return ErrProvisionInProgress
			default:
				return ErrProvisionClosed
			}
		}
		if lookup.Error != gorm.ErrRecordNotFound {
			return lookup.Error
		}
		provision = models.CardProvisionRequest{
			ID: dbID("card_provision"), IdempotencyKey: request.IdempotencyKey,
			PoolID: pool.ID, Provider: request.Provider, Status: ProvisionCreating,
		}
		return tx.Create(&provision).Error
	})
	if err != nil {
		return PaymentCard{}, err
	}
	if duplicateCardID != "" {
		var existing models.PaymentCard
		if err := s.DB.WithContext(ctx).Where("id = ?", duplicateCardID).First(&existing).Error; err != nil {
			return PaymentCard{}, err
		}
		return modelToPaymentCard(existing), nil
	}

	provider, err := s.Registry.Get(request.Provider)
	if err != nil {
		return PaymentCard{}, err
	}
	acquired, err := provider.CreateCard(ctx, request)
	if err != nil {
		_ = s.DB.WithContext(ctx).Model(&models.CardProvisionRequest{}).Where("id = ?", provision.ID).Updates(map[string]any{
			"status": ProvisionFailed, "failure_code": providerErrorCode(err), "failure_message": truncateError(err),
		}).Error
		return PaymentCard{}, err
	}
	if acquired.Provider != "" && !strings.EqualFold(strings.TrimSpace(acquired.Provider), request.Provider) {
		err = NewProviderError(request.Provider, "create_card", CategoryInvalidRequest, false, false, fmt.Errorf("provider returned card owned by %s", acquired.Provider))
	} else if strings.TrimSpace(acquired.ProviderCardID) == "" {
		err = NewProviderError(request.Provider, "create_card", CategoryTechnicalFailure, true, false, errors.New("provider returned an empty card id"))
	}
	if err != nil {
		_ = s.DB.WithContext(ctx).Model(&models.CardProvisionRequest{}).Where("id = ?", provision.ID).Updates(map[string]any{
			"status": ProvisionFailed, "failure_code": providerErrorCode(err), "failure_message": truncateError(err),
		}).Error
		return PaymentCard{}, err
	}
	if acquired.Provider == "" {
		acquired.Provider = request.Provider
	}
	if acquired.PoolID == "" {
		acquired.PoolID = request.PoolID
	}
	if acquired.UsageType == "" {
		acquired.UsageType = request.UsageType
	}
	if acquired.Currency == "" {
		acquired.Currency = request.Currency
	}
	if acquired.CardholderName == "" {
		acquired.CardholderName = request.CardholderName
	}
	if acquired.BusinessAccountID == "" {
		acquired.BusinessAccountID = request.BusinessAccountID
	}
	if acquired.InternalCardID == "" {
		acquired.InternalCardID = dbID("payment_card")
	}
	if acquired.Status == "" || acquired.Status == CardCreating || acquired.Status == CardAssigned || acquired.Status == CardInUse {
		acquired.Status = CardActive
	}
	if acquired.Status == CardCancelled || acquired.Status == CardFailed || acquired.Status == CardFrozen || acquired.Status == CardUsed {
		err = NewProviderError(request.Provider, "create_card", CategoryBusinessDecline, false, false, fmt.Errorf("provider returned unusable card status %s", acquired.Status))
		_ = s.DB.WithContext(ctx).Model(&models.CardProvisionRequest{}).Where("id = ?", provision.ID).Updates(map[string]any{
			"status": ProvisionFailed, "failure_code": providerErrorCode(err), "failure_message": truncateError(err),
		}).Error
		return PaymentCard{}, err
	}

	var persisted models.PaymentCard
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		queryErr := tx.Where("provider = ? AND provider_card_id = ?", acquired.Provider, acquired.ProviderCardID).First(&persisted).Error
		if queryErr == nil {
			if err := validateExistingCardForProvision(persisted, acquired); err != nil {
				return err
			}
			if err := tx.Model(&persisted).Updates(map[string]any{
				"pool_id": firstNonEmpty(persisted.PoolID, acquired.PoolID), "business_account_id": firstNonEmpty(persisted.BusinessAccountID, acquired.BusinessAccountID),
				"last4": acquired.Last4, "cardholder_name": acquired.CardholderName, "card_type": acquired.CardType,
				"usage_type": string(acquired.UsageType), "currency": acquired.Currency,
				"status": firstNonEmpty(persisted.Status, string(CardActive)), "provider_status": acquired.ProviderStatus,
			}).Error; err != nil {
				return err
			}
		} else if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			persisted = models.PaymentCard{
				ID: acquired.InternalCardID, PoolID: acquired.PoolID, Provider: acquired.Provider,
				ProviderCardID: acquired.ProviderCardID, BusinessAccountID: acquired.BusinessAccountID,
				Last4: acquired.Last4, CardholderName: acquired.CardholderName, CardType: acquired.CardType,
				UsageType: string(acquired.UsageType), Currency: acquired.Currency, Status: string(CardActive),
				ProviderStatus: acquired.ProviderStatus, InUse: false,
			}
			if err := tx.Create(&persisted).Error; err != nil {
				return err
			}
		} else {
			return queryErr
		}
		return tx.Model(&models.CardProvisionRequest{}).Where("id = ?", provision.ID).Updates(map[string]any{
			"status": ProvisionSucceeded, "payment_card_id": persisted.ID, "failure_code": "", "failure_message": "",
		}).Error
	}); err != nil {
		_ = s.DB.WithContext(ctx).Model(&models.CardProvisionRequest{}).Where("id = ?", provision.ID).Updates(map[string]any{
			"status": ProvisionFailed, "failure_code": "persistence_failed", "failure_message": truncateError(err),
		}).Error
		if acquired.ProviderCardID != "" && provider.Supports(CapabilityCancel) && !isCardReconciliationError(err) {
			_ = provider.CancelCard(ctx, acquired.ProviderCardID)
		}
		return PaymentCard{}, err
	}
	return modelToPaymentCard(persisted), nil
}

func (s *Service) AcquireCard(ctx context.Context, request AcquireCardRequest) (AllocationResult, error) {
	if s == nil || s.DB == nil {
		return AllocationResult{}, errors.New("card pool database is unavailable")
	}
	request.PoolID = strings.TrimSpace(request.PoolID)
	request.BusinessAccountID = strings.TrimSpace(request.BusinessAccountID)
	request.PaymentTaskID = strings.TrimSpace(request.PaymentTaskID)
	request.AllocationID = strings.TrimSpace(request.AllocationID)
	request.IdempotencyKey = allocationKey(request)
	request.OwnerKey = strings.TrimSpace(request.OwnerKey)
	if request.OwnerKey == "" {
		request.OwnerKey = request.IdempotencyKey
	}

	var pool models.CardPool
	var allocation models.CardAllocation
	var existingCard models.PaymentCard
	var returnExisting bool
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		pool, err = s.loadPool(tx.Clauses(clause.Locking{Strength: "UPDATE"}), request.PoolID)
		if err != nil {
			return err
		}
		request.PoolID = pool.ID
		// Recharge allocations are intentionally single-use even when a legacy
		// pool row still contains RECURRING or MULTI_USE.
		request.UsageType = normalizeRechargeUsage(request.UsageType, pool.UsageType)
		if request.Currency == "" {
			request.Currency = firstNonEmpty(pool.Currency, pool.MerchantCurrency)
		}

		var current models.CardAllocation
		lookup := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("idempotency_key = ?", request.IdempotencyKey).First(&current)
		if lookup.Error == nil {
			allocation = current
			if current.PaymentCardID != "" {
				if err := tx.Where("id = ?", current.PaymentCardID).First(&existingCard).Error; err != nil {
					return err
				}
				returnExisting = current.Status == string(AllocationAssigned) || current.Status == string(AllocationInUse) || current.Status == string(AllocationReleased)
				if current.Status == string(AllocationReleased) {
					return ErrAllocationClosed
				}
				if returnExisting {
					return nil
				}
			}
			if current.Status == string(AllocationCreating) {
				return ErrAllocationInProgress
			}
			if current.Status == string(AllocationFailed) {
				return ErrAllocationClosed
			}
			allocation.Status = string(AllocationCreating)
			allocation.FailureCode = ""
			allocation.FailureMessage = ""
			if err := tx.Model(&current).Updates(map[string]any{"status": allocation.Status, "failure_code": "", "failure_message": "", "released_at": nil}).Error; err != nil {
				return err
			}
			allocation = current
			allocation.Status = string(AllocationCreating)
			return nil
		}
		if lookup.Error != gorm.ErrRecordNotFound {
			return lookup.Error
		}

		provider, err := s.selectProviderForPool(pool, "")
		if err != nil {
			return err
		}
		if normalizeCardCreationMode(CardCreationMode(pool.CardCreationMode), string(CardCreationPoolOnly)) == CardCreationPoolOnly {
			// Provider-issued cards created from the admin card-pool screen are
			// reusable inventory. In POOL_ONLY mode the payment path must consume
			// one of those rows and must never issue a new remote card as a side
			// effect. LOCAL_TEXT remains the compatibility exception: legacy TXT/
			// CSV assets are acquired lazily by its adapter.
			var reusable models.PaymentCard
			query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
				Where("pool_id = ? AND provider = ?", pool.ID, provider.ProviderName()).
				Where("status IN ? AND in_use = ?", []string{string(CardActive), string(CardAssigned)}, false).
				Where("usage_count = 0").
				Where("cooldown_until IS NULL OR cooldown_until < ?", s.now()).
				Order("usage_count ASC, COALESCE(last_used_at, '1970-01-01') ASC, created_at ASC, id ASC").First(&reusable)
			if query.Error == nil {
				allocation = models.CardAllocation{
					ID: dbID("allocation"), IdempotencyKey: request.IdempotencyKey,
					PaymentTaskID: request.PaymentTaskID, PaymentCardID: reusable.ID,
					PoolID: pool.ID, Provider: reusable.Provider, ProviderCardID: reusable.ProviderCardID,
					BusinessAccountID: request.BusinessAccountID, UsageType: string(request.UsageType), OwnerKey: request.OwnerKey,
					Status: string(AllocationInUse), AllocatedAt: s.now(),
				}
				if err := tx.Create(&allocation).Error; err != nil {
					return err
				}
				if err := tx.Model(&reusable).Updates(map[string]any{"status": string(CardInUse), "usage_type": string(UsageOneTime), "in_use": true}).Error; err != nil {
					return err
				}
				reusable.UsageType = string(UsageOneTime)
				existingCard = reusable
				returnExisting = true
				return nil
			}
			if query.Error != gorm.ErrRecordNotFound {
				return query.Error
			}
			if !strings.EqualFold(provider.ProviderName(), "LOCAL_TEXT") {
				return ErrNoAvailableCard
			}
		}
		allocation = models.CardAllocation{
			ID: dbID("allocation"), IdempotencyKey: request.IdempotencyKey,
			PaymentTaskID: request.PaymentTaskID, PoolID: pool.ID, Provider: provider.ProviderName(),
			BusinessAccountID: request.BusinessAccountID, UsageType: string(request.UsageType), OwnerKey: request.OwnerKey,
			Status: string(AllocationCreating), AllocatedAt: s.now(),
		}
		return tx.Create(&allocation).Error
	})
	if err != nil {
		if isUniqueConflict(err) {
			if recovered, recoverErr := s.recoverAllocation(request.IdempotencyKey); recoverErr == nil {
				return recovered, nil
			}
		}
		return AllocationResult{}, err
	}
	if returnExisting {
		return AllocationResult{Card: modelToPaymentCard(existingCard), AllocationID: allocation.ID, Provider: existingCard.Provider}, nil
	}

	request.PoolID = pool.ID
	request.AllocationID = allocation.ID
	var acquired PaymentCard
	var selectedProvider string
	err = s.Router.ExecuteSelected(RoutingStrategy(pool.RoutingStrategy), pool.DefaultProvider, routeModels(pool), allocation.Provider, func(provider CardProvider) error {
		selectedProvider = provider.ProviderName()
		_ = s.DB.Model(&models.CardAllocation{}).Where("id = ?", allocation.ID).Update("provider", selectedProvider).Error
		acquired, err = provider.AcquireCard(ctx, request)
		return err
	})
	if err == nil && acquired.Provider != "" && !strings.EqualFold(strings.TrimSpace(acquired.Provider), strings.TrimSpace(selectedProvider)) {
		err = NewProviderError(selectedProvider, "acquire_card", CategoryInvalidRequest, false, false, fmt.Errorf("provider returned card owned by %s", acquired.Provider))
	}
	if err == nil && strings.TrimSpace(acquired.ProviderCardID) == "" {
		err = NewProviderError(selectedProvider, "acquire_card", CategoryTechnicalFailure, true, true, errors.New("provider returned an empty card id"))
	}
	if err != nil {
		_ = s.DB.Model(&models.CardAllocation{}).Where("id = ?", allocation.ID).Updates(map[string]any{
			"status": string(AllocationFailed), "failure_code": providerErrorCode(err), "failure_message": truncateError(err),
		}).Error
		return AllocationResult{}, err
	}
	if acquired.Provider == "" {
		acquired.Provider = selectedProvider
	}
	if acquired.PoolID == "" {
		acquired.PoolID = pool.ID
	}
	if acquired.UsageType == "" {
		acquired.UsageType = request.UsageType
	}
	if acquired.BusinessAccountID == "" {
		acquired.BusinessAccountID = request.BusinessAccountID
	}
	if acquired.InternalCardID == "" {
		acquired.InternalCardID = dbID("payment_card")
	}
	switch acquired.Status {
	case "", CardActive, CardCreating, CardAssigned, CardInUse:
		acquired.Status = CardInUse
	case CardFrozen, CardUsed, CardCancelled, CardFailed:
		// A Provider must never turn a terminal/frozen response into an
		// allocation that the payment worker can use.
		err = NewProviderError(selectedProvider, "acquire_card", CategoryBusinessDecline, false, false, fmt.Errorf("provider returned unusable card status %s", acquired.Status))
	}
	if err != nil {
		_ = s.DB.Model(&models.CardAllocation{}).Where("id = ?", allocation.ID).Updates(map[string]any{
			"status": string(AllocationFailed), "failure_code": providerErrorCode(err), "failure_message": truncateError(err),
		}).Error
		if acquired.ProviderCardID != "" {
			if provider, providerErr := s.Registry.Get(selectedProvider); providerErr == nil && provider.Supports(CapabilityCancel) {
				_ = provider.CancelCard(ctx, acquired.ProviderCardID)
			}
		}
		return AllocationResult{}, err
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		var persisted models.PaymentCard
		queryErr := gorm.ErrRecordNotFound
		if acquired.Provider != "" && acquired.ProviderCardID != "" {
			queryErr = tx.Where("provider = ? AND provider_card_id = ?", acquired.Provider, acquired.ProviderCardID).First(&persisted).Error
		}
		if queryErr == gorm.ErrRecordNotFound && acquired.InternalCardID != "" {
			queryErr = tx.Where("id = ?", acquired.InternalCardID).First(&persisted).Error
		}
		if queryErr == nil {
			if err := validateExistingCardForAllocation(persisted, acquired.PoolID); err != nil {
				return err
			}
			// A local text card has a stable internal ID. Keep its counters while
			// refreshing only provider metadata from the latest acquire operation.
			if err := tx.Model(&persisted).Updates(map[string]any{
				"pool_id": firstNonEmpty(persisted.PoolID, acquired.PoolID), "provider": firstNonEmpty(persisted.Provider, acquired.Provider), "provider_card_id": firstNonEmpty(persisted.ProviderCardID, acquired.ProviderCardID),
				"local_card_asset_id": acquired.LocalCardAssetID, "business_account_id": acquired.BusinessAccountID,
				"last4": acquired.Last4, "cardholder_name": acquired.CardholderName, "card_type": acquired.CardType,
				"usage_type": string(acquired.UsageType), "currency": acquired.Currency,
				"status": string(CardInUse), "provider_status": acquired.ProviderStatus, "in_use": true,
			}).Error; err != nil {
				return err
			}
		} else if queryErr == gorm.ErrRecordNotFound {
			persisted = models.PaymentCard{
				ID: acquired.InternalCardID, PoolID: acquired.PoolID, Provider: acquired.Provider,
				ProviderCardID: acquired.ProviderCardID, LocalCardAssetID: acquired.LocalCardAssetID,
				BusinessAccountID: acquired.BusinessAccountID, Last4: acquired.Last4, CardholderName: acquired.CardholderName,
				CardType: acquired.CardType, UsageType: string(acquired.UsageType), Currency: acquired.Currency,
				Status: string(CardInUse), ProviderStatus: acquired.ProviderStatus, InUse: true,
			}
			if err := tx.Create(&persisted).Error; err != nil {
				return err
			}
		} else {
			return queryErr
		}
		if err := tx.Model(&models.CardAllocation{}).Where("id = ?", allocation.ID).Updates(map[string]any{
			"payment_card_id": persisted.ID, "provider": persisted.Provider, "provider_card_id": persisted.ProviderCardID, "status": string(AllocationInUse),
		}).Error; err != nil {
			return err
		}
		allocation.PaymentCardID = persisted.ID
		allocation.Provider = persisted.Provider
		allocation.ProviderCardID = persisted.ProviderCardID
		allocation.Status = string(AllocationInUse)
		existingCard = persisted
		return nil
	})
	if err != nil {
		// Provider creation happens outside the database transaction. If local
		// persistence fails after a remote card was created, best-effort cancel
		// it so a retry cannot leave an untracked active card behind.
		if acquired.ProviderCardID != "" && !isCardReconciliationError(err) {
			if provider, providerErr := s.Registry.Get(acquired.Provider); providerErr == nil && provider.Supports(CapabilityCancel) {
				_ = provider.CancelCard(ctx, acquired.ProviderCardID)
			}
		}
		_ = s.DB.Model(&models.CardAllocation{}).Where("id = ?", allocation.ID).Updates(map[string]any{
			"status": string(AllocationFailed), "failure_code": "persistence_failed", "failure_message": truncateError(err),
		}).Error
		return AllocationResult{}, err
	}
	return AllocationResult{Card: modelToPaymentCard(existingCard), AllocationID: allocation.ID, Provider: existingCard.Provider}, nil
}

func (s *Service) GetCard(ctx context.Context, internalID string) (PaymentCard, error) {
	var row models.PaymentCard
	if err := s.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(internalID)).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PaymentCard{}, ErrCardNotFound
		}
		return PaymentCard{}, err
	}
	provider, err := s.Registry.Get(row.Provider)
	if err != nil {
		return modelToPaymentCard(row), err
	}
	remote, err := provider.GetCard(ctx, row.ProviderCardID)
	if err == nil {
		merged := mergePaymentCard(modelToPaymentCard(row), remote)
		_ = s.DB.WithContext(ctx).Model(&models.PaymentCard{}).Where("id = ?", row.ID).Updates(map[string]any{
			"last4": merged.Last4, "cardholder_name": merged.CardholderName, "card_type": merged.CardType,
			"currency": merged.Currency, "status": string(merged.Status), "provider_status": merged.ProviderStatus,
		}).Error
		return merged, nil
	}
	return modelToPaymentCard(row), err
}

func (s *Service) GetSensitiveCardDetails(ctx context.Context, internalID string) (SensitiveCardDetails, error) {
	var row models.PaymentCard
	if err := s.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(internalID)).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return SensitiveCardDetails{}, ErrCardNotFound
		}
		return SensitiveCardDetails{}, err
	}
	provider, err := s.Registry.Get(row.Provider)
	if err != nil {
		return SensitiveCardDetails{}, err
	}
	return provider.GetSensitiveCardDetails(ctx, row.ProviderCardID)
}

var activeAllocationStatuses = []string{
	string(AllocationCreating), string(AllocationAssigned), string(AllocationInUse),
}

// Provider cancellation is intentionally performed outside the database
// transaction. Keep a durable, short-lived claim on the allocation so
// duplicate Worker callbacks and recovery loops do not issue the same remote
// cancellation concurrently. A stale claim is recoverable after a process
// crash; providers are still expected to make cancellation idempotent.
const providerReleaseClaimTTL = 5 * time.Minute

func providerReleaseClaimIsActive(claimedAt *time.Time, now time.Time) bool {
	return claimedAt != nil && claimedAt.Add(providerReleaseClaimTTL).After(now)
}

func (s *Service) ReleaseCard(ctx context.Context, internalID string) error {
	return s.ReleaseCardForAllocation(ctx, internalID, "")
}

// ReleaseCardForAllocation releases only the allocation that owns the current
// payment attempt. A card can be shared by multiple recurring allocations, so
// the underlying provider reservation is released only after the last active
// allocation is gone.
func (s *Service) ReleaseCardForAllocation(ctx context.Context, internalID, allocationID string) error {
	return s.releaseCardForAllocation(ctx, internalID, allocationID, "")
}

// releaseCardForAllocation optionally continues a claim that was atomically
// acquired by RetryPendingProviderReleases. A blank claim token means this is
// a normal Worker/API call and the method will acquire its own claim.
func (s *Service) releaseCardForAllocation(ctx context.Context, internalID, allocationID, requestedClaimToken string) error {
	if s == nil || s.DB == nil {
		return errors.New("card pool database is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestedClaimToken = strings.TrimSpace(requestedClaimToken)
	if requestedClaimToken == "" {
		requestedClaimToken = uuid.NewString()
	}
	var row models.PaymentCard
	if err := s.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(internalID)).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCardNotFound
		}
		return err
	}

	var provider CardProvider
	var providerLookupErr error
	if s.Registry == nil {
		providerLookupErr = ErrProviderNotFound
	} else {
		provider, providerLookupErr = s.Registry.Get(row.Provider)
	}
	_, hasAllocationReservation := provider.(AllocationReservationProvider)
	_, hasReservation := provider.(ReservationProvider)
	releasedAllocationID := ""
	providerReleaseAction := ""
	providerReleaseRequired := false
	providerReleaseClaimed := false
	var err error
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked models.PaymentCard
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", row.ID).First(&locked).Error; err != nil {
			return err
		}

		allocation, found, err := findAllocationForMutation(tx, locked.ID, allocationID)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		pendingRelease := allocation.ProviderReleasePending || strings.TrimSpace(allocation.ProviderReleaseError) != ""
		if allocation.Status == string(AllocationReleased) || (allocation.Status == string(AllocationFailed) && pendingRelease) {
			if !pendingRelease && allocation.UsageType == string(UsageOneTime) && allocation.ProviderReleasedAt == nil {
				pendingRelease = true
			}
			if !pendingRelease {
				return nil
			}
			var remaining int64
			if err := tx.Model(&models.CardAllocation{}).
				Where("payment_card_id = ? AND status IN ?", locked.ID, activeAllocationStatuses).
				Count(&remaining).Error; err != nil {
				return err
			}
			if remaining > 0 {
				return nil
			}
			providerReleaseRequired = true
			releasedAllocationID = allocation.ID
			providerReleaseAction = firstNonEmpty(allocation.ProviderReleaseAction, releaseActionForProvider(allocation.UsageType, allocation.UsageRecordedAt != nil, hasAllocationReservation, hasReservation))
			if allocation.ProviderReleaseAction == "" && providerReleaseAction != "" {
				if err := tx.Model(&allocation).Updates(map[string]any{
					"provider_release_pending": true,
					"provider_release_action":  providerReleaseAction,
				}).Error; err != nil {
					return err
				}
			}
			providerReleaseClaimed, err = claimProviderRelease(tx, &allocation, requestedClaimToken, s.now())
			if err != nil {
				return err
			}
			return nil
		}
		if !isActiveAllocationStatus(allocation.Status) {
			return ErrAllocationClosed
		}

		now := s.now()
		if err := tx.Model(&allocation).Updates(map[string]any{
			"status": string(AllocationReleased), "released_at": now,
		}).Error; err != nil {
			return err
		}

		var remaining int64
		if err := tx.Model(&models.CardAllocation{}).
			Where("payment_card_id = ? AND status IN ?", locked.ID, activeAllocationStatuses).
			Count(&remaining).Error; err != nil {
			return err
		}
		if remaining > 0 {
			return tx.Model(&locked).Updates(map[string]any{"in_use": true, "status": string(CardInUse)}).Error
		}

		status := locked.Status
		if status == string(CardInUse) || status == string(CardAssigned) || status == string(CardCreating) {
			status = string(CardActive)
		}
		if allocation.UsageRecordedAt != nil && allocation.UsageType == string(UsageOneTime) {
			status = string(CardUsed)
		}
		providerReleaseAction = releaseActionForProvider(allocation.UsageType, allocation.UsageRecordedAt != nil, hasAllocationReservation, hasReservation)
		providerReleaseRequired = providerReleaseAction != ""
		cardUpdates := map[string]any{"in_use": false, "status": status}
		if providerReleaseAction == ProviderReleaseActionCancel {
			// A card whose remote cancellation is still pending must not be
			// offered to another payment task.
			cardUpdates["status"] = string(CardFailed)
		}
		if err := tx.Model(&locked).Updates(cardUpdates).Error; err != nil {
			return err
		}
		releasedAllocationID = allocation.ID
		allocationUpdates := map[string]any{}
		if providerReleaseRequired {
			allocationUpdates["provider_release_pending"] = true
			allocationUpdates["provider_release_action"] = providerReleaseAction
		}
		if len(allocationUpdates) > 0 {
			if providerReleaseRequired {
				allocationUpdates["provider_release_claim_token"] = requestedClaimToken
				allocationUpdates["provider_release_claimed_at"] = s.now()
				providerReleaseClaimed = true
			}
			if err := tx.Model(&allocation).Updates(allocationUpdates).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !providerReleaseRequired || !providerReleaseClaimed {
		return nil
	}
	if providerLookupErr != nil {
		// The card has already been made unavailable and the allocation carries
		// a durable pending-cancellation marker. A missing Provider registration
		// must not leave the card IN_USE forever; the retry loop can complete the
		// remote cancellation once configuration/registration is restored.
		releaseErr := providerLookupErr
		if persistErr := s.persistProviderReleaseResult(ctx, row.ID, releasedAllocationID, providerReleaseAction, requestedClaimToken, releaseErr); persistErr != nil {
			return errors.Join(releaseErr, persistErr)
		}
		return releaseErr
	}
	var releaseErr error
	switch providerReleaseAction {
	case ProviderReleaseActionCancel:
		if !provider.Supports(CapabilityCancel) {
			releaseErr = UnsupportedCapability(row.Provider, CapabilityCancel)
		} else {
			releaseErr = provider.CancelCard(ctx, row.ProviderCardID)
		}
	case ProviderReleaseActionReservation:
		if allocationProvider, ok := provider.(AllocationReservationProvider); ok {
			releaseErr = allocationProvider.ReleaseReservationForAllocation(ctx, row.ProviderCardID, releasedAllocationID)
		} else if lifecycle, ok := provider.(ReservationProvider); ok {
			releaseErr = lifecycle.ReleaseReservation(ctx, row.ProviderCardID)
		} else {
			releaseErr = UnsupportedCapability(row.Provider, CapabilityCancel)
		}
	default:
		releaseErr = errors.New("provider release action is missing")
	}
	if releaseErr != nil {
		if persistErr := s.persistProviderReleaseResult(ctx, row.ID, releasedAllocationID, providerReleaseAction, requestedClaimToken, releaseErr); persistErr != nil {
			return errors.Join(releaseErr, persistErr)
		}
		return releaseErr
	}
	if persistErr := s.persistProviderReleaseResult(ctx, row.ID, releasedAllocationID, providerReleaseAction, requestedClaimToken, nil); persistErr != nil {
		return persistErr
	}
	return nil
}

// claimProviderRelease is called while the allocation row is already locked
// by its surrounding transaction. It returns false when another live claim
// owns the remote release. A stale claim is safely replaced.
func claimProviderRelease(tx *gorm.DB, allocation *models.CardAllocation, claimToken string, now time.Time) (bool, error) {
	if allocation == nil || strings.TrimSpace(claimToken) == "" {
		return false, nil
	}
	current := strings.TrimSpace(allocation.ProviderReleaseClaimToken)
	if current != "" && current != claimToken && providerReleaseClaimIsActive(allocation.ProviderReleaseClaimedAt, now) {
		return false, nil
	}
	if current == claimToken && providerReleaseClaimIsActive(allocation.ProviderReleaseClaimedAt, now) {
		return true, nil
	}
	if err := tx.Model(allocation).Updates(map[string]any{
		"provider_release_pending":     true,
		"provider_release_claim_token": claimToken,
		"provider_release_claimed_at":  now,
	}).Error; err != nil {
		return false, err
	}
	return true, nil
}

// persistProviderReleaseResult finalizes a Provider release only if the
// caller still owns the allocation claim. If a stale claim was taken over by
// recovery, the old caller becomes a no-op instead of changing the newer
// result. This transaction also makes the local card terminal state and the
// allocation's remote-release state commit together.
func (s *Service) persistProviderReleaseResult(ctx context.Context, cardID, allocationID, action, claimToken string, releaseErr error) error {
	if strings.TrimSpace(allocationID) == "" {
		return nil
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var allocation models.CardAllocation
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND provider_release_claim_token = ?", allocationID, claimToken).
			First(&allocation)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// Another recovery worker owns (or already completed) this release.
			return nil
		}
		if result.Error != nil {
			return result.Error
		}
		if releaseErr != nil {
			return tx.Model(&allocation).Updates(map[string]any{
				"provider_release_pending":     true,
				"provider_release_action":      action,
				"provider_release_error":       truncateError(releaseErr),
				"provider_release_claim_token": "",
				"provider_release_claimed_at":  nil,
			}).Error
		}

		updates := map[string]any{
			"provider_released_at":         s.now(),
			"provider_release_error":       "",
			"provider_release_pending":     false,
			"provider_release_action":      "",
			"provider_release_claim_token": "",
			"provider_release_claimed_at":  nil,
		}
		if action == ProviderReleaseActionCancel {
			updates["status"] = string(AllocationReleased)
			if err := tx.Model(&models.PaymentCard{}).Where("id = ?", cardID).Updates(map[string]any{
				"status": string(CardCancelled), "in_use": false,
			}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&allocation).Updates(updates).Error
	})
}

func releaseActionForProvider(usageType string, usageRecorded bool, hasAllocationReservation, hasReservation bool) string {
	// A one-time card is cancelled only after the payment attempt has been
	// recorded. If checkout never submitted (missing button, pre-submit
	// captcha, form not ready), releasing the allocation must not invalidate the
	// card or call the Provider cancellation API.
	if usageType == string(UsageOneTime) && usageRecorded {
		return ProviderReleaseActionCancel
	}
	if hasAllocationReservation || hasReservation {
		return ProviderReleaseActionReservation
	}
	return ""
}

type ProviderReleaseRetryReport struct {
	Attempted int64
	Succeeded int64
	Failed    int64
}

// RetryPendingProviderReleases is deliberately best effort. The database
// state has already been made unavailable to new allocations; a temporary
// Provider outage must not make task recovery fail or re-open that card.
func (s *Service) RetryPendingProviderReleases(ctx context.Context, limit int) (ProviderReleaseRetryReport, error) {
	var report ProviderReleaseRetryReport
	if s == nil || s.DB == nil {
		return report, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 100
	}
	var allocations []models.CardAllocation
	if err := s.DB.WithContext(ctx).Where("provider_release_pending = ?", true).
		Order("updated_at ASC, id ASC").Limit(limit).Find(&allocations).Error; err != nil {
		return report, err
	}
	for _, allocation := range allocations {
		if strings.TrimSpace(allocation.PaymentCardID) == "" {
			report.Failed++
			continue
		}
		var card models.PaymentCard
		if err := s.DB.WithContext(ctx).Where("id = ?", allocation.PaymentCardID).First(&card).Error; err != nil {
			report.Failed++
			continue
		}
		claimToken := uuid.NewString()
		claimTime := s.now()
		claimCutoff := claimTime.Add(-providerReleaseClaimTTL)
		claim := s.DB.WithContext(ctx).Model(&models.CardAllocation{}).
			Where("id = ? AND provider_release_pending = ?", allocation.ID, true).
			Where("provider_release_claim_token IS NULL OR provider_release_claim_token = '' OR provider_release_claimed_at IS NULL OR provider_release_claimed_at < ?", claimCutoff).
			Updates(map[string]any{
				"provider_release_claim_token": claimToken,
				"provider_release_claimed_at":  claimTime,
			})
		if claim.Error != nil {
			report.Failed++
			continue
		}
		if claim.RowsAffected == 0 {
			// Another retry loop already owns this release claim.
			continue
		}
		report.Attempted++
		if err := s.releaseCardForAllocation(ctx, card.ID, allocation.ID, claimToken); err != nil {
			report.Failed++
			continue
		}
		report.Succeeded++
	}
	return report, nil
}

// MarkCardExhausted keeps the legacy card-wide semantics: a declined card is
// unusable for every recurring allocation that points at it. It still marks
// each affected allocation explicitly as failed instead of silently releasing
// unrelated reservations during normal cleanup.
func (s *Service) MarkCardExhausted(ctx context.Context, internalID string) error {
	return s.MarkCardExhaustedForAllocation(ctx, internalID, "")
}

// RecordCardFailure stores the provider-facing failure classification on the
// normalized card, its allocation, and (when known) the recharge task. This
// is intentionally separate from MarkCardExhausted: a form validation,
// hCaptcha, or provider timeout is useful diagnostic data but must not be
// mistaken for a card-decline that requires remote cancellation.
func (s *Service) RecordCardFailure(ctx context.Context, internalID, allocationID, taskID, failureCode, failureMessage string) error {
	if s == nil || s.DB == nil {
		return errors.New("card pool database is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	internalID = strings.TrimSpace(internalID)
	allocationID = strings.TrimSpace(allocationID)
	taskID = strings.TrimSpace(taskID)
	failureCode = strings.TrimSpace(failureCode)
	if failureCode == "" {
		failureCode = "payment_failed"
	}
	failureMessage = truncateError(errors.New(strings.TrimSpace(failureMessage)))
	now := s.now()
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var card models.PaymentCard
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", internalID).First(&card).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCardNotFound
			}
			return err
		}
		if err := tx.Model(&card).Updates(map[string]any{
			"last_attempt_task_id": taskID,
			"last_failure_code":    failureCode,
			"last_failure_message": failureMessage,
			"last_failure_at":      now,
		}).Error; err != nil {
			return err
		}
		if allocationID != "" {
			if err := tx.Model(&models.CardAllocation{}).Where("id = ? AND payment_card_id = ?", allocationID, card.ID).Updates(map[string]any{
				"failure_code": failureCode, "failure_message": failureMessage,
			}).Error; err != nil {
				return err
			}
		}
		if taskID == "" && allocationID != "" {
			var allocation models.CardAllocation
			if err := tx.Select("payment_task_id").Where("id = ?", allocationID).First(&allocation).Error; err == nil {
				taskID = strings.TrimSpace(allocation.PaymentTaskID)
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if taskID != "" {
			if err := tx.Model(&models.RechargeTask{}).Where("id = ?", taskID).Updates(map[string]any{
				"card_failure_code": failureCode, "card_failure_message": failureMessage, "card_failure_at": now,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) MarkCardExhaustedForAllocation(ctx context.Context, internalID, allocationID string) error {
	if s == nil || s.DB == nil {
		return errors.New("card pool database is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var row models.PaymentCard
	if err := s.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(internalID)).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCardNotFound
		}
		return err
	}
	if row.Status == string(CardCancelled) {
		return nil
	}
	if strings.TrimSpace(allocationID) != "" {
		var allocation models.CardAllocation
		if err := s.DB.WithContext(ctx).Where("id = ? AND payment_card_id = ?", strings.TrimSpace(allocationID), row.ID).First(&allocation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAllocationNotFound
			}
			return err
		}
		if !isActiveAllocationStatus(allocation.Status) && !(allocation.ProviderReleasePending && allocation.ProviderReleaseAction == ProviderReleaseActionCancel) {
			return ErrAllocationClosed
		}
	}
	provider, err := s.Registry.Get(row.Provider)
	if err != nil {
		return err
	}
	if !provider.Supports(CapabilityCancel) {
		return UnsupportedCapability(row.Provider, CapabilityCancel)
	}
	cancelErr := provider.CancelCard(ctx, row.ProviderCardID)
	if cancelErr != nil {
		persistErr := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var locked models.PaymentCard
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", row.ID).First(&locked).Error; err != nil {
				return err
			}
			target, found, err := findCancellationAllocation(tx, locked.ID, allocationID)
			if err != nil {
				return err
			}
			if err := tx.Model(&locked).Updates(map[string]any{"in_use": false, "status": string(CardFailed)}).Error; err != nil {
				return err
			}
			now := s.now()
			if found {
				updates := map[string]any{
					"provider_release_pending": true, "provider_release_action": ProviderReleaseActionCancel,
					"provider_release_error": truncateError(cancelErr), "failure_code": "provider_cancel_pending",
					"failure_message": truncateError(cancelErr),
				}
				if isActiveAllocationStatus(target.Status) {
					updates["status"] = string(AllocationReleased)
					updates["released_at"] = now
				}
				if err := tx.Model(&target).Updates(updates).Error; err != nil {
					return err
				}
				if err := tx.Model(&models.CardAllocation{}).
					Where("payment_card_id = ? AND status IN ? AND id <> ?", locked.ID, activeAllocationStatuses, target.ID).
					Updates(map[string]any{"status": string(AllocationFailed), "released_at": now, "failure_code": "card_cancel_pending", "failure_message": "card cancellation is pending"}).Error; err != nil {
					return err
				}
			} else if err := tx.Model(&models.CardAllocation{}).
				Where("payment_card_id = ? AND status IN ?", locked.ID, activeAllocationStatuses).
				Updates(map[string]any{"status": string(AllocationFailed), "released_at": now, "failure_code": "card_cancel_pending", "failure_message": "card cancellation is pending", "provider_release_pending": true, "provider_release_action": ProviderReleaseActionCancel, "provider_release_error": truncateError(cancelErr)}).Error; err != nil {
				return err
			}
			return nil
		})
		if persistErr != nil {
			return fmt.Errorf("cancel card: %w (persist cancellation pending: %v)", cancelErr, persistErr)
		}
		return cancelErr
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked models.PaymentCard
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", row.ID).First(&locked).Error; err != nil {
			return err
		}
		if allocationID != "" {
			if _, found, err := findAllocationForMutation(tx, locked.ID, allocationID); err != nil {
				return err
			} else if !found {
				return ErrAllocationNotFound
			}
		}
		if err := tx.Model(&locked).Updates(map[string]any{"in_use": false, "status": string(CardCancelled)}).Error; err != nil {
			return err
		}
		now := s.now()
		if err := tx.Model(&models.CardAllocation{}).
			Where("payment_card_id = ? AND status IN ?", locked.ID, activeAllocationStatuses).
			Updates(map[string]any{"status": string(AllocationFailed), "released_at": now, "failure_code": "card_cancelled", "failure_message": "card cancelled", "provider_release_pending": false, "provider_release_action": "", "provider_release_error": "", "provider_released_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&models.CardAllocation{}).
			Where("payment_card_id = ? AND provider_release_pending = ?", locked.ID, true).
			Updates(map[string]any{"provider_release_pending": false, "provider_release_action": "", "provider_release_error": "", "provider_released_at": now}).Error
	})
}

func (s *Service) RecordUsage(ctx context.Context, internalID string) (UsageStats, error) {
	return s.RecordUsageForAllocation(ctx, internalID, "")
}

// RecordUsageForAllocation records one payment attempt exactly once. It does
// not release the allocation; the worker calls ReleaseCardForAllocation after
// the payment result is persisted.
func (s *Service) RecordUsageForAllocation(ctx context.Context, internalID, allocationID string) (UsageStats, error) {
	var row models.PaymentCard
	if err := s.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(internalID)).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return UsageStats{}, ErrCardNotFound
		}
		return UsageStats{}, err
	}
	var provider CardProvider
	var providerLookupErr error
	if s.Registry == nil {
		providerLookupErr = ErrProviderNotFound
	} else {
		provider, providerLookupErr = s.Registry.Get(row.Provider)
	}

	var result UsageStats
	var err error
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked models.PaymentCard
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", row.ID).First(&locked).Error; err != nil {
			return err
		}
		allocation, found, err := findAllocationForMutation(tx, locked.ID, allocationID)
		if err != nil {
			return err
		}
		if !found {
			result = UsageStats{DailyUsageCount: locked.DailyUsageCount, CooledDown: locked.CooldownUntil != nil && locked.CooldownUntil.After(s.now())}
			return nil
		}
		if allocation.UsageRecordedAt != nil {
			result = UsageStats{DailyUsageCount: locked.DailyUsageCount, CooledDown: locked.CooldownUntil != nil && locked.CooldownUntil.After(s.now())}
			return nil
		}
		if !isActiveAllocationStatus(allocation.Status) {
			return ErrAllocationClosed
		}

		// The allocation row is locked for the whole operation. A duplicate
		// Worker therefore waits here and observes UsageRecordedAt instead of
		// incrementing a provider-local usage counter a second time.
		providerStats := UsageStats{}
		if providerLookupErr == nil {
			if transactional, ok := provider.(TransactionalAllocationReservationProvider); ok {
				providerStats, err = transactional.RecordUsageForAllocationTx(ctx, tx, locked.ProviderCardID, allocation.ID)
			} else if allocationProvider, ok := provider.(AllocationReservationProvider); ok {
				providerStats, err = allocationProvider.RecordUsageForAllocation(ctx, locked.ProviderCardID, allocation.ID)
			} else if lifecycle, ok := provider.(ReservationProvider); ok {
				providerStats, err = lifecycle.RecordUsage(ctx, locked.ProviderCardID)
			}
		}
		if err != nil {
			return err
		}

		now := s.now()
		dailyCount := locked.DailyUsageCount + 1
		resetAt := locked.DailyUsageResetAt
		if resetAt == nil || resetAt.Before(now.Add(-24*time.Hour)) {
			dailyCount = 1
			resetAt = &now
		}
		if providerStats.DailyUsageCount > dailyCount {
			dailyCount = providerStats.DailyUsageCount
		}
		cooledDown := providerStats.CooledDown || dailyCount >= 3
		updates := map[string]any{
			"usage_count": gorm.Expr("usage_count + 1"), "daily_usage_count": dailyCount,
			"daily_usage_reset_at": resetAt, "last_used_at": now, "in_use": true,
			"status":         string(CardInUse),
			"cooldown_until": nil,
		}
		if allocation.UsageType == string(UsageOneTime) {
			updates["status"] = string(CardUsed)
		}
		if cooledDown {
			updates["cooldown_until"] = now.Add(24 * time.Hour)
		}
		if err := tx.Model(&locked).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Model(&allocation).Updates(map[string]any{"usage_recorded_at": now}).Error; err != nil {
			return err
		}
		result = UsageStats{DailyUsageCount: dailyCount, CooledDown: cooledDown}
		return nil
	})
	if err != nil {
		return result, err
	}
	// Keep the normalized usage counter authoritative even if the Provider was
	// temporarily unavailable in the registry. Settlement will still retry the
	// Provider cancellation separately, but the card is already consumed from
	// the platform's point of view.
	return result, providerLookupErr
}

// SettleCardForAllocation closes the card lifecycle for one recharge attempt.
// It is deliberately best-effort across the three side effects: a failure to
// record diagnostic usage must not prevent the release/cancellation attempt,
// and a temporary Provider failure is persisted by ReleaseCardForAllocation
// for the background retry loop. Repeating this method is safe because usage,
// allocation release, and Provider cancellation are all idempotent.
func (s *Service) SettleCardForAllocation(ctx context.Context, internalID, allocationID, taskID, failureCode, failureMessage string) (PaymentCard, error) {
	if s == nil || s.DB == nil {
		return PaymentCard{}, errors.New("card pool database is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var settlementErr error
	failureCode = strings.TrimSpace(failureCode)
	failureMessage = strings.TrimSpace(failureMessage)
	if failureCode != "" || failureMessage != "" {
		if err := s.RecordCardFailure(ctx, internalID, allocationID, taskID, failureCode, failureMessage); err != nil {
			settlementErr = errors.Join(settlementErr, err)
		}
	}
	if _, err := s.RecordUsageForAllocation(ctx, internalID, allocationID); err != nil {
		settlementErr = errors.Join(settlementErr, err)
	}
	if err := s.ReleaseCardForAllocation(ctx, internalID, allocationID); err != nil {
		settlementErr = errors.Join(settlementErr, err)
	}

	var card models.PaymentCard
	if err := s.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(internalID)).First(&card).Error; err != nil {
		settlementErr = errors.Join(settlementErr, err)
		return PaymentCard{}, settlementErr
	}
	return modelToPaymentCard(card), settlementErr
}

func isActiveAllocationStatus(status string) bool {
	for _, active := range activeAllocationStatuses {
		if status == active {
			return true
		}
	}
	return false
}

func findAllocationForMutation(tx *gorm.DB, cardID, allocationID string) (models.CardAllocation, bool, error) {
	var allocation models.CardAllocation
	allocationID = strings.TrimSpace(allocationID)
	if allocationID != "" {
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND payment_card_id = ?", allocationID, cardID).First(&allocation)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return models.CardAllocation{}, false, ErrAllocationNotFound
		}
		return allocation, result.Error == nil, result.Error
	}
	var allocations []models.CardAllocation
	result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("payment_card_id = ? AND (status IN ? OR provider_release_pending = ?)", cardID, activeAllocationStatuses, true).
		Order("created_at DESC, id DESC").Limit(2).Find(&allocations)
	if result.Error != nil {
		return models.CardAllocation{}, false, result.Error
	}
	if len(allocations) > 1 {
		return models.CardAllocation{}, false, ErrAllocationRequired
	}
	if len(allocations) == 0 {
		return models.CardAllocation{}, false, nil
	}
	return allocations[0], true, nil
}

func findCancellationAllocation(tx *gorm.DB, cardID, allocationID string) (models.CardAllocation, bool, error) {
	allocationID = strings.TrimSpace(allocationID)
	var allocation models.CardAllocation
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("payment_card_id = ?", cardID)
	if allocationID != "" {
		result := query.Where("id = ?", allocationID).First(&allocation)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return models.CardAllocation{}, false, ErrAllocationNotFound
		}
		return allocation, result.Error == nil, result.Error
	}
	result := query.Where("status IN ? OR provider_release_pending = ?", activeAllocationStatuses, true).
		Order("created_at DESC, id DESC").First(&allocation)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return models.CardAllocation{}, false, nil
	}
	return allocation, result.Error == nil, result.Error
}

func (s *Service) ListCards(poolID string) ([]PaymentCard, error) {
	var rows []models.PaymentCard
	query := s.DB.Order("created_at ASC, id ASC")
	if strings.TrimSpace(poolID) != "" {
		query = query.Where("pool_id = ?", strings.TrimSpace(poolID))
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]PaymentCard, 0, len(rows))
	for _, row := range rows {
		result = append(result, modelToPaymentCard(row))
	}
	return result, nil
}

func modelToPaymentCard(row models.PaymentCard) PaymentCard {
	return PaymentCard{
		InternalCardID: row.ID, PoolID: row.PoolID, Provider: row.Provider, ProviderCardID: row.ProviderCardID,
		LocalCardAssetID: row.LocalCardAssetID, BusinessAccountID: row.BusinessAccountID, Last4: row.Last4,
		CardholderName: row.CardholderName, CardType: row.CardType, UsageType: UsageType(row.UsageType), Currency: row.Currency,
		Status: InternalCardStatus(row.Status), ProviderStatus: row.ProviderStatus, InUse: row.InUse, UsageCount: row.UsageCount,
		DailyUsageCount: row.DailyUsageCount, CooldownUntil: row.CooldownUntil, LastUsedAt: row.LastUsedAt,
		LastAttemptTaskID: row.LastAttemptTaskID, LastFailureCode: row.LastFailureCode,
		LastFailureMessage: row.LastFailureMessage, LastFailureAt: row.LastFailureAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func mergePaymentCard(current, remote PaymentCard) PaymentCard {
	if remote.InternalCardID == "" {
		remote.InternalCardID = current.InternalCardID
	}
	if remote.PoolID == "" {
		remote.PoolID = current.PoolID
	}
	if remote.Provider == "" {
		remote.Provider = current.Provider
	}
	if remote.ProviderCardID == "" {
		remote.ProviderCardID = current.ProviderCardID
	}
	if remote.UsageType == "" {
		remote.UsageType = current.UsageType
	}
	// Local lifecycle state is authoritative for terminal/unavailable states.
	// LOCAL_TEXT reports the inventory row as ACTIVE even after the business
	// card was marked USED/CANCELLED, and remote providers may report an active
	// object while local cancellation is still pending. A metadata refresh must
	// never resurrect a card that the allocation lifecycle has consumed.
	if isCardLifecycleLocked(current.Status) {
		remote.Status = current.Status
	} else if remote.Status == "" {
		remote.Status = current.Status
	}
	remote.UsageCount = current.UsageCount
	remote.DailyUsageCount = current.DailyUsageCount
	remote.CooldownUntil = current.CooldownUntil
	remote.LastUsedAt = current.LastUsedAt
	remote.LastAttemptTaskID = current.LastAttemptTaskID
	remote.LastFailureCode = current.LastFailureCode
	remote.LastFailureMessage = current.LastFailureMessage
	remote.LastFailureAt = current.LastFailureAt
	return remote
}

func isCardLifecycleLocked(status InternalCardStatus) bool {
	switch status {
	case CardUsed, CardCancelled, CardFailed, CardFrozen, CardInUse:
		return true
	default:
		return false
	}
}

func validateExistingCardForProvision(existing models.PaymentCard, acquired PaymentCard) error {
	if existing.Provider != "" && acquired.Provider != "" && !strings.EqualFold(existing.Provider, acquired.Provider) {
		return ErrCardPoolMismatch
	}
	if existing.PoolID != "" && acquired.PoolID != "" && existing.PoolID != acquired.PoolID {
		return ErrCardPoolMismatch
	}
	if existing.UsageCount > 0 || existing.Status == string(CardUsed) || existing.Status == string(CardCancelled) {
		return ErrCardConsumed
	}
	if existing.InUse || isCardLifecycleLocked(InternalCardStatus(existing.Status)) {
		return ErrCardUnavailable
	}
	return nil
}

func validateExistingCardForAllocation(existing models.PaymentCard, poolID string) error {
	if existing.PoolID != "" && poolID != "" && existing.PoolID != poolID {
		return ErrCardPoolMismatch
	}
	if existing.UsageCount > 0 || existing.Status == string(CardUsed) || existing.Status == string(CardCancelled) {
		return ErrCardConsumed
	}
	if existing.InUse || isCardLifecycleLocked(InternalCardStatus(existing.Status)) {
		return ErrCardUnavailable
	}
	return nil
}

func isCardReconciliationError(err error) bool {
	return errors.Is(err, ErrCardConsumed) || errors.Is(err, ErrCardUnavailable) || errors.Is(err, ErrCardPoolMismatch)
}

func dbID(prefix string) string {
	return prefix + "_" + uuid.NewString()
}

func providerErrorCode(err error) string {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && providerErr != nil && providerErr.Category != "" {
		return strings.ToLower(string(providerErr.Category))
	}
	if errors.Is(err, ErrNoAvailableCard) {
		return "no_available_card"
	}
	if errors.Is(err, ErrUnsupportedCapability) {
		return "unsupported_capability"
	}
	if errors.Is(err, ErrCardConsumed) {
		return "card_consumed"
	}
	if errors.Is(err, ErrCardUnavailable) {
		return "card_unavailable"
	}
	if errors.Is(err, ErrCardPoolMismatch) {
		return "card_pool_mismatch"
	}
	return "provider_error"
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(err.Error())
	if len(value) > 500 {
		return value[:500]
	}
	return value
}

func (s *Service) recoverAllocation(idempotencyKey string) (AllocationResult, error) {
	var allocation models.CardAllocation
	if err := s.DB.Where("idempotency_key = ?", idempotencyKey).First(&allocation).Error; err != nil {
		return AllocationResult{}, err
	}
	if allocation.PaymentCardID == "" {
		if allocation.Status == string(AllocationCreating) {
			return AllocationResult{}, ErrAllocationInProgress
		}
		return AllocationResult{}, gorm.ErrDuplicatedKey
	}
	var row models.PaymentCard
	if err := s.DB.Where("id = ?", allocation.PaymentCardID).First(&row).Error; err != nil {
		return AllocationResult{}, err
	}
	return AllocationResult{Card: modelToPaymentCard(row), AllocationID: allocation.ID, Provider: row.Provider}, nil
}

func isUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key") || strings.Contains(message, "unique constraint") || strings.Contains(message, "uniqueindex") || strings.Contains(message, "unique index")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
