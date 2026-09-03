package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestTraceMiddlewarePreservesAndGeneratesTraceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(traceMiddleware)
	router.GET("/trace", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"traceId": requestTraceID(c)})
	})

	request := httptest.NewRequest(http.MethodGet, "/trace", nil)
	request.Header.Set("X-Trace-ID", "trace_reference_123")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("X-Trace-ID") != "trace_reference_123" {
		t.Fatalf("trace header was not preserved: status=%d header=%q", recorder.Code, recorder.Header().Get("X-Trace-ID"))
	}

	request = httptest.NewRequest(http.MethodGet, "/trace", nil)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	generated := recorder.Header().Get("X-Trace-ID")
	if recorder.Code != http.StatusOK || len(generated) < len("trace_") || generated[:len("trace_")] != "trace_" {
		t.Fatalf("trace header was not generated: status=%d header=%q", recorder.Code, generated)
	}
}

func TestCORSPreflightAllowsTraceIDHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{Cfg: config.Config{CORSOrigins: []string{"http://web.test"}}}
	router := NewRouter(server)
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/plans", nil)
	request.Header.Set("Origin", "http://web.test")
	request.Header.Set("Access-Control-Request-Headers", "content-type, x-trace-id")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("CORS preflight status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if !strings.Contains(strings.ToLower(recorder.Header().Get("Access-Control-Allow-Headers")), "x-trace-id") {
		t.Fatalf("CORS headers = %q, want x-trace-id", recorder.Header().Get("Access-Control-Allow-Headers"))
	}
}

func TestStoreInputNormalizationMatchesPublicFormRules(t *testing.T) {
	email, err := normalizeStoreEmail(" Customer@Example.COM ")
	if err != nil || email != "customer@example.com" {
		t.Fatalf("normalized email = %q, err=%v", email, err)
	}
	for _, value := range []string{"", "bad", "a@@example.com"} {
		if _, err := normalizeStoreEmail(value); err == nil {
			t.Errorf("invalid email accepted: %q", value)
		}
	}

	country, number, e164, err := normalizeStorePhone("86", "138 0013-8000")
	if err != nil || country != "+86" || number != "13800138000" || e164 != "+8613800138000" {
		t.Fatalf("normalized phone = %q %q %q, err=%v", country, number, e164, err)
	}
	for _, value := range []struct{ country, number string }{
		{"", "13800138000"},
		{"+86", "123"},
		{"+9999", "12345678"},
	} {
		if _, _, _, err := normalizeStorePhone(value.country, value.number); err == nil {
			t.Errorf("invalid phone accepted: %#v", value)
		}
	}
}

func TestAggregateStoreOrderLookupMatchesOrderEmailAndPhoneTogether(t *testing.T) {
	lookup, args, err := aggregateStoreOrderLookup(" Customer@Example.COM ")
	if err != nil {
		t.Fatalf("aggregate email lookup failed: %v", err)
	}
	if lookup != "(order_no = ? OR email = ?)" {
		t.Fatalf("aggregate email lookup = %q", lookup)
	}
	if len(args) != 2 || args[0] != "Customer@Example.COM" || args[1] != "customer@example.com" {
		t.Fatalf("aggregate email args = %#v", args)
	}

	lookup, args, err = aggregateStoreOrderLookup("+86 138-0013-8000")
	if err != nil {
		t.Fatalf("aggregate phone lookup failed: %v", err)
	}
	if lookup != "(order_no = ? OR phone_number = ? OR phone_e164 = ?)" {
		t.Fatalf("aggregate phone lookup = %q", lookup)
	}
	if len(args) != 3 || args[1] != "8613800138000" || args[2] != "+8613800138000" {
		t.Fatalf("aggregate phone args = %#v", args)
	}
}

func TestAggregateStoreOrderLookupRejectsInvalidEmail(t *testing.T) {
	if _, _, err := aggregateStoreOrderLookup("customer@"); err == nil {
		t.Fatal("invalid aggregate email was accepted")
	}
}

func TestPublicStoreOrderResponseMasksContactFieldsAndHidesInternals(t *testing.T) {
	order := models.StoreOrder{
		ID: "order_1", OrderNo: "ORD-1", TraceID: "trace_1", Status: storeOrderPaid,
		Email: "customer@example.com", PhoneCountryCode: "+86", PhoneNumber: "13800138000",
		CDKCode: "KC-SECRET", Amount: 20, Currency: "usd", StripeSessionID: "cs_private", FailureReason: "card_declined",
		Plan: models.Plan{Code: "plus", Name: "ChatGPT Plus"},
	}
	response := publicStoreOrderResponse(order)
	if response["email"] != "c******r@example.com" {
		t.Fatalf("email mask = %v", response["email"])
	}
	if response["phoneNumber"] != "+86 *******8000" {
		t.Fatalf("phone mask = %v", response["phoneNumber"])
	}
	if response["cdkCode"] != "KC-SECRET" {
		t.Fatalf("customer CDK was not retained: %#v", response)
	}
	for _, privateKey := range []string{"traceId", "stripeSessionId", "failureReason", "updatedAt"} {
		if _, ok := response[privateKey]; ok {
			t.Fatalf("public order response contains private field %q: %#v", privateKey, response)
		}
	}
}

