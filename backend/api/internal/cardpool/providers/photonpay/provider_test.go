package photonpay

import (
	"context"
	"crypto"
	"crypto/md5" // #nosec G501 -- PhotonPay's documented test signing contract.
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

func TestProviderUsesBasicAuthSignedRequestsAndFullLifecycle(t *testing.T) {
	privateKey := generatePhotonPrivateKey(t)
	privateRSAKey, err := parsePrivateKey(privateKey)
	if err != nil {
		t.Fatalf("parse test private key: %v", err)
	}

	var authCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/oauth2/token/accessToken":
			authCalls.Add(1)
			if request.Method != http.MethodPost || request.Header.Get("Accept") != "application/json" || request.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("app-1/app-secret-1")) {
				t.Fatalf("authentication request = method=%s accept=%q authorization=%q", request.Method, request.Header.Get("Accept"), request.Header.Get("Authorization"))
			}
			_, _ = io.WriteString(writer, `{"code":"0000","data":{"token":"photon-token","expiresIn":7200}}`)
		case "/vcc/openApi/v4/openCard":
			if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" || request.Header.Get("X-PD-TOKEN") != "photon-token" {
				t.Fatalf("open card headers = method=%s content-type=%q token=%q", request.Method, request.Header.Get("Content-Type"), request.Header.Get("X-PD-TOKEN"))
			}
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Fatalf("read open card body: %v", readErr)
			}
			assertPhotonRequestSignature(t, request, body, &privateRSAKey.PublicKey)
			if request.Header.Get("X-PD-TOKEN") != "photon-token" {
				t.Fatalf("X-PD-TOKEN = %q", request.Header.Get("X-PD-TOKEN"))
			}
			var bodyObject map[string]any
			if err := json.Unmarshal(body, &bodyObject); err != nil {
				t.Fatalf("decode open card body: %v", err)
			}
			if bodyObject["requestId"] != "photon-request-1" || bodyObject["cardBin"] != "411111" || bodyObject["cardCurrency"] != "USD" {
				t.Fatalf("open card body = %#v", bodyObject)
			}
			_, _ = io.WriteString(writer, `{"code":"0000","data":{"cardDetail":{"cardId":"photon-card-1","cardStatus":"normal","maskCardNo":"**** 4242","cardholderNameAbbreviation":"Photon Test","cardCurrency":"USD"}}}`)
		case "/vcc/openApi/v4/getCardDetail":
			if request.Method != http.MethodGet || request.URL.Query().Get("cardId") != "photon-card-1" || request.Header.Get("X-PD-TOKEN") != "photon-token" {
				t.Fatalf("card detail request = method=%s query=%s token=%q", request.Method, request.URL.RawQuery, request.Header.Get("X-PD-TOKEN"))
			}
			_, _ = io.WriteString(writer, `{"code":"0000","data":{"cardDetail":{"cardId":"photon-card-1","cardStatus":"normal","maskCardNo":"**** 4242","cardNo":"4242424242424242","expirationDate":"12/30","cardholderNameAbbreviation":"Photon Test","cardCurrency":"USD"}}}`)
		case "/vcc/openApi/v4/getCvv":
			if request.Method != http.MethodGet || request.URL.Query().Get("cardId") != "photon-card-1" || request.Header.Get("X-PD-TOKEN") != "photon-token" {
				t.Fatalf("CVV request = method=%s query=%s token=%q", request.Method, request.URL.RawQuery, request.Header.Get("X-PD-TOKEN"))
			}
			_, _ = io.WriteString(writer, `{"code":"0000","data":{"cvv":"123"}}`)
		case "/vcc/openApi/v4/freezeCard", "/vcc/openApi/v4/updateCard", "/vcc/openApi/v4/cancelCard":
			if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" || request.Header.Get("X-PD-TOKEN") != "photon-token" {
				t.Fatalf("lifecycle headers = path=%s method=%s content-type=%q token=%q", request.URL.Path, request.Method, request.Header.Get("Content-Type"), request.Header.Get("X-PD-TOKEN"))
			}
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Fatalf("read lifecycle body: %v", readErr)
			}
			assertPhotonRequestSignature(t, request, body, &privateRSAKey.PublicKey)
			_, _ = io.WriteString(writer, `{"code":"0000"}`)
		case "/vcc/openApi/v4/pagingVccTradeOrder":
			if request.Method != http.MethodGet || request.URL.Query().Get("cardId") != "photon-card-1" || request.URL.Query().Get("pageIndex") != "1" || request.URL.Query().Get("pageSize") != "100" || request.Header.Get("X-PD-TOKEN") != "photon-token" {
				t.Fatalf("transactions request = method=%s query=%s token=%q", request.Method, request.URL.RawQuery, request.Header.Get("X-PD-TOKEN"))
			}
			_, _ = io.WriteString(writer, `{"code":"0000","data":[{"transactionId":"photon-txn-1","cardId":"photon-card-1","transactionAmount":12.5,"transactionCurrency":"usd","transactionStatus":"settled","transactionType":"capture","merchantNameLocation":"Example Merchant","txnDate":1777334400}]}`)
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(writer, `{"code":"NOT_FOUND","msg":"not found"}`)
		}
	}))
	defer server.Close()

	provider := New(testConfig{
		"card_provider_photonpay_enabled": "1",
		"photonpay_base_url":              server.URL,
		"photonpay_app_id":                "app-1",
		"photonpay_app_secret":            "app-secret-1",
		"photonpay_private_key":           privateKey,
		"photonpay_card_bin":              "411111",
		"photonpay_cardholder_id":         "holder-1",
		"photonpay_card_type":             "recharge",
	}, server.Client())
	if err := provider.ValidateConfiguration(); err != nil {
		t.Fatalf("valid PhotonPay configuration rejected: %v", err)
	}

	card, err := provider.CreateCard(context.Background(), cardpool.CreateCardRequest{
		UsageType: cardpool.UsageRecurring, Amount: 20, Currency: "usd", IdempotencyKey: "photon-request-1",
	})
	if err != nil {
		t.Fatalf("CreateCard() error = %v", err)
	}
	if card.ProviderCardID != "photon-card-1" || card.Status != cardpool.CardActive || card.Last4 != "4242" || card.Currency != "USD" {
		t.Fatalf("unexpected card: %#v", card)
	}
	metadata, err := provider.GetCard(context.Background(), "photon-card-1")
	if err != nil || metadata.ProviderCardID != "photon-card-1" {
		t.Fatalf("GetCard() = %#v, err = %v", metadata, err)
	}
	details, err := provider.GetSensitiveCardDetails(context.Background(), "photon-card-1")
	if err != nil {
		t.Fatalf("GetSensitiveCardDetails() error = %v", err)
	}
	if details.CardNumber != "4242424242424242" || details.CVC != "123" || details.ExpiryMonth != 12 || details.ExpiryYear != 2030 {
		t.Fatalf("unexpected sensitive details: %#v", details)
	}
	if err := provider.FreezeCard(context.Background(), "photon-card-1"); err != nil {
		t.Fatalf("FreezeCard() error = %v", err)
	}
	if err := provider.UnfreezeCard(context.Background(), "photon-card-1"); err != nil {
		t.Fatalf("UnfreezeCard() error = %v", err)
	}
	if err := provider.UpdateLimits(context.Background(), "photon-card-1", cardpool.CardLimits{PerTransaction: 20, Daily: 100, Monthly: 500, AllTime: 1000}); err != nil {
		t.Fatalf("UpdateLimits() error = %v", err)
	}
	transactions, err := provider.GetTransactions(context.Background(), "photon-card-1")
	if err != nil || len(transactions) != 1 || transactions[0].MerchantName != "Example Merchant" || transactions[0].Currency != "USD" {
		t.Fatalf("GetTransactions() = %#v, err = %v", transactions, err)
	}
	if err := provider.CancelCard(context.Background(), "photon-card-1"); err != nil {
		t.Fatalf("CancelCard() error = %v", err)
	}
	if got := authCalls.Load(); got != 1 {
		t.Fatalf("authentication calls = %d, want cached single call", got)
	}
}

