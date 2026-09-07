package cardpool

import "testing"

func TestReleaseActionForProviderDoesNotCancelUnsubmittedOneTimePayment(t *testing.T) {
	if got := releaseActionForProvider(string(UsageOneTime), false, false, false); got != "" {
		t.Fatalf("unsubmitted one-time payment release action = %q, want no provider action", got)
	}
}

func TestReleaseActionForProviderCancelsCompletedOneTimePayment(t *testing.T) {
	if got := releaseActionForProvider(string(UsageOneTime), true, false, false); got != ProviderReleaseActionCancel {
		t.Fatalf("completed one-time payment release action = %q, want %q", got, ProviderReleaseActionCancel)
	}
}

func TestReleaseActionForProviderKeepsReservationReleaseForReusableProviders(t *testing.T) {
	if got := releaseActionForProvider(string(UsageRecurring), false, true, false); got != ProviderReleaseActionReservation {
		t.Fatalf("recurring reservation release action = %q, want %q", got, ProviderReleaseActionReservation)
	}
}
