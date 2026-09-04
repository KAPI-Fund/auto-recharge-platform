package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestValidateAddressFieldsMatchesLegacyRules(t *testing.T) {
	valid := map[string]any{
		"region": "us", "line1": "123 Main St", "city": "Portland",
		"state": "Oregon", "postal_code": "97201", "country": "US",
	}
	if errors := validateAddressFields(valid, true); len(errors) != 0 {
		t.Fatalf("valid address rejected: %v", errors)
	}

	invalid := map[string]any{
		"region": "US", "line1": "", "city": "Portland", "state": "Oregon",
		"postal_code": "97201", "country": "us",
	}
	errors := validateAddressFields(invalid, true)
	if len(errors) != 2 {
		t.Fatalf("expected empty line1 and lowercase country errors, got %v", errors)
	}

	tooLong := map[string]any{
		"region": "US", "line1": string(make([]byte, 201)), "city": "Portland",
		"state": "Oregon", "postal_code": "97201", "country": "US",
	}
	if errors := validateAddressFields(tooLong, true); len(errors) != 1 || errors[0] != "line1 长度不能超过 200 字符" {
		t.Fatalf("unexpected length validation: %v", errors)
	}
}

func TestAddressResponseIncludesBindingTime(t *testing.T) {
	boundAt := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	row := models.TaxFreeAddress{ID: "address_1", Region: "US", Line1: "123 Main St", City: "Portland", State: "Oregon", PostalCode: "97201", Country: "US", Active: true, BoundCardID: "card_1", BoundAt: &boundAt}
	response := addressResponse(row)
	if response["is_bound"] != true || response["can_edit"] != false || response["can_delete"] != false || response["bound_card_id"] != "card_1" || response["bound_at"] != &boundAt {
		t.Fatalf("binding fields were not preserved: %#v", response)
	}
}

func TestCheckoutDebugModeForcesBrowser(t *testing.T) {
	if got := checkoutDebugMode("protocol", "protocol"); got != "browser" {
		t.Fatalf("protocol override = %s, want browser", got)
	}
	if got := checkoutDebugMode("", "protocol"); got != "browser" {
		t.Fatalf("protocol fallback = %s, want browser", got)
	}
	if got := checkoutDebugMode("dry_run", "protocol"); got != "browser" {
		t.Fatalf("unsupported debug mode = %s, want browser", got)
	}
}

func TestTaskLogResponseUsesLegacySuccessStatus(t *testing.T) {
	row := models.RechargeTask{JobKey: "job_1", Status: models.TaskSucceeded, Mode: "browser", Progress: 100}
	if got := taskLogResponse(row)["status"]; got != "success" {
		t.Fatalf("status = %v, want success", got)
	}

	row.Status = models.TaskFailed
	if got := taskLogResponse(row)["status"]; got != models.TaskFailed {
		t.Fatalf("failed status = %v, want failed", got)
	}
}

func TestLegacyLoginLogTimeMatchesReferenceISOString(t *testing.T) {
	value := time.Date(2026, time.August, 25, 7, 41, 46, 123456789, time.FixedZone("local", 8*60*60))
	if got := legacyISOTimeString(value); got != "2026-08-24T23:41:46.123Z" {
		t.Fatalf("login log time = %q, want 2026-08-24T23:41:46.123Z", got)
	}
	if got := legacyISOTimeString(time.Time{}); got != "" {
		t.Fatalf("zero login log time = %q, want empty string", got)
	}
}

func TestTaskLogResponsePreservesAutomationStageContract(t *testing.T) {
	raw := "订单创建成功\nCheckout 页面已打开\n信用卡卡池支付流程\n已预留卡片\n无法定位信用卡号\nmanual_intervention"
	row := models.RechargeTask{
		ID: "task_1", JobKey: "job_1", Status: models.TaskManual, Progress: 92,
		Message: "银行卡被拒绝或 Stripe 驳回", RawOutput: raw,
	}
	response := taskLogResponse(row)
	if response["id"] != "job_1" || response["job_key"] != "job_1" || response["jobKey"] != "job_1" {
		t.Fatalf("legacy task identifiers were not preserved: %#v", response)
	}
	automation, ok := response["automation"].(gin.H)
	if !ok {
		t.Fatalf("automation response has type %T, want gin.H", response["automation"])
	}
	if automation["phase"] != "需人工介入" || automation["checkoutOpened"] != true {
		t.Fatalf("automation summary = %#v", automation)
	}
	stages, ok := automation["stages"].([]gin.H)
	if !ok || len(stages) != 6 {
		t.Fatalf("automation stages = %#v", automation["stages"])
	}
	want := map[string]struct {
		done   bool
		failed bool
	}{
		"order":       {done: true},
		"checkout":    {done: true},
		"payment":     {done: true},
		"card":        {done: true},
		"stripe_form": {failed: true},
		"paid":        {},
	}
	for _, stage := range stages {
		key, _ := stage["key"].(string)
		expected, exists := want[key]
		if !exists {
			t.Fatalf("unexpected automation stage: %#v", stage)
		}
		if stage["done"] != expected.done || stage["failed"] != expected.failed {
			t.Fatalf("stage %q = %#v, want done=%t failed=%t", key, stage, expected.done, expected.failed)
		}
	}
}

