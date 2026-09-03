package stripeissuing

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

func (p *Provider) ParseWebhook(ctx context.Context, headers http.Header, body []byte) (cardpool.InternalEvent, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return cardpool.InternalEvent{}, err
		}
	}
	secret := p.secret("stripe_issuing_webhook_secret", p.value("stripe_issuing_webhook_secret", ""))
	if secret == "" {
		secret = p.secret("stripe_webhook_secret", p.value("stripe_webhook_secret", ""))
	}
	tolerance := parseStripeWebhookTolerance(p.value("stripe_issuing_webhook_tolerance_seconds", "300"))
	if !shared.VerifyStripeSignature(body, headers.Get("Stripe-Signature"), secret, time.Now(), tolerance) {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: Stripe signature", cardpool.ErrInvalidWebhookSignature))
	}
	payload, err := decodeStripeObject(body)
	if err != nil {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: invalid JSON", cardpool.ErrInvalidWebhook))
	}
	eventID := stripeString(payload, "id")
	eventType := stripeString(payload, "type")
	if eventID == "" || eventType == "" {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: missing event id or type", cardpool.ErrInvalidWebhook))
	}
	event := cardpool.InternalEvent{ProviderEventID: eventID, EventType: eventType, OccurredAt: stripeEventTime(payload)}
	resource := stripeWebhookResource(payload)
	name := strings.ToLower(strings.TrimSpace(eventType))
	switch {
	case name == "issuing_card.created" || name == "issuing_card.updated":
		event.ProviderCardID = stripeString(resource, "id")
		event.ProviderStatus = stripeString(resource, "status")
		event.Status = stripeCardStatus(event.ProviderStatus)
	case strings.HasPrefix(name, "issuing_authorization.") || strings.HasPrefix(name, "issuing_transaction."):
		transaction := parseStripeTransaction(resource, name, event.OccurredAt)
		if transaction.ProviderTransactionID == "" {
			event.Ignored = true
		} else {
			event.ProviderCardID = transaction.ProviderCardID
			event.ProviderTxnID = transaction.ProviderTransactionID
			event.Transaction = &transaction
		}
	default:
		// Stripe may add new Issuing event types. Record and acknowledge them so
		// delivery retries do not continue forever until a parser is deployed.
		if strings.HasPrefix(name, "issuing_") {
			event.Ignored = true
		} else {
			return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: unsupported event namespace", cardpool.ErrInvalidWebhook))
		}
	}
	return event, nil
}

func decodeStripeObject(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, errors.New("webhook payload is not an object")
	}
	return payload, nil
}

func stripeWebhookResource(payload map[string]any) map[string]any {
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		return payload
	}
	if object, ok := data["object"].(map[string]any); ok {
		return object
	}
	return data
}

func parseStripeTransaction(resource map[string]any, eventType string, occurredAt *time.Time) cardpool.CardTransaction {
	amount := stripeNumber(resource, "amount") / 100
	transaction := cardpool.CardTransaction{
		ProviderTransactionID: stripeString(resource, "id"),
		ProviderCardID:        stripeString(resource, "card"),
		Amount:                amount,
		Currency:              strings.ToUpper(stripeString(resource, "currency")),
		Status:                strings.ToUpper(stripeString(resource, "status")),
		Type:                  "card_payment",
		FailureCode:           stripeString(resource, "failure_code"),
		OccurredAt:            occurredAt,
	}
	if strings.HasPrefix(eventType, "issuing_authorization.") {
		transaction.Type = "authorization"
	}
	if merchant, ok := resource["merchant_data"].(map[string]any); ok {
		transaction.MerchantName = stripeString(merchant, "name")
	}
	if transaction.Status == "" {
		transaction.Status = strings.ToUpper(eventSuffix(eventType))
	}
	if transaction.OccurredAt == nil {
		transaction.OccurredAt = stripeUnixTime(resource, "created")
	}
	return transaction
}

func stripeCardStatus(status string) cardpool.InternalCardStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active":
		return cardpool.CardActive
	case "inactive":
		return cardpool.CardFrozen
	case "canceled", "cancelled":
		return cardpool.CardCancelled
	case "pending", "creating":
		return cardpool.CardCreating
	default:
		return cardpool.CardFailed
	}
}

func stripeEventTime(payload map[string]any) *time.Time {
	return stripeUnixTime(payload, "created")
}

func stripeUnixTime(values map[string]any, key string) *time.Time {
	value, ok := values[key]
	if !ok || value == nil {
		return nil
	}
	var unix int64
	switch typed := value.(type) {
	case json.Number:
		unix, _ = typed.Int64()
	case float64:
		unix = int64(typed)
	case string:
		unix, _ = strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
	}
	if unix == 0 {
		return nil
	}
	parsed := time.Unix(unix, 0).UTC()
	return &parsed
}

func stripeString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
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
	return ""
}

func stripeNumber(values map[string]any, key string) float64 {
	value, ok := values[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case float64:
		return typed
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed
	default:
		return 0
	}
}

func eventSuffix(eventType string) string {
	if index := strings.LastIndex(eventType, "."); index >= 0 {
		return strings.TrimSpace(eventType[index+1:])
	}
	return strings.TrimSpace(eventType)
}

func parseStripeWebhookTolerance(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}
