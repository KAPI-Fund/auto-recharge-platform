package localtext

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const providerName = "LOCAL_TEXT"

type Provider struct {
	DB                     *gorm.DB
	EncryptionKey          string
	EncryptionKeyFallbacks []string
	Config                 cardpool.ConfigReader
	Now                    func() time.Time
}

func New(database *gorm.DB, encryptionKey string, configs ...cardpool.ConfigReader) *Provider {
	return NewWithKeys(database, encryptionKey, nil, configs...)
}

func NewWithKeys(database *gorm.DB, encryptionKey string, fallbackKeys []string, configs ...cardpool.ConfigReader) *Provider {
	var configReader cardpool.ConfigReader
	if len(configs) > 0 {
		configReader = configs[0]
	}
	return &Provider{DB: database, EncryptionKey: encryptionKey, EncryptionKeyFallbacks: append([]string(nil), fallbackKeys...), Config: configReader, Now: time.Now}
}

func (p *Provider) ProviderName() string { return providerName }

func (p *Provider) now() time.Time {
	if p != nil && p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Provider) HealthCheck(ctx context.Context) (cardpool.ProviderHealth, error) {
	if !p.enabled() {
		return cardpool.ProviderHealth{Provider: providerName, Status: "disabled"}, nil
	}
	var count int64
	query := p.DB.WithContext(ctx).Model(&models.CardAsset{}).
		Where("active = ? AND status = ? AND in_use = ?", true, "正常", false).
		Where("cooldown_until IS NULL OR cooldown_until < ?", p.now())
	if err := query.Count(&count).Error; err != nil {
		return cardpool.ProviderHealth{Provider: providerName, Available: false, Status: "unavailable"}, err
	}
	status := "healthy"
	if count == 0 {
		status = "depleted"
	}
	now := p.now()
	return cardpool.ProviderHealth{Provider: providerName, Available: true, Status: status, LastSuccessAt: &now}, nil
}

func (p *Provider) AcquireCard(ctx context.Context, request cardpool.AcquireCardRequest) (cardpool.PaymentCard, error) {
	if !p.enabled() {
		return cardpool.PaymentCard{}, cardpool.NewProviderError(providerName, "acquire_card", cardpool.CategoryProviderUnavailable, false, true, errors.New("LOCAL_TEXT provider is disabled"))
	}
	var asset models.CardAsset
	err := p.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := p.now()
		if err := reclaimStaleReservations(tx, now); err != nil {
			return err
		}
		poolClause := "pool_id = ?"
		args := []any{request.PoolID}
		if request.PoolID == "" || request.PoolID == "pool_legacy" {
			poolClause = "(pool_id = ? OR pool_id = '' OR pool_id IS NULL)"
		}
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where(poolClause, args...).
			Where("active = ? AND status = ?", true, "正常").
			Where("in_use = ?", false).
			Where("cooldown_until IS NULL OR cooldown_until < ?", p.now()).
			Order("usage_count ASC, COALESCE(last_used_at, '1970-01-01') ASC, sort_order ASC, id ASC").First(&asset)
		if query.Error != nil {
			if errors.Is(query.Error, gorm.ErrRecordNotFound) {
				return cardpool.ErrNoAvailableCard
			}
			return query.Error
		}
		updates := map[string]any{"in_use": true, "locked_at": now, "locked_by": firstNonEmpty(request.AllocationID, request.OwnerKey, request.IdempotencyKey)}
		if asset.PoolID == "" {
			updates["pool_id"] = request.PoolID
		}
		if err := tx.Model(&asset).Updates(updates).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return cardpool.PaymentCard{}, err
	}
	return cardpool.PaymentCard{
		InternalCardID: asset.ID, PoolID: firstNonEmpty(request.PoolID, asset.PoolID, "pool_legacy"),
		Provider: providerName, ProviderCardID: asset.ID, LocalCardAssetID: asset.ID,
		Last4: asset.Last4, CardholderName: firstNonEmpty(asset.PaymentHolderName, asset.Holder),
		CardType: "virtual", UsageType: firstUsage(request.UsageType), Currency: request.Currency,
		Status: cardpool.CardInUse, ProviderStatus: asset.Status, UsageCount: asset.UsageCount,
		DailyUsageCount: asset.DailyUsageCount, CooldownUntil: asset.CooldownUntil, LastUsedAt: asset.LastUsedAt,
	}, nil
}

func (p *Provider) CreateCard(context.Context, cardpool.CreateCardRequest) (cardpool.PaymentCard, error) {
	return cardpool.PaymentCard{}, cardpool.UnsupportedCapability(providerName, cardpool.CapabilityCreateCard)
}

