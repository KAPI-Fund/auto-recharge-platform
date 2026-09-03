package airwallex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestProviderCapabilitiesDeclareWebhookSupport(t *testing.T) {
	provider := New(testConfig{}, nil)
	if !provider.Supports(cardpool.CapabilityWebhook) {
		t.Fatal("Airwallex provider must advertise webhook capability")
	}
	if provider.Supports(cardpool.CapabilityTransactionLimit) {
		t.Fatal("Airwallex provider must not advertise unsupported transaction limits")
	}
}

func TestProviderValidatesEnabledCredentialsBeforeRuntime(t *testing.T) {
	provider := New(testConfig{"card_provider_airwallex_enabled": "1"}, nil)
	if err := provider.ValidateConfiguration(); err == nil || !strings.Contains(err.Error(), "Client ID") {
		t.Fatalf("validation error = %v, want missing Client ID", err)
	}
	provider = New(testConfig{
		"card_provider_airwallex_enabled": "1", "airwallex_client_id": "client", "airwallex_api_key": "key",
	}, nil)
	if err := provider.ValidateConfiguration(); err != nil {
		t.Fatalf("valid Airwallex configuration rejected: %v", err)
	}
}

func TestProviderUsesGeneratedClientForIssuingLifecycle(t *testing.T) {
	var createBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			if request.Header.Get("Authorization") != "" {
				t.Fatalf("login request must not use a bearer token")
			}
			_, _ = io.WriteString(writer, `{"token":"aw-token","expires_at":"2099-01-01T00:00:00Z"}`)
		case "/api/v1/issuing/cards/create":
			if request.Header.Get("Authorization") != "Bearer aw-token" {
				t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
			}
			if err := json.NewDecoder(request.Body).Decode(&createBody); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			writer.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(writer, `{"card_id":"aw-card-1","card_status":"ACTIVE","brand":"visa","last4":"4111"}`)
		case "/api/v1/issuing/cards/aw-card-1/details":
			_, _ = io.WriteString(writer, `{"card_number":"4242424242424242","cvv":"123","expiry_month":12,"expiry_year":2030,"name_on_card":"Test User"}`)
		case "/api/v1/issuing/cards/aw-card-1/update":
			_, _ = io.WriteString(writer, `{"card_id":"aw-card-1","card_status":"INACTIVE"}`)
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(writer, `{ "error": "not found" }`)
		}
	}))
	defer server.Close()

	provider := New(testConfig{
		"card_provider_airwallex_enabled": "1", "airwallex_base_url": server.URL,
		"airwallex_client_id": "client", "airwallex_api_key": "key", "airwallex_cardholder_id": "holder",
	}, server.Client())
	card, err := provider.CreateCard(context.Background(), cardpool.CreateCardRequest{
		UsageType: cardpool.UsageOneTime, Amount: 20, Currency: "USD", IdempotencyKey: "request-1",
	})
	if err != nil {
		t.Fatalf("CreateCard() error = %v", err)
	}
	if card.ProviderCardID != "aw-card-1" || card.Status != cardpool.CardActive || card.Last4 != "4111" {
		t.Fatalf("unexpected card: %#v", card)
	}
	if createBody["request_id"] != "request-1" || createBody["cardholder_id"] != "holder" {
		t.Fatalf("unexpected create request: %#v", createBody)
	}
	controls, ok := createBody["authorization_controls"].(map[string]any)
	if !ok || controls["allowed_transaction_count"] != "SINGLE" {
		t.Fatalf("single-use controls missing: %#v", createBody["authorization_controls"])
	}

	details, err := provider.GetSensitiveCardDetails(context.Background(), "aw-card-1")
	if err != nil {
		t.Fatalf("GetSensitiveCardDetails() error = %v", err)
	}
	if details.CardNumber != "4242424242424242" || details.CVC != "123" || details.ExpiryYear != 2030 {
		t.Fatalf("unexpected sensitive details: %#v", details)
	}
	if err := provider.FreezeCard(context.Background(), "aw-card-1"); err != nil {
		t.Fatalf("FreezeCard() error = %v", err)
	}
}

func TestProviderMapsTechnicalFailureForFailover(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/v1/authentication/login" {
			_, _ = io.WriteString(writer, `{"token":"aw-token"}`)
			return
		}
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(writer, `{"message":"temporary provider outage"}`)
	}))
	defer server.Close()
	provider := New(testConfig{"card_provider_airwallex_enabled": "1", "airwallex_base_url": server.URL, "airwallex_client_id": "client", "airwallex_api_key": "key", "airwallex_cardholder_id": "holder"}, server.Client())
	_, err := provider.CreateCard(context.Background(), cardpool.CreateCardRequest{Currency: "USD"})
	if err == nil || !cardpool.IsFailoverEligible(err) {
		t.Fatalf("error = %v, want failover-eligible provider error", err)
	}
}

func TestParseTimeAcceptsAirwallexTimestamps(t *testing.T) {
	for _, value := range []string{"2026-08-28T10:00:00Z", "2026-08-28T10:00:00+0000", "2026-08-28 10:00:00"} {
		if _, err := parseTime(value); err != nil {
			t.Fatalf("parseTime(%q): %v", value, err)
		}
	}
	if _, err := parseTime(time.Now().Format(time.RFC1123)); err == nil {
		t.Fatal("unexpectedly accepted unsupported timestamp")
	}
}
