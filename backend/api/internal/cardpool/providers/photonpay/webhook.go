package photonpay

import (
	"bytes"
	"context"
	"crypto"
	"crypto/md5" // #nosec G501 -- PhotonPay's documented request/webhook signature uses MD5withRSA.
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/shared"
)

// ParseWebhook verifies PhotonPay's RSA signature with the configured
// platform public key, then maps card and trade events into the common event
// model. PAN/CVV-like fields are never copied into the returned event.
func (p *Provider) ParseWebhook(ctx context.Context, headers http.Header, body []byte) (cardpool.InternalEvent, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return cardpool.InternalEvent{}, err
		}
	}
	publicKeyValue := p.secret("photonpay_webhook_public_key", p.value("photonpay_webhook_public_key", ""))
	signature := photonHeaderValue(headers, "X-PD-SIGN", "X-Signature")
	if !verifyPhotonWebhookSignature(body, signature, publicKeyValue) {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: PhotonPay signature", cardpool.ErrInvalidWebhookSignature))
	}
	payload, err := decodePhotonWebhookObject(body)
	if err != nil {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: invalid JSON", cardpool.ErrInvalidWebhook))
	}
	eventID := shared.StringField(payload, "eventId", "event_id", "eventID", "event_identifier", "eventIdentifier", "id", "requestId", "request_id")
	eventType := shared.StringField(payload, "eventType", "event_type", "type", "name", "event")
	if eventID == "" || eventType == "" {
		return cardpool.InternalEvent{}, cardpool.NewProviderError(providerName, "parse_webhook", cardpool.CategoryInvalidRequest, false, false, fmt.Errorf("%w: missing event id or type", cardpool.ErrInvalidWebhook))
	}
	event := cardpool.InternalEvent{ProviderEventID: eventID, EventType: eventType, OccurredAt: photonWebhookTime(payload)}
	resource := photonWebhookResource(payload)
	name := strings.ToLower(strings.TrimSpace(eventType))
	if isPhotonTransactionEvent(name) {
		transaction := parsePhotonWebhookTransaction(resource, event.OccurredAt)
		if transaction.ProviderTransactionID == "" {
			event.Ignored = true
			return event, nil
		}
		event.ProviderCardID = transaction.ProviderCardID
		event.ProviderTxnID = transaction.ProviderTransactionID
		event.Transaction = &transaction
		return event, nil
	}
	if isPhotonCardEvent(name) || photonCardID(resource) != "" {
		event.ProviderCardID = photonCardID(resource)
		event.ProviderStatus = firstNonEmpty(shared.StringField(resource, "cardStatus", "card_status", "status", "state"), photonEventSuffix(name))
		event.Status = mapStatus(event.ProviderStatus)
		if event.ProviderCardID == "" || event.Status == cardpool.CardFailed {
			event.Ignored = true
		}
		return event, nil
	}
	// Correctly signed future PhotonPay events should be acknowledged and
	// recorded even if this adapter does not yet understand their payload.
	event.Ignored = true
	return event, nil
}

func verifyPhotonWebhookSignature(body []byte, signature, publicKeyValue string) bool {
	key, err := parsePublicKey(publicKeyValue)
	if err != nil || strings.TrimSpace(signature) == "" {
		return false
	}
	encoded := strings.TrimSpace(signature)
	encoded = strings.TrimPrefix(encoded, "Base64(")
	encoded = strings.TrimSuffix(encoded, ")")
	var decodedCandidates [][]byte
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if value, decodeErr := encoding.DecodeString(encoded); decodeErr == nil {
			decodedCandidates = append(decodedCandidates, value)
		}
	}
	if value, decodeErr := hex.DecodeString(encoded); decodeErr == nil {
		decodedCandidates = append(decodedCandidates, value)
	}
	if len(decodedCandidates) == 0 {
		return false
	}

	md5Digest := md5.Sum(body) // #nosec G401 -- required by PhotonPay's signature contract.
	shaDigest := sha256.Sum256(body)
	for _, decoded := range decodedCandidates {
		if rsa.VerifyPKCS1v15(key, crypto.MD5, md5Digest[:], decoded) == nil {
			return true
		}
		if rsa.VerifyPKCS1v15(key, crypto.SHA256, shaDigest[:], decoded) == nil {
			return true
		}
	}
	return false
}

