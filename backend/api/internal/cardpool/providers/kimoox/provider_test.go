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
		"kimoox_card_bin_ids":                "1001",
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
		case "/openapi/v1/card-bins/query":
			_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"binId":1001,"bin":"40024200"}]}}`)
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
			if payload["taskId"] != float64(901) || payload["batchNo"] != nil || payload["requestNo"] != nil {
				t.Fatalf("apply status body = %#v, want only taskId", payload)
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
		case "/openapi/v1/cards/balances/query":
			_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"cardId":"VC-1","availableBalance":"0.00","currency":"USD"}]}}`)
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

func TestCancelCardWithdrawsRemainingBalanceBeforeCancel(t *testing.T) {
	const apiSecret = "api-secret"
	config := testConfig{
		"card_provider_kimoox_enabled":       "1",
		"kimoox_api_key":                     "api-key",
		"kimoox_api_secret":                  apiSecret,
		"kimoox_apply_poll_attempts":         "3",
		"kimoox_apply_poll_interval_seconds": "0",
	}
	var balanceCalls atomic.Int32
	var withdrew, cancelled bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		assertKimooxSignature(t, request, body, apiSecret)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		switch request.URL.Path {
		case "/openapi/v1/cards/balances/query":
			call := balanceCalls.Add(1)
			if call == 1 {
				_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"cardId":"VC-1","availableBalance":"25.00","currency":"USD"}]}}`)
				return
			}
			_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"cardId":"VC-1","availableBalance":"0.01","currency":"USD"}]}}`)
		case "/openapi/v1/cards/funds/operate":
			if payload["cardId"] != "VC-1" || payload["operationType"] != "WITHDRAW" || payload["amount"] != "24.99" || payload["currency"] != "USD" {
				t.Fatalf("withdraw body = %#v, want 24.99 leaving 0.01", payload)
			}
			if cancelled {
				t.Fatal("WITHDRAW after CANCEL")
			}
			withdrew = true
			_, _ = io.WriteString(writer, `{"code":200,"data":{"status":"SUBMITTED"}}`)
		case "/openapi/v1/cards/status/operate":
			if payload["cardId"] != "VC-1" || payload["operationType"] != "CANCEL" {
				t.Fatalf("cancel body = %#v", payload)
			}
			if !withdrew {
				t.Fatal("CANCEL before WITHDRAW")
			}
			cancelled = true
			_, _ = io.WriteString(writer, `{"code":200,"data":{"status":"SUBMITTED"}}`)
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	config["kimoox_base_url"] = server.URL

	provider := New(config, server.Client())
	if err := provider.CancelCard(context.Background(), "VC-1"); err != nil {
		t.Fatalf("CancelCard() error = %v", err)
	}
	if !withdrew || !cancelled {
		t.Fatalf("withdrew=%t cancelled=%t", withdrew, cancelled)
	}
}

func TestKimooxWithdrawableAmountLeavesOneCent(t *testing.T) {
	if got := kimooxWithdrawableAmount(25); got != 24.99 {
		t.Fatalf("kimooxWithdrawableAmount(25) = %v, want 24.99", got)
	}
	if got := kimooxWithdrawableAmount(0.01); got != 0 {
		t.Fatalf("kimooxWithdrawableAmount(0.01) = %v, want 0", got)
	}
	if got := kimooxWithdrawableAmount(0); got != 0 {
		t.Fatalf("kimooxWithdrawableAmount(0) = %v, want 0", got)
	}
	if !kimooxBalanceRetainSatisfied(0.01) || kimooxBalanceRetainSatisfied(0.02) {
		t.Fatal("retain check should pass at 0.01 and fail at 0.02")
	}
}

func TestPrepaidRejectsMissingRechargeAmount(t *testing.T) {
	provider := New(testConfig{
		"card_provider_kimoox_enabled":       "1",
		"kimoox_base_url":                    "https://card.kimoox.com",
		"kimoox_api_key":                     "api-key",
		"kimoox_api_secret":                  "api-secret",
		"kimoox_card_bin_ids":                "1001",
		"kimoox_card_type":                   "PREPAID",
		"kimoox_prepaid_recharge_amount":     "220",
		"kimoox_apply_poll_attempts":         "1",
		"kimoox_apply_poll_interval_seconds": "0",
	}, nil)
	_, err := provider.CreateCard(context.Background(), cardpool.CreateCardRequest{IdempotencyKey: "manual-1"})
	if err == nil || !strings.Contains(err.Error(), "大于 0 的首充金额") {
		t.Fatalf("CreateCard() error = %v, want missing amount", err)
	}
}

