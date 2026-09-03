package chromeapi

import (
	"strings"

	"github.com/chromedp/cdproto/network"
)

const chatgptURL = "https://chatgpt.com"

func cookieParams(name, value string) *network.SetCookieParams {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	set := network.SetCookie(name, value).
		WithURL(chatgptURL).
		WithPath("/").
		WithSecure(true).
		WithHTTPOnly(cookieHTTPOnly(name)).
		WithSameSite(network.CookieSameSiteLax)
	if strings.HasPrefix(name, "__Host-") || strings.HasPrefix(name, "__Secure-") {
		return set
	}
	return set.WithDomain(".chatgpt.com")
}

func cookieHTTPOnly(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(name, "__Host-") || strings.HasPrefix(name, "__Secure-") || strings.Contains(lower, "token") || strings.Contains(lower, "session")
}

func interestingBody(text string) bool {
	return strings.Contains(text, "client_secret") ||
		strings.Contains(text, "pk_live") ||
		strings.Contains(text, "pk_test") ||
		strings.Contains(text, "oaics_") ||
		strings.Contains(text, "cs_live") ||
		strings.Contains(text, "cs_test") ||
		strings.Contains(text, "publishable_key") ||
		strings.Contains(text, "confirmation_token")
}

func interestingURL(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	return strings.Contains(lower, "/payments/checkout") ||
		strings.Contains(lower, "/checkout/") ||
		strings.Contains(lower, "stripe_client_bootstrap") ||
		strings.Contains(lower, "client_secret")
}
