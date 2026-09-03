package stripeissuing

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

func TestParseWebhookVerifiesStripeIssuingSignatureAndMapsCardEvent(t *testing.T) {
	secret := "whsec_issuing"
	timestamp := time.Now().Unix()
	body := []byte(fmt.Sprintf(`{"id":"evt_card_1","type":"issuing_card.updated","created":%d,"data":{"object":{"id":"ic_1","status":"inactive","number":"4242424242424242","cvc":"123"}}}`, timestamp))
	header := stripeTestSignature(body, timestamp, secret)
	provider := New(testConfig{"stripe_issuing_webhook_secret": secret}, nil)
	event, err := provider.ParseWebhook(context.Background(), http.Header{"Stripe-Signature": []string{header}}, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.ProviderEventID != "evt_card_1" || event.EventType != "issuing_card.updated" || event.ProviderCardID != "ic_1" {
		t.Fatalf("unexpected event identity: %#v", event)
	}
	if event.Status != cardpool.CardFrozen || event.ProviderStatus != "inactive" || event.Ignored {
		t.Fatalf("unexpected card event mapping: %#v", event)
	}
}

func TestParseWebhookMapsStripeIssuingAuthorization(t *testing.T) {
	secret := "whsec_issuing"
	timestamp := time.Now().Unix()
	body := []byte(fmt.Sprintf(`{"id":"evt_auth_1","type":"issuing_authorization.request","created":%d,"data":{"object":{"id":"ia_1","card":"ic_1","amount":1234,"currency":"usd","status":"pending","created":%d,"merchant_data":{"name":"Merchant A"},"failure_code":""}}}`, timestamp, timestamp))
	provider := New(testConfig{"stripe_issuing_webhook_secret": secret}, nil)
	event, err := provider.ParseWebhook(context.Background(), http.Header{"Stripe-Signature": []string{stripeTestSignature(body, timestamp, secret)}}, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.ProviderCardID != "ic_1" || event.ProviderTxnID != "ia_1" || event.Transaction == nil {
		t.Fatalf("authorization identity = %#v", event)
	}
	if event.Transaction.Amount != 12.34 || event.Transaction.Currency != "USD" || event.Transaction.Status != "PENDING" || event.Transaction.Type != "authorization" || event.Transaction.MerchantName != "Merchant A" {
		t.Fatalf("authorization mapping = %#v", event.Transaction)
	}
}

func TestParseWebhookRejectsInvalidStripeSignature(t *testing.T) {
	provider := New(testConfig{"stripe_issuing_webhook_secret": "whsec_issuing"}, nil)
	timestamp := time.Now().Unix()
	_, err := provider.ParseWebhook(context.Background(), http.Header{"Stripe-Signature": []string{fmt.Sprintf("t=%d,v1=00", timestamp)}}, []byte(`{"id":"evt_1","type":"issuing_card.updated"}`))
	if err == nil || !cardpool.IsWebhookValidationError(err) || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("error = %v, want webhook signature validation error", err)
	}
}

func stripeTestSignature(body []byte, timestamp int64, secret string) string {
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write([]byte(strconv.FormatInt(timestamp, 10)))
	_, _ = digest.Write([]byte("."))
	_, _ = digest.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(digest.Sum(nil)))
}
