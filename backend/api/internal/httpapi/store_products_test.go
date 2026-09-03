package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
)

func TestStoreProductResponseAlwaysUsesPlatformCurrency(t *testing.T) {
	product := storeProductResponse(models.Plan{
		ID: "plan_1", Code: "plus", Name: "ChatGPT Plus", ProviderPlanName: "plus",
		Country: "US", Currency: "USD", Price: 20, Active: true, SortOrder: 10, SaleLimit: 5, SoldCount: 2,
	})

	if product["currency"] != models.PlatformStoreCurrency {
		t.Fatalf("store product currency = %v, want %s", product["currency"], models.PlatformStoreCurrency)
	}
	if product["deliveryMode"] != "generated_cdk" || product["published"] != true {
		t.Fatalf("store product delivery contract = %#v", product)
	}
	remaining, ok := product["remainingQuantity"].(*int)
	if !ok || remaining == nil || *remaining != 3 || product["saleLimit"] != 5 || product["soldCount"] != 2 || product["soldOut"] != false || product["availabilityLabel"] != "在售" {
		t.Fatalf("store product inventory projection = %#v", product)
	}
}

func TestPlanInventoryProjectionUsesZeroAsUnlimited(t *testing.T) {
	plan := models.Plan{Active: true, SaleLimit: 0, SoldCount: 99}
	decoratePlanInventory(&plan)
	if plan.RemainingQuantity != nil || plan.SoldOut || !plan.PurchaseEnabled || plan.AvailabilityLabel != "在售" {
		t.Fatalf("unlimited plan projection = %#v", plan)
	}

	plan = models.Plan{Active: true, SaleLimit: 3, SoldCount: 4}
	decoratePlanInventory(&plan)
	if plan.RemainingQuantity == nil || *plan.RemainingQuantity != 0 || !plan.SoldOut || plan.PurchaseEnabled || plan.AvailabilityLabel != "已售罄，补货中" {
		t.Fatalf("sold-out plan projection = %#v", plan)
	}

	plan = models.Plan{Active: false, SaleLimit: 3, SoldCount: 1}
	decoratePlanInventory(&plan)
	if plan.SoldOut || plan.PurchaseEnabled || plan.AvailabilityLabel != "已下架" {
		t.Fatalf("unpublished plan projection = %#v", plan)
	}
}

func TestBuildStoreOrderCDKUsesCanonicalPlanTypeForCustomProduct(t *testing.T) {
	order := models.StoreOrder{
		PlanID: "plan_custom",
		Plan: models.Plan{
			Code:             "custom-product-code-that-exceeds-cdk-plan-type-width",
			ProviderPlanName: "chatgptprolite",
		},
	}
	cdk := buildStoreOrderCDK(order, "cdk_custom", "KC-TEST-CODE", time.Now())
	if cdk.PlanType != "pro_5x" {
		t.Fatalf("store order CDK plan type = %q, want pro_5x", cdk.PlanType)
	}
	if len(cdk.PlanType) > 24 {
		t.Fatalf("store order CDK plan type exceeds database width: %q", cdk.PlanType)
	}
}

func TestConsumeStoreProductInventoryUsesConditionalAtomicUpdate(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "plans" SET "sold_count"=CASE WHEN sale_limit > 0 THEN sold_count + 1 ELSE sold_count END WHERE id = $1 AND (sale_limit <= 0 OR sold_count < sale_limit)`)).
		WithArgs("plan_limited").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := db.Transaction(func(tx *gorm.DB) error {
		return consumeStoreProductInventory(tx, "plan_limited")
	})
	if err != nil {
		t.Fatalf("consume inventory error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestConsumeStoreProductInventoryRejectsSoldOutAtomicUpdate(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "plans" SET "sold_count"=CASE WHEN sale_limit > 0 THEN sold_count + 1 ELSE sold_count END WHERE id = $1 AND (sale_limit <= 0 OR sold_count < sale_limit)`)).
		WithArgs("plan_sold_out").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := db.Transaction(func(tx *gorm.DB) error {
		return consumeStoreProductInventory(tx, "plan_sold_out")
	})
	if !errors.Is(err, errStoreProductSoldOut) {
		t.Fatalf("consume sold-out error = %v, want %v", err, errStoreProductSoldOut)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestUpsertStoreProductRejectsValuesOutsideLinkedDropdowns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{}
	router := gin.New()
	router.POST("/products", server.upsertStoreProduct)

	for name, input := range map[string]map[string]any{
		"provider plan": {"code": "plus", "name": "Plus", "providerPlanName": "custom-plan", "country": "US", "price": 20},
		"country":       {"code": "plus", "name": "Plus", "providerPlanName": "plus", "country": "CN", "price": 20},
	} {
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/products", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want %d; body=%s", name, recorder.Code, http.StatusBadRequest, recorder.Body.String())
		}
	}
}