func TestPrepaidUsesRequestedPlanBufferAmount(t *testing.T) {
	const apiSecret = "api-secret"
	config := testConfig{
		"card_provider_kimoox_enabled":       "1",
		"kimoox_api_key":                     "api-key",
		"kimoox_api_secret":                  apiSecret,
		"kimoox_card_bin_ids":                "1001",
		"kimoox_card_type":                   "PREPAID",
		"kimoox_prepaid_recharge_amount":     "220",
		"kimoox_apply_poll_attempts":         "1",
		"kimoox_apply_poll_interval_seconds": "0",
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		assertKimooxSignature(t, request, body, apiSecret)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		switch request.URL.Path {
		case "/openapi/v1/card-bins/query":
			_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"binId":1001,"bin":"40024200"}]}}`)
		case "/openapi/v1/cards/apply":
			if payload["rechargeAmount"] != "25.00" {
				t.Fatalf("rechargeAmount = %#v, want 25.00 from Plus 20+5", payload["rechargeAmount"])
			}
			_, _ = io.WriteString(writer, `{"code":200,"data":{"taskId":901,"batchNo":"BATCH-1"}}`)
		case "/openapi/v1/cards/apply-status/query":
			_, _ = io.WriteString(writer, `{"code":200,"data":{"taskId":901,"batchNo":"BATCH-1","applyStatus":"SUCCESS","taskStatus":"SUCCESS"}}`)
		case "/openapi/v1/cards/query":
			_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"cardId":"VC-1","cardNoMask":"486880****1234","cardType":"PREPAID","cardStatus":"ACTIVE"}]}}`)
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	config["kimoox_base_url"] = server.URL

	provider := New(config, server.Client())
	if _, err := provider.CreateCard(context.Background(), cardpool.CreateCardRequest{Amount: 25, Currency: "USD", IdempotencyKey: "plus-1"}); err != nil {
		t.Fatalf("CreateCard() error = %v", err)
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
		"kimoox_card_bin_ids": "1001",
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
	if err := provider.ValidateConfiguration(); err == nil || !strings.Contains(err.Error(), "Card BIN") {
		t.Fatalf("ValidateConfiguration() error = %v", err)
	}
	legacyOnly := testConfig{
		"card_provider_kimoox_enabled": "1", "kimoox_api_key": "api-key", "kimoox_api_secret": "secret",
		"kimoox_card_bin_id": "1001", "kimoox_base_url": "https://card.kimoox.com",
	}
	if err := New(legacyOnly, nil).ValidateConfiguration(); err == nil || !strings.Contains(err.Error(), "Card BIN") {
		t.Fatalf("legacy single BIN configuration was accepted: %v", err)
	}

	valid := testConfig{
		"card_provider_kimoox_enabled": "1", "kimoox_api_key": "api-key", "kimoox_api_secret": "secret",
		"kimoox_card_bin_ids": "1001", "kimoox_base_url": "https://card.kimoox.com",
	}
	if err := New(valid, nil).ValidateConfiguration(); err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
}

func TestParseKimooxBINIDsAcceptsLegacyAndMultiValueFormats(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []int64
	}{
		{name: "single", value: "1001", want: []int64{1001}},
		{name: "card BIN number", value: "40024200,40041606", want: []int64{40024200, 40041606}},
		{name: "delimited", value: "1001, 1002;1003\n1004", want: []int64{1001, 1002, 1003, 1004}},
		{name: "json strings", value: `["1001", "1002"]`, want: []int64{1001, 1002}},
		{name: "json numbers", value: `[1001,1002]`, want: []int64{1001, 1002}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseKimooxBINIDs(test.value)
			if err != nil {
				t.Fatalf("parseKimooxBINIDs(%q) error = %v", test.value, err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("parseKimooxBINIDs(%q) = %#v, want %#v", test.value, got, test.want)
			}
			for index := range test.want {
				if got[index] != test.want[index] {
					t.Fatalf("parseKimooxBINIDs(%q) = %#v, want %#v", test.value, got, test.want)
				}
			}
		})
	}
}

func TestParseKimooxBINIDsRejectsInvalidAndDuplicateValues(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1001,abc", "1001,1001", `[1001,"1001"]`} {
		t.Run(value, func(t *testing.T) {
			if _, err := parseKimooxBINIDs(value); err == nil {
				t.Fatalf("parseKimooxBINIDs(%q) unexpectedly succeeded", value)
			}
		})
	}
}

