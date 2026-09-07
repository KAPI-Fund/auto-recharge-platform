package cardpool

import "testing"

func TestNormalizeRechargeUsageAlwaysReturnsOneTime(t *testing.T) {
	for _, value := range []UsageType{"", UsageOneTime, UsageRecurring, UsageMultiUse} {
		if got := normalizeRechargeUsage(value, string(value)); got != UsageOneTime {
			t.Fatalf("normalizeRechargeUsage(%q) = %q, want %q", value, got, UsageOneTime)
		}
	}
}

func TestSingleUseReleaseCancelsProviderCardAfterUsage(t *testing.T) {
	if got := releaseActionForProvider(string(UsageOneTime), true, true, true, true, true); got != ProviderReleaseActionCancel {
		t.Fatalf("release action = %q, want %q", got, ProviderReleaseActionCancel)
	}
}
