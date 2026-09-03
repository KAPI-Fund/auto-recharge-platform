package stripe

import (
	"strings"
	"testing"
)

func TestEncodeFormAndExpiry(t *testing.T) {
	encoded := EncodeForm(map[string]any{
		"type": "card",
		"card": map[string]any{"number": "4242", "exp_month": "12", "exp_year": "2030"},
		"billing_details": map[string]any{"address": map[string]any{"country": "US"}},
	})
	if !strings.Contains(encoded, "type=card") || !strings.Contains(encoded, "card%5Bnumber%5D=4242") {
		t.Fatalf("encoded=%s", encoded)
	}
	month, year := ParseExpiry("12/30")
	if month != "12" || year != "2030" {
		t.Fatalf("%s %s", month, year)
	}
	if SessionID("cs_live_abc_secret_xyz") != "cs_live_abc" {
		t.Fatalf("session id %s", SessionID("cs_live_abc_secret_xyz"))
	}
	classified := Classify("Your card was declined", "card_declined", "do_not_honor", 402)
	if !classified.Declined {
		t.Fatal("expected declined")
	}
	if Paid("", 200, false) {
		t.Fatal("HTTP 200 without status must not count as paid")
	}
	if !Paid("succeeded", 200, false) {
		t.Fatal("succeeded should count as paid")
	}
}
