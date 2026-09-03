package cardpool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	webhookEventProcessing = "processing"
	webhookEventProcessed  = "processed"
	webhookEventIgnored    = "ignored"
)

var sensitiveWebhookKey = regexp.MustCompile(`(?i)^(card[_-]?number|account[_-]?number|pan|number|cvc|cvv|security[_-]?code|client[_-]?secret|api[_-]?key|secret|token)$`)

// ProcessWebhook verifies and applies one provider event. The provider
// adapter owns signature verification and external payload mapping; this
// method only deals with internal models and idempotent state transitions.
func (s *Service) ProcessWebhook(ctx context.Context, providerName string, headers http.Header, body []byte, traceID string) (WebhookResult, error) {
	if s == nil || s.DB == nil {
		return WebhookResult{}, errors.New("card pool database is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	providerName = strings.ToUpper(strings.TrimSpace(providerName))
	provider, err := s.Registry.Get(providerName)
	if err != nil {
		return WebhookResult{}, err
	}
	webhookProvider, ok := provider.(WebhookProvider)
	if !ok || !provider.Supports(CapabilityWebhook) {
		return WebhookResult{}, UnsupportedCapability(providerName, CapabilityWebhook)
	}
	event, err := webhookProvider.ParseWebhook(ctx, headers, body)
	if err != nil {
		return WebhookResult{}, err
	}
	event.ProviderEventID = strings.TrimSpace(event.ProviderEventID)
	event.EventType = strings.TrimSpace(event.EventType)
	if event.ProviderEventID == "" || event.EventType == "" {
		return WebhookResult{}, NewProviderError(providerName, "process_webhook", CategoryInvalidRequest, false, false, fmt.Errorf("%w: missing event identity", ErrInvalidWebhook))
	}
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		traceID = "trace_" + uuid.NewString()
	}
	result := WebhookResult{Provider: providerName, ProviderEventID: event.ProviderEventID, EventType: event.EventType, Ignored: event.Ignored}
	payload := SanitizeWebhookPayload(body)

	processErr := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record models.CardProviderEvent
		newRecord := false
		lookup := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"provider = ? AND provider_event_id = ?", providerName, event.ProviderEventID,
		).First(&record)
		if lookup.Error == nil {
			if record.Status == webhookEventProcessed || record.Status == webhookEventIgnored {
				result.Duplicate = true
				result.Ignored = record.Status == webhookEventIgnored
				return nil
			}
		} else if !errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
			return lookup.Error
		} else {
			record = models.CardProviderEvent{
				ID: dbID("card_event"), Provider: providerName, ProviderEventID: event.ProviderEventID,
				EventType: event.EventType, Status: webhookEventProcessing,
			}
			newRecord = true
		}

		card, cardFound, err := findPaymentCard(tx, providerName, event.ProviderCardID)
		if err != nil {
			return err
		}
		if event.Transaction != nil && strings.TrimSpace(event.Transaction.ProviderTransactionID) != "" {
			if err := upsertCardTransaction(tx, providerName, card, cardFound, *event.Transaction); err != nil {
				return err
			}
			result.TransactionSaved = true
		}

		if event.Status != "" && cardFound {
			if applyCardStatus(card, event, s.now()) {
				updates := map[string]any{"status": string(event.Status), "provider_status_at": eventTimeOrNow(event.OccurredAt, s.now())}
				if strings.TrimSpace(event.ProviderStatus) != "" {
					updates["provider_status"] = strings.TrimSpace(event.ProviderStatus)
				}
				if err := tx.Model(&card).Updates(updates).Error; err != nil {
					return err
				}
				result.CardUpdated = true
			} else if strings.TrimSpace(event.ProviderStatus) != "" && newerProviderEvent(card.ProviderStatusAt, event.OccurredAt, s.now()) {
				if err := tx.Model(&card).Updates(map[string]any{
					"provider_status":    strings.TrimSpace(event.ProviderStatus),
					"provider_status_at": eventTimeOrNow(event.OccurredAt, s.now()),
				}).Error; err != nil {
					return err
				}
			}
		} else if event.Status != "" && !cardFound {
			result.Ignored = true
		}
		if event.Ignored {
			result.Ignored = true
		}
		status := webhookEventProcessed
		if result.Ignored && !result.CardUpdated && !result.TransactionSaved {
			status = webhookEventIgnored
		}
		now := s.now()
		updates := map[string]any{
			"event_type": event.EventType, "payment_card_id": cardID(card, cardFound),
			"provider_card_id": strings.TrimSpace(event.ProviderCardID), "status": status,
			"trace_id": traceID, "payload": payload, "processed_at": now,
		}
		if event.OccurredAt != nil {
			updates["occurred_at"] = event.OccurredAt
		}
		if newRecord {
			record.EventType = event.EventType
			record.Status = status
			record.PaymentCardID = cardID(card, cardFound)
			record.ProviderCardID = strings.TrimSpace(event.ProviderCardID)
			record.TraceID = traceID
			record.Payload = payload
			record.OccurredAt = event.OccurredAt
			record.ProcessedAt = &now
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
		} else if err := tx.Model(&record).Updates(updates).Error; err != nil {
			return err
		}
		return nil
	})
	if processErr == nil {
		return result, nil
	}
	if isUniqueConflict(processErr) {
		var existing models.CardProviderEvent
		if lookupErr := s.DB.WithContext(ctx).Where("provider = ? AND provider_event_id = ?", providerName, event.ProviderEventID).First(&existing).Error; lookupErr == nil && (existing.Status == webhookEventProcessed || existing.Status == webhookEventIgnored) {
			result.Duplicate = true
			result.Ignored = existing.Status == webhookEventIgnored
			return result, nil
		}
	}
	return WebhookResult{}, processErr
}

