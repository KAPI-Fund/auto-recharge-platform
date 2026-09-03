package dogpay

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
)

func TestParseWebhookAcceptsEventIdentifierAndMapsTransactionDetails(t *testing.T) {
	secret := "dogpay-webhook-secret"
	timestamp := time.Now().UnixMilli()
	body := []byte(fmt.Sprintf(`{"event_id":"evt-dog-1","event_identifier":"card.transaction.failed","timestamp":%d,"data":{"id":"txn-dog-1","cardId":"dog-card-1","amount":12.5,"currency":"usd","status":"failed","type":"authorization","merchantInfo":{"name":"Example Merchant","reasonCode":"INSUFFICIENT_FUNDS"}}}`, timestamp))
	signature := dogPayTestSignature(body, secret)
	provider := New(testConfig{"dogpay_webhook_secret": secret}, nil)
	event, err := provider.ParseWebhook(context.Background(), http.Header{"Wh-Signature": []string{signature}}, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.ProviderEventID != "evt-dog-1" || event.EventType != "card.transaction.failed" || event.ProviderCardID != "dog-card-1" || event.ProviderTxnID != "txn-dog-1" {
		t.Fatalf("unexpected event identity: %#v", event)
	}
	if event.Transaction == nil || event.Transaction.MerchantName != "Example Merchant" || event.Transaction.FailureCode != "INSUFFICIENT_FUNDS" || event.Transaction.Currency != "USD" {
		t.Fatalf("unexpected transaction mapping: %#v", event.Transaction)
	}
	if event.OccurredAt == nil || event.OccurredAt.UnixMilli() != timestamp {
		t.Fatalf("unexpected event time: %#v", event.OccurredAt)
	}
}

func TestParseWebhookKeepsLegacyEventEnvelopeCompatibility(t *testing.T) {
	secret := "dogpay-webhook-secret"
	body := []byte(`{"event_identifier":"legacy-event-id","event_type":"card.transaction.failed","data":{"id":"legacy-txn","cardId":"legacy-card","status":"failed"}}`)
	provider := New(testConfig{"dogpay_webhook_secret": secret}, nil)
	event, err := provider.ParseWebhook(context.Background(), http.Header{"Wh-Signature": []string{dogPayTestSignature(body, secret)}}, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.ProviderEventID != "legacy-event-id" || event.EventType != "card.transaction.failed" || event.ProviderTxnID != "legacy-txn" {
		t.Fatalf("unexpected legacy event mapping: %#v", event)
	}
}

func TestParseWebhookMapsTransactionUpdateAndOfficialFallbackFields(t *testing.T) {
	secret := "dogpay-webhook-secret"
	body := []byte(`{"event_id":"evt-dog-update","event_identifier":"card.transaction.update","data":{"id":"txn-dog-update","cardId":"dog-card-update","amount":"12.50","currency":"USD","status":"settled","detail":"Example Merchant Detail","reasonCode":0,"completeAt":"2026-08-28T10:01:02Z"}}`)
	provider := New(testConfig{"dogpay_webhook_secret": secret}, nil)
	event, err := provider.ParseWebhook(context.Background(), http.Header{"Wh-Signature": []string{dogPayTestSignature(body, secret)}}, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.EventType != "card.transaction.update" || event.Transaction == nil {
		t.Fatalf("unexpected update event: %#v", event)
	}
	transaction := event.Transaction
	if transaction.ProviderTransactionID != "txn-dog-update" || transaction.MerchantName != "Example Merchant Detail" || transaction.FailureCode != "" {
		t.Fatalf("unexpected transaction fallback mapping: %#v", transaction)
	}
	if transaction.OccurredAt == nil || transaction.OccurredAt.UTC().Format(time.RFC3339) != "2026-08-28T10:01:02Z" {
		t.Fatalf("unexpected completeAt mapping: %v", transaction.OccurredAt)
	}
}

func TestParseWebhookMapsDeletedCardEventsToCancelled(t *testing.T) {
	secret := "dogpay-webhook-secret"
	for _, eventType := range []string{"card.pre_delete", "card.deleted"} {
		t.Run(eventType, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"event_id":"evt-%s","event_identifier":%q,"data":{"cardId":"dog-card-delete","status":%q}}`, strings.ReplaceAll(eventType, ".", "-"), eventType, strings.TrimPrefix(eventType, "card.")))
			provider := New(testConfig{"dogpay_webhook_secret": secret}, nil)
			event, err := provider.ParseWebhook(context.Background(), http.Header{"Wh-Signature": []string{dogPayTestSignature(body, secret)}}, body)
			if err != nil {
				t.Fatalf("ParseWebhook() error = %v", err)
			}
			if event.Status != cardpool.CardCancelled || event.ProviderCardID != "dog-card-delete" || event.Ignored {
				t.Fatalf("unexpected cancelled card event: %#v", event)
			}
		})
	}
}

func TestParseWebhookAcceptsBase64Signature(t *testing.T) {
	secret := "dogpay-webhook-secret"
	body := []byte(`{"id":"evt-dog-2","type":"issuing.card.active","data":{"cardId":"dog-card-2","status":"active"}}`)
	provider := New(testConfig{"dogpay_webhook_secret": secret}, nil)
	digest := hmac.New(sha512.New, []byte(secret))
	_, _ = digest.Write(body)
	event, err := provider.ParseWebhook(context.Background(), http.Header{"X-Signature": []string{base64.StdEncoding.EncodeToString(digest.Sum(nil))}}, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.ProviderEventID != "evt-dog-2" || event.ProviderCardID != "dog-card-2" || event.Status != cardpool.CardActive {
		t.Fatalf("unexpected card event: %#v", event)
	}
}

func TestParseWebhookMapsOfficialDataObjectAndCreateAt(t *testing.T) {
	secret := "dogpay-webhook-secret"
	body := []byte(`{"event_id":"evt-dog-4","event":"card.transaction.created","data":{"id":"txn-dog-4","cardId":"dog-card-4","amount":"12.50","currency":"USD","status":"authorized","type":"authorization","createAt":"2026-08-28T10:00:00Z","merchantInfo":{"name":"Example Merchant"}}}`)
	provider := New(testConfig{"dogpay_webhook_secret": secret}, nil)
	event, err := provider.ParseWebhook(context.Background(), http.Header{"Wh-Signature": []string{dogPayTestSignature(body, secret)}}, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.ProviderEventID != "evt-dog-4" || event.EventType != "card.transaction.created" || event.ProviderCardID != "dog-card-4" || event.ProviderTxnID != "txn-dog-4" {
		t.Fatalf("unexpected event identity: %#v", event)
	}
	if event.Transaction == nil || event.Transaction.Amount != 12.5 || event.Transaction.MerchantName != "Example Merchant" || event.Transaction.OccurredAt == nil {
		t.Fatalf("unexpected transaction mapping: %#v", event.Transaction)
	}
	if event.Transaction.OccurredAt.UTC().Format(time.RFC3339) != "2026-08-28T10:00:00Z" {
		t.Fatalf("unexpected transaction time: %v", event.Transaction.OccurredAt)
	}
}

func TestMapCardAcceptsLastFourFieldWithoutReadingPAN(t *testing.T) {
	card := mapCard(map[string]any{"id": "dog-card-5", "status": "active", "lastFour": "4242", "cardNumber": "4242424242424242"}, cardpool.CreateCardRequest{})
	if card.ProviderCardID != "dog-card-5" || card.Last4 != "4242" || card.Status != cardpool.CardActive {
		t.Fatalf("unexpected card mapping: %+v", card)
	}
}

func TestParseWebhookRejectsInvalidSignature(t *testing.T) {
	provider := New(testConfig{"dogpay_webhook_secret": "dogpay-webhook-secret"}, nil)
	_, err := provider.ParseWebhook(context.Background(), http.Header{"X-Signature": []string{"00"}}, []byte(`{"id":"evt-dog-3","type":"issuing.card.active"}`))
	if err == nil || !cardpool.IsWebhookValidationError(err) || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("error = %v, want signature validation error", err)
	}
}

func dogPayTestSignature(body []byte, secret string) string {
	digest := hmac.New(sha512.New, []byte(secret))
	_, _ = digest.Write(body)
	return hex.EncodeToString(digest.Sum(nil))
}

func TestParseTimeAcceptsDogPayUnixSecondsAndMilliseconds(t *testing.T) {
	seconds := strconv.FormatInt(time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC).Unix(), 10)
	if parsed, ok := parseTime(seconds); !ok || parsed.Year() != 2026 {
		t.Fatalf("parseTime(%q) = %v, %t", seconds, parsed, ok)
	}
	milliseconds := strconv.FormatInt(time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC).UnixMilli(), 10)
	if parsed, ok := parseTime(milliseconds); !ok || parsed.Year() != 2026 {
		t.Fatalf("parseTime(%q) = %v, %t", milliseconds, parsed, ok)
	}
}

func TestMapStatusMapsDogPayLifecycleStates(t *testing.T) {
	for _, test := range []struct {
		providerStatus string
		want           cardpool.InternalCardStatus
	}{
		{providerStatus: "opening", want: cardpool.CardCreating},
		{providerStatus: "processing", want: cardpool.CardCreating},
		{providerStatus: "pre_delete", want: cardpool.CardCancelled},
		{providerStatus: "deleted", want: cardpool.CardCancelled},
	} {
		t.Run(test.providerStatus, func(t *testing.T) {
			if got := mapStatus(test.providerStatus); got != test.want {
				t.Fatalf("mapStatus(%q) = %q, want %q", test.providerStatus, got, test.want)
			}
		})
	}
}
