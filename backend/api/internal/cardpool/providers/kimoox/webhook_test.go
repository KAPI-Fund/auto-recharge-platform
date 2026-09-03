package kimoox

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
)

func TestParseWebhookMapsCardIssueSuccessWithoutSensitiveFields(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"eventId":"evt-issue-1","eventType":"CARD_ISSUE.SUCCESS","eventTime":"2026-09-02 12:00:00","data":{"taskId":901,"batchNo":"BATCH-1","cards":[{"cardId":"VC-1","cardNoMask":"486880******1234","cardStatus":1,"cardNumberCiphertext":"ciphertext","expiryDateCiphertext":"ciphertext","cvvCiphertext":"ciphertext"}]}}`)
	headers := signedWebhookHeaders(body, "evt-issue-1", "CARD_ISSUE.SUCCESS", secret)
	provider := New(testConfig{"kimoox_webhook_secret": secret, "kimoox_webhook_tolerance_seconds": "300"}, nil)
	event, err := provider.ParseWebhook(context.Background(), headers, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.ProviderEventID != "evt-issue-1" || event.EventType != "CARD_ISSUE.SUCCESS" || event.ProviderCardID != "VC-1" || event.Status != cardpool.CardActive {
		t.Fatalf("event = %#v", event)
	}
	if event.Transaction != nil || event.ProviderStatus != "1" {
		t.Fatalf("unexpected sensitive/transaction fields: %#v", event)
	}
}

func TestParseWebhookMapsTransactionStagesAndTime(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"eventId":"evt-txn-1","eventType":"CARD_TRANSACTION.AUTH_FAILED","eventTime":"2026-09-02 12:03:00","data":{"transactionId":"TX-1","cardId":"VC-1","eventStatus":"AUTH_FAILED","transactionType":"PURCHASE","originalAmount":"12.00","originalCurrency":"USD","actualAmount":"0.00","actualCurrency":"USD","merchantName":"Example Store","displayFailReason":"insufficient balance","transactionTime":"2026-09-02 12:02:55"}}`)
	headers := signedWebhookHeaders(body, "evt-txn-1", "CARD_TRANSACTION.AUTH_FAILED", secret)
	provider := New(testConfig{"kimoox_webhook_secret": secret}, nil)
	event, err := provider.ParseWebhook(context.Background(), headers, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.ProviderCardID != "VC-1" || event.ProviderTxnID != "TX-1" || event.Transaction == nil {
		t.Fatalf("event = %#v", event)
	}
	if event.Transaction.Status != "AUTH_FAILED" || event.Transaction.FailureCode != "insufficient balance" || event.Transaction.Amount != 0 || event.Transaction.Currency != "USD" {
		t.Fatalf("transaction = %#v", event.Transaction)
	}
	if event.OccurredAt == nil || event.OccurredAt.Format("2006-01-02 15:04:05") != "2026-09-02 12:03:00" {
		t.Fatalf("occurred at = %v", event.OccurredAt)
	}
}

func TestParseWebhookMapsCardOperations(t *testing.T) {
	secret := "webhook-secret"
	for _, test := range []struct {
		eventType string
		status    cardpool.InternalCardStatus
	}{
		{"CARD_OPERATION.FREEZE_SUCCESS", cardpool.CardFrozen},
		{"CARD_OPERATION.UNFREEZE_SUCCESS", cardpool.CardActive},
		{"CARD_OPERATION.CANCEL_SUCCESS", cardpool.CardCancelled},
		{"CARD.RISK_CANCELLED", cardpool.CardCancelled},
	} {
		t.Run(test.eventType, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"eventId":"evt-%s","eventType":%q,"eventTime":"2026-09-02 12:00:00","data":{"cardId":"VC-1"}}`, strings.ReplaceAll(test.eventType, ".", "-"), test.eventType))
			eventID := "evt-" + strings.ReplaceAll(test.eventType, ".", "-")
			headers := signedWebhookHeaders(body, eventID, test.eventType, secret)
			provider := New(testConfig{"kimoox_webhook_secret": secret}, nil)
			event, err := provider.ParseWebhook(context.Background(), headers, body)
			if err != nil {
				t.Fatalf("ParseWebhook() error = %v", err)
			}
			if event.ProviderCardID != "VC-1" || event.Status != test.status || event.Ignored {
				t.Fatalf("event = %#v", event)
			}
		})
	}
}

func TestParseWebhookAcknowledgesUnknownSignedEvents(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"eventId":"evt-future","eventType":"BUDGET_OPERATION.CREATED","eventTime":"2026-09-02 12:00:00","data":{"budgetId":501}}`)
	headers := signedWebhookHeaders(body, "evt-future", "BUDGET_OPERATION.CREATED", secret)
	provider := New(testConfig{"kimoox_webhook_secret": secret}, nil)
	event, err := provider.ParseWebhook(context.Background(), headers, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if !event.Ignored || event.ProviderEventID != "evt-future" || event.EventType != "BUDGET_OPERATION.CREATED" {
		t.Fatalf("event = %#v", event)
	}
}

func TestParseWebhookRejectsWrongSignatureAndStaleTimestamp(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"eventId":"evt-invalid","eventType":"CARD_TRANSACTION.SETTLED","data":{"transactionId":"TX-1","cardId":"VC-1"}}`)
	provider := New(testConfig{"kimoox_webhook_secret": secret, "kimoox_webhook_tolerance_seconds": "10"}, nil)
	bad := signedWebhookHeaders(body, "evt-invalid", "CARD_TRANSACTION.SETTLED", "other-secret")
	if _, err := provider.ParseWebhook(context.Background(), bad, body); err == nil || !cardpool.IsWebhookValidationError(err) {
		t.Fatalf("wrong signature error = %v", err)
	}
	stale := signedWebhookHeadersAt(body, "evt-invalid", "CARD_TRANSACTION.SETTLED", secret, time.Now().Add(-time.Minute))
	if _, err := provider.ParseWebhook(context.Background(), stale, body); err == nil || !cardpool.IsWebhookValidationError(err) {
		t.Fatalf("stale timestamp error = %v", err)
	}
}

func signedWebhookHeaders(body []byte, eventID, eventType, secret string) http.Header {
	return signedWebhookHeadersAt(body, eventID, eventType, secret, time.Now())
}

func signedWebhookHeadersAt(body []byte, eventID, eventType, secret string, now time.Time) http.Header {
	timestamp := fmt.Sprintf("%d", now.UnixMilli())
	nonce := "nonce-1"
	bodyHash := sha256.Sum256(body)
	canonical := strings.Join([]string{eventID, eventType, timestamp, nonce, hex.EncodeToString(bodyHash[:])}, "\n")
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write([]byte(canonical))
	headers := make(http.Header)
	headers.Set("X-VCC-WEBHOOK-ID", eventID)
	headers.Set("X-VCC-WEBHOOK-EVENT", eventType)
	headers.Set("X-VCC-WEBHOOK-TIMESTAMP", timestamp)
	headers.Set("X-VCC-WEBHOOK-NONCE", nonce)
	headers.Set("X-VCC-WEBHOOK-SIGNATURE", "v1="+hex.EncodeToString(digest.Sum(nil)))
	return headers
}
