package httpapi

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Server) internalReservePoolEmail(ownerKey string) (gin.H, error) {
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		ownerKey = "worker"
	}
	var email models.PoolEmail
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		cutoff := time.Now().Add(-assetLockStaleAfter)
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("active = ? AND registered = ?", true, false).
			Where("in_use = ? OR locked_at IS NULL OR locked_at < ?", false, cutoff).
			Order("sort_order ASC, created_at ASC, id ASC").First(&email)
		if query.Error != nil {
			return query.Error
		}
		now := time.Now()
		return tx.Model(&email).Updates(map[string]any{"in_use": true, "locked_at": now, "locked_by": ownerKey}).Error
	})
	if err == gorm.ErrRecordNotFound {
		return gin.H{"email": nil}, nil
	}
	if err != nil {
		return nil, err
	}
	return gin.H{"email": gin.H{
		"id": email.ID, "email": email.Email, "password": email.Password,
		"clientId": email.ClientID, "refreshToken": email.RefreshToken,
	}}, nil
}

func (s *Server) internalReleasePoolEmailReservation(id string) (gin.H, error) {
	result := s.DB.Model(&models.PoolEmail{}).Where("id = ? AND registered = ?", strings.TrimSpace(id), false).
		Updates(map[string]any{"in_use": false, "locked_at": nil, "locked_by": ""})
	return gin.H{"ok": result.Error == nil, "released": result.RowsAffected}, result.Error
}

func (s *Server) internalMarkPoolEmailRegistered(id string) (gin.H, error) {
	now := time.Now()
	result := s.DB.Model(&models.PoolEmail{}).Where("id = ?", strings.TrimSpace(id)).Updates(map[string]any{
		"registered": true, "registered_at": now, "in_use": false, "locked_at": nil, "locked_by": "",
	})
	return gin.H{"ok": result.Error == nil, "updated": result.RowsAffected}, result.Error
}

