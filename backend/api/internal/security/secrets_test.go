package security

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestValidateAccessToken(t *testing.T) {
	valid := testJWT(map[string]any{
		"typ": "JWT",
		"alg": "RS256",
	}, map[string]any{
		"iss": "https://auth.openai.com",
		"aud": []string{"https://api.openai.com/v1"},
		"https://api.openai.com/auth": map[string]string{
			"chatgpt_account_id": "acct_test",
			"chatgpt_user_id":    "user_test",
		},
		"scp": []string{"model.request"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if err := ValidateAccessToken(valid); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(map[string]any, map[string]any)
		want   string
	}{
		{name: "algorithm", mutate: func(header, _ map[string]any) { header["alg"] = "HS256" }, want: "算法错误"},
		{name: "issuer", mutate: func(_, payload map[string]any) { payload["iss"] = "https://example.test" }, want: "签发方错误"},
		{name: "account", mutate: func(_, payload map[string]any) { payload["https://api.openai.com/auth"] = map[string]string{} }, want: "缺少账户信息"},
		{name: "scope", mutate: func(_, payload map[string]any) { payload["scp"] = []string{"openid"} }, want: "model.request"},
		{name: "expired", mutate: func(_, payload map[string]any) { payload["exp"] = time.Now().Add(-time.Hour).Unix() }, want: "已过期"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			header := map[string]any{"typ": "JWT", "alg": "RS256"}
			payload := map[string]any{
				"iss": "https://auth.openai.com",
				"aud": []string{"https://api.openai.com/v1"},
				"https://api.openai.com/auth": map[string]string{
					"chatgpt_account_id": "acct_test",
					"chatgpt_user_id":    "user_test",
				},
				"scp": []string{"model.request"},
				"exp": time.Now().Add(time.Hour).Unix(),
			}
			test.mutate(header, payload)
			if err := ValidateAccessToken(testJWT(header, payload)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestDecryptWithFallbacksUsesConfiguredLegacyKey(t *testing.T) {
	ciphertext, err := Encrypt("legacy-value", "legacy-key")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	plaintext, err := DecryptWithFallbacks(ciphertext, "primary-key", "legacy-key", "legacy-key")
	if err != nil {
		t.Fatalf("DecryptWithFallbacks() error = %v", err)
	}
	if plaintext != "legacy-value" {
		t.Fatalf("plaintext = %q, want legacy-value", plaintext)
	}
}

func TestDecryptWithFallbacksRejectsMissingKeys(t *testing.T) {
	if _, err := DecryptWithFallbacks("not-a-ciphertext", "", " "); err == nil {
		t.Fatal("DecryptWithFallbacks() unexpectedly succeeded without keys")
	}
}

func testJWT(header, payload map[string]any) string {
	encode := func(value map[string]any) string {
		raw, err := json.Marshal(value)
		if err != nil {
			panic(fmt.Sprintf("marshal JWT test part: %v", err))
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	return encode(header) + "." + encode(payload) + ".signature"
}