func (p *Provider) GetCard(ctx context.Context, providerCardID string) (cardpool.PaymentCard, error) {
	var asset models.CardAsset
	if err := p.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(providerCardID)).First(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return cardpool.PaymentCard{}, cardpool.ErrCardNotFound
		}
		return cardpool.PaymentCard{}, err
	}
	return p.metadata(asset), nil
}

func (p *Provider) GetSensitiveCardDetails(ctx context.Context, providerCardID string) (cardpool.SensitiveCardDetails, error) {
	var asset models.CardAsset
	if err := p.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(providerCardID)).First(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return cardpool.SensitiveCardDetails{}, cardpool.ErrCardNotFound
		}
		return cardpool.SensitiveCardDetails{}, err
	}
	keys := append([]string{p.EncryptionKey}, p.EncryptionKeyFallbacks...)
	number, err := security.DecryptWithFallbacks(asset.CardNumberCiphertext, keys...)
	if err != nil {
		return cardpool.SensitiveCardDetails{}, fmt.Errorf("decrypt local card number: %w", err)
	}
	expiry, err := security.DecryptWithFallbacks(asset.ExpiryCiphertext, keys...)
	if err != nil {
		return cardpool.SensitiveCardDetails{}, fmt.Errorf("decrypt local card expiry: %w", err)
	}
	cvc, err := security.DecryptWithFallbacks(asset.CVVCiphertext, keys...)
	if err != nil {
		return cardpool.SensitiveCardDetails{}, fmt.Errorf("decrypt local card cvc: %w", err)
	}
	month, year, err := parseExpiry(expiry)
	if err != nil {
		return cardpool.SensitiveCardDetails{}, err
	}
	return cardpool.SensitiveCardDetails{CardNumber: number, ExpiryMonth: month, ExpiryYear: year, CVC: cvc, CardholderName: firstNonEmpty(asset.PaymentHolderName, asset.Holder)}, nil
}

func (p *Provider) FreezeCard(ctx context.Context, providerCardID string) error {
	return p.updateStatus(ctx, providerCardID, "冻结", false)
}

func (p *Provider) UnfreezeCard(ctx context.Context, providerCardID string) error {
	return p.updateStatus(ctx, providerCardID, "正常", true)
}

func (p *Provider) CancelCard(ctx context.Context, providerCardID string) error {
	return p.updateStatus(ctx, providerCardID, "报废", false)
}

func (p *Provider) UpdateLimits(context.Context, string, cardpool.CardLimits) error {
	return cardpool.UnsupportedCapability(providerName, cardpool.CapabilityTransactionLimit)
}

func (p *Provider) GetTransactions(ctx context.Context, providerCardID string) ([]cardpool.CardTransaction, error) {
	var asset models.CardAsset
	if err := p.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(providerCardID)).First(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, cardpool.ErrCardNotFound
		}
		return nil, err
	}
	var records []models.BillingRecord
	if err := p.DB.WithContext(ctx).Where("card_last4 = ?", asset.Last4).Order("payment_time DESC").Limit(100).Find(&records).Error; err != nil {
		return nil, err
	}
	result := make([]cardpool.CardTransaction, 0, len(records))
	for _, record := range records {
		result = append(result, cardpool.CardTransaction{ProviderTransactionID: record.ID, ProviderCardID: asset.ID, Amount: record.Amount, Currency: record.Currency, Status: record.Status, Type: "billing", FailureCode: record.ErrorCode, OccurredAt: &record.PaymentTime})
	}
	return result, nil
}

func (p *Provider) Supports(capability cardpool.Capability) bool {
	switch capability {
	case cardpool.CapabilitySensitiveDetails, cardpool.CapabilityFreeze, cardpool.CapabilityUnfreeze,
		cardpool.CapabilityCancel, cardpool.CapabilityMultiUse, cardpool.CapabilityRecurringPayment,
		cardpool.CapabilityTransactionQuery:
		return true
	default:
		return false
	}
}

func (p *Provider) enabled() bool {
	if p.Config == nil {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(p.Config.Value("card_provider_local_text_enabled", "1")))
	return value != "0" && value != "false" && value != "no" && value != "off"
}

// reclaimStaleReservations keeps the legacy timeout while protecting a card
// whose lock belongs to a currently leased RechargeTask. The allocation ID is
// stored in locked_by by AcquireCard, so a long hCaptcha/Checkout run cannot
// be reclaimed merely because it crossed the old 15-minute cutoff.
func reclaimStaleReservations(tx *gorm.DB, now time.Time) error {
	cutoff := now.Add(-15 * time.Minute)
	var stale []models.CardAsset
	if err := tx.Where("in_use = ? AND (locked_at IS NULL OR locked_at < ?)", true, cutoff).Find(&stale).Error; err != nil {
		return err
	}
	for _, asset := range stale {
		if reservationHasActiveLease(tx, asset, now) {
			continue
		}
		if err := tx.Model(&models.CardAsset{}).
			Where("id = ? AND in_use = ? AND (locked_at IS NULL OR locked_at < ?)", asset.ID, true, cutoff).
			Updates(map[string]any{"in_use": false, "locked_at": nil, "locked_by": ""}).Error; err != nil {
			return err
		}
	}
	return nil
}

