package dogpay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/shared"
)

// ParseWebhook verifies the raw DogPay webhook body and maps known card and
// transaction events into the provider-neutral internal event model.
func (p *Provider) ParseWebhook(ctx context.Context, headers http.Header, body []byte) (cardpool.InternalEvent, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return cardpool.InternalEvent{}, err
		}
	}
	secret := p.secret("dogpay_webhook_secret", p.value("dogpay_webhook_secret", ""))
	if secret == "" {
		secret = p.secret("dogpay_appid", p.value("dogpay_appid", ""))
	}
	signature := dogPayHeaderValue(headers, "wh-signature", "x-signature")
	if !shared.VerifyDogPaySignature(body, signature, secret) {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: DogPay signature", cardpool.ErrInvalidWebhookSignature))
	}
	payload, err := decodeWebhookObject(body)
	if err != nil {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: invalid JSON", cardpool.ErrInvalidWebhook))
	}
	// DogPay's webhook envelope uses event_id for the unique delivery id and
	// event_identifier for the event name (for example card.transaction).
	// Keep the older aliases for compatibility with previously captured
	// payloads, but do not treat event_identifier as the event id.
	eventID := shared.StringField(payload, "event_id", "eventId", "eventID", "id", "requestId", "request_id")
	eventType := shared.StringField(payload, "event_identifier", "eventIdentifier", "event", "eventType", "event_type", "type", "name")
	// A pre-CaaS payload used event_identifier for the delivery id and
	// event_type for the event name. Preserve that shape only when an explicit
	// legacy event_type is present; official DogPay payloads use
	// event_identifier as the event name and event_id as the delivery id.
	if eventID == "" && shared.StringField(payload, "event_type") != "" {
		eventID = shared.StringField(payload, "event_identifier", "eventIdentifier")
		eventType = shared.StringField(payload, "event_type")
	}
	if eventID == "" || eventType == "" {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: missing event id or type", cardpool.ErrInvalidWebhook))
	}
	event := cardpool.InternalEvent{ProviderEventID: eventID, EventType: eventType, OccurredAt: dogPayEventTime(payload)}
	resource := dogPayWebhookResource(payload)
	name := strings.ToLower(strings.TrimSpace(eventType))

	if isDogPayTransactionEvent(name) {
		transaction := parseDogPayTransaction(resource, event.OccurredAt)
		if transaction.ProviderTransactionID == "" {
			event.Ignored = true
			return event, nil
		}
		event.ProviderCardID = transaction.ProviderCardID
		event.ProviderTxnID = transaction.ProviderTransactionID
		event.Transaction = &transaction
		return event, nil
	}

	if isDogPayCardEvent(name) {
		event.ProviderCardID = dogPayCardID(resource)
		event.ProviderStatus = firstNonEmpty(shared.StringField(resource, "status", "cardStatus", "card_status", "state"), dogPayEventSuffix(name))
		event.Status = mapStatus(event.ProviderStatus)
		if event.ProviderCardID == "" || event.Status == cardpool.CardFailed {
			event.Ignored = true
		}
		return event, nil
	}

	// Unknown but correctly signed events are acknowledged and kept in the
	// internal ledger. This avoids endless provider retries when DogPay adds a
	// new event type before this adapter is updated.
	event.Ignored = true
	return event, nil
}

func dogPayHeaderValue(headers http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			return value
		}
	}
	for key, values := range headers {
		for _, name := range names {
			if strings.EqualFold(key, name) && len(values) > 0 {
				if value := strings.TrimSpace(values[0]); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func decodeWebhookObject(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, errors.New("webhook payload is not an object")
	}
	return payload, nil
}

func dogPayWebhookResource(payload map[string]any) map[string]any {
	for _, key := range []string{"data", "result", "object", "resource"} {
		if object := shared.ObjectField(payload, key); object != nil {
			if nested := shared.ObjectField(object, "object", "card", "transaction", "authorization"); nested != nil {
				return nested
			}
			return object
		}
	}
	return payload
}

func dogPayEventTime(payload map[string]any) *time.Time {
	for _, object := range []map[string]any{payload, dogPayWebhookResource(payload)} {
		for _, key := range []string{"createdAt", "createAt", "created_at", "create_at", "completeAt", "complete_at", "occurredAt", "occurred_at", "timestamp", "time"} {
			value := shared.StringField(object, key)
			if parsed, ok := parseTime(value); ok {
				return &parsed
			}
		}
	}
	return nil
}

func isDogPayCardEvent(eventType string) bool {
	return strings.Contains(eventType, "card") || strings.Contains(eventType, "virtual")
}

func isDogPayTransactionEvent(eventType string) bool {
	return strings.Contains(eventType, "transaction") || strings.Contains(eventType, "authorization") || strings.Contains(eventType, "payment") || strings.Contains(eventType, "trade")
}

func parseDogPayTransaction(resource map[string]any, occurredAt *time.Time) cardpool.CardTransaction {
	transaction := cardpool.CardTransaction{
		ProviderTransactionID: firstNonEmpty(shared.StringField(resource, "transactionId", "transaction_id", "authorizationId", "authorization_id", "id", "orderId", "order_id")),
		ProviderCardID:        firstNonEmpty(shared.StringField(resource, "cardId", "card_id"), dogPayCardID(shared.ObjectField(resource, "card"))),
		Amount:                shared.FloatField(resource, "amount", "transactionAmount", "transaction_amount", "authorizedAmount", "authorized_amount"),
		Currency:              strings.ToUpper(shared.StringField(resource, "currency", "transactionCurrency", "transaction_currency")),
		Status:                strings.ToUpper(shared.StringField(resource, "status", "transactionStatus", "transaction_status")),
		Type:                  firstNonEmpty(shared.StringField(resource, "type", "transactionType", "transaction_type"), "card_payment"),
		MerchantName:          firstNonEmpty(shared.StringField(resource, "merchantName", "merchant_name"), shared.StringField(resource, "detail")),
		FailureCode:           dogPayFailureCode(resource),
		OccurredAt:            occurredAt,
	}
	if merchant := shared.ObjectField(resource, "merchant", "merchantData", "merchant_data", "merchantInfo", "merchant_info"); merchant != nil {
		transaction.MerchantName = firstNonEmpty(transaction.MerchantName, shared.StringField(merchant, "name", "merchantName", "merchant_name", "displayName", "display_name"))
		transaction.FailureCode = firstNonEmpty(transaction.FailureCode, dogPayFailureCode(merchant))
	}
	if transaction.OccurredAt == nil {
		for _, key := range []string{"createdAt", "createAt", "created_at", "create_at", "completeAt", "complete_at", "transactionAt", "transaction_at"} {
			if parsed, ok := parseTime(shared.StringField(resource, key)); ok {
				transaction.OccurredAt = &parsed
				break
			}
		}
	}
	return transaction
}

func dogPayFailureCode(value map[string]any) string {
	code := firstNonEmpty(shared.StringField(value, "failureCode", "failure_code", "declineCode", "decline_code", "reasonCode", "reason_code"))
	// DogPay uses numeric reasonCode=0 for a successful/non-declined
	// transaction. Do not persist that as a failure code.
	if code == "0" {
		return ""
	}
	return code
}

func dogPayEventSuffix(eventType string) string {
	if index := strings.LastIndexAny(eventType, ".:_/-"); index >= 0 {
		return strings.TrimSpace(eventType[index+1:])
	}
	return strings.TrimSpace(eventType)
}

var _ cardpool.WebhookProvider = (*Provider)(nil)
