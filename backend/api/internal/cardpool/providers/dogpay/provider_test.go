package dogpay

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestProviderUsesDocumentedAuthAndCardLifecycle(t *testing.T) {
	privateKey := generateDogPayPrivateKey(t)
	privateRSAKey, err := parsePrivateKey(privateKey)
	if err != nil {
		t.Fatalf("parse test private key: %v", err)
	}
	privateInfo, err := json.Marshal(map[string]any{
		"card": map[string]any{
			"account_number":  "4242424242424242",
			"expire_m":        "12",
			"expire_y":        "2030",
			"cvv":             "123",
			"cardholder_name": "DogPay Test",
		},
	})
	if err != nil {
		t.Fatalf("marshal private info: %v", err)
	}
	encrypted, err := rsa.EncryptPKCS1v15(rand.Reader, &privateRSAKey.PublicKey, privateInfo)
	if err != nil {
		t.Fatalf("encrypt private info: %v", err)
	}
	encryptedInfo := encodeRawURLBase64(encrypted)

	var authCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.URL.Path == "/open-api/v1/auth/access_token":
			if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" || request.Header.Get("Accept") != "application/json" {
				t.Fatalf("authentication request headers = method=%s content-type=%q accept=%q", request.Method, request.Header.Get("Content-Type"), request.Header.Get("Accept"))
			}
			authCalls.Add(1)
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode authentication body: %v", err)
			}
			if body["grant_type"] != dogPayGrantType || body["appid"] != "app-1" || body["secret"] != "secret-1" {
				t.Fatalf("authentication body = %#v", body)
			}
			_, _ = io.WriteString(writer, `{"code":0,"data":{"access_token":"dog-token","expires_in":7200}}`)
		case request.URL.Path == "/open-api/v1/cards" && request.Method == http.MethodPost:
			if request.Header.Get("Authorization") != "Bearer dog-token" || request.Header.Get("dgp-entity-id") != "entity-1" {
				t.Fatalf("create headers = %#v", request.Header)
			}
			if request.Header.Get("Content-Type") != "application/json" || request.Header.Get("Accept") != "application/json" {
				t.Fatalf("create content headers = content-type=%q accept=%q", request.Header.Get("Content-Type"), request.Header.Get("Accept"))
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if body["cardType"] != "virtual" || body["channelId"] != "channel-1" {
				t.Fatalf("create body = %#v", body)
			}
			options, ok := body["cardOptions"].(map[string]any)
			if !ok || options["callId"] != "dog-request-1" || options["cardHolderId"] != "holder-1" || options["velocityAmountLimit"] != 5.0 {
				t.Fatalf("card options = %#v", body["cardOptions"])
			}
			_, _ = io.WriteString(writer, `{"code":0,"data":{"card":{"cardId":"dog-card-1","status":"active","last4":"4242","cardholderName":"DogPay Test","currency":"usd"}}}`)
		case request.URL.Path == "/open-api/v1/cards" && request.Method == http.MethodGet:
			if request.URL.Query().Get("cardId") != "dog-card-1" || request.URL.Query().Get("page") != "1" || request.URL.Query().Get("take") != "1" || request.Header.Get("dgp-entity-id") != "entity-1" {
				t.Fatalf("card query/header = query=%s entity=%q", request.URL.RawQuery, request.Header.Get("dgp-entity-id"))
			}
			_, _ = io.WriteString(writer, `{"code":0,"data":{"items":[{"cardId":"dog-card-1","status":"active","last4":"4242","cardholderName":"DogPay Test","currency":"usd"}]}}`)
		case request.URL.Path == "/open-api/v1/cards/private-info":
			if request.Method != http.MethodGet || request.URL.Query().Get("cardId") != "dog-card-1" || request.Header.Get("dgp-entity-id") != "entity-1" {
				t.Fatalf("private-info request = method=%s query=%s entity=%q", request.Method, request.URL.RawQuery, request.Header.Get("dgp-entity-id"))
			}
			_, _ = io.WriteString(writer, `{"code":0,"data":{"encrypted_info":"`+encryptedInfo+`"}}`)
		case request.URL.Path == "/open-api/v1/cards/frozen" && request.Method == http.MethodPut:
			assertDogPayCardIDRequest(t, request, "entity-1", "dog-card-1")
			_, _ = io.WriteString(writer, `{"code":0}`)
		case request.URL.Path == "/open-api/v1/cards/unfrozen" && request.Method == http.MethodPut:
			assertDogPayCardIDRequest(t, request, "entity-1", "dog-card-1")
			_, _ = io.WriteString(writer, `{"code":0}`)
		case request.URL.Path == "/open-api/v1/cards" && request.Method == http.MethodDelete:
			assertDogPayCardIDRequest(t, request, "entity-1", "dog-card-1")
			_, _ = io.WriteString(writer, `{"code":0}`)
		case request.URL.Path == "/open-api/v1/cards/spending/limit" && request.Method == http.MethodPut:
			if request.Header.Get("dgp-entity-id") != "entity-1" {
				t.Fatalf("limits entity = %q", request.Header.Get("dgp-entity-id"))
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode limits body: %v", err)
			}
			if body["singleAmountLimit"] != 20.0 || body["velocityAmountLimit"] != 1000.0 || body["weekAmountLimit"] != 700.0 || body["dayAmountLimit"] != 100.0 || body["monthAmountLimit"] != 500.0 {
				t.Fatalf("limits body = %#v", body)
			}
			_, _ = io.WriteString(writer, `{"code":0}`)
		case request.URL.Path == "/open-api/v1/cards/transactions" && request.Method == http.MethodGet:
			if request.URL.Query().Get("cardId") != "dog-card-1" || request.URL.Query().Get("page") != "1" || request.URL.Query().Get("take") != "100" || request.Header.Get("dgp-entity-id") != "entity-1" {
				t.Fatalf("transactions query/header = query=%s entity=%q", request.URL.RawQuery, request.Header.Get("dgp-entity-id"))
			}
			_, _ = io.WriteString(writer, `{"code":0,"data":[{"transactionId":"dog-txn-1","cardId":"dog-card-1","amount":12.5,"currency":"usd","status":"settled","type":"capture","merchantInfo":{"name":"Example Merchant"},"reasonCode":""}]}`)
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(writer, `{"code":"NOT_FOUND","message":"not found"}`)
		}
	}))
	defer server.Close()

	provider := New(testConfig{
		"card_provider_dogpay_enabled": "1",
		"dogpay_base_url":              server.URL,
		"dogpay_appid":                 "app-1",
		"dogpay_secret":                "secret-1",
		"dogpay_channel_id":            "channel-1",
		"dogpay_cardholder_id":         "holder-1",
		"dogpay_entity_id":             "entity-1",
		"dogpay_card_type":             "VIRTUAL",
		"dogpay_velocity_amount_limit": "5",
		"dogpay_private_key":           privateKey,
	}, server.Client())
	if err := provider.ValidateConfiguration(); err != nil {
		t.Fatalf("valid DogPay configuration rejected: %v", err)
	}

	card, err := provider.CreateCard(context.Background(), cardpool.CreateCardRequest{
		UsageType: cardpool.UsageMultiUse, Amount: 20, Currency: "USD", PaymentTaskID: "task-1", IdempotencyKey: "dog-request-1",
	})
	if err != nil {
		t.Fatalf("CreateCard() error = %v", err)
	}
	if card.ProviderCardID != "dog-card-1" || card.Status != cardpool.CardActive || card.Last4 != "4242" || card.Currency != "USD" {
		t.Fatalf("unexpected card: %#v", card)
	}

	metadata, err := provider.GetCard(context.Background(), "dog-card-1")
	if err != nil || metadata.ProviderCardID != "dog-card-1" {
		t.Fatalf("GetCard() = %#v, err = %v", metadata, err)
	}
	details, err := provider.GetSensitiveCardDetails(context.Background(), "dog-card-1")
	if err != nil {
		t.Fatalf("GetSensitiveCardDetails() error = %v", err)
	}
	if details.CardNumber != "4242424242424242" || details.CVC != "123" || details.ExpiryMonth != 12 || details.ExpiryYear != 2030 {
		t.Fatalf("unexpected sensitive details: %#v", details)
	}
	if err := provider.FreezeCard(context.Background(), "dog-card-1"); err != nil {
		t.Fatalf("FreezeCard() error = %v", err)
	}
	if err := provider.UnfreezeCard(context.Background(), "dog-card-1"); err != nil {
		t.Fatalf("UnfreezeCard() error = %v", err)
	}
	if err := provider.UpdateLimits(context.Background(), "dog-card-1", cardpool.CardLimits{PerTransaction: 20, VelocityAmount: 1000, Weekly: 700, Daily: 100, Monthly: 500}); err != nil {
		t.Fatalf("UpdateLimits() error = %v", err)
	}
	transactions, err := provider.GetTransactions(context.Background(), "dog-card-1")
	if err != nil || len(transactions) != 1 || transactions[0].MerchantName != "Example Merchant" {
		t.Fatalf("GetTransactions() = %#v, err = %v", transactions, err)
	}
	if err := provider.CancelCard(context.Background(), "dog-card-1"); err != nil {
		t.Fatalf("CancelCard() error = %v", err)
	}
	if got := authCalls.Load(); got != 1 {
		t.Fatalf("authentication calls = %d, want cached single call", got)
	}
}