func TestPaidStoreOrderCDKIsAvailableSelfServiceAndShipped(t *testing.T) {
	shippedAt := time.Date(2026, time.August, 23, 10, 30, 0, 0, time.UTC)
	order := models.StoreOrder{
		ID: "order_1", PlanID: "plan_plus",
		Plan: models.Plan{Code: "plus", Name: "ChatGPT Plus"},
	}

	cdk := buildStoreOrderCDK(order, "cdk_1", "KC-ABCD-EFGH-JKLM", shippedAt)
	if cdk.ID != "cdk_1" || cdk.Code != "KC-ABCD-EFGH-JKLM" {
		t.Fatalf("CDK identity = %#v", cdk)
	}
	if cdk.PlanID != order.PlanID || cdk.PlanType != order.Plan.Code {
		t.Fatalf("CDK plan mapping = %#v, want plan id/type %q/%q", cdk, order.PlanID, order.Plan.Code)
	}
	if cdk.Type != models.CDKTypeSelf || cdk.Status != models.CDKAvailable {
		t.Fatalf("CDK delivery state = %#v, want self-service/available", cdk)
	}
	if cdk.ShippedAt == nil || !cdk.ShippedAt.Equal(shippedAt) {
		t.Fatalf("CDK shipped timestamp = %v, want %v", cdk.ShippedAt, shippedAt)
	}
}

func TestStripeSignatureAcceptsFreshPayloadAndRejectsTampering(t *testing.T) {
	secret := "whsec_test"
	payload := []byte(`{"type":"checkout.session.completed"}`)
	timestamp := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d.", timestamp)))
	_, _ = mac.Write(payload)
	header := fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
	if !validStripeSignature(payload, header, secret) {
		t.Fatal("fresh Stripe signature was rejected")
	}
	if validStripeSignature([]byte(`{"type":"tampered"}`), header, secret) {
		t.Fatal("tampered Stripe payload was accepted")
	}
	if validStripeSignature(payload, fmt.Sprintf("t=%d,v1=%s", timestamp-301, hex.EncodeToString(mac.Sum(nil))), secret) {
		t.Fatal("expired Stripe signature was accepted")
	}
}

func TestTaskResponseKeepsLegacyStatusAndTraceFields(t *testing.T) {
	task := models.RechargeTask{
		ID: "task_1", JobKey: "job_1", TraceID: "trace_1", Status: models.TaskFailed,
		Progress: 65, Message: "支付失败", Mode: "browser", CDKCode: "KC-1",
		Plan: models.Plan{Code: "plus", Name: "ChatGPT Plus", Price: 20, Currency: "USD"},
		CDK:  models.CDK{Code: "KC-1", Status: models.CDKUsed},
	}
	response := taskResponse(task)
	if response["status"] != models.TaskFailed || response["traceId"] != "trace_1" || response["progress"] != 65 {
		t.Fatalf("task response lost compatibility fields: %#v", response)
	}
	plan := response["plan"].(gin.H)
	if plan["code"] != "plus" || plan["name"] != "ChatGPT Plus" {
		t.Fatalf("task plan response = %#v", plan)
	}
}

func TestTaskResponseSupportsCheckoutDebugWithoutCDK(t *testing.T) {
	task := models.RechargeTask{
		ID: "task_debug", JobKey: "job_debug", TraceID: "trace_debug", Mode: "browser",
		Status: models.TaskQueued, CDKCode: "[checkout-debug]", Plan: models.Plan{Code: "plus", Name: "ChatGPT Plus"},
	}
	response := taskResponse(task)
	cdk := response["cdk"].(gin.H)
	plan := response["plan"].(gin.H)
	if cdk["code"] != "[checkout-debug]" {
		t.Fatalf("debug task CDK display = %#v", cdk)
	}
	if plan["code"] != "plus" || response["traceId"] != "trace_debug" {
		t.Fatalf("debug task response = %#v", response)
	}
}

