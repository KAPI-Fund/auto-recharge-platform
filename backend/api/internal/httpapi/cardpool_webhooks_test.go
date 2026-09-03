package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/airwallex"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
)

type cardWebhookTestConfig map[string]string

func (c cardWebhookTestConfig) Value(key, fallback string) string {
	if value := strings.TrimSpace(c[key]); value != "" {
		return value
	}
	return fallback
}

func (c cardWebhookTestConfig) Secret(key, fallback string) string {
	return c.Value(key, fallback)
}

func TestCardProviderWebhookRouteRejectsInvalidSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database, _ := newAssetRecoveryMock(t)
	registry := cardpool.NewProviderRegistry()
	registry.Register(airwallex.New(cardWebhookTestConfig{"airwallex_webhook_secret": "webhook-secret"}, nil))
	router := NewRouter(&Server{
		DB:        database,
		Cfg:       config.Config{},
		CardPools: cardpool.NewService(database, registry, nil),
	})

	body := []byte(`{"id":"evt-invalid","name":"issuing.card.active"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/cards/airwallex", bytes.NewReader(body))
	request.Header.Set("X-Trace-ID", "trace-http-invalid")
	request.Header.Set("X-Timestamp", fmt.Sprintf("%d", time.Now().UnixMilli()))
	request.Header.Set("X-Signature", "00")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("X-Trace-ID") != "trace-http-invalid" {
		t.Fatalf("trace header = %q", recorder.Header().Get("X-Trace-ID"))
	}
	if strings.Contains(recorder.Body.String(), "webhook-secret") {
		t.Fatal("webhook secret leaked in validation response")
	}
}

func TestCardProviderWebhookRoutePersistsVerifiedEventAndTraceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database, mock := newAssetRecoveryMock(t)
	secret := "webhook-secret"
	provider := airwallex.New(cardWebhookTestConfig{"airwallex_webhook_secret": secret}, nil)
	registry := cardpool.NewProviderRegistry()
	registry.Register(provider)
	router := NewRouter(&Server{
		DB:        database,
		Cfg:       config.Config{},
		CardPools: cardpool.NewService(database, registry, nil),
	})

	body := []byte(`{"id":"evt-http-1","name":"issuing.card.active","created_at":"2026-08-28T10:00:00Z","data":{"card_id":"aw-card-not-yet-imported","card_status":"ACTIVE"}}`)
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write([]byte(timestamp))
	_, _ = digest.Write(body)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "card_provider_events"`)+`.*`+
		`WHERE provider = \$1 AND provider_event_id = \$2.*`+
		`LIMIT \$3 FOR UPDATE`).
		WithArgs("AIRWALLEX", "evt-http-1", 1).
		WillReturnRows(sqlmockRowsForNotFound("id"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "payment_cards"`)+`.*`+
		`WHERE provider = \$1 AND provider_card_id = \$2.*`+
		`LIMIT \$3 FOR UPDATE`).
		WithArgs("AIRWALLEX", "aw-card-not-yet-imported", 1).
		WillReturnRows(sqlmockRowsForNotFound("id"))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO "card_provider_events"`)+`.*`).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/cards/airwallex", bytes.NewReader(body))
	request.Header.Set("X-Trace-ID", "trace-http-event")
	request.Header.Set("X-Timestamp", timestamp)
	request.Header.Set("X-Signature", hex.EncodeToString(digest.Sum(nil)))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("X-Trace-ID") != "trace-http-event" {
		t.Fatalf("trace header = %q", recorder.Header().Get("X-Trace-ID"))
	}
	if !strings.Contains(recorder.Body.String(), `"received":true`) || !strings.Contains(recorder.Body.String(), `"ignored":true`) {
		t.Fatalf("unexpected webhook response: %s", recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

// Keep the row construction in one place so webhook tests do not depend on
// the provider event model's complete column list.
func sqlmockRowsForNotFound(column string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{column})
}
