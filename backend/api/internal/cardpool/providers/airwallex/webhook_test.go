package airwallex

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
)

func TestParseWebhookVerifiesOfficialAirwallexSignatureAndMapsCardEvent(t *testing.T) {
	secret := "airwallex-webhook-secret"
	body := []byte(fmt.Sprintf(`{"id":"evt-card-1","name":"issuing.card.active","created_at":%q,"data":{"card_id":"aw-card-1","card_status":"ACTIVE","card_number":"4242424242424242"}}`, time.Now().UTC().Format(time.RFC3339)))
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write([]byte(timestamp))
	_, _ = digest.Write(body)

	provider := New(testConfig{"airwallex_webhook_secret": secret}, nil)
	event, err := provider.ParseWebhook(context.Background(), http.Header{
		"X-Timestamp": []string{timestamp}, "X-Signature": []string{hex.EncodeToString(digest.Sum(nil))},
	}, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.ProviderEventID != "evt-card-1" || event.EventType != "issuing.card.active" || event.ProviderCardID != "aw-card-1" {
		t.Fatalf("unexpected event identity: %#v", event)
	}
	if event.Status != cardpool.CardActive || event.ProviderStatus != "ACTIVE" || event.Ignored {
		t.Fatalf("unexpected card event mapping: %#v", event)
	}
}

func TestParseWebhookMapsAirwallexTransactionEvent(t *testing.T) {
	secret := "airwallex-webhook-secret"
	body := []byte(`{"id":"evt-txn-1","name":"issuing.transaction.failed","created_at":"2026-08-28T10:00:00Z","data":{"transaction_id":"txn-1","card_id":"aw-card-1","billing_amount":-100,"billing_currency":"USD","status":"FAILED","transaction_type":"AUTHORIZATION","failure_reason":"CURRENCY_NOT_ALLOWED","merchant":{"name":"Merchant A"},"transaction_date":"2026-08-28T10:00:00Z"}}`)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write([]byte(timestamp))
	_, _ = digest.Write(body)
	provider := New(testConfig{"airwallex_webhook_secret": secret}, nil)
	event, err := provider.ParseWebhook(context.Background(), http.Header{"X-Timestamp": []string{timestamp}, "X-Signature": []string{hex.EncodeToString(digest.Sum(nil))}}, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.ProviderCardID != "aw-card-1" || event.ProviderTxnID != "txn-1" || event.Transaction == nil {
		t.Fatalf("transaction identity = %#v", event)
	}
	if event.Transaction.Amount != -100 || event.Transaction.Currency != "USD" || event.Transaction.MerchantName != "Merchant A" || event.Transaction.FailureCode != "CURRENCY_NOT_ALLOWED" {
		t.Fatalf("transaction mapping = %#v", event.Transaction)
	}
}

func TestParseWebhookRejectsInvalidAirwallexSignature(t *testing.T) {
	provider := New(testConfig{"airwallex_webhook_secret": "airwallex-webhook-secret"}, nil)
	_, err := provider.ParseWebhook(context.Background(), http.Header{"X-Timestamp": []string{strconv.FormatInt(time.Now().UnixMilli(), 10)}, "X-Signature": []string{"00"}}, []byte(`{"id":"evt-1","name":"issuing.card.active"}`))
	if err == nil || !cardpool.IsWebhookValidationError(err) || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("error = %v, want webhook signature validation error", err)
	}
}
