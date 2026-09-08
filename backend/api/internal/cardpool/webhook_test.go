package cardpool

import (
	"strings"
	"testing"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestSanitizeWebhookPayloadRemovesSensitiveCardFields(t *testing.T) {
	payload := SanitizeWebhookPayload([]byte(`{"id":"evt-1","data":{"object":{"card_number":"4242424242424242","number":"4242424242424242","cvc":"123","cvv":"456","otpCode":"387123","transaction_id":"txn-1"}}}`))
	for _, secret := range []string{"4242424242424242", "123", "456", "387123"} {
		if strings.Contains(payload, secret) {
			t.Fatalf("sanitized payload contains sensitive value %q: %s", secret, payload)
		}
	}
	if !strings.Contains(payload, "txn-1") || !strings.Contains(payload, "[REDACTED]") {
		t.Fatalf("useful metadata was not retained: %s", payload)
	}
}

func TestApplyCardStatusDoesNotRegressLocalLifecycle(t *testing.T) {
	old := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	newer := old.Add(time.Minute)
	for _, test := range []struct {
		name        string
		status      string
		eventStatus InternalCardStatus
		want        bool
	}{
		{name: "used remains used", status: string(CardUsed), eventStatus: CardFrozen, want: false},
		{name: "in use remains in use when provider is active", status: string(CardInUse), eventStatus: CardActive, want: false},
		{name: "active card may freeze", status: string(CardActive), eventStatus: CardFrozen, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			card := models.PaymentCard{Status: test.status, ProviderStatusAt: &old}
			event := InternalEvent{Status: test.eventStatus, OccurredAt: &newer}
			if got := applyCardStatus(card, event, newer); got != test.want {
				t.Fatalf("applyCardStatus() = %t, want %t", got, test.want)
			}
		})
	}
	older := old.Add(-time.Minute)
	if applyCardStatus(models.PaymentCard{Status: string(CardActive), ProviderStatusAt: &old}, InternalEvent{Status: CardFrozen, OccurredAt: &older}, newer) {
		t.Fatal("older provider status event was applied")
	}
}
