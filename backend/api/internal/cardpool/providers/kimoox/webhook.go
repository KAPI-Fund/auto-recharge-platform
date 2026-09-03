package kimoox

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/shared"
)

// ParseWebhook verifies Kimoox's raw-body HMAC contract and maps the published
// event families into the provider-neutral event model. Sensitive ciphertexts
// are intentionally not copied into InternalEvent; the common webhook layer
// also redacts any sensitive keys before persisting the payload.
func (p *Provider) ParseWebhook(ctx context.Context, headers http.Header, body []byte) (cardpool.InternalEvent, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return cardpool.InternalEvent{}, err
		}
	}
	secret := p.secret("kimoox_webhook_secret", p.value("kimoox_webhook_secret", ""))
	if secret == "" {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, errors.New("Kimoox Webhook Secret 未配置"))
	}
	payload, err := decodeKimooxWebhook(body)
	if err != nil {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: invalid JSON", cardpool.ErrInvalidWebhook))
	}
	eventID := firstNonEmpty(shared.StringField(payload, "eventId", "event_id"), strings.TrimSpace(headers.Get("X-VCC-WEBHOOK-ID")))
	eventType := firstNonEmpty(shared.StringField(payload, "eventType", "event_type"), strings.TrimSpace(headers.Get("X-VCC-WEBHOOK-EVENT")))
	timestamp := strings.TrimSpace(headers.Get("X-VCC-WEBHOOK-TIMESTAMP"))
	nonce := strings.TrimSpace(headers.Get("X-VCC-WEBHOOK-NONCE"))
	signature := strings.TrimSpace(headers.Get("X-VCC-WEBHOOK-SIGNATURE"))
	if eventID == "" || eventType == "" || timestamp == "" || nonce == "" || signature == "" {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: missing event identity or signature headers", cardpool.ErrInvalidWebhook))
	}
	if !verifyKimooxWebhookSignature(body, eventID, eventType, timestamp, nonce, signature, secret, time.Now(), p.webhookTolerance()) {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: Kimoox signature", cardpool.ErrInvalidWebhookSignature))
	}

	event := cardpool.InternalEvent{
		ProviderEventID: eventID,
		EventType:       eventType,
		OccurredAt:      kimooxEventTime(payload),
	}
	data := objectData(payload)
	name := strings.ToUpper(strings.TrimSpace(eventType))
	switch {
	case name == "CARD_ISSUE.SUCCESS":
		items := shared.ArrayField(data, "cards", "list")
		if len(items) > 0 {
			if cardData, ok := items[0].(map[string]any); ok {
				event.ProviderCardID = shared.StringField(cardData, "cardId", "card_id", "id")
				event.ProviderStatus = shared.StringField(cardData, "cardStatus", "card_status", "status", "state")
				event.Status = mapStatus(event.ProviderStatus)
			}
		}
		if event.ProviderCardID == "" {
			event.ProviderCardID = shared.StringField(data, "cardId", "card_id")
		}
		if event.ProviderCardID == "" {
			event.Ignored = true
		}
	case name == "CARD_ISSUE.FAILED":
		event.ProviderCardID = shared.StringField(data, "cardId", "card_id")
		event.ProviderStatus = "FAILED"
		event.Status = cardpool.CardFailed
		if event.ProviderCardID == "" {
			event.Ignored = true
		}
	case strings.HasPrefix(name, "CARD_OPERATION.") || name == "CARD.RISK_CANCELLED":
		event.ProviderCardID = shared.StringField(data, "cardId", "card_id")
		event.ProviderStatus, event.Status = kimooxOperationStatus(name, data)
		if event.Status == "" || event.ProviderCardID == "" {
			event.Ignored = true
		}
	case strings.HasPrefix(name, "CARD_TRANSACTION."):
		transaction := parseKimooxTransaction(data, event.OccurredAt)
		if transaction.ProviderTransactionID == "" {
			event.Ignored = true
		} else {
			event.ProviderCardID = transaction.ProviderCardID
			event.ProviderTxnID = transaction.ProviderTransactionID
			event.Transaction = &transaction
		}
	default:
		// Signed future Kimoox events are acknowledged and recorded, but do not
		// mutate normalized card state until an explicit mapping is available.
		event.Ignored = true
	}
	return event, nil
}

