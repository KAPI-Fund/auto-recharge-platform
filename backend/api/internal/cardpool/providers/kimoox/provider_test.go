package kimoox

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
)

type testConfig map[string]string

func (c testConfig) Value(key, fallback string) string {
	if value := strings.TrimSpace(c[key]); value != "" {
		return value
	}
	return fallback
}

func (c testConfig) Secret(key, fallback string) string { return c.Value(key, fallback) }

func TestProviderUsesSignedAsyncApplyAndCardLifecycle(t *testing.T) {
	const apiSecret = "api-secret"
	config := testConfig{
		"card_provider_kimoox_enabled":       "1",
		"kimoox_base_url":                    "",
		"kimoox_api_key":                     "api-key",
		"kimoox_api_secret":                  apiSecret,
		"kimoox_card_bin_id":                 "1001",
		"kimoox_card_type":                   "PREPAID",
		"kimoox_apply_poll_attempts":         "3",
		"kimoox_apply_poll_interval_seconds": "0",
	}
	var statusCalls atomic.Int32
	var queryCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", request.Method)
		}
		assertKimooxSignature(t, request, body, apiSecret)
		if request.Header.Get("X-VCC-API-KEY") != "api-key" || request.Header.Get("X-VCC-REQUEST-ID") == "" {
			t.Fatalf("auth headers = %#v", request.Header)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		switch request.URL.Path {
		case "/openapi/v1/cards/apply":
			if payload["cardType"] != "PREPAID" || payload["cardBinId"] != float64(1001) || payload["cardCount"] != float64(1) || payload["rechargeAmount"] != "100.00" {
				t.Fatalf("apply body = %#v", payload)
			}
			requestNo, ok := payload["requestNo"].(string)
			if !ok || !strings.HasPrefix(requestNo, "KX") || len(requestNo) != 26 {
				t.Fatalf("requestNo = %#v", payload["requestNo"])
			}
			_, _ = io.WriteString(writer, `{"code":200,"data":{"taskId":901,"batchNo":"BATCH-1","requestNo":"KXREQUEST"}}`)
		case "/openapi/v1/cards/apply-status/query":
			call := statusCalls.Add(1)
			if payload["taskId"] != float64(901) || payload["batchNo"] != "BATCH-1" {
				t.Fatalf("apply status body = %#v", payload)
			}
			if call == 1 {
				_, _ = io.WriteString(writer, `{"code":200,"data":{"taskId":901,"batchNo":"BATCH-1","applyStatus":"PROCESSING","taskStatus":"PENDING"}}`)
			} else {
				_, _ = io.WriteString(writer, `{"code":200,"data":{"taskId":901,"batchNo":"BATCH-1","applyStatus":"SUCCESS","taskStatus":"SUCCESS"}}`)
			}
		case "/openapi/v1/cards/query":
			queryCalls.Add(1)
			if payload["batchNo"] == "BATCH-1" {
				_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"cardId":"VC-1","cardNoMask":"486880****1234","cardType":"PREPAID","cardStatus":"ACTIVE","holderName":"Alice Miller","currency":"USD"}]}}`)
				break
			}
			if payload["cardId"] != "VC-1" {
				t.Fatalf("card query body = %#v", payload)
			}
			_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"cardId":"VC-1","cardNoMask":"486880****1234","cardType":"PREPAID","cardStatus":"ACTIVE","holderName":"Alice Miller","currency":"USD"}]}}`)
		case "/openapi/v1/cards/status/operate":
			if payload["cardId"] != "VC-1" || payload["operationType"] == "" || payload["requestNo"] == "" {
				t.Fatalf("status body = %#v", payload)
			}
			_, _ = io.WriteString(writer, `{"code":200,"data":{"status":"SUBMITTED"}}`)
		case "/openapi/v1/cards/limit/adjust":
			if payload["cardId"] != "VC-1" || payload["perTransLimit"] != "20.00" || payload["dayLimit"] != "100.00" {
				t.Fatalf("limit body = %#v", payload)
			}
			_, _ = io.WriteString(writer, `{"code":200,"data":{"status":"SUCCESS"}}`)
		case "/openapi/v1/card-transactions/query":
			if payload["cardNo"] != "1234" || payload["pageSize"] != float64(100) {
				t.Fatalf("transaction body = %#v", payload)
			}
			_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"transactionId":"TX-1","cardId":"VC-1","originalAmount":"12.50","originalCurrency":"USD","transactionStatus":"AUTHORIZED","transactionType":"PURCHASE","merchantName":"Example Merchant","transactionTime":"2026-09-02 12:00:00"}]}}`)
		case "/openapi/v1/account/balance/query":
			_, _ = io.WriteString(writer, `{"code":200,"data":{"balances":[]}}`)
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	config["kimoox_base_url"] = server.URL

	provider := New(config, server.Client())
	card, err := provider.CreateCard(context.Background(), cardpool.CreateCardRequest{Amount: 100, Currency: "USD", UsageType: cardpool.UsageOneTime, IdempotencyKey: "task-1"})
	if err != nil {
		t.Fatalf("CreateCard() error = %v", err)
	}
	if card.Provider != providerName || card.ProviderCardID != "VC-1" || card.Last4 != "1234" || card.Status != cardpool.CardActive || card.Currency != "USD" {
		t.Fatalf("card = %#v", card)
	}
	if statusCalls.Load() != 2 || queryCalls.Load() != 1 {
		t.Fatalf("status calls = %d, query calls = %d", statusCalls.Load(), queryCalls.Load())
	}
	if err := provider.FreezeCard(context.Background(), "VC-1"); err != nil {
		t.Fatalf("FreezeCard() error = %v", err)
	}
	if err := provider.UnfreezeCard(context.Background(), "VC-1"); err != nil {
		t.Fatalf("UnfreezeCard() error = %v", err)
	}
	if err := provider.UpdateLimits(context.Background(), "VC-1", cardpool.CardLimits{PerTransaction: 20, Daily: 100}); err != nil {
		t.Fatalf("UpdateLimits() error = %v", err)
	}
	transactions, err := provider.GetTransactions(context.Background(), "VC-1")
	if err != nil || len(transactions) != 1 || transactions[0].ProviderTransactionID != "TX-1" || transactions[0].MerchantName != "Example Merchant" {
		t.Fatalf("GetTransactions() = %#v, err = %v", transactions, err)
	}
	if err := provider.CancelCard(context.Background(), "VC-1"); err != nil {
		t.Fatalf("CancelCard() error = %v", err)
	}
	health, err := provider.HealthCheck(context.Background())
	if err != nil || !health.Available || health.Status != "healthy" {
		t.Fatalf("HealthCheck() = %#v, err = %v", health, err)
	}
}

func TestProviderDecryptsKimooxSensitiveDetails(t *testing.T) {
	secret := "webhook-secret"
	key := kimooxSensitiveKey(secret)
	cardNumber := mustEncryptKimooxValue(t, key, "4242424242424242")
	expiry := mustEncryptKimooxValue(t, key, "12/2030")
	cvv := mustEncryptKimooxValue(t, key, "123")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/openapi/v1/cards/query" {
			_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"cardId":"VC-1","cardNoMask":"424242******4242","cardType":"PREPAID","cardStatus":"ACTIVE","holderName":"Alice"}]}}`)
			return
		}
		if request.URL.Path != "/openapi/v1/cards/private-info/query" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		_, _ = io.WriteString(writer, fmt.Sprintf(`{"code":200,"data":{"cardId":"VC-1","sensitiveEncryptAlg":"AES-256-GCM","cardNumberCiphertext":%q,"expiryDateCiphertext":%q,"cvvCiphertext":%q}}`, cardNumber, expiry, cvv))
	}))
	defer server.Close()
	provider := New(testConfig{
		"card_provider_kimoox_enabled": "1", "kimoox_base_url": server.URL,
		"kimoox_api_key": "api-key", "kimoox_api_secret": "api-secret", "kimoox_webhook_secret": secret,
		"kimoox_card_bin_id": "1001",
	}, server.Client())
	details, err := provider.GetSensitiveCardDetails(context.Background(), "VC-1")
	if err != nil {
		t.Fatalf("GetSensitiveCardDetails() error = %v", err)
	}
	if details.CardNumber != "4242424242424242" || details.CVC != "123" || details.ExpiryMonth != 12 || details.ExpiryYear != 2030 {
		t.Fatalf("details = %#v", details)
	}
}