func TestMediaPathsNormalizeAndDeduplicateWorkerPaths(t *testing.T) {
	raw := "FAILURE_SCREENSHOT: /srv/app/runtime/legacy/task-parity/sample.png\nVIDEO_FILE: /srv/app/runtime/legacy/task-parity/sample.webm"
	stored := `["legacy/task-parity/sample.png","legacy/task-parity/sample.webm"]`

	screenshots := mediaPaths(raw, stored, "screenshot")
	if len(screenshots) != 1 || screenshots[0] != "legacy/task-parity/sample.png" {
		t.Fatalf("screenshots = %#v, want one canonical runtime-relative path", screenshots)
	}
	videos := mediaPaths(raw, stored, "video")
	if len(videos) != 1 || videos[0] != "legacy/task-parity/sample.webm" {
		t.Fatalf("videos = %#v, want one canonical runtime-relative path", videos)
	}
}

func TestNormalizePathAndCheckoutURLCompatibility(t *testing.T) {
	if got := normalizePath("  /My-Admin/ ", "/admin"); got != "/my-admin" {
		t.Fatalf("normalized path = %q, want /my-admin", got)
	}
	if got := extractCheckoutURL("log\nCHECKOUT_URL: https://pay.example/checkout\n"); got != "https://pay.example/checkout" {
		t.Fatalf("checkout URL = %q", got)
	}
	if got := extractCheckoutURL("no checkout"); got != "" {
		t.Fatalf("unexpected checkout URL = %q", got)
	}
}

func TestTOTPURIMatchesReferenceContract(t *testing.T) {
	got := legacyTOTPURI("admin@example.com", " abcd efgh ")
	want := "otpauth://totp/PlusPapay%3Aadmin%40example.com?secret=ABCDEFGH&issuer=PlusPapay&algorithm=SHA1&digits=6&period=30"
	if got != want {
		t.Fatalf("TOTP URI = %q, want %q", got, want)
	}
}

func TestClampProgress(t *testing.T) {
	for input, want := range map[int]int{-1: 0, 0: 0, 50: 50, 101: 100} {
		if got := clampProgress(input); got != want {
			t.Errorf("clampProgress(%d) = %d, want %d", input, got, want)
		}
	}
}

func TestLegacyCDKListDefaultsMatchReference(t *testing.T) {
	if got := nullableLegacyString(""); got != nil {
		t.Fatalf("empty legacy string = %#v, want nil", got)
	}
	if got := nullableLegacyString("KC-TEST"); got != "KC-TEST" {
		t.Fatalf("non-empty legacy string = %#v, want KC-TEST", got)
	}
	if got := boolToInt(true); got != 1 {
		t.Fatalf("true is_active = %d, want 1", got)
	}
	if got := boolToInt(false); got != 0 {
		t.Fatalf("false is_active = %d, want 0", got)
	}
	got := normalizeLegacyCDKs([]string{" KC-1 ", "", "KC-1", "KC-2"})
	if len(got) != 2 || got[0] != "KC-1" || got[1] != "KC-2" {
		t.Fatalf("normalized CDKs = %#v, want [KC-1 KC-2]", got)
	}
}

func TestLegacyCDKPlanAliasesAndLabels(t *testing.T) {
	if got := normalizePlanType("chatgptgoplan"); got != "go" {
		t.Fatalf("normalized legacy Go plan = %q, want go", got)
	}
	if got := legacyPlanLabel("chatgptgoplan"); got != "ChatGPT Go" {
		t.Fatalf("legacy Go label = %q, want ChatGPT Go", got)
	}
	row := models.CDK{PlanType: "", Plan: models.Plan{Code: "chatgptgoplan"}}
	if got := legacyCDKPlanType(row); got != "go" {
		t.Fatalf("CDK plan fallback = %q, want go", got)
	}
}

func TestSecondarySessionMatchesLegacyUnauthenticatedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	server := &Server{Cfg: config.Config{AdminTokenSecret: "test-secret"}}
	router.GET("/secondary-session", server.secondarySession)

	request := httptest.NewRequest(http.MethodGet, "/secondary-session", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 2 || body["success"] != true || body["verified"] != false {
		t.Fatalf("unexpected response: %#v", body)
	}
}