func TestPublicTaskResponseHidesInternalExecutionDetails(t *testing.T) {
	task := models.RechargeTask{
		ID: "task_public", TraceID: "trace_private", Status: models.TaskFailed, Progress: 99,
		Message:   "[Stripe] Step 2: 填写信用卡字段（iframe 自动识别）",
		ErrorCode: "manual_intervention", ErrorMessage: "card declined", CardLast4: "4242",
		RawOutput: "worker raw output", SessionPreview: "session-preview",
	}
	response := publicTaskResponse(task)
	allowed := map[string]bool{"id": true, "status": true, "progress": true, "message": true}
	for key := range response {
		if !allowed[key] {
			t.Fatalf("public task response contains private field %q: %#v", key, response)
		}
	}
	if response["message"] != "内部错误，请联系客服" {
		t.Fatalf("public failure message = %#v", response["message"])
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal public task response: %v", err)
	}
	for _, privateValue := range []string{"trace_private", "manual_intervention", "card declined", "4242", "worker raw output", "session-preview", "Stripe"} {
		if strings.Contains(string(encoded), privateValue) {
			t.Fatalf("public task response leaked %q: %s", privateValue, encoded)
		}
	}

	for _, test := range []struct {
		status  string
		message string
	}{
		{models.TaskQueued, "任务已提交，请耐心等待"},
		{models.TaskRunning, "正在处理中，请耐心等待"},
		{models.TaskSucceeded, "开通成功"},
		{models.TaskManual, "内部错误，请联系客服"},
	} {
		if got := publicTaskMessage(test.status); got != test.message {
			t.Errorf("public task message for %q = %q, want %q", test.status, got, test.message)
		}
	}
}

func TestPublicFailUsesCustomerSafeMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/public-error", func(c *gin.Context) {
		publicFail(c, http.StatusInternalServerError, "database password or Stripe response")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/public-error", nil))
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("public error is not JSON: %v", err)
	}
	if response["message"] != "内部错误，请联系客服" {
		t.Fatalf("public error message = %#v", response["message"])
	}
	if _, ok := response["traceId"]; ok {
		t.Fatalf("public error exposed traceId: %s", recorder.Body.String())
	}
	if _, ok := response["trace_id"]; ok {
		t.Fatalf("public error exposed trace_id: %s", recorder.Body.String())
	}
	if recorder.Header().Get("X-Trace-ID") == "" {
		t.Fatal("public error did not preserve trace header for internal support")
	}
	if strings.Contains(recorder.Body.String(), "database password") || strings.Contains(recorder.Body.String(), "Stripe response") {
		t.Fatalf("public error leaked internal details: %s", recorder.Body.String())
	}
}

func TestAdminSessionRejectsMissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	server := &Server{Cfg: config.Config{AdminAPIToken: "test-token", AdminTokenSecret: "test-secret"}}
	router.GET("/private", server.requireAdminSession, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/private", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing admin token status = %d", recorder.Code)
	}
}

func TestAdminSessionAcceptsLegacyAdminTokenHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	server := &Server{Cfg: config.Config{AdminAPIToken: "test-token", AdminTokenSecret: "test-secret"}}
	router.GET("/private", server.requireAdminSession, func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodGet, "/private", nil)
	request.Header.Set("X-Admin-Token", "test-token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("legacy admin token status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestUnauthorizedAdminRouteReturnsSingleJSONResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{Cfg: config.Config{AdminAPIToken: "test-token", AdminTokenSecret: "test-secret"}}
	router := gin.New()
	router.Use(traceMiddleware)
	router.GET("/private", server.requireAdmin, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"unexpected": true})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/private", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	decoder := json.NewDecoder(recorder.Body)
	var response map[string]any
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("unauthorized response is not JSON: %v; body=%q", err, recorder.Body.String())
	}
	if response["message"] != "需要管理员授权" || response["success"] != false {
		t.Fatalf("unauthorized response = %#v", response)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("unauthorized response contains trailing JSON: err=%v extra=%#v body=%q", err, extra, recorder.Body.String())
	}
}

func TestLegacyGPTStatusIncludesTraceOnMissingConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(traceMiddleware)
	router.GET("/gpt-status", (&Server{}).legacyGPTStatus)

	request := httptest.NewRequest(http.MethodGet, "/gpt-status", nil)
	request.Header.Set("X-Trace-ID", "trace-gpt-missing")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing GPT config status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("missing GPT config response is not JSON: %v", err)
	}
	if response["traceId"] != "trace-gpt-missing" || recorder.Header().Get("X-Trace-ID") != "trace-gpt-missing" {
		t.Fatalf("missing GPT config trace = %#v header=%q", response["traceId"], recorder.Header().Get("X-Trace-ID"))
	}
}
