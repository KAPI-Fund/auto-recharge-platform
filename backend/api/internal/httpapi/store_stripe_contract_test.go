package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestCheckoutReturnURLKeepsConfiguredQueryAndAddsStripeContext(t *testing.T) {
	value := checkoutReturnURL("https://example.test/recharge?source=admin&order_id={ORDER_ID}", "success", "order_123")
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("source") != "admin" || query.Get("checkout") != "success" || query.Get("order_id") != "order_123" {
		t.Fatalf("return query = %#v", query)
	}
	if query.Get("session_id") != "{CHECKOUT_SESSION_ID}" {
		t.Fatalf("session placeholder = %q", query.Get("session_id"))
	}
	if strings.Contains(value, "%7BCHECKOUT_SESSION_ID%7D") || strings.Contains(value, "{ORDER_ID}") {
		t.Fatalf("return URL still contains an unresolved placeholder: %s", value)
	}
}

func TestBuildStripeReturnURLRequiresAbsoluteHTTPURL(t *testing.T) {
	if _, err := buildStripeReturnURL("/recharge", "", "success", "order_1"); err == nil {
		t.Fatal("relative Stripe return URL was accepted")
	}
	if _, err := buildStripeReturnURL("https://example.test/recharge", "", "cancel", "order_1"); err != nil {
		t.Fatalf("absolute Stripe return URL rejected: %v", err)
	}
}