func reservationHasActiveLease(tx *gorm.DB, asset models.CardAsset, now time.Time) bool {
	// A recurring card may have several allocations while CardAsset can store
	// only one lock owner. Check the normalized card/allocation rows first so a
	// stale secondary owner cannot reclaim a card still used by another task.
	var paymentCard models.PaymentCard
	if err := tx.Where("local_card_asset_id = ?", asset.ID).First(&paymentCard).Error; err == nil {
		var allocations []models.CardAllocation
		if err := tx.Where("payment_card_id = ? AND status IN ?", paymentCard.ID, []string{
			string(cardpool.AllocationCreating), string(cardpool.AllocationAssigned), string(cardpool.AllocationInUse),
		}).Find(&allocations).Error; err == nil {
			for _, allocation := range allocations {
				if strings.TrimSpace(allocation.PaymentTaskID) == "" {
					return true
				}
				var task models.RechargeTask
				if err := tx.Select("status", "lease_expires_at").Where("id = ?", allocation.PaymentTaskID).First(&task).Error; err == nil &&
					task.Status == models.TaskRunning && task.LeaseExpiresAt != nil && task.LeaseExpiresAt.After(now) {
					return true
				}
			}
		}
	}
	// Compatibility locks created before allocation-aware Worker context still
	// use ownerKey. Preserve their old timeout behavior when no active lease can
	// be resolved.
	lockOwner := strings.TrimSpace(asset.LockedBy)
	if lockOwner == "" {
		return false
	}
	var allocation models.CardAllocation
	if err := tx.Where("id = ?", lockOwner).First(&allocation).Error; err != nil {
		return false
	}
	return allocation.Status == string(cardpool.AllocationCreating) || allocation.Status == string(cardpool.AllocationAssigned) || allocation.Status == string(cardpool.AllocationInUse)
}

func (p *Provider) ReleaseReservation(ctx context.Context, providerCardID string) error {
	return p.ReleaseReservationForAllocation(ctx, providerCardID, "")
}

func (p *Provider) ReleaseReservationForAllocation(ctx context.Context, providerCardID, allocationID string) error {
	assetID := strings.TrimSpace(providerCardID)
	var paymentCard models.PaymentCard
	if err := p.DB.WithContext(ctx).Where("local_card_asset_id = ?", assetID).First(&paymentCard).Error; err == nil {
		var active int64
		if err := p.DB.WithContext(ctx).Model(&models.CardAllocation{}).
			Where("payment_card_id = ? AND status IN ?", paymentCard.ID, []string{
				string(cardpool.AllocationCreating), string(cardpool.AllocationAssigned), string(cardpool.AllocationInUse),
			}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return nil
		}
	}
	// The allocation ID is used for tracing/ownership, but the underlying local
	// asset lock is released only after the service has proven there are no
	// remaining allocations. Do not filter by locked_by: recurring allocations
	// can share an asset and the last allocation may not own the original lock.
	result := p.DB.WithContext(ctx).Model(&models.CardAsset{}).Where("id = ?", assetID).
		Updates(map[string]any{"in_use": false, "locked_at": nil, "locked_by": ""})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var exists int64
		if err := p.DB.WithContext(ctx).Model(&models.CardAsset{}).Where("id = ?", strings.TrimSpace(providerCardID)).Count(&exists).Error; err != nil {
			return err
		}
		if exists == 0 {
			return cardpool.ErrCardNotFound
		}
	}
	return nil
}

func (p *Provider) RecordUsage(ctx context.Context, providerCardID string) (cardpool.UsageStats, error) {
	return p.RecordUsageForAllocation(ctx, providerCardID, "")
}

func (p *Provider) RecordUsageForAllocation(ctx context.Context, providerCardID, allocationID string) (cardpool.UsageStats, error) {
	var result cardpool.UsageStats
	err := p.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = p.recordUsageTx(ctx, tx, providerCardID, allocationID)
		return err
	})
	return result, err
}

// RecordUsageForAllocationTx is used by CardPoolService while the normalized
// allocation row is locked. Keeping the local asset update in that same
// transaction makes duplicate Worker calls increment the asset at most once.
func (p *Provider) RecordUsageForAllocationTx(ctx context.Context, tx *gorm.DB, providerCardID, allocationID string) (cardpool.UsageStats, error) {
	return p.recordUsageTx(ctx, tx, providerCardID, allocationID)
}

