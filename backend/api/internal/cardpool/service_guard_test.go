package cardpool

import (
	"testing"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestProviderReleaseClaimExpiry(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	active := now.Add(-providerReleaseClaimTTL + time.Second)
	stale := now.Add(-providerReleaseClaimTTL - time.Second)
	if !providerReleaseClaimIsActive(&active, now) {
		t.Fatal("a claim inside the TTL was treated as stale")
	}
	if providerReleaseClaimIsActive(&stale, now) {
		t.Fatal("a claim outside the TTL was treated as active")
	}
	if providerReleaseClaimIsActive(nil, now) {
		t.Fatal("a missing claim timestamp was treated as active")
	}
}

func TestValidateExistingCardForProvisionRejectsConsumedAndUnavailableCards(t *testing.T) {
	tests := []struct {
		name string
		card models.PaymentCard
		want error
	}{
		{name: "used status", card: models.PaymentCard{PoolID: "pool-a", Provider: "TEST", Status: string(CardUsed), UsageCount: 1}, want: ErrCardConsumed},
		{name: "cancelled status", card: models.PaymentCard{PoolID: "pool-a", Provider: "TEST", Status: string(CardCancelled)}, want: ErrCardConsumed},
		{name: "failed status", card: models.PaymentCard{PoolID: "pool-a", Provider: "TEST", Status: string(CardFailed)}, want: ErrCardUnavailable},
		{name: "frozen status", card: models.PaymentCard{PoolID: "pool-a", Provider: "TEST", Status: string(CardFrozen)}, want: ErrCardUnavailable},
		{name: "in use flag", card: models.PaymentCard{PoolID: "pool-a", Provider: "TEST", Status: string(CardActive), InUse: true}, want: ErrCardUnavailable},
		{name: "pool mismatch", card: models.PaymentCard{PoolID: "pool-b", Provider: "TEST", Status: string(CardActive)}, want: ErrCardPoolMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateExistingCardForProvision(test.card, PaymentCard{PoolID: "pool-a", Provider: "TEST"})
			if err != test.want {
				t.Fatalf("validateExistingCardForProvision() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateExistingCardForAllocationRejectsConsumedCardEvenIfProviderReportsActive(t *testing.T) {
	err := validateExistingCardForAllocation(models.PaymentCard{
		PoolID: "pool-a", Provider: "TEST", ProviderCardID: "remote-card-1",
		Status: string(CardCancelled), UsageCount: 1,
	}, "pool-a")
	if err != ErrCardConsumed {
		t.Fatalf("validateExistingCardForAllocation() error = %v, want %v", err, ErrCardConsumed)
	}
}

func TestMergePaymentCardPreservesLocalConsumedLifecycleState(t *testing.T) {
	for _, status := range []InternalCardStatus{CardUsed, CardCancelled, CardFailed, CardFrozen, CardInUse} {
		merged := mergePaymentCard(
			PaymentCard{InternalCardID: "card-1", PoolID: "pool-a", Provider: "TEST", ProviderCardID: "remote-1", Status: status, UsageCount: 1},
			PaymentCard{Status: CardActive, ProviderStatus: "active", Last4: "4242"},
		)
		if merged.Status != status {
			t.Fatalf("mergePaymentCard() status = %s for local status %s, want local status preserved", merged.Status, status)
		}
	}
}

func TestProviderErrorCodeIncludesCardLifecycleGuard(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{err: ErrCardConsumed, want: "card_consumed"},
		{err: ErrCardUnavailable, want: "card_unavailable"},
		{err: ErrCardPoolMismatch, want: "card_pool_mismatch"},
	}
	for _, test := range tests {
		if got := providerErrorCode(test.err); got != test.want {
			t.Errorf("providerErrorCode(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}