func findPaymentCard(tx *gorm.DB, providerName, providerCardID string) (models.PaymentCard, bool, error) {
	providerCardID = strings.TrimSpace(providerCardID)
	if providerCardID == "" {
		return models.PaymentCard{}, false, nil
	}
	var card models.PaymentCard
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"provider = ? AND provider_card_id = ?", providerName, providerCardID,
	).First(&card).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.PaymentCard{}, false, nil
	}
	return card, err == nil, err
}

func cardID(card models.PaymentCard, found bool) string {
	if !found {
		return ""
	}
	return card.ID
}

func upsertCardTransaction(tx *gorm.DB, providerName string, card models.PaymentCard, cardFound bool, transaction CardTransaction) error {
	providerTransactionID := strings.TrimSpace(transaction.ProviderTransactionID)
	if providerTransactionID == "" {
		return nil
	}
	var row models.CardTransaction
	lookup := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"provider = ? AND provider_transaction_id = ?", providerName, providerTransactionID,
	).First(&row)
	if lookup.Error != nil && !errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
		return lookup.Error
	}
	cardID := ""
	if cardFound {
		cardID = card.ID
	}
	occurredAt := transaction.OccurredAt
	if lookup.Error == nil && !newerTransaction(row.OccurredAt, occurredAt) {
		return nil
	}
	values := map[string]any{
		"provider": providerName, "provider_transaction_id": providerTransactionID,
		"provider_card_id": strings.TrimSpace(transaction.ProviderCardID), "payment_card_id": cardID,
		"amount": transaction.Amount, "currency": strings.TrimSpace(transaction.Currency),
		"status": strings.TrimSpace(transaction.Status), "type": strings.TrimSpace(transaction.Type),
		"merchant_name": strings.TrimSpace(transaction.MerchantName), "failure_code": strings.TrimSpace(transaction.FailureCode),
		"occurred_at": occurredAt,
	}
	if lookup.Error == nil {
		return tx.Model(&row).Updates(values).Error
	}
	row = models.CardTransaction{ID: dbID("card_txn"), Provider: providerName, ProviderTransactionID: providerTransactionID,
		PaymentCardID: cardID, ProviderCardID: strings.TrimSpace(transaction.ProviderCardID), Amount: transaction.Amount,
		Currency: strings.TrimSpace(transaction.Currency), Status: strings.TrimSpace(transaction.Status), Type: strings.TrimSpace(transaction.Type),
		MerchantName: strings.TrimSpace(transaction.MerchantName), FailureCode: strings.TrimSpace(transaction.FailureCode), OccurredAt: occurredAt}
	return tx.Create(&row).Error
}

func newerTransaction(existing, incoming *time.Time) bool {
	if existing == nil || incoming == nil {
		return true
	}
	return !incoming.Before(*existing)
}

func newerProviderEvent(existing, incoming *time.Time, now time.Time) bool {
	if existing == nil {
		return true
	}
	if incoming == nil {
		return !now.Before(*existing)
	}
	return !incoming.Before(*existing)
}

func eventTimeOrNow(value *time.Time, fallback time.Time) *time.Time {
	if value != nil {
		return value
	}
	return &fallback
}

func applyCardStatus(card models.PaymentCard, event InternalEvent, now time.Time) bool {
	if !newerProviderEvent(card.ProviderStatusAt, event.OccurredAt, now) {
		return false
	}
	if card.Status == string(CardCancelled) && event.Status != CardCancelled {
		return false
	}
	if card.Status == string(CardUsed) && event.Status != CardCancelled {
		return false
	}
	if (card.Status == string(CardInUse) || card.Status == string(CardAssigned)) && event.Status == CardActive {
		return false
	}
	return true
}

// SanitizeWebhookPayload keeps useful event metadata for troubleshooting while
// ensuring PAN/CVC-like fields never enter the event ledger.
func SanitizeWebhookPayload(body []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return `{"redacted":true}`
	}
	sanitizeWebhookValue(payload)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return `{"redacted":true}`
	}
	return string(encoded)
}

func sanitizeWebhookValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitiveWebhookKey.MatchString(strings.ToLower(strings.TrimSpace(key))) {
				typed[key] = "[REDACTED]"
				continue
			}
			sanitizeWebhookValue(child)
		}
	case []any:
		for _, child := range typed {
			sanitizeWebhookValue(child)
		}
	}
}