func (s *Server) internalReserveRuntimeAssets(ctx context.Context, ownerKey string) (gin.H, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		ownerKey = "worker"
	}
	var phone models.PhoneAsset
	var card models.CardAsset
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		cutoff := time.Now().Add(-assetLockStaleAfter)
		phoneQuery := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("active = ? AND status NOT IN ?", true, []string{"封禁", "已报废", "报废", "disabled"}).
			Where("in_use = ? OR locked_at IS NULL OR locked_at < ?", false, cutoff).
			Order("usage_count ASC, sort_order ASC, created_at ASC, id ASC").First(&phone)
		if phoneQuery.Error != nil && phoneQuery.Error != gorm.ErrRecordNotFound {
			return phoneQuery.Error
		}
		if phoneQuery.Error == nil {
			now := time.Now()
			if err := tx.Model(&phone).Updates(map[string]any{"in_use": true, "locked_at": now, "locked_by": ownerKey}).Error; err != nil {
				return err
			}
		}
		cardQuery := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("active = ? AND status = ?", true, "正常").
			Where("in_use = ? OR locked_at IS NULL OR locked_at < ?", false, cutoff).
			Where("cooldown_until IS NULL OR cooldown_until < ?", time.Now()).
			Order("usage_count ASC, sort_order ASC, created_at ASC, id ASC").First(&card)
		if cardQuery.Error != nil && cardQuery.Error != gorm.ErrRecordNotFound {
			return cardQuery.Error
		}
		if cardQuery.Error == nil {
			now := time.Now()
			if err := tx.Model(&card).Updates(map[string]any{"in_use": true, "locked_at": now, "locked_by": ownerKey}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	unlockReserved := func(proxyID string) {
		s.unlockRuntimeAssets(phone.ID, card.ID, proxyID)
	}
	proxyID := ""
	proxyValue := strings.TrimSpace(firstNonEmpty(s.configValue("proxy", ""), s.Cfg.OutboundProxy))
	if phone.ID == "" || card.ID == "" {
		if proxyValue != "" {
			proxyValue = applyProxySession(proxyValue, strings.TrimPrefix(db.NewID("session"), "session_"))
		}
	} else if claimedID, claimed, _, claimErr := s.claimActiveProxyFor(ctx, ownerKey, ""); claimErr == nil {
		proxyID = claimedID
		proxyValue = claimed
	} else if envProxyFallbackAllowed(claimErr) {
		if proxyValue != "" {
			proxyValue = applyProxySession(proxyValue, strings.TrimPrefix(db.NewID("session"), "session_"))
		}
	} else {
		unlockReserved("")
		return nil, claimErr
	}
	result := gin.H{
		"phoneAssetId": "", "cardAssetId": "", "proxyAssetId": proxyID,
		"phone": gin.H{"phone": "未配置", "key": "", "usage_count": 0},
		"card":  gin.H{"number": "", "expiry": "", "cvc": "", "usage_count": 0},
		"proxy": proxyValue,
	}
	if phone.ID != "" {
		result["phoneAssetId"] = phone.ID
		result["phone"] = gin.H{"phone": phone.Phone, "key": phone.APIKey, "usage_count": phone.UsageCount}
	}
	if card.ID != "" {
		number, decryptErr := s.decryptSessionValue(card.CardNumberCiphertext)
		if decryptErr != nil {
			unlockReserved(proxyID)
			return nil, decryptErr
		}
		expiry, expiryErr := s.decryptSessionValue(card.ExpiryCiphertext)
		if expiryErr != nil {
			unlockReserved(proxyID)
			return nil, expiryErr
		}
		cvc, cvcErr := s.decryptSessionValue(card.CVVCiphertext)
		if cvcErr != nil {
			unlockReserved(proxyID)
			return nil, cvcErr
		}
		result["cardAssetId"] = card.ID
		result["card"] = gin.H{"number": number, "expiry": expiry, "cvc": cvc, "usage_count": card.UsageCount}
	}
	return result, nil
}

func (s *Server) unlockRuntimeAssets(phoneID, cardID, proxyID string) {
	updates := map[string]any{"in_use": false, "locked_at": nil, "locked_by": ""}
	if phoneID != "" {
		_ = s.DB.Model(&models.PhoneAsset{}).Where("id = ?", phoneID).Updates(updates).Error
	}
	if cardID != "" {
		_ = s.DB.Model(&models.CardAsset{}).Where("id = ?", cardID).Updates(updates).Error
	}
	if proxyID != "" {
		_ = s.unlockProxyAsset(proxyID)
	}
}

func (s *Server) internalReleaseRuntimeAssets(phoneID, cardID string, proxyIDs ...string) (gin.H, error) {
	proxyID := ""
	if len(proxyIDs) > 0 {
		proxyID = proxyIDs[0]
	}
	updates := map[string]any{"in_use": false, "locked_at": nil, "locked_by": ""}
	if phoneID != "" {
		if err := s.DB.Model(&models.PhoneAsset{}).Where("id = ?", phoneID).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	if cardID != "" {
		if err := s.DB.Model(&models.CardAsset{}).Where("id = ?", cardID).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	if proxyID != "" {
		if err := s.unlockProxyAsset(proxyID); err != nil {
			return nil, err
		}
	}
	return gin.H{"ok": true}, nil
}

func (s *Server) internalDeletePhoneAsset(phone string) (gin.H, error) {
	result := s.DB.Model(&models.PhoneAsset{}).Where("phone = ?", strings.TrimSpace(phone)).Updates(map[string]any{
		"active": false, "status": "已报废", "in_use": false, "locked_at": nil, "locked_by": "",
	})
	return gin.H{"ok": result.Error == nil, "updated": result.RowsAffected}, result.Error
}

func (s *Server) internalDeleteCardAsset(cardNumber string) (gin.H, error) {
	want := strings.TrimSpace(cardNumber)
	if want == "" {
		return gin.H{"ok": false, "updated": 0}, nil
	}
	var cards []models.CardAsset
	if err := s.DB.Find(&cards).Error; err != nil {
		return nil, err
	}
	for _, card := range cards {
		number, err := s.decryptSessionValue(card.CardNumberCiphertext)
		if err != nil || number != want {
			continue
		}
		result := s.DB.Model(&models.CardAsset{}).Where("id = ?", card.ID).Updates(map[string]any{
			"active": false, "status": "已报废", "in_use": false, "locked_at": nil, "locked_by": "",
		})
		return gin.H{"ok": result.Error == nil, "updated": result.RowsAffected}, result.Error
	}
	return gin.H{"ok": false, "updated": 0}, nil
}

func (s *Server) internalIncrementAssetSuccessCount(phone, cardNumber string) (gin.H, error) {
	if phone != "" {
		if err := s.DB.Model(&models.PhoneAsset{}).Where("phone = ?", phone).UpdateColumn("usage_count", gorm.Expr("usage_count + 1")).Error; err != nil {
			return nil, err
		}
	}
	if cardNumber != "" {
		var cards []models.CardAsset
		if err := s.DB.Find(&cards).Error; err != nil {
			return nil, err
		}
		for _, card := range cards {
			number, err := s.decryptSessionValue(card.CardNumberCiphertext)
			if err == nil && number == cardNumber {
				if err := s.DB.Model(&models.CardAsset{}).Where("id = ?", card.ID).UpdateColumn("usage_count", gorm.Expr("usage_count + 1")).Error; err != nil {
					return nil, err
				}
				break
			}
		}
	}
	return gin.H{"ok": true}, nil
}

func (s *Server) internalUpsertPendingProduct(email, token string) (gin.H, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return gin.H{"ok": false}, nil
	}
	var row models.ProductAsset
	err := s.DB.Where("email = ?", email).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		row = models.ProductAsset{ID: db.NewID("product"), Email: email, Token: token, Status: "待协议", Active: true}
		if err := s.DB.Create(&row).Error; err != nil {
			return nil, err
		}
		return gin.H{"ok": true, "created": true}, nil
	}
	if err != nil {
		return nil, err
	}
	if token != "" {
		if err := s.DB.Model(&row).Update("token", token).Error; err != nil {
			return nil, err
		}
	}
	return gin.H{"ok": true, "created": false}, nil
}

func (s *Server) internalMarkProductReady(email, filePath, imapKey string) (gin.H, error) {
	updates := map[string]any{"status": "正常"}
	if filePath != "" {
		updates["file_path"] = filePath
	}
	if imapKey != "" {
		updates["imap_key"] = imapKey
	}
	result := s.DB.Model(&models.ProductAsset{}).Where("email = ?", strings.TrimSpace(email)).Updates(updates)
	return gin.H{"ok": result.Error == nil, "updated": result.RowsAffected}, result.Error
}

func (s *Server) internalAddProduct(input map[string]any) (gin.H, error) {
	email := strings.TrimSpace(stringValue(input, "email"))
	if email == "" {
		return gin.H{"ok": false}, nil
	}
	var row models.ProductAsset
	err := s.DB.Where("email = ?", email).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		row = models.ProductAsset{
			ID: db.NewID("product"), Email: email, Password: stringValue(input, "password"),
			Token: stringValue(input, "token"), IMAPKey: stringValue(input, "imapKey"),
			FilePath: stringValue(input, "filePath"), Status: "正常", Active: true,
		}
		if err := s.DB.Create(&row).Error; err != nil {
			return nil, err
		}
		return gin.H{"ok": true, "created": true}, nil
	}
	if err != nil {
		return nil, err
	}
	updates := map[string]any{"status": "正常"}
	for key, field := range map[string]string{"password": "password", "token": "token", "imapKey": "imap_key", "filePath": "file_path"} {
		if value := stringValue(input, key); value != "" {
			updates[field] = value
		}
	}
	if err := s.DB.Model(&row).Updates(updates).Error; err != nil {
		return nil, err
	}
	return gin.H{"ok": true, "created": false}, nil
}