func TestProviderRejectsInvalidVelocityLimit(t *testing.T) {
	provider := New(testConfig{
		"card_provider_dogpay_enabled": "1",
		"dogpay_appid":                 "app-1",
		"dogpay_secret":                "secret-1",
		"dogpay_channel_id":            "channel-1",
		"dogpay_cardholder_id":         "holder-1",
		"dogpay_private_key":           generateDogPayPrivateKey(t),
		"dogpay_velocity_amount_limit": "not-a-number",
	}, nil)
	if err := provider.ValidateConfiguration(); err == nil || !strings.Contains(err.Error(), "dogpay_velocity_amount_limit") {
		t.Fatalf("validation error = %v, want invalid velocity limit", err)
	}
}

func TestProviderMapsDocumentedReasonCodes(t *testing.T) {
	tests := []struct {
		code     string
		category cardpool.ErrorCategory
		failover bool
	}{
		{"100001", cardpool.CategoryInsufficientFunds, false},
		{"100004", cardpool.CategoryComplianceBlock, false},
		{"100007", cardpool.CategoryProviderUnavailable, true},
		{"100016", cardpool.CategoryBusinessDecline, false},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			err := dogPayMappedBusinessError("test", test.code, "")
			var providerErr *cardpool.ProviderError
			if !errors.As(err, &providerErr) || providerErr.Category != test.category || cardpool.IsFailoverEligible(err) != test.failover {
				t.Fatalf("error = %#v, want category=%s failover=%t", err, test.category, test.failover)
			}
		})
	}
}