func TestKimooxApplyStatusBodyUsesExactlyOneQueryKey(t *testing.T) {
	body, err := kimooxApplyStatusBody("901", "BATCH-1", "KXREQUEST")
	if err != nil {
		t.Fatalf("kimooxApplyStatusBody() error = %v", err)
	}
	if body["taskId"] != int64(901) || body["batchNo"] != nil || body["requestNo"] != nil {
		t.Fatalf("body = %#v, want only taskId", body)
	}
	body, err = kimooxApplyStatusBody("", "BATCH-1", "KXREQUEST")
	if err != nil {
		t.Fatalf("kimooxApplyStatusBody() error = %v", err)
	}
	if body["batchNo"] != "BATCH-1" || body["taskId"] != nil || body["requestNo"] != nil {
		t.Fatalf("body = %#v, want only batchNo", body)
	}
	body, err = kimooxApplyStatusBody("", "", "KXREQUEST")
	if err != nil {
		t.Fatalf("kimooxApplyStatusBody() error = %v", err)
	}
	if body["requestNo"] != "KXREQUEST" || body["taskId"] != nil || body["batchNo"] != nil {
		t.Fatalf("body = %#v, want only requestNo", body)
	}
	if _, err := kimooxApplyStatusBody("", "", ""); err == nil {
		t.Fatal("empty apply-status query unexpectedly succeeded")
	}
}

func TestSelectBINIDIsStableForTheSameIdempotencyKey(t *testing.T) {
	ids := []int64{1001, 1002, 1003}
	first := selectBINID(ids, "task-123")
	for attempt := 0; attempt < 20; attempt++ {
		if got := selectBINID(ids, "task-123"); got != first {
			t.Fatalf("selectBINID changed across retries: first=%d retry=%d", first, got)
		}
	}
	if first != selectBINID(ids, "task-123") {
		t.Fatal("same idempotency key must select the same BIN")
	}
}

func TestResolveCardBINIDsMapsConfiguredBINNumbersToInternalIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/openapi/v1/card-bins/query" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"binId":40,"bin":"40024200"},{"binId":38,"bin":"40041606"}]}}`)
	}))
	defer server.Close()
	base := testConfig{
		"card_provider_kimoox_enabled": "1",
		"kimoox_base_url":              server.URL,
		"kimoox_api_key":               "api-key",
		"kimoox_api_secret":            "api-secret",
	}

	tests := []struct {
		name        string
		configured  string
		want        []int64
		wantErrPart string
	}{
		{name: "bin numbers", configured: "40024200,40041606", want: []int64{40, 38}},
		{name: "legacy internal ids", configured: "40,38", want: []int64{40, 38}},
		{name: "mixed number and id of same bin", configured: "40024200,40", want: []int64{40}},
		{name: "unknown bin", configured: "99999999", wantErrPart: "不在可用列表中"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig{}
			for key, value := range base {
				config[key] = value
			}
			config["kimoox_card_bin_ids"] = test.configured
			got, err := New(config, server.Client()).resolveCardBINIDs(context.Background())
			if test.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrPart) {
					t.Fatalf("resolveCardBINIDs(%q) error = %v, want %q", test.configured, err, test.wantErrPart)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveCardBINIDs(%q) error = %v", test.configured, err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("resolveCardBINIDs(%q) = %#v, want %#v", test.configured, got, test.want)
			}
			for index := range test.want {
				if got[index] != test.want[index] {
					t.Fatalf("resolveCardBINIDs(%q) = %#v, want %#v", test.configured, got, test.want)
				}
			}
		})
	}
}

