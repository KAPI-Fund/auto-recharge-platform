package session

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestParseSessionJSON(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"exp": 2000000000,
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct"},
	})
	token := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".x"
	raw, _ := json.Marshal(map[string]any{
		"accessToken": token,
		"cookies": []any{
			map[string]any{"name": "__Secure-next-auth.session-token", "value": "abc", "domain": ".chatgpt.com"},
			map[string]any{"name": "oai-did", "value": "device-1", "domain": ".chatgpt.com"},
			map[string]any{"name": "cf_clearance", "value": "stale", "domain": ".chatgpt.com"},
			map[string]any{"name": "__cf_bm", "value": "stale", "domain": ".chatgpt.com"},
			map[string]any{"name": "skip", "value": "x", "domain": "example.com"},
		},
	})
	sess, err := Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if sess.AccountID != "acct" {
		t.Fatalf("account=%s", sess.AccountID)
	}
	if sess.Cookies["__Secure-next-auth.session-token"] != "abc" {
		t.Fatalf("cookies=%v", sess.Cookies)
	}
	if _, exists := sess.Cookies["skip"]; exists {
		t.Fatal("foreign cookie leaked")
	}
	if _, exists := sess.Cookies["cf_clearance"]; exists {
		t.Fatal("stale Cloudflare cookie must not overwrite live Chrome clearance")
	}
	if sess.DeviceID != "device-1" {
		t.Fatalf("device=%s", sess.DeviceID)
	}
}
