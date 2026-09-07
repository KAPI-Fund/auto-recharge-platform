package cardpool

import "testing"

func TestReleaseActionForProviderDoesNotCancelUnsubmittedOneTimePayment(t *testing.T) {
	if got := releaseActionForProvider(string(UsageOneTime), false, false, true, false, false); got != "" {
		t.Fatalf("unsubmitted one-time payment release action = %q, want no provider action", got)
	}
}

func TestReleaseActionForProviderCancelsCompletedOneTimePayment(t *testing.T) {
	if got := releaseActionForProvider(string(UsageOneTime), true, true, true, false, false); got != ProviderReleaseActionCancel {
		t.Fatalf("completed one-time payment release action = %q, want %q", got, ProviderReleaseActionCancel)
	}
}

func TestReleaseActionForProviderDoesNotCancelFailedPayment(t *testing.T) {
	if got := releaseActionForProvider(string(UsageOneTime), true, false, true, false, false); got != "" {
		t.Fatalf("failed one-time payment release action = %q, want no provider action", got)
	}
}

func TestReleaseActionForProviderDoesNotCancelWhenSwitchOff(t *testing.T) {
	if got := releaseActionForProvider(string(UsageOneTime), true, true, false, false, false); got != "" {
		t.Fatalf("successful payment with cancel switch off = %q, want no provider action", got)
	}
}

func TestReleaseActionForProviderKeepsReservationReleaseForReusableProviders(t *testing.T) {
	if got := releaseActionForProvider(string(UsageRecurring), false, false, true, true, false); got != ProviderReleaseActionReservation {
		t.Fatalf("recurring reservation release action = %q, want %q", got, ProviderReleaseActionReservation)
	}
}

func TestCancelProviderAfterSuccessfulPaymentReadsConfig(t *testing.T) {
	on := NewService(nil, nil, NewMapConfigReader(map[string]string{"card_pool_cancel_after_payment": "1"}))
	off := NewService(nil, nil, NewMapConfigReader(map[string]string{"card_pool_cancel_after_payment": "0"}))
	if !on.cancelProviderAfterSuccessfulPayment() {
		t.Fatal("switch on should cancel after successful payment")
	}
	if off.cancelProviderAfterSuccessfulPayment() {
		t.Fatal("switch off should not cancel after successful payment")
	}
}
