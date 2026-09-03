package captcha

import "testing"

func TestExtractAndChallenge(t *testing.T) {
	html := `<div class="h-captcha" data-sitekey="c7faac4c-1cd7-4b1b-b2d4-42ba98d09c7a"></div>`
	if ExtractSiteKey(html) != "c7faac4c-1cd7-4b1b-b2d4-42ba98d09c7a" {
		t.Fatal(ExtractSiteKey(html))
	}
	if !LooksLikeChallenge(403, "Just a moment", nil) {
		t.Fatal("cloudflare")
	}
	if !LooksLikeHCaptcha("hCaptcha challenge") {
		t.Fatal("hcaptcha")
	}
	tasks := hcaptchaTasks("site", "https://js.stripe.com/", "rq", "ua")
	if len(tasks) != 2 {
		t.Fatalf("tasks=%d", len(tasks))
	}
}