func photonHeaderValue(headers http.Header, names ...string) string {
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

func parsePublicKey(value string) (*rsa.PublicKey, error) {
	data := []byte(strings.TrimSpace(strings.ReplaceAll(value, `\n`, "\n")))
	if len(data) == 0 {
		return nil, errors.New("PhotonPay webhook public key 未配置")
	}
	if block, _ := pem.Decode(data); block != nil {
		data = block.Bytes
	}
	if key, err := x509.ParsePKIXPublicKey(data); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}
	if key, err := x509.ParsePKCS1PublicKey(data); err == nil {
		return key, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value)); err == nil {
		if key, parseErr := x509.ParsePKIXPublicKey(decoded); parseErr == nil {
			if rsaKey, ok := key.(*rsa.PublicKey); ok {
				return rsaKey, nil
			}
		}
		if key, parseErr := x509.ParsePKCS1PublicKey(decoded); parseErr == nil {
			return key, nil
		}
	}
	return nil, errors.New("PhotonPay webhook public key 格式无效")
}

func decodePhotonWebhookObject(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, errors.New("webhook payload is not an object")
	}
	return payload, nil
}

func photonWebhookResource(payload map[string]any) map[string]any {
	for _, key := range []string{"data", "result", "object", "resource"} {
		if object := shared.ObjectField(payload, key); object != nil {
			if nested := shared.ObjectField(object, "object", "card", "cardDetail", "card_detail", "transaction", "tradeOrder", "trade_order"); nested != nil {
				return nested
			}
			return object
		}
	}
	return payload
}

func photonWebhookTime(payload map[string]any) *time.Time {
	for _, object := range []map[string]any{payload, photonWebhookResource(payload)} {
		for _, key := range []string{"createdAt", "createAt", "created_at", "create_at", "completeAt", "complete_at", "occurredAt", "occurred_at", "txnDate", "transactionAt", "timestamp"} {
			value := shared.StringField(object, key)
			if parsed, ok := parsePhotonTime(value); ok {
				return &parsed
			}
		}
	}
	return nil
}

func photonCardID(resource map[string]any) string {
	return firstNonEmpty(shared.StringField(resource, "cardId", "card_id", "vccId", "vcc_id", "id"))
}

func isPhotonCardEvent(eventType string) bool {
	return strings.Contains(eventType, "card") || strings.Contains(eventType, "vcc")
}

func isPhotonTransactionEvent(eventType string) bool {
	return strings.Contains(eventType, "transaction") || strings.Contains(eventType, "trade") || strings.Contains(eventType, "order") || strings.Contains(eventType, "authorization")
}

func parsePhotonWebhookTransaction(resource map[string]any, occurredAt *time.Time) cardpool.CardTransaction {
	transaction := cardpool.CardTransaction{
		ProviderTransactionID: firstNonEmpty(shared.StringField(resource, "transactionId", "transaction_id", "tradeOrderId", "trade_order_id", "orderId", "order_id", "id")),
		ProviderCardID:        firstNonEmpty(shared.StringField(resource, "cardId", "card_id", "vccId", "vcc_id"), photonCardID(shared.ObjectField(resource, "card"))),
		Amount:                shared.FloatField(resource, "transactionAmount", "transaction_amount", "amount", "tradeAmount", "trade_amount"),
		Currency:              strings.ToUpper(shared.StringField(resource, "transactionCurrency", "transaction_currency", "currency")),
		Status:                strings.ToUpper(shared.StringField(resource, "transactionStatus", "transaction_status", "status")),
		Type:                  firstNonEmpty(shared.StringField(resource, "transactionType", "transaction_type", "type"), "card_payment"),
		MerchantName:          firstNonEmpty(shared.StringField(resource, "merchantName", "merchant_name", "merchantNameLocation", "merchant_name_location"), shared.StringField(resource, "detail")),
		FailureCode:           shared.StringField(resource, "failureCode", "failure_code", "code", "declineCode", "decline_code", "reasonCode", "reason_code"),
		OccurredAt:            occurredAt,
	}
	if merchant := shared.ObjectField(resource, "merchant", "merchantData", "merchant_data", "merchantInfo", "merchant_info"); merchant != nil {
		transaction.MerchantName = firstNonEmpty(transaction.MerchantName, shared.StringField(merchant, "name", "merchantName", "merchant_name", "displayName", "display_name"))
		transaction.FailureCode = firstNonEmpty(transaction.FailureCode, shared.StringField(merchant, "reasonCode", "reason_code", "failureCode", "failure_code"))
	}
	if transaction.OccurredAt == nil {
		if parsed, ok := parsePhotonTime(firstNonEmpty(shared.StringField(resource, "txnDate", "txn_date", "createdAt", "created_at"))); ok {
			transaction.OccurredAt = &parsed
		}
	}
	return transaction
}

func photonEventSuffix(eventType string) string {
	if index := strings.LastIndexAny(eventType, ".:_/-"); index >= 0 {
		return strings.TrimSpace(eventType[index+1:])
	}
	return strings.TrimSpace(eventType)
}

var _ cardpool.WebhookProvider = (*Provider)(nil)
