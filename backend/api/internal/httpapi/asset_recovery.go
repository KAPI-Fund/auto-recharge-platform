package httpapi

import (
	"fmt"
	"strings"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
)

// AssetRecoveryReport records how many rows were changed by a recovery pass.
// The counts are used for startup and periodic operational logs.
type AssetRecoveryReport struct {
	PhoneReleased int64
	CardReleased  int64
	PoolReleased  int64
	ProxyReleased int64
}

// ResetAssetLocks mirrors the legacy process-start recovery. Assets are
// process-local reservations, so no reservation can survive an API restart.
func (s *Server) ResetAssetLocks() (AssetRecoveryReport, error) {
	var report AssetRecoveryReport
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		if report.PhoneReleased, err = clearLockedRows(tx, &models.PhoneAsset{}, "in_use = ?", true); err != nil {
			return fmt.Errorf("reset phone asset locks: %w", err)
		}
		if report.CardReleased, err = releaseCardAssetLocks(tx, false); err != nil {
			return fmt.Errorf("reset card asset locks: %w", err)
		}
		if report.PoolReleased, err = clearLockedRows(tx, &models.PoolEmail{}, "registered = ? AND in_use = ?", false, true); err != nil {
			return fmt.Errorf("reset pool email locks: %w", err)
		}
		if report.ProxyReleased, err = clearLockedRows(tx, &models.ProxyAsset{}, "in_use = ?", true); err != nil {
			return fmt.Errorf("reset proxy asset locks: %w", err)
		}
		return nil
	})
	return report, err
}

// ReleaseStaleAssetLocks is the periodic crash-recovery pass from the legacy
// service. Registered mailbox records are intentionally never reclaimed.
func (s *Server) ReleaseStaleAssetLocks() (AssetRecoveryReport, error) {
	cutoff := time.Now().Add(-assetLockStaleAfter)
	var report AssetRecoveryReport
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		where := "in_use = ? AND (locked_at IS NULL OR locked_at < ?)"
		if report.PhoneReleased, err = clearLockedRows(tx, &models.PhoneAsset{}, where, true, cutoff); err != nil {
			return fmt.Errorf("release stale phone asset locks: %w", err)
		}
		if report.CardReleased, err = releaseCardAssetLocks(tx, true); err != nil {
			return fmt.Errorf("release stale card asset locks: %w", err)
		}
		if report.PoolReleased, err = clearLockedRows(tx, &models.PoolEmail{}, "registered = ? AND "+where, false, true, cutoff); err != nil {
			return fmt.Errorf("release stale pool email locks: %w", err)
		}
		if report.ProxyReleased, err = clearLockedRows(tx, &models.ProxyAsset{}, where, true, cutoff); err != nil {
			return fmt.Errorf("release stale proxy asset locks: %w", err)
		}
		return nil
	})
	return report, err
}

// CleanupStaleProductGenerationTasks is the split-service equivalent of the
// legacy ADMIN_PRODUCT_GEN cleanup. Redis recovery owns queued work; only a
// running task with no heartbeat for the asset-lock timeout is stale enough to
// mark failed here.
func (s *Server) CleanupStaleProductGenerationTasks() (int64, error) {
	cutoff := time.Now().Add(-assetLockStaleAfter)
	result := s.DB.Model(&models.ProductGenerationTask{}).
		Where("status = ? AND updated_at < ?", models.ProductGenerationRunning, cutoff).
		Updates(map[string]any{
			"status":      models.ProductGenerationFailed,
			"message":     "遗留成品任务已自动清理",
			"progress":    100,
			"finished_at": time.Now(),
		})
	return result.RowsAffected, result.Error
}

func clearLockedRows(tx *gorm.DB, model any, where string, args ...any) (int64, error) {
	result := tx.Model(model).Where(where, args...).Updates(map[string]any{
		"in_use":    false,
		"locked_at": nil,
		"locked_by": "",
	})
	return result.RowsAffected, result.Error
}

// releaseCardAssetLocks preserves locks belonging to a live recharge task.
// The old implementation treated every card lock as process-local and cleared
// it on API restart or after 15 minutes, which allowed a long-running Worker
// and a new Worker to use the same local card concurrently.
func releaseCardAssetLocks(tx *gorm.DB, staleOnly bool) (int64, error) {
	var cards []models.CardAsset
	query := tx.Where("in_use = ?", true)
	if staleOnly {
		cutoff := time.Now().Add(-assetLockStaleAfter)
		query = query.Where("locked_at IS NULL OR locked_at < ?", cutoff)
	}
	if err := query.Find(&cards).Error; err != nil {
		return 0, err
	}

	var released int64
	now := time.Now()
	for _, card := range cards {
		known, active := cardAssetLeaseState(tx, card.ID, card.LockedBy, now)
		if active || (staleOnly && !known && card.LockedAt != nil && card.LockedAt.After(now.Add(-assetLockStaleAfter))) {
			continue
		}
		result := tx.Model(&models.CardAsset{}).
			Where("id = ? AND in_use = ?", card.ID, true).
			Updates(map[string]any{"in_use": false, "locked_at": nil, "locked_by": ""})
		if result.Error != nil {
			return released, result.Error
		}
		released += result.RowsAffected
	}
	return released, nil
}

// cardAssetLeaseState returns whether the asset is associated with the new
// normalized allocation model and whether at least one owner still has a live
// task lease. Unknown legacy owner keys intentionally remain governed by the
// original stale timeout.
func cardAssetLeaseState(tx *gorm.DB, assetID, lockOwner string, now time.Time) (known, active bool) {
	var paymentCard models.PaymentCard
	if err := tx.Where("local_card_asset_id = ?", assetID).First(&paymentCard).Error; err == nil {
		known = true
		var allocations []models.CardAllocation
		if err := tx.Where("payment_card_id = ? AND status IN ?", paymentCard.ID, []string{
			"CREATING", "ASSIGNED", "IN_USE",
		}).Find(&allocations).Error; err != nil {
			return known, false
		}
		for _, allocation := range allocations {
			if strings.TrimSpace(allocation.PaymentTaskID) == "" {
				return known, true
			}
			var task models.RechargeTask
			if err := tx.Select("status", "lease_expires_at").Where("id = ?", allocation.PaymentTaskID).First(&task).Error; err == nil &&
				task.Status == models.TaskRunning && task.LeaseExpiresAt != nil && task.LeaseExpiresAt.After(now) {
				return known, true
			}
		}
		return known, false
	}

	lockOwner = strings.TrimSpace(lockOwner)
	if lockOwner == "" {
		return false, false
	}
	var allocation models.CardAllocation
	if err := tx.Where("id = ?", lockOwner).First(&allocation).Error; err != nil {
		return false, false
	}
	known = true
	if allocation.Status != "CREATING" && allocation.Status != "ASSIGNED" && allocation.Status != "IN_USE" {
		return known, false
	}
	if strings.TrimSpace(allocation.PaymentTaskID) == "" {
		return known, true
	}
	var task models.RechargeTask
	if err := tx.Select("status", "lease_expires_at").Where("id = ?", allocation.PaymentTaskID).First(&task).Error; err != nil {
		return known, false
	}
	return known, task.Status == models.TaskRunning && task.LeaseExpiresAt != nil && task.LeaseExpiresAt.After(now)
}