func TestCreateStripeCheckoutMirrorsReferenceRequestContract(t *testing.T) {
	var received url.Values
	var receivedRequest *http.Request
	stripeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedRequest = request
		if err := request.ParseForm(); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		received = request.PostForm
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"cs_test_123","url":"https://checkout.stripe.test/cs_test_123","payment_intent":{"id":"pi_test_123"}}`))
	}))
	defer stripeServer.Close()

	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodPost, "/store/orders", nil)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	server := &Server{Cfg: config.Config{
		PublicBaseURL:        stripeServer.URL,
		StripeAPIBaseURL:     stripeServer.URL,
		StripeSuccessURL:     stripeServer.URL + "/recharge?source=success&order_id={ORDER_ID}",
		StripeCancelURL:      stripeServer.URL + "/recharge?source=cancel&order_id={ORDER_ID}",
		SessionEncryptionKey: "test-session-key",
	}}
	order := models.StoreOrder{
		ID: "order_123", OrderNo: "ORD-123", TraceID: "trace_123", Email: "buyer@example.com",
		Amount: 12.34, Currency: models.PlatformStoreCurrency, PhoneE164: "+8613800138000",
	}
	plan := models.Plan{ID: "plan_plus", Code: "plus", Name: "ChatGPT Plus", Description: "monthly access"}

	checkoutURL, sessionID, paymentID, err := server.createStripeCheckout(context, order, plan, "sk_test_secret")
	if err != nil {
		t.Fatal(err)
	}
	if checkoutURL != "https://checkout.stripe.test/cs_test_123" || sessionID != "cs_test_123" || paymentID != "pi_test_123" {
		t.Fatalf("checkout response = %q %q %q", checkoutURL, sessionID, paymentID)
	}
	if receivedRequest == nil || receivedRequest.Header.Get("Idempotency-Key") != "cs-order_123" {
		t.Fatalf("idempotency key = %q", receivedRequest.Header.Get("Idempotency-Key"))
	}
	if receivedRequest.Header.Get("Authorization") != "Basic c2tfdGVzdF9zZWNyZXQ6" {
		t.Fatalf("authorization header = %q", receivedRequest.Header.Get("Authorization"))
	}
	checks := map[string]string{
		"mode":                                               "payment",
		"client_reference_id":                                "ORD-123",
		"billing_address_collection":                         "required",
		"customer_creation":                                  "always",
		"line_items[0][price_data][currency]":                "cny",
		"line_items[0][price_data][unit_amount]":             "1234",
		"invoice_creation[enabled]":                          "true",
		"payment_intent_data[receipt_email]":                 "buyer@example.com",
		"metadata[orderId]":                                  "order_123",
		"metadata[orderNo]":                                  "ORD-123",
		"payment_intent_data[metadata][order_id]":            "order_123",
		"invoice_creation[invoice_data][metadata][trace_id]": "trace_123",
	}
	for key, expected := range checks {
		if received.Get(key) != expected {
			t.Errorf("form %s = %q, want %q", key, received.Get(key), expected)
		}
	}
	success, err := url.Parse(received.Get("success_url"))
	if err != nil {
		t.Fatal(err)
	}
	if success.Query().Get("checkout") != "success" || success.Query().Get("order_id") != "order_123" || success.Query().Get("session_id") != "{CHECKOUT_SESSION_ID}" {
		t.Fatalf("success_url = %s", received.Get("success_url"))
	}
}

func TestValidateStripePaymentIdentityRejectsCrossedObjects(t *testing.T) {
	order := models.StoreOrder{StripeSessionID: "cs_order_a", StripePaymentID: "pi_order_a"}

	tests := []struct {
		name      string
		sessionID string
		paymentID string
	}{
		{name: "crossed checkout session", sessionID: "cs_order_b", paymentID: "pi_order_a"},
		{name: "crossed payment intent", sessionID: "cs_order_a", paymentID: "pi_order_b"},
		{name: "both crossed", sessionID: "cs_order_b", paymentID: "pi_order_b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateStripePaymentIdentity(order, test.sessionID, test.paymentID); err == nil {
				t.Fatal("crossed Stripe object identity was accepted")
			}
		})
	}

	if err := validateStripePaymentIdentity(order, "cs_order_a", "pi_order_a"); err != nil {
		t.Fatalf("matching Stripe object identity was rejected: %v", err)
	}
}

func TestParseStripePaymentNotificationSupportsReferenceEventShapes(t *testing.T) {
	tests := []struct {
		name       string
		body       map[string]any
		action     stripeWebhookAction
		orderID    string
		orderNo    string
		amount     int64
		currency   string
		paymentID  string
		reasonPart string
	}{
		{
			name: "checkout session completed",
			body: map[string]any{
				"id": "evt_checkout", "type": "checkout.session.completed",
				"data": map[string]any{"object": map[string]any{
					"id": "cs_123", "client_reference_id": "ORD-123", "payment_status": "paid", "amount_total": 1234, "currency": "cny",
					"payment_intent": map[string]any{"id": "pi_123"}, "metadata": map[string]string{"orderId": "order_123", "orderNo": "ORD-123", "traceId": "trace_123"},
				}},
			},
			action: stripeWebhookActionFulfill, orderID: "order_123", orderNo: "ORD-123", amount: 1234, currency: "CNY", paymentID: "pi_123",
		},
		{
			name: "invoice paid",
			body: map[string]any{
				"id": "evt_invoice", "type": "invoice.paid",
				"data": map[string]any{"object": map[string]any{
					"id": "in_123", "amount_paid": 1234, "currency": "CNY", "payment_intent": "pi_123", "metadata": map[string]string{"order_id": "order_123"},
				}},
			},
			action: stripeWebhookActionFulfill, orderID: "order_123", amount: 1234, currency: "CNY", paymentID: "pi_123",
		},
		{
			name: "payment intent failed",
			body: map[string]any{
				"id": "evt_payment_failed", "type": "payment_intent.payment_failed",
				"data": map[string]any{"object": map[string]any{
					"id": "pi_failed", "status": "requires_payment_method", "currency": "cny", "metadata": map[string]string{"order_id": "order_123"},
					"last_payment_error": map[string]string{"message": "card declined"},
				}},
			},
			action: stripeWebhookActionFail, orderID: "order_123", currency: "CNY", paymentID: "pi_failed", reasonPart: "card declined",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(test.body)
			if err != nil {
				t.Fatal(err)
			}
			event, err := parseStripeWebhookEvent(body)
			if err != nil {
				t.Fatal(err)
			}
			notification, err := parseStripePaymentNotification(event)
			if err != nil {
				t.Fatal(err)
			}
			if notification.Action != test.action || notification.OrderID != test.orderID || notification.OrderNo != test.orderNo || notification.AmountMinor != test.amount || notification.Currency != test.currency || notification.PaymentID != test.paymentID {
				t.Fatalf("notification = %#v", notification)
			}
			if test.reasonPart != "" && !strings.Contains(notification.FailureReason, test.reasonPart) {
				t.Fatalf("failure reason = %q", notification.FailureReason)
			}
		})
	}
}

func TestStripeSignatureAcceptsAnyMatchingV1Signature(t *testing.T) {
	secret := "whsec_test"
	payload := []byte(`{"id":"evt_123"}`)
	timestamp := time.Now().Unix()
	signature := stripeTestSignature(payload, secret, timestamp)
	header := fmt.Sprintf("t=%d,v1=invalid,v1=%s", timestamp, signature)
	if !validStripeSignature(payload, header, secret) {
		t.Fatal("matching v1 signature was rejected when another v1 signature preceded it")
	}
}

func stripeTestSignature(payload []byte, secret string, timestamp int64) string {
	mac := hmacSHA256([]byte(fmt.Sprintf("%d.%s", timestamp, payload)), []byte(secret))
	return fmt.Sprintf("%x", mac)
}

func hmacSHA256(payload, key []byte) []byte {
	// Keep this test helper independent from the production signature parser.
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}