func TestProviderMapsBusinessFailureWithoutFailover(t *testing.T) {
	privateKey := generatePhotonPrivateKey(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/oauth2/token/accessToken" {
			_, _ = io.WriteString(writer, `{"code":"0000","data":{"token":"photon-token","expiresIn":7200}}`)
			return
		}
		_, _ = io.WriteString(writer, `{"success":false,"code":"INSUFFICIENT_FUNDS","msg":"insufficient funds"}`)
	}))
	defer server.Close()
	provider := New(testConfig{
		"card_provider_photonpay_enabled": "1", "photonpay_base_url": server.URL,
		"photonpay_app_id": "app-1", "photonpay_app_secret": "secret-1", "photonpay_private_key": privateKey,
		"photonpay_card_bin": "411111", "photonpay_cardholder_id": "holder-1",
	}, server.Client())
	_, err := provider.CreateCard(context.Background(), cardpool.CreateCardRequest{IdempotencyKey: "request-1"})
	var providerErr *cardpool.ProviderError
	if err == nil || !errors.As(err, &providerErr) || providerErr.Category != cardpool.CategoryInsufficientFunds || cardpool.IsFailoverEligible(err) {
		t.Fatalf("error = %#v, want non-failover insufficient funds", err)
	}
}

func TestParsePhotonTimeAcceptsUnixMilliseconds(t *testing.T) {
	if parsed, ok := parsePhotonTime("1787911200000"); !ok || parsed.UnixMilli() != 1787911200000 {
		t.Fatalf("parsePhotonTime milliseconds = %v, %t", parsed, ok)
	}
	if parsed, ok := parsePhotonTime("1787911200"); !ok || parsed.Unix() != 1787911200 {
		t.Fatalf("parsePhotonTime seconds = %v, %t", parsed, ok)
	}
}

func TestParsePrivateKeyAcceptsEscapedNewlines(t *testing.T) {
	key := generatePhotonPrivateKey(t)
	escaped := strings.ReplaceAll(key, "\n", `\n`)
	parsed, err := parsePrivateKey(escaped)
	if err != nil {
		t.Fatalf("parsePrivateKey() error = %v", err)
	}
	if parsed.N.Cmp(mustParsePrivateKey(t, key).N) != 0 {
		t.Fatal("escaped-newline key parsed to a different RSA key")
	}
}

func mustParsePrivateKey(t *testing.T, value string) *rsa.PrivateKey {
	t.Helper()
	parsed, err := parsePrivateKey(value)
	if err != nil {
		t.Fatalf("parsePrivateKey() error = %v", err)
	}
	return parsed
}

func generatePhotonPrivateKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func assertPhotonRequestSignature(t *testing.T, request *http.Request, body []byte, publicKey *rsa.PublicKey) {
	t.Helper()
	signature, err := base64.StdEncoding.DecodeString(request.Header.Get("X-PD-SIGN"))
	if err != nil {
		t.Fatalf("decode X-PD-SIGN: %v", err)
	}
	digest := md5.Sum(body) // #nosec G401 -- required by PhotonPay's documented contract.
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.MD5, digest[:], signature); err != nil {
		t.Fatalf("invalid PhotonPay request signature: %v", err)
	}
}