func decodeKimooxWebhook(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, errors.New("webhook payload is not an object")
	}
	return payload, nil
}

func verifyKimooxWebhookSignature(body []byte, eventID, eventType, timestamp, nonce, signature, secret string, now time.Time, tolerance time.Duration) bool {
	parsed, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return false
	}
	if tolerance > 0 {
		received := time.UnixMilli(parsed)
		if now.Sub(received) > tolerance || received.Sub(now) > tolerance {
			return false
		}
	}
	bodyHash := sha256.Sum256(body)
	canonical := strings.Join([]string{eventID, eventType, timestamp, nonce, hex.EncodeToString(bodyHash[:])}, "\n")
	digest := hmac.New(sha256.New, []byte(strings.TrimSpace(secret)))
	_, _ = digest.Write([]byte(canonical))
	expected := hex.EncodeToString(digest.Sum(nil))
	value := strings.TrimSpace(signature)
	if strings.HasPrefix(strings.ToLower(value), "v1=") {
		value = strings.TrimSpace(value[3:])
	}
	return hmac.Equal([]byte(strings.ToLower(value)), []byte(expected))
}

func (p *Provider) webhookTolerance() time.Duration {
	seconds, err := strconv.Atoi(p.value("kimoox_webhook_tolerance_seconds", "300"))
	if err != nil || seconds <= 0 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}

func kimooxOperationStatus(eventType string, data map[string]any) (string, cardpool.InternalCardStatus) {
	switch eventType {
	case "CARD_OPERATION.FREEZE_SUCCESS":
		return "FROZEN", cardpool.CardFrozen
	case "CARD_OPERATION.UNFREEZE_SUCCESS":
		return "ACTIVE", cardpool.CardActive
	case "CARD_OPERATION.CANCEL_SUCCESS", "CARD.RISK_CANCELLED":
		return "CANCELLED", cardpool.CardCancelled
	case "CARD_OPERATION.RECHARGE_SUCCESS", "CARD_OPERATION.WITHDRAW_SUCCESS", "CARD_OPERATION.LIMIT_CHANGE_SUCCESS", "CARD_OPERATION.REMARK_UPDATE_SUCCESS":
		return shared.StringField(data, "status", "cardStatus", "card_status"), ""
	default:
		return shared.StringField(data, "status", "cardStatus", "card_status"), mapStatus(shared.StringField(data, "status", "cardStatus", "card_status"))
	}
}

func parseKimooxTransaction(data map[string]any, occurredAt *time.Time) cardpool.CardTransaction {
	transaction := cardpool.CardTransaction{
		ProviderTransactionID: shared.StringField(data, "transactionId", "transaction_id", "id"),
		ProviderCardID:        shared.StringField(data, "cardId", "card_id"),
		Amount:                shared.FloatField(data, "actualAmount", "actual_amount", "originalAmount", "original_amount"),
		Currency:              strings.ToUpper(firstNonEmpty(shared.StringField(data, "actualCurrency", "actual_currency"), shared.StringField(data, "originalCurrency", "original_currency"))),
		Status:                strings.ToUpper(shared.StringField(data, "eventStatus", "event_status", "transactionStatus", "transaction_status", "status")),
		Type:                  firstNonEmpty(shared.StringField(data, "transactionType", "transaction_type"), "card_payment"),
		MerchantName:          shared.StringField(data, "merchantName", "merchant_name", "transactionInfo", "transaction_info"),
		FailureCode:           shared.StringField(data, "displayFailReason", "display_fail_reason", "failReasonCode", "fail_reason_code"),
		OccurredAt:            occurredAt,
	}
	if transaction.OccurredAt == nil {
		if parsed, ok := parseKimooxTime(firstNonEmpty(shared.StringField(data, "transactionTime", "transaction_time", "txnTime", "txn_time"))); ok {
			transaction.OccurredAt = &parsed
		}
	}
	return transaction
}

func kimooxEventTime(payload map[string]any) *time.Time {
	for _, value := range []string{
		shared.StringField(payload, "eventTime", "event_time", "createdAt", "created_at", "timestamp"),
		shared.StringField(objectData(payload), "eventTime", "event_time", "createdAt", "created_at", "timestamp"),
	} {
		if parsed, ok := parseKimooxTime(value); ok {
			return &parsed
		}
	}
	return nil
}

var _ cardpool.WebhookProvider = (*Provider)(nil)