func (p *Provider) recordUsageTx(ctx context.Context, tx *gorm.DB, providerCardID, allocationID string) (cardpool.UsageStats, error) {
	tx = tx.WithContext(ctx)
	var asset models.CardAsset
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", strings.TrimSpace(providerCardID)).First(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return cardpool.UsageStats{}, cardpool.ErrCardNotFound
		}
		return cardpool.UsageStats{}, err
	}

	oneTime := false
	if strings.TrimSpace(allocationID) != "" {
		var allocation models.CardAllocation
		if err := tx.Select("usage_type").Where("id = ?", strings.TrimSpace(allocationID)).First(&allocation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return cardpool.UsageStats{}, cardpool.ErrAllocationNotFound
			}
			return cardpool.UsageStats{}, err
		}
		oneTime = allocation.UsageType == string(cardpool.UsageOneTime)
	}

	now := p.now()
	dailyCount := asset.DailyUsageCount + 1
	resetAt := asset.DailyUsageResetAt
	if resetAt == nil || resetAt.Before(now.Add(-24*time.Hour)) {
		dailyCount = 1
		resetAt = &now
	}
	cooledDown := dailyCount >= 3
	updates := map[string]any{"usage_count": gorm.Expr("usage_count + 1"), "daily_usage_count": dailyCount, "daily_usage_reset_at": resetAt, "last_used_at": now}
	if cooledDown {
		updates["cooldown_until"] = now.Add(24 * time.Hour)
	}
	if oneTime {
		// A one-time card must not return to the available local inventory even
		// if the Worker crashes between recording payment usage and releasing
		// the normalized allocation.
		updates["active"] = false
		updates["status"] = "已报废"
		updates["in_use"] = false
		updates["locked_at"] = nil
		updates["locked_by"] = ""
	}
	if err := tx.Model(&asset).Updates(updates).Error; err != nil {
		return cardpool.UsageStats{}, err
	}
	return cardpool.UsageStats{DailyUsageCount: dailyCount, CooledDown: cooledDown}, nil
}

func (p *Provider) metadata(asset models.CardAsset) cardpool.PaymentCard {
	return cardpool.PaymentCard{InternalCardID: asset.ID, PoolID: firstNonEmpty(asset.PoolID, "pool_legacy"), Provider: providerName, ProviderCardID: asset.ID, LocalCardAssetID: asset.ID, Last4: asset.Last4, CardholderName: firstNonEmpty(asset.PaymentHolderName, asset.Holder), CardType: "virtual", UsageType: cardpool.UsageOneTime, Status: mapStatus(asset.Status, asset.Active), ProviderStatus: asset.Status, UsageCount: asset.UsageCount, DailyUsageCount: asset.DailyUsageCount, CooldownUntil: asset.CooldownUntil, LastUsedAt: asset.LastUsedAt}
}

func (p *Provider) updateStatus(ctx context.Context, providerCardID, status string, active bool) error {
	result := p.DB.WithContext(ctx).Model(&models.CardAsset{}).Where("id = ?", strings.TrimSpace(providerCardID)).Updates(map[string]any{"status": status, "active": active, "in_use": false, "locked_at": nil, "locked_by": ""})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return cardpool.ErrCardNotFound
	}
	return nil
}

func mapStatus(status string, active bool) cardpool.InternalCardStatus {
	switch strings.TrimSpace(status) {
	case "报废", "已报废", "disabled":
		return cardpool.CardCancelled
	case "冻结", "暂停":
		return cardpool.CardFrozen
	default:
		if active {
			return cardpool.CardActive
		}
		return cardpool.CardFailed
	}
}

func parseExpiry(value string) (int, int, error) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(value), " ", "")
	parts := strings.Split(cleaned, "/")
	if len(parts) == 2 && len(parts[0]) == 2 && len(parts[1]) == 4 {
		month, err := strconv.Atoi(parts[0])
		if err != nil || month < 1 || month > 12 {
			return 0, 0, errors.New("银行卡有效期月份无效")
		}
		year, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, errors.New("银行卡有效期年份无效")
		}
		return month, year, nil
	}
	cleaned = strings.ReplaceAll(cleaned, "/", "")
	if len(cleaned) != 4 {
		return 0, 0, errors.New("银行卡有效期格式错误，应为 MMYY、MM/YY 或 MM/YYYY")
	}
	month, err := strconv.Atoi(cleaned[:2])
	if err != nil || month < 1 || month > 12 {
		return 0, 0, errors.New("银行卡有效期月份无效")
	}
	year, err := strconv.Atoi(cleaned[2:])
	if err != nil {
		return 0, 0, errors.New("银行卡有效期年份无效")
	}
	if year < 100 {
		year += 2000
	}
	return month, year, nil
}

func firstUsage(value cardpool.UsageType) cardpool.UsageType {
	if value == "" {
		return cardpool.UsageOneTime
	}
	return cardpool.UsageOneTime
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
