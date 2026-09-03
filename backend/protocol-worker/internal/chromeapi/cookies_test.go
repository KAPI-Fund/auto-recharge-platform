package chromeapi

import "testing"

func TestCookieParamsPrefixedCookiesHaveNoDomain(t *testing.T) {
	host := cookieParams("__Host-next-auth.csrf-token", "abc")
	if host.Domain != "" {
		t.Fatalf("__Host- must not set Domain, got %q", host.Domain)
	}
	if host.URL != chatgptURL || host.Path != "/" || !host.Secure {
		t.Fatalf("__Host- cookie %+v", host)
	}
	secure := cookieParams("__Secure-next-auth.session-token", "tok")
	if secure.Domain != "" {
		t.Fatalf("__Secure- must use URL not Domain, got %q", secure.Domain)
	}
	if !secure.HTTPOnly || !secure.Secure {
		t.Fatalf("__Secure- flags %+v", secure)
	}
	plain := cookieParams("oai-did", "device")
	if plain.Domain != ".chatgpt.com" {
		t.Fatalf("plain cookie domain=%q", plain.Domain)
	}
}

func TestInterestingCaptureFilters(t *testing.T) {
	if !interestingBody(`{"checkout_session_id":"oaics_abc"}`) {
		t.Fatal("oaics body")
	}
	if interestingBody(`{"ok":true}`) {
		t.Fatal("plain json should be ignored")
	}
	if !interestingURL("https://chatgpt.com/backend-api/payments/checkout") {
		t.Fatal("checkout url")
	}
}
