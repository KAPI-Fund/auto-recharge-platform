package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	appdb "github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

type storeInventoryHTTPResult struct {
	status  int
	traceID string
	body    map[string]any
	err     error
}

func TestStoreProductInventoryConcurrentDebugFulfillment(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("RECHARGE_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("RECHARGE_TEST_DATABASE_URL is not set")
	}

	database, err := appdb.Open(databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	if err := appdb.Migrate(database); err != nil {
		t.Fatalf("migrate integration database: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("get integration sql database: %v", err)
	}
	defer sqlDB.Close()

	planCode := "inventory-e2e-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	plan := models.Plan{
		ID:               appdb.NewID("inventory_plan"),
		Code:             planCode,
		Name:             "Inventory concurrency test",
		Description:      "",
		ProviderPlanName: appdb.ProviderPlanNameForCode("plus"),
		Country:          "US",
		Currency:         models.PlatformStoreCurrency,
		Price:            1,
		Active:           true,
		SortOrder:        9999,
		SaleLimit:        3,
	}
	if err := database.Create(&plan).Error; err != nil {
		t.Fatalf("create inventory test plan: %v", err)
	}
	defer func() {
		// Orders are not referenced by a foreign key from CDKs; remove orders
		// first so this cleanup remains safe if the model gains that relation.
		_ = database.Where("plan_id = ?", plan.ID).Delete(&models.StoreOrder{}).Error
		_ = database.Where("plan_id = ?", plan.ID).Delete(&models.CDK{}).Error
		_ = database.Delete(&models.Plan{}, "id = ?", plan.ID).Error
	}()

	server := &Server{
		DB: database,
		Cfg: config.Config{
			StoreDebugMode:       true,
			EmailEnabled:         false,
			EmailNotifyPurchase:  false,
			EmailNotifyRedeem:    false,
			PublicBaseURL:        "http://127.0.0.1",
			SessionEncryptionKey: "inventory-integration-session-key",
		},
	}
	httpServer := httptest.NewServer(NewRouter(server))
	defer httpServer.Close()

	const requestCount = 12
	results := make(chan storeInventoryHTTPResult, requestCount)
	var waitGroup sync.WaitGroup
	waitGroup.Add(requestCount)
	for index := 0; index < requestCount; index++ {
		index := index
		go func() {
			defer waitGroup.Done()
			traceID := fmt.Sprintf("trace-inventory-%d", index)
			payload, marshalErr := json.Marshal(map[string]string{
				"planCode":         planCode,
				"email":            fmt.Sprintf("inventory-%d@example.com", index),
				"phoneCountryCode": "+86",
				"phoneNumber":      fmt.Sprintf("1390000%04d", index),
			})
			if marshalErr != nil {
				results <- storeInventoryHTTPResult{traceID: traceID, err: marshalErr}
				return
			}
			request, requestErr := http.NewRequest(http.MethodPost, httpServer.URL+"/api/v1/store/orders", bytes.NewReader(payload))
			if requestErr != nil {
				results <- storeInventoryHTTPResult{traceID: traceID, err: requestErr}
				return
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Trace-ID", traceID)
			response, requestErr := http.DefaultClient.Do(request)
			if requestErr != nil {
				results <- storeInventoryHTTPResult{traceID: traceID, err: requestErr}
				return
			}
			defer response.Body.Close()
			responseBody, readErr := io.ReadAll(response.Body)
			if readErr != nil {
				results <- storeInventoryHTTPResult{status: response.StatusCode, traceID: traceID, err: readErr}
				return
			}
			var decoded map[string]any
			if decodeErr := json.Unmarshal(responseBody, &decoded); decodeErr != nil {
				results <- storeInventoryHTTPResult{status: response.StatusCode, traceID: traceID, err: decodeErr}
				return
			}
			results <- storeInventoryHTTPResult{
				status:  response.StatusCode,
				traceID: response.Header.Get("X-Trace-ID"),
				body:    decoded,
			}
		}()
	}
	waitGroup.Wait()
	close(results)

	paid := 0
	soldOut := 0
	cdkCodes := map[string]bool{}
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent request failed: %v", result.err)
		}
		if result.traceID == "" {
			t.Fatal("concurrent response did not return X-Trace-ID")
		}
		switch result.status {
		case http.StatusCreated:
			paid++
			order, ok := result.body["order"].(map[string]any)
			if !ok || order["status"] != "paid" || order["planCode"] != planCode {
				t.Fatalf("successful order response = %#v", result.body)
			}
			cdkCode, ok := order["cdkCode"].(string)
			if !ok || strings.TrimSpace(cdkCode) == "" {
				t.Fatalf("successful order did not return a CDK: %#v", result.body)
			}
			if cdkCodes[cdkCode] {
				t.Fatalf("duplicate CDK returned under concurrency: %s", cdkCode)
			}
			cdkCodes[cdkCode] = true
		case http.StatusConflict:
			soldOut++
			if result.body["message"] != errStoreProductSoldOut.Error() {
				t.Fatalf("sold-out response = %#v", result.body)
			}
		default:
			t.Fatalf("concurrent request returned unexpected HTTP %d: %#v", result.status, result.body)
		}
	}

	if paid != plan.SaleLimit || soldOut != requestCount-plan.SaleLimit {
		t.Fatalf("concurrent inventory results paid=%d sold_out=%d, want paid=%d sold_out=%d", paid, soldOut, plan.SaleLimit, requestCount-plan.SaleLimit)
	}

	var storedPlan models.Plan
	if err := database.First(&storedPlan, "id = ?", plan.ID).Error; err != nil {
		t.Fatalf("reload inventory test plan: %v", err)
	}
	if storedPlan.SoldCount != plan.SaleLimit {
		t.Fatalf("stored sold_count=%d, want %d", storedPlan.SoldCount, plan.SaleLimit)
	}
	var orderCount int64
	if err := database.Model(&models.StoreOrder{}).Where("plan_id = ? AND status = ?", plan.ID, storeOrderPaid).Count(&orderCount).Error; err != nil {
		t.Fatalf("count paid inventory orders: %v", err)
	}
	if orderCount != int64(plan.SaleLimit) {
		t.Fatalf("paid order rows=%d, want %d", orderCount, plan.SaleLimit)
	}
	var cdkCount int64
	if err := database.Model(&models.CDK{}).Where("plan_id = ?", plan.ID).Count(&cdkCount).Error; err != nil {
		t.Fatalf("count generated inventory CDKs: %v", err)
	}
	if cdkCount != int64(plan.SaleLimit) {
		t.Fatalf("generated CDK rows=%d, want %d", cdkCount, plan.SaleLimit)
	}
}