func TestCreateCardSendsResolvedInternalBINID(t *testing.T) {
	const apiSecret = "api-secret"
	config := testConfig{
		"card_provider_kimoox_enabled":       "1",
		"kimoox_api_key":                     "api-key",
		"kimoox_api_secret":                  apiSecret,
		"kimoox_card_bin_ids":                "40024200",
		"kimoox_card_type":                   "PREPAID",
		"kimoox_apply_poll_attempts":         "1",
		"kimoox_apply_poll_interval_seconds": "0",
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		assertKimooxSignature(t, request, body, apiSecret)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		switch request.URL.Path {
		case "/openapi/v1/card-bins/query":
			_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"binId":40,"bin":"40024200"},{"binId":38,"bin":"40041606"}]}}`)
		case "/openapi/v1/cards/apply":
			if payload["cardBinId"] != float64(40) {
				t.Fatalf("cardBinId = %#v, want internal id 40 for BIN 40024200", payload["cardBinId"])
			}
			_, _ = io.WriteString(writer, `{"code":200,"data":{"taskId":901,"batchNo":"BATCH-1"}}`)
		case "/openapi/v1/cards/apply-status/query":
			_, _ = io.WriteString(writer, `{"code":200,"data":{"taskId":901,"batchNo":"BATCH-1","applyStatus":"SUCCESS","taskStatus":"SUCCESS"}}`)
		case "/openapi/v1/cards/query":
			_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"cardId":"VC-1","cardNoMask":"486880****1234","cardType":"PREPAID","cardStatus":"ACTIVE"}]}}`)
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	config["kimoox_base_url"] = server.URL

	provider := New(config, server.Client())
	if _, err := provider.CreateCard(context.Background(), cardpool.CreateCardRequest{Amount: 25, Currency: "USD", IdempotencyKey: "bin-1"}); err != nil {
		t.Fatalf("CreateCard() error = %v", err)
	}
}

func TestProviderListCardBINsMapsAndDeduplicatesMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/openapi/v1/card-bins/query" {
			t.Fatalf("path = %s, want /openapi/v1/card-bins/query", request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		assertKimooxSignature(t, request, body, "api-secret")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"code":200,"data":{"list":[{"binId":40,"bin":"40024200","name":"US Debit","cardType":"PREPAID","status":"ACTIVE","maxCardCount":20,"issuedCardCount":3},{"binId":"40","name":"duplicate"},{"id":"38","bin":"40041606","cardType":"BUDGET","status":"OPEN"}]}}`)
	}))
	defer server.Close()

	provider := New(testConfig{
		"card_provider_kimoox_enabled": "0",
		"kimoox_base_url":              server.URL,
		"kimoox_api_key":               "api-key",
		"kimoox_api_secret":            "api-secret",
	}, server.Client())
	bins, err := provider.ListCardBINs(context.Background())
	if err != nil {
		t.Fatalf("ListCardBINs() error = %v", err)
	}
	if len(bins) != 2 {
		t.Fatalf("ListCardBINs() returned %d bins, want 2: %#v", len(bins), bins)
	}
	if bins[0].ID != "40" || bins[0].BIN != "40024200" || bins[0].Name != "US Debit" || bins[0].MaxCardCount != 20 || bins[0].IssuedCount != 3 {
		t.Fatalf("first BIN = %#v", bins[0])
	}
	if bins[1].ID != "38" || bins[1].BIN != "40041606" || bins[1].CardType != "BUDGET" || bins[1].Status != "OPEN" {
		t.Fatalf("second BIN = %#v", bins[1])
	}
}

func TestProviderListCardBINsRequiresSavedCredentials(t *testing.T) {
	provider := New(testConfig{
		"card_provider_kimoox_enabled": "0",
		"kimoox_base_url":              "https://card.kimoox.com",
	}, nil)
	_, err := provider.ListCardBINs(context.Background())
	if err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Fatalf("ListCardBINs() error = %v, want missing credentials", err)
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

func TestKimooxBusinessErrorKeepsUnusedCardCancelMessage(t *testing.T) {
	err := kimooxBusinessError(map[string]any{"code": "500", "msg": "销卡失败：该卡暂无有效消费交易，暂不支持销卡"}, "status_operate")
	if err == nil || !strings.Contains(err.Error(), "该卡暂无有效消费交易，暂不支持销卡") {
		t.Fatalf("error = %v", err)
	}
	if cardpool.IsFailoverEligible(err) {
		t.Fatal("unused-card cancel restriction must not failover")
	}
	wrapped := kimooxUnusedCardCancelError(fmt.Errorf(`KIMOOX status_operate: HTTP 500: {"msg":"销卡失败：该卡暂无有效消费交易，暂不支持销卡"}`))
	if wrapped == nil || !strings.Contains(wrapped.Error(), "该卡暂无有效消费交易，暂不支持销卡") {
		t.Fatalf("wrapped = %v", wrapped)
	}
}

func TestKimooxSensitiveKeyMatchesDocumentedDerivation(t *testing.T) {
	got := hex.EncodeToString(kimooxSensitiveKey("secret"))
	want := hex.EncodeToString(hmacSHA256([]byte("secret"), []byte("vcc-webhook-sensitive-v1")))
	if got != want {
		t.Fatalf("key = %s, want %s", got, want)
	}
}
