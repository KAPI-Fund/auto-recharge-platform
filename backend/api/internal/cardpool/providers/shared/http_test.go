package shared

import (
	"errors"
	"strings"
	"testing"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
)

func TestHTTPErrorClassifiesServerFailureBeforeBusinessText(t *testing.T) {
	err := HTTPError("AIRWALLEX", "create_card", 502, []byte(`{"message":"insufficient funds while provider is unavailable","card_number":"4242424242424242"}`))
	var providerErr *cardpool.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Category != cardpool.CategoryProviderUnavailable || !providerErr.FailoverAllowed {
		t.Fatalf("error = %#v, want failover-eligible provider unavailable", err)
	}
	if message := err.Error(); message == "" || stringContainsAny(message, "4242424242424242") {
		t.Fatalf("provider error exposed sensitive value: %s", message)
	}
}

func TestSummarizeRedactsSensitivePlainTextAssignmentsAndPAN(t *testing.T) {
	value := summarize([]byte("card_number=4242424242424242 cvc=123 pan 5555555555554444"))
	if stringContainsAny(value, "4242424242424242", "5555555555554444", "cvc=123") {
		t.Fatalf("sensitive plain-text response was not redacted: %s", value)
	}
}

func stringContainsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if len(needle) > 0 && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
