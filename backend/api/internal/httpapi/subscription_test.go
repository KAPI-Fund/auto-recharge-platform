package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func testJWT(payload map[string]any) string {
	header, _ := json.Marshal(map[string]any{"typ": "JWT", "alg": "RS256"})
	body, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body) + ".signature"
}

func TestValidateSubscriptionToken(t *testing.T) {
	token := testJWT(map[string]any{
		"iss":                            "https://auth.openai.com",
		"exp":                            time.Now().Add(time.Hour).Unix(),
		"https://api.openai.com/auth":    map[string]any{"chatgpt_account_id": "acct_123"},
		"https://api.openai.com/profile": map[string]any{"email": "owner@example.com"},
	})

	profile, message := validateSubscriptionToken(token)
	if message != "" {
		t.Fatalf("unexpected validation error: %s", message)
	}
	if profile.Email != "owner@example.com" || profile.AccountID != "acct_123" {
		t.Fatalf("unexpected profile: %#v", profile)
	}

	if _, message = validateSubscriptionToken("not-a-jwt"); message != "该 Token 不合法：格式错误" {
		t.Fatalf("unexpected malformed token error: %s", message)
	}
	if _, message = validateSubscriptionToken(testJWT(map[string]any{
		"iss": "https://example.com",
		"exp": time.Now().Add(time.Hour).Unix(),
	})); message != "该 Token 不合法：签发方错误" {
		t.Fatalf("unexpected issuer error: %s", message)
	}
}

func TestParseSubscriptionResponse(t *testing.T) {
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	expires := now.Add(26 * time.Hour).Format(time.RFC3339)
	data := map[string]any{
		"accounts": map[string]any{
			"default": map[string]any{
				"account": map[string]any{
					"account_id":                       "acct_response",
					"has_previously_paid_subscription": true,
				},
				"entitlement": map[string]any{
					"has_active_subscription": true,
					"subscription_plan":       "pro_20x",
					"expires_at":              expires,
					"currency":                "usd",
				},
				"last_active_subscription": map[string]any{
					"purchase_origin_platform": "chatgpt_web",
					"will_renew":               false,
				},
			},
		},
	}
	result := parseSubscriptionResponse(data, subscriptionProfile{Email: "owner@example.com", AccountID: "acct_token"}, now)

	checks := map[string]any{
		"accountId":             "acct_response",
		"plan":                  "ChatGPT Pro",
		"planKey":               "pro",
		"hasActiveSubscription": true,
		"subscriptionChannel":   "Web (Stripe)",
		"currency":              "USD",
		"autoRenew":             "否",
		"autoRenewRaw":          false,
		"hasPreviouslyPaid":     "是",
		"remainingDays":         2,
		"remainingDaysDisplay":  "2 天",
	}
	for key, want := range checks {
		if result[key] != want {
			t.Errorf("%s = %#v, want %#v", key, result[key], want)
		}
	}
	if result["billingPageUrl"] != chatGPTBillingPageURL {
		t.Errorf("billingPageUrl = %v", result["billingPageUrl"])
	}
}

func TestSubscriptionAccessTokenExtraction(t *testing.T) {
	token := testJWT(map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	if got := extractSubscriptionAccessToken(`{"accessToken":"` + token + `"}`); got != token {
		t.Fatalf("JSON token = %q, want %q", got, token)
	}
	if got := extractSubscriptionAccessToken("prefix " + token + " suffix"); got != token {
		t.Fatalf("embedded token = %q, want %q", got, token)
	}
}

func TestMapSubscriptionCheckError(t *testing.T) {
	status, message := mapSubscriptionCheckError(403, "<title>Just a moment...</title>")
	if status != 403 || message != "OpenAI 风控拦截（Cloudflare），请稍后重试或在后台配置住宅代理 PROXY" {
		t.Fatalf("cloudflare error = %d %s", status, message)
	}
	status, message = mapSubscriptionCheckError(401, nil)
	if status != 401 || message != "Session 无效或已过期，请重新获取 Session" {
		t.Fatalf("auth error = %d %s", status, message)
	}
}
