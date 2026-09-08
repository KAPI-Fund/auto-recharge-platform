package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestCardEventTypeLabel(t *testing.T) {
	if got := cardEventTypeLabel("CARD_TRANSACTION.AUTH_FAILED"); got != "授权失败" {
		t.Fatalf("label = %q", got)
	}
	if got := cardEventTypeLabel("CARD_ISSUE.SUCCESS"); got != "开卡成功" {
		t.Fatalf("label = %q", got)
	}
	if got := cardEventTypeLabel("CUSTOM.EVENT"); got != "CUSTOM.EVENT" {
		t.Fatalf("unknown label = %q", got)
	}
}

func TestAdminCardActivityRequiresCardID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/cards//activity", nil)
	ctx.Params = gin.Params{{Key: "id", Value: "  "}}
	server.adminCardActivity(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminCardActivityReturnsLinkedEventsAndTransactions(t *testing.T) {
	database := openHTTPIntegrationDatabase(t)
	cardID := db.NewID("card_activity")
	providerCardID := "km-" + cardID
	card := models.PaymentCard{
		ID: cardID, PoolID: "pool_provider", Provider: "KIMOOX", ProviderCardID: providerCardID,
		Last4: "4242", UsageType: "ONE_TIME", Status: "ACTIVE",
	}
	if err := database.Create(&card).Error; err != nil {
		t.Fatalf("create payment card: %v", err)
	}
	occurred := time.Date(2026, time.September, 8, 12, 0, 0, 0, time.UTC)
	event := models.CardProviderEvent{
		ID: db.NewID("card_event"), Provider: "KIMOOX", ProviderEventID: "evt-" + cardID,
		EventType: "CARD_TRANSACTION.AUTH_FAILED", PaymentCardID: cardID, ProviderCardID: providerCardID,
		Status: "processed", Payload: `{"data":{"otpCode":"387123","cardNumber":"4242424242424242"}}`, OccurredAt: &occurred,
	}
	transaction := models.CardTransaction{
		ID: db.NewID("card_txn"), Provider: "KIMOOX", ProviderTransactionID: "txn-" + cardID,
		PaymentCardID: cardID, ProviderCardID: providerCardID, Amount: 25.5, Currency: "USD",
		Status: "AUTH_FAILED", Type: "PURCHASE", MerchantName: "OpenAI", FailureCode: "insufficient balance",
		OccurredAt: &occurred,
	}
	if err := database.Create(&event).Error; err != nil {
		t.Fatalf("create event: %v", err)
	}
	if err := database.Create(&transaction).Error; err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	defer database.Where("id = ?", cardID).Delete(&models.PaymentCard{})
	defer database.Where("id = ?", event.ID).Delete(&models.CardProviderEvent{})
	defer database.Where("id = ?", transaction.ID).Delete(&models.CardTransaction{})

	gin.SetMode(gin.TestMode)
	server := &Server{DB: database}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/cards/"+cardID+"/activity", nil)
	ctx.Params = gin.Params{{Key: "id", Value: cardID}}
	server.adminCardActivity(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Contains(body, "387123") || strings.Contains(body, "4242424242424242") || strings.Contains(body, `"payload"`) {
		t.Fatalf("activity response leaked webhook payload: %s", body)
	}
	var response struct {
		Success      bool             `json:"success"`
		Card         map[string]any   `json:"card"`
		Events       []map[string]any `json:"events"`
		Transactions []map[string]any `json:"transactions"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !response.Success || response.Card["id"] != cardID || response.Card["provider"] != "KIMOOX" {
		t.Fatalf("card = %#v", response.Card)
	}
	if len(response.Events) != 1 || response.Events[0]["eventType"] != "CARD_TRANSACTION.AUTH_FAILED" || response.Events[0]["eventTypeLabel"] != "授权失败" {
		t.Fatalf("events = %#v", response.Events)
	}
	if len(response.Transactions) != 1 || response.Transactions[0]["merchantName"] != "OpenAI" || response.Transactions[0]["statusLabel"] != "授权失败" {
		t.Fatalf("transactions = %#v", response.Transactions)
	}
}

func TestAdminCardActivityLocalCardHasEmptyLists(t *testing.T) {
	database := openHTTPIntegrationDatabase(t)
	cardID := db.NewID("card_local_activity")
	asset := models.CardAsset{ID: cardID, PoolID: "pool_legacy", Provider: "LOCAL_TEXT", Last4: "1111", CardNumberCiphertext: "cipher", Status: "正常", Active: true}
	if err := database.Create(&asset).Error; err != nil {
		t.Fatalf("create card asset: %v", err)
	}
	defer database.Delete(&models.CardAsset{}, "id = ?", cardID)

	gin.SetMode(gin.TestMode)
	server := &Server{DB: database}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/cards/"+cardID+"/activity", nil)
	ctx.Params = gin.Params{{Key: "id", Value: cardID}}
	server.adminCardActivity(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Card         map[string]any   `json:"card"`
		Events       []map[string]any `json:"events"`
		Transactions []map[string]any `json:"transactions"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Card["local"] != true || len(response.Events) != 0 || len(response.Transactions) != 0 {
		t.Fatalf("local activity = %#v events=%#v tx=%#v", response.Card, response.Events, response.Transactions)
	}
}