func TestUpsertStoreProductUpdatesExistingPlanAndForcesCNY(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "plans" WHERE code = $1 ORDER BY "plans"."id" LIMIT $2`)).
		WithArgs("plus", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "code", "name", "description", "provider_plan_name", "country", "currency", "price", "active", "sort_order", "created_at", "updated_at"}).
			AddRow("plan_plus", "plus", "ChatGPT Plus", "old", "plus", "US", "USD", 20, true, 10, now, now))
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .*plans.*`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	gin.SetMode(gin.TestMode)
	server := &Server{DB: db}
	router := gin.New()
	router.POST("/products", server.upsertStoreProduct)
	body, err := json.Marshal(map[string]any{
		"code": "PLUS", "name": "ChatGPT Plus 新版", "description": "updated", "providerPlanName": "plus",
		"country": "us", "currency": "USD", "price": 28, "sortOrder": 15, "published": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/products", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Product map[string]any `json:"product"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Product["currency"] != models.PlatformStoreCurrency {
		t.Fatalf("updated product currency = %v, want %s", response.Product["currency"], models.PlatformStoreCurrency)
	}
	if response.Product["name"] != "ChatGPT Plus 新版" || response.Product["published"] != false {
		t.Fatalf("updated product fields = %#v", response.Product)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestUpsertStoreProductCreatesUnpublishedPlan(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "plans" WHERE code = $1 ORDER BY "plans"."id" LIMIT $2`)).
		WithArgs("draft", 1).
		WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO "plans"`)).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	gin.SetMode(gin.TestMode)
	server := &Server{DB: db}
	router := gin.New()
	router.POST("/products", server.upsertStoreProduct)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/products", bytes.NewReader([]byte(`{
		"code":"draft","name":"Draft product","providerPlanName":"plus",
		"country":"US","price":1,"sortOrder":10,"published":false
	}`)))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("create status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Product map[string]any `json:"product"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Product["published"] != false || response.Product["published_label"] != "已下架" {
		t.Fatalf("created unpublished product = %#v", response.Product)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestToggleStoreProductPublishedIsServerOwned(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "plans" WHERE code = $1 ORDER BY "plans"."id" LIMIT $2`)).
		WithArgs("plus", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "code", "name", "description", "provider_plan_name", "country", "currency", "price", "active", "sort_order", "created_at", "updated_at"}).
			AddRow("plan_plus", "plus", "ChatGPT Plus", "", "plus", "US", "CNY", 20, true, 10, now, now))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "plans"`)+`.*`).
		WithArgs(false, sqlmock.AnyArg(), "plan_plus").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	gin.SetMode(gin.TestMode)
	server := &Server{DB: db}
	router := gin.New()
	router.POST("/products/:code/toggle", server.toggleStoreProduct)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/products/plus/toggle", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("toggle status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Product map[string]any `json:"product"`
		Message string         `json:"message"`
		TraceID string         `json:"traceId"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Product["published"] != false || response.Product["published_label"] != "已下架" || response.Product["published_tone"] != "neutral" {
		t.Fatalf("server-owned published view = %#v", response.Product)
	}
	if response.Message != "商品 plus 已下架" {
		t.Fatalf("toggle message = %q", response.Message)
	}
	if response.TraceID == "" || recorder.Header().Get("X-Trace-ID") != response.TraceID {
		t.Fatalf("toggle trace id = body %q/header %q", response.TraceID, recorder.Header().Get("X-Trace-ID"))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
