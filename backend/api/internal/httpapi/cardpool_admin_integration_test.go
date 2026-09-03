package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
)

func openHTTPIntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databaseURL := os.Getenv("RECHARGE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("RECHARGE_TEST_DATABASE_URL is not set")
	}
	database, err := db.Open(databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return database
}

func TestUpdateCardPoolRemovesOmittedProviders(t *testing.T) {
	database := openHTTPIntegrationDatabase(t)
	poolID := db.NewID("pool_admin_update")
	pool := models.CardPool{ID: poolID, Name: poolID, Type: "EXTERNAL_API", UsageType: "MULTI_USE", RoutingStrategy: "FAILOVER", DefaultProvider: "LOCAL_TEXT", Enabled: true}
	providers := []models.CardPoolProvider{
		{ID: db.NewID("pool_provider"), PoolID: poolID, Provider: "LOCAL_TEXT", Enabled: true, Priority: 1, Weight: 100},
		{ID: db.NewID("pool_provider"), PoolID: poolID, Provider: "STRIPE_ISSUING", Enabled: true, Priority: 2, Weight: 30},
	}
	if err := database.Create(&pool).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if err := database.Create(&providers).Error; err != nil {
		t.Fatalf("create providers: %v", err)
	}
	defer func() {
		database.Where("pool_id = ?", poolID).Delete(&models.CardPoolProvider{})
		database.Delete(&models.CardPool{}, "id = ?", poolID)
	}()

	gin.SetMode(gin.TestMode)
	server := &Server{DB: database}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/admin/card-pools/"+poolID, bytes.NewBufferString(`{"name":"`+poolID+`","type":"EXTERNAL_API","usageType":"MULTI_USE","routingStrategy":"FAILOVER","defaultProvider":"LOCAL_TEXT","providers":[{"provider":"LOCAL_TEXT","enabled":true,"priority":1,"weight":100}]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: poolID}}
	server.updateCardPool(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var stored []models.CardPoolProvider
	if err := database.Where("pool_id = ?", poolID).Order("provider ASC").Find(&stored).Error; err != nil {
		t.Fatalf("read providers: %v", err)
	}
	if len(stored) != 1 || stored[0].Provider != "LOCAL_TEXT" {
		t.Fatalf("stored providers = %+v, want only LOCAL_TEXT", stored)
	}
}

func TestBillingProjectionMasksPANBeforeJSONSerialization(t *testing.T) {
	server := &Server{}
	response, err := server.billingResponse(models.BillingRecord{ID: "billing_1", CardLast4: "4242", CardNumber: "4242424242424242", Status: "success"})
	if err != nil {
		t.Fatalf("billingResponse() error = %v", err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal billing response: %v", err)
	}
	if response["card_number"] != "**** **** **** 4242" || strings.Contains(string(encoded), "4242424242424242") || strings.Contains(string(encoded), "card_cvc") {
		t.Fatalf("sensitive billing response = %s", encoded)
	}
}

func TestAdminCardListMasksSensitiveCardFields(t *testing.T) {
	database := openHTTPIntegrationDatabase(t)
	cardID := db.NewID("card_admin_mask")
	asset := models.CardAsset{ID: cardID, PoolID: "pool_legacy", Last4: "4242", CardNumberCiphertext: "4242424242424242", ExpiryCiphertext: "12/30", CVVCiphertext: "123", Holder: "Audit User", Status: "正常", Active: true}
	if err := database.Create(&asset).Error; err != nil {
		t.Fatalf("create card asset: %v", err)
	}
	defer database.Delete(&models.CardAsset{}, "id = ?", cardID)

	gin.SetMode(gin.TestMode)
	server := &Server{DB: database}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/cards", nil)
	server.adminCards(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("card list status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Cards []map[string]any `json:"cards"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode card list response: %v; body = %s", err, recorder.Body.String())
	}
	var cardProjection map[string]any
	for _, item := range response.Cards {
		if item["id"] == cardID {
			cardProjection = item
			break
		}
	}
	if cardProjection == nil {
		t.Fatalf("created card is missing from card list: %s", recorder.Body.String())
	}
	for _, key := range []string{"card_number", "cardNumber", "number", "expiry", "card_expiry", "cvc", "card_cvc", "cvv"} {
		if _, present := cardProjection[key]; present {
			t.Fatalf("card list exposed sensitive field %q: %#v", key, cardProjection)
		}
	}
	encodedCard, err := json.Marshal(cardProjection)
	if err != nil {
		t.Fatalf("encode card projection: %v", err)
	}
	cardJSON := string(encodedCard)
	if strings.Contains(cardJSON, "4242424242424242") || strings.Contains(cardJSON, "12/30") || strings.Contains(cardJSON, "123") {
		t.Fatalf("card list leaked sensitive data: %s", cardJSON)
	}
}

func TestAdminCardListIncludesProviderCardsInPrimaryCardsCollection(t *testing.T) {
	database := openHTTPIntegrationDatabase(t)
	prefix := db.NewID("admin_provider_cards")
	providerCards := []models.PaymentCard{
		{ID: prefix + "-active", PoolID: "pool_provider", Provider: "AIRWALLEX", ProviderCardID: "aw-active", Last4: "1111", UsageType: "ONE_TIME", Status: "ACTIVE"},
		{ID: prefix + "-used", PoolID: "pool_provider", Provider: "STRIPE_ISSUING", ProviderCardID: "ii-used", Last4: "2222", UsageType: "ONE_TIME", Status: "USED", UsageCount: 1},
		{ID: prefix + "-cancelled", PoolID: "pool_provider", Provider: "DOGPAY", ProviderCardID: "dog-cancelled", Last4: "3333", UsageType: "ONE_TIME", Status: "CANCELLED", UsageCount: 1},
		{ID: prefix + "-failed", PoolID: "pool_provider", Provider: "KIMOOX", ProviderCardID: "km-failed", Last4: "4444", UsageType: "ONE_TIME", Status: "FAILED", UsageCount: 1},
	}
	if err := database.Create(&providerCards).Error; err != nil {
		t.Fatalf("create provider cards: %v", err)
	}
	defer database.Where("id LIKE ?", prefix+"-%").Delete(&models.PaymentCard{})

	gin.SetMode(gin.TestMode)
	server := &Server{DB: database, CardPools: cardpool.NewService(database, cardpool.NewProviderRegistry(), nil)}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/cards?limit=200", nil)
	server.adminCards(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("provider card list status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Cards        []map[string]any `json:"cards"`
		PaymentCards []map[string]any `json:"paymentCards"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode provider card list: %v; body = %s", err, recorder.Body.String())
	}
	seen := make(map[string]map[string]any, len(response.Cards))
	for _, item := range response.Cards {
		if id, ok := item["id"].(string); ok {
			seen[id] = item
		}
	}
	for _, card := range providerCards {
		item, ok := seen[card.ID]
		if !ok {
			t.Fatalf("provider card %s is missing from primary cards collection: %s", card.ID, recorder.Body.String())
		}
		if item["provider"] != card.Provider || item["last4"] != card.Last4 || item["status"] != card.Status {
			t.Fatalf("provider card projection = %#v, want provider=%s last4=%s status=%s", item, card.Provider, card.Last4, card.Status)
		}
		for _, key := range []string{"card_number", "cardNumber", "number", "expiry", "card_expiry", "cvc", "card_cvc", "cvv"} {
			if _, present := item[key]; present {
				t.Fatalf("provider card list exposed sensitive field %q: %#v", key, item)
			}
		}
	}
	if len(response.PaymentCards) < len(providerCards) {
		t.Fatalf("paymentCards length = %d, want at least %d", len(response.PaymentCards), len(providerCards))
	}
}

func TestLegacyBillingExportMasksStoredCardLast4(t *testing.T) {
	database := openHTTPIntegrationDatabase(t)
	recordID := db.NewID("billing_export")
	record := models.BillingRecord{ID: recordID, CardLast4: "4242", Amount: 20, Currency: "USD", PlanType: "plus", Status: "success", PaymentTime: dbTestNow()}
	if err := database.Create(&record).Error; err != nil {
		t.Fatalf("create billing record: %v", err)
	}
	defer database.Delete(&models.BillingRecord{}, "id = ?", recordID)

	gin.SetMode(gin.TestMode)
	server := &Server{DB: database}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/billing/export", nil).WithContext(context.Background())
	server.legacyBillingExport(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("export status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "**** **** **** 4242") || strings.Contains(body, "4242424242424242") {
		t.Fatalf("export leaked card data: %s", body)
	}
}

func dbTestNow() time.Time { return time.Date(2026, time.August, 29, 0, 0, 0, 0, time.UTC) }
