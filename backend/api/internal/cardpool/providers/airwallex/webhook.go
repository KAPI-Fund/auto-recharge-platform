package airwallex

import (
	"bytes"
	"context"
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

var airwallexCardEventStatus = map[string]cardpool.InternalCardStatus{
	"pending":  cardpool.CardCreating,
	"failed":   cardpool.CardFailed,
	"active":   cardpool.CardActive,
	"inactive": cardpool.CardFrozen,
	"blocked":  cardpool.CardFrozen,
	"lost":     cardpool.CardFrozen,
	"stolen":   cardpool.CardFrozen,
	"closed":   cardpool.CardCancelled,
	"expired":  cardpool.CardCancelled,
}

func (p *Provider) ParseWebhook(ctx context.Context, headers http.Header, body []byte) (cardpool.InternalEvent, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return cardpool.InternalEvent{}, err
		}
	}
	secret := p.secret("airwallex_webhook_secret", p.value("airwallex_webhook_secret", ""))
	timestamp := headers.Get("x-timestamp")
	if timestamp == "" {
		timestamp = headers.Get("X-Timestamp")
	}
	signature := headers.Get("x-signature")
	if signature == "" {
		signature = headers.Get("X-Signature")
	}
	tolerance := parseWebhookTolerance(p.value("airwallex_webhook_tolerance_seconds", "300"))
	if !shared.VerifyAirwallexSignature(body, timestamp, signature, secret, time.Now(), tolerance) {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: Airwallex signature", cardpool.ErrInvalidWebhookSignature))
	}

	payload, err := decodeObject(body)
	if err != nil {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: invalid JSON", cardpool.ErrInvalidWebhook))
	}
	eventID := stringField(payload, "id")
	eventType := stringField(payload, "name", "type")
	if eventID == "" || eventType == "" {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: missing event id or name", cardpool.ErrInvalidWebhook))
	}

	event := cardpool.InternalEvent{ProviderEventID: eventID, EventType: eventType}
	event.OccurredAt = eventTime(payload)
	resource := webhookResource(payload)
	name := strings.ToLower(strings.TrimSpace(eventType))
	switch {
	case strings.HasPrefix(name, "issuing.card."):
		event.ProviderCardID = stringField(resource, "card_id", "id")
		event.ProviderStatus = firstNonEmpty(stringField(resource, "card_status", "status"), eventSuffix(name))
		event.Status = airwallexCardEventStatus[eventSuffix(name)]
		if event.Status == "" {
			event.Ignored = true
		}
	case name == "issuing.transaction.succeeded" || name == "issuing.transaction.failed" ||
		strings.HasPrefix(name, "issuing.card_transaction."):
		transaction := parseAirwallexTransaction(resource, name, event.OccurredAt)
		if transaction.ProviderTransactionID == "" {
			event.Ignored = true
		} else {
			event.ProviderCardID = transaction.ProviderCardID
			event.ProviderTxnID = transaction.ProviderTransactionID
			event.Transaction = &transaction
		}
	case strings.HasPrefix(name, "issuing.card_transaction_event.") ||
		strings.HasPrefix(name, "issuing.card_transaction_lifecycle.") ||
		strings.HasPrefix(name, "issuing.cardholder.") ||
		strings.HasPrefix(name, "issuing.transaction_dispute.") ||
		name == "issuing.reissue.succeeded":
		event.Ignored = true
	default:
		// Future Issuing event names should still be acknowledged and recorded;
		// only non-Issuing payloads are malformed for this endpoint.
		if strings.HasPrefix(name, "issuing.") {
			event.Ignored = true
		} else {
			return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: unsupported event namespace", cardpool.ErrInvalidWebhook))
		}
	}
	return event, nil
}

func decodeObject(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, errors.New("webhook payload is not an object")
	}
	return payload, nil
}

func webhookResource(payload map[string]any) map[string]any {
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		return payload
	}
	if object, ok := data["object"].(map[string]any); ok {
		return object
	}
	return data
}

func parseAirwallexTransaction(resource map[string]any, eventType string, occurredAt *time.Time) cardpool.CardTransaction {
	transaction := cardpool.CardTransaction{
		ProviderTransactionID: stringField(resource, "transaction_id", "id"),
		ProviderCardID:        stringField(resource, "card_id", "cardId"),
		Amount:                numberField(resource, "billing_amount", "transaction_amount", "amount"),
		Currency:              stringField(resource, "billing_currency", "transaction_currency", "currency"),
		Status:                strings.ToUpper(stringField(resource, "status")),
		Type:                  firstNonEmpty(stringField(resource, "transaction_type", "type"), "card_payment"),
		FailureCode:           stringField(resource, "failure_reason", "failure_code"),
		OccurredAt:            occurredAt,
	}
	if merchant, ok := resource["merchant"].(map[string]any); ok {
		transaction.MerchantName = stringField(merchant, "name", "merchant_name")
	}
	if transaction.Status == "" {
		if eventSuffix(eventType) == "failed" || eventSuffix(eventType) == "declined" {
			transaction.Status = "FAILED"
		} else {
			transaction.Status = strings.ToUpper(eventSuffix(eventType))
		}
	}
	if transaction.OccurredAt == nil {
		transaction.OccurredAt = timeField(resource, "transaction_date", "posted_date", "created_at")
	}
	return transaction
}

func eventTime(payload map[string]any) *time.Time {
	return timeField(payload, "created_at", "occurred_at")
}

func timeField(values map[string]any, keys ...string) *time.Time {
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			if parsed, err := parseTime(text); err == nil {
				return &parsed
			}
		}
		if number, ok := value.(json.Number); ok {
			if parsed, err := number.Int64(); err == nil {
				valueTime := time.UnixMilli(parsed)
				return &valueTime
			}
		}
	}
	return nil
}

func stringField(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			switch typed := value.(type) {
			case string:
				if text := strings.TrimSpace(typed); text != "" {
					return text
				}
			case json.Number:
				if text := strings.TrimSpace(typed.String()); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func numberField(values map[string]any, keys ...string) float64 {
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case json.Number:
			if parsed, err := typed.Float64(); err == nil {
				return parsed
			}
		case float64:
			return typed
		case string:
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func eventSuffix(eventType string) string {
	if index := strings.LastIndex(eventType, "."); index >= 0 {
		return strings.ToLower(strings.TrimSpace(eventType[index+1:]))
	}
	return strings.ToLower(strings.TrimSpace(eventType))
}

func parseWebhookTolerance(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}