func TestProviderDoesNotExposeUnsupportedSingleUseCapability(t *testing.T) {
	provider := New(testConfig{"card_provider_kimoox_enabled": "1"}, nil)
	if provider.Supports(cardpool.CapabilitySingleUse) {
		t.Fatal("Kimoox must not claim undocumented native single-use support")
	}
	for _, capability := range []cardpool.Capability{
		cardpool.CapabilityCreateCard, cardpool.CapabilitySensitiveDetails, cardpool.CapabilityFreeze,
		cardpool.CapabilityUnfreeze, cardpool.CapabilityCancel, cardpool.CapabilityMultiUse,
		cardpool.CapabilityRecurringPayment, cardpool.CapabilityTransactionLimit,
		cardpool.CapabilityTransactionQuery, cardpool.CapabilityWebhook,
	} {
		if !provider.Supports(capability) {
			t.Fatalf("Supports(%q) = false", capability)
		}
	}
}

func TestProviderValidationRequiresConfiguredIssuingInputs(t *testing.T) {
	provider := New(testConfig{"card_provider_kimoox_enabled": "1", "kimoox_api_key": "api-key", "kimoox_api_secret": "secret"}, nil)
	if err := provider.ValidateConfiguration(); err == nil || !strings.Contains(err.Error(), "Card BIN ID") {
		t.Fatalf("ValidateConfiguration() error = %v", err)
	}

	valid := testConfig{
		"card_provider_kimoox_enabled": "1", "kimoox_api_key": "api-key", "kimoox_api_secret": "secret",
		"kimoox_card_bin_id": "1001", "kimoox_base_url": "https://card.kimoox.com",
	}
	if err := New(valid, nil).ValidateConfiguration(); err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
}

