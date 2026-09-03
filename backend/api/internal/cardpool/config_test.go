package cardpool

import "testing"

func TestOverlayConfigReaderOverridesPersistedValues(t *testing.T) {
	base := NewMapConfigReader(map[string]string{
		"provider": "LOCAL_TEXT",
		"empty":    "persisted",
	})
	reader := NewOverlayConfigReader(base, map[string]string{
		"provider": "PHOTONPAY",
		"empty":    "",
		"secret":   "request-secret",
	})

	if got := reader.Value("provider", ""); got != "PHOTONPAY" {
		t.Fatalf("provider = %q, want PHOTONPAY", got)
	}
	if got := reader.Value("empty", "fallback"); got != "" {
		t.Fatalf("explicit empty override = %q, want empty", got)
	}
	if got := reader.Secret("secret", ""); got != "request-secret" {
		t.Fatalf("secret = %q, want request-secret", got)
	}
	if got := reader.Value("missing", "fallback"); got != "fallback" {
		t.Fatalf("missing value = %q, want fallback", got)
	}
}

func TestMapConfigReaderDoesNotExposeMutableInput(t *testing.T) {
	values := map[string]string{"provider": "LOCAL_TEXT"}
	reader := NewMapConfigReader(values)
	values["provider"] = "DOGPAY"
	if got := reader.Value("provider", ""); got != "LOCAL_TEXT" {
		t.Fatalf("reader changed with input map mutation: %q", got)
	}
}
