package stripeissuing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

func TestProviderCapabilitiesDeclareWebhookSupport(t *testing.T) {
	provider := New(testConfig{}, nil)
	if !provider.Supports(cardpool.CapabilityWebhook) {
		t.Fatal("Stripe Issuing provider must advertise webhook capability")
	}
	if provider.Supports(cardpool.CapabilitySingleUse) {
		t.Fatal("Stripe Issuing provider must not advertise native single-use support")
	}
}

func TestProviderValidatesEnabledCredentialsBeforeRuntime(t *testing.T) {
	provider := New(testConfig{"card_provider_stripe_issuing_enabled": "1"}, nil)
	if err := provider.ValidateConfiguration(); err == nil || !strings.Contains(err.Error(), "secret key") {
		t.Fatalf("validation error = %v, want missing secret key", err)
	}
	provider = New(testConfig{
		"card_provider_stripe_issuing_enabled": "1", "stripe_issuing_secret_key": "sk_test_issuing",
	}, nil)
	if err := provider.ValidateConfiguration(); err != nil {
		t.Fatalf("valid Stripe Issuing configuration rejected: %v", err)
	}
}

func TestProviderUsesGeneratedClientForStripeIssuingLifecycle(t *testing.T) {
	var createForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Header.Get("Authorization") != "Bearer sk_test_issuing" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/v1/issuing/cards":
			if request.Method == http.MethodPost {
				if err := request.ParseForm(); err != nil {
					t.Fatalf("ParseForm() error = %v", err)
				}
				createForm = request.PostForm
				if request.Header.Get("Idempotency-Key") != "stripe-request-1" {
					t.Fatalf("idempotency key = %q", request.Header.Get("Idempotency-Key"))
				}
				_, _ = io.WriteString(writer, `{"id":"ii-card-1","cardholder":"ich_1","currency":"usd","type":"virtual","status":"active","last4":"4242"}`)
				return
			}
			_, _ = io.WriteString(writer, `{"object":"list","data":[]}`)
		case "/v1/issuing/cards/ii-card-1":
			if request.Method == http.MethodGet {
				if len(request.URL.Query()["expand[]"]) != 2 {
					t.Fatalf("expand query = %#v", request.URL.Query()["expand[]"])
				}
				_, _ = io.WriteString(writer, `{"id":"ii-card-1","cardholder":"ich_1","currency":"usd","type":"virtual","status":"active","last4":"4242","number":"4242424242424242","cvc":"123","exp_month":12,"exp_year":2030}`)
				return
			}
			_, _ = io.WriteString(writer, `{"id":"ii-card-1","status":"inactive"}`)
		case "/v1/issuing/transactions":
			_, _ = io.WriteString(writer, `{"object":"list","data":[{"id":"txn-1","card":"ii-card-1","amount":1234,"currency":"usd","status":"pending","type":"capture","created":1777334400,"merchant_data":{"name":"Example"}}]}`)
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(writer, `{"error":{"message":"not found"}}`)
		}
	}))
	defer server.Close()

	provider := New(testConfig{
		"card_provider_stripe_issuing_enabled": "1", "stripe_issuing_base_url": server.URL,
		"stripe_issuing_secret_key": "sk_test_issuing", "stripe_issuing_cardholder_id": "ich_1",
	}, server.Client())
	card, err := provider.CreateCard(context.Background(), cardpool.CreateCardRequest{Currency: "USD", UsageType: cardpool.UsageMultiUse, IdempotencyKey: "stripe-request-1"})
	if err != nil {
		t.Fatalf("CreateCard() error = %v", err)
	}
	if card.ProviderCardID != "ii-card-1" || card.Status != cardpool.CardActive || card.Last4 != "4242" {
		t.Fatalf("unexpected card: %#v", card)
	}
	if createForm.Get("cardholder") != "ich_1" || createForm.Get("currency") != "usd" || createForm.Get("type") != "virtual" {
		t.Fatalf("unexpected create form: %#v", createForm)
	}

	details, err := provider.GetSensitiveCardDetails(context.Background(), "ii-card-1")
	if err != nil {
		t.Fatalf("GetSensitiveCardDetails() error = %v", err)
	}
	if details.CardNumber != "4242424242424242" || details.CVC != "123" || details.ExpiryMonth != 12 || details.ExpiryYear != 2030 {
		t.Fatalf("unexpected sensitive details: %#v", details)
	}
	transactions, err := provider.GetTransactions(context.Background(), "ii-card-1")
	if err != nil {
		t.Fatalf("GetTransactions() error = %v", err)
	}
	if len(transactions) != 1 || transactions[0].Amount != 12.34 || transactions[0].MerchantName != "Example" {
		t.Fatalf("unexpected transactions: %#v", transactions)
	}
	if err := provider.FreezeCard(context.Background(), "ii-card-1"); err != nil {
		t.Fatalf("FreezeCard() error = %v", err)
	}
}

func TestProviderMapsProviderUnavailableForFailover(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(writer, `{"error":{"message":"temporarily unavailable"}}`)
	}))
	defer server.Close()
	provider := New(testConfig{"card_provider_stripe_issuing_enabled": "1", "stripe_issuing_base_url": server.URL, "stripe_issuing_secret_key": "sk_test_issuing", "stripe_issuing_cardholder_id": "ich_1"}, server.Client())
	_, err := provider.CreateCard(context.Background(), cardpool.CreateCardRequest{Currency: "USD"})
	if err == nil || !cardpool.IsFailoverEligible(err) {
		t.Fatalf("error = %v, want failover-eligible provider error", err)
	}
}