func assertKimooxSignature(t *testing.T, request *http.Request, body []byte, secret string) {
	t.Helper()
	timestamp := request.Header.Get("X-VCC-TIMESTAMP")
	nonce := request.Header.Get("X-VCC-NONCE")
	if timestamp == "" || nonce == "" || request.Header.Get("X-VCC-SIGNATURE") == "" {
		t.Fatalf("signature headers missing: %#v", request.Header)
	}
	bodyHash := sha256.Sum256(body)
	canonical := strings.Join([]string{request.Method, request.URL.Path, timestamp, nonce, hex.EncodeToString(bodyHash[:])}, "\n")
	digest := hmacSHA256([]byte(secret), []byte(canonical))
	if request.Header.Get("X-VCC-SIGNATURE") != hex.EncodeToString(digest) {
		t.Fatalf("signature = %q, want %q", request.Header.Get("X-VCC-SIGNATURE"), hex.EncodeToString(digest))
	}
}

func hmacSHA256(key, value []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}

func mustEncryptKimooxValue(t *testing.T, key []byte, value string) string {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	encoded := append(iv, gcm.Seal(nil, iv, []byte(value), nil)...)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func TestParseExpiryVariants(t *testing.T) {
	for _, test := range []struct {
		value       string
		month, year int
	}{
		{"12/2030", 12, 2030}, {"2030-12", 12, 2030}, {"12-30", 12, 2030}, {"1230", 12, 2030},
	} {
		t.Run(test.value, func(t *testing.T) {
			month, year, ok := parseExpiry(test.value)
			if !ok || month != test.month || year != test.year {
				t.Fatalf("parseExpiry(%q) = %d/%d/%t", test.value, month, year, ok)
			}
		})
	}
}

func TestKimooxRequestNoIsStableAndSafe(t *testing.T) {
	left := kimooxRequestNo("task:alpha/1")
	right := kimooxRequestNo("task:alpha/1")
	if left != right || !strings.HasPrefix(left, "KX") || len(left) != 26 {
		t.Fatalf("request numbers = %q and %q", left, right)
	}
	if _, err := json.Marshal(map[string]string{"requestNo": left}); err != nil {
		t.Fatal(err)
	}
}

func TestKimooxBusinessErrorsDoNotAllowFailoverForFundsOrDeclines(t *testing.T) {
	for _, test := range []struct {
		code     string
		message  string
		category cardpool.ErrorCategory
	}{
		{"INSUFFICIENT_FUNDS", "insufficient balance", cardpool.CategoryInsufficientFunds},
		{"CARD_DECLINED", "merchant declined", cardpool.CategoryBusinessDecline},
		{"RATE_LIMIT", "rate limit", cardpool.CategoryRateLimit},
	} {
		t.Run(test.code, func(t *testing.T) {
			err := kimooxBusinessError(map[string]any{"code": test.code, "msg": test.message}, "create_card")
			var providerErr *cardpool.ProviderError
			if !errors.As(err, &providerErr) || providerErr.Category != test.category {
				t.Fatalf("error = %#v, want category %s", err, test.category)
			}
			if test.category != cardpool.CategoryRateLimit && cardpool.IsFailoverEligible(err) {
				t.Fatal("business error must not be failover eligible")
			}
		})
	}
}

func TestKimooxSensitiveKeyMatchesDocumentedDerivation(t *testing.T) {
	got := hex.EncodeToString(kimooxSensitiveKey("secret"))
	want := hex.EncodeToString(hmacSHA256([]byte("secret"), []byte("vcc-webhook-sensitive-v1")))
	if got != want {
		t.Fatalf("key = %s, want %s", got, want)
	}
}