func TestProviderRefreshesExpiredToken(t *testing.T) {
	var authCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/open-api/v1/auth/access_token" {
			t.Fatalf("unexpected auth path: %s", request.URL.Path)
		}
		authCalls.Add(1)
		_, _ = io.WriteString(writer, `{"code":0,"data":{"access_token":"dog-token","expires_in":7200}}`)
	}))
	defer server.Close()
	provider := New(testConfig{
		"card_provider_dogpay_enabled": "1", "dogpay_base_url": server.URL,
		"dogpay_appid": "app-1", "dogpay_secret": "secret-1",
	}, server.Client())
	if _, err := provider.accessToken(context.Background()); err != nil {
		t.Fatalf("first accessToken() error = %v", err)
	}
	provider.tokenMu.Lock()
	provider.tokenExp = time.Now().Add(-time.Second)
	provider.tokenMu.Unlock()
	if _, err := provider.accessToken(context.Background()); err != nil {
		t.Fatalf("refreshed accessToken() error = %v", err)
	}
	if got := authCalls.Load(); got != 2 {
		t.Fatalf("authentication calls = %d, want 2 after expiry", got)
	}
}

func TestProviderMapsBusinessDeclineWithoutFailover(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/open-api/v1/auth/access_token" {
			_, _ = io.WriteString(writer, `{"code":0,"data":{"access_token":"dog-token","expires_in":7200}}`)
			return
		}
		_, _ = io.WriteString(writer, `{"success":false,"code":"INSUFFICIENT_FUNDS","message":"insufficient funds"}`)
	}))
	defer server.Close()
	provider := New(testConfig{
		"card_provider_dogpay_enabled": "1", "dogpay_base_url": server.URL,
		"dogpay_appid": "app-1", "dogpay_secret": "secret-1", "dogpay_channel_id": "channel-1", "dogpay_cardholder_id": "holder-1",
	}, server.Client())
	_, err := provider.CreateCard(context.Background(), cardpool.CreateCardRequest{IdempotencyKey: "request-1"})
	var providerErr *cardpool.ProviderError
	if err == nil || !errors.As(err, &providerErr) || providerErr.Category != cardpool.CategoryInsufficientFunds || cardpool.IsFailoverEligible(err) {
		t.Fatalf("error = %#v, want non-failover insufficient funds", err)
	}
}

func generateDogPayPrivateKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	encoded := x509.MarshalPKCS1PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: encoded}))
}

func encodeRawURLBase64(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func assertDogPayCardIDRequest(t *testing.T, request *http.Request, entity, wantCardID string) {
	t.Helper()
	if request.Header.Get("Authorization") != "Bearer dog-token" || request.Header.Get("dgp-entity-id") != entity || request.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("card lifecycle headers = auth=%q entity=%q content-type=%q", request.Header.Get("Authorization"), request.Header.Get("dgp-entity-id"), request.Header.Get("Content-Type"))
	}
	var body map[string]any
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Fatalf("decode card lifecycle body: %v", err)
	}
	if body["cardId"] != wantCardID {
		t.Fatalf("card lifecycle body = %#v", body)
	}
}
