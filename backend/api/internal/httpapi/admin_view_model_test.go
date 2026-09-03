package httpapi

import (
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestNormalizeAddressInputCanonicalizesLegacyValues(t *testing.T) {
	input := map[string]any{
		"region": " us ", "country": " us ", "state": " or ",
		"line1": " 123 Main St ", "city": " Portland ", "postal_code": " 97201 ",
	}
	normalized := normalizeAddressInput(input)
	checks := map[string]string{
		"region": "US", "country": "US", "state": "Oregon",
		"line1": "123 Main St", "city": "Portland", "postal_code": "97201",
	}
	for key, want := range checks {
		if got := stringValue(normalized, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestAddressValidationFailureContainsReadableDetails(t *testing.T) {
	response := addressValidationFailure([]string{"line1 不能为空", "country 必须是恰好 2 位大写字母 (ISO 3166-1 alpha-2)"})
	message, _ := response["message"].(string)
	if message != "字段校验失败：line1 不能为空；country 必须是恰好 2 位大写字母 (ISO 3166-1 alpha-2)" {
		t.Fatalf("validation message = %q", message)
	}
	if response["error"] != message {
		t.Fatalf("error and message diverged: %#v", response)
	}
}

func TestLegacyCardImportNormalizesMMYYAndValidation(t *testing.T) {
	items := parseLegacyCardImport("4111111111111111|0431|123|Jane Doe\n# ignored\n")
	if len(items) != 1 || items[0].Number != "4111111111111111" || items[0].CVC != "123" || items[0].Holder != "Jane Doe" {
		t.Fatalf("parsed card = %#v", items)
	}
	if got := normalizeLegacyCardExpiry(items[0].Expiry); got != "04/31" {
		t.Fatalf("expiry = %q, want 04/31", got)
	}
	if errors := legacyCardValidationErrors(items[0].Number, normalizeLegacyCardExpiry(items[0].Expiry), items[0].CVC); len(errors) != 0 {
		t.Fatalf("valid card rejected: %v", errors)
	}
	if errors := legacyCardValidationErrors("4111x", "13/31", "12"); len(errors) != 3 {
		t.Fatalf("invalid card errors = %v, want three validation errors", errors)
	}
}

func TestCardProxyAndAddressSummariesAreServerDerived(t *testing.T) {
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	cooldownUntil := now.Add(time.Hour)
	cards := []models.CardAsset{
		{ID: "available", Active: true, Status: "正常"},
		{ID: "cooldown", Active: true, Status: "正常", CooldownUntil: &cooldownUntil},
		{ID: "exhausted", Active: false, Status: "disabled"},
		{ID: "in-use", Active: true, Status: "正常", InUse: true},
	}
	cardStats := cardPoolStats(cards, now)
	if cardStats["total"] != 4 || cardStats["active"] != 1 || cardStats["cooldown"] != 1 || cardStats["exhausted"] != 1 {
		t.Fatalf("card stats = %#v", cardStats)
	}

	proxyStats := proxySummary([]models.ProxyAsset{{Active: true}, {Active: false}, {Active: true}})
	if proxyStats["total"] != 3 || proxyStats["active"] != 2 || proxyStats["inactive"] != 1 {
		t.Fatalf("proxy stats = %#v", proxyStats)
	}

	addressStats := addressSummary([]models.TaxFreeAddress{{BoundCardID: "card_1"}, {}, {BoundCardID: "  "}})
	if addressStats["total"] != 3 || addressStats["bound"] != 1 || addressStats["unbound"] != 2 {
		t.Fatalf("address stats = %#v", addressStats)
	}
}

func TestBrowserPoolViewModelMirrorsWorkerStats(t *testing.T) {
	viewModel := browserPoolViewModel(map[string]any{
		"enabled": true, "initialized": true, "configuredSize": 4, "maxPoolSize": 24,
		"size": 2, "idle": 1, "busy": 1, "waiting": 3, "totalUses": 7,
		"slots": []any{
			map[string]any{"slotId": 0, "profileSizeBytes": float64(1024 * 1024)},
			map[string]any{"slotId": 1, "profileSizeBytes": float64(2 * 1024 * 1024)},
		},
		"queue":  []any{map[string]any{"jobKey": "job_1"}, map[string]any{"jobKey": "job_2"}},
		"memory": map[string]any{"hostTotalGb": 16.0, "hostFreeGb": 8.5, "sizingHint": "worker hint"},
	}, true, map[string]any{"cpu": map[string]any{"percent": 12}}, 5)

	view, ok := viewModel["view"].(gin.H)
	if !ok {
		t.Fatalf("view type = %T", viewModel["view"])
	}
	if view["statusLabel"] != "运行中" || view["ready"] != true || view["configuredSize"] != 4 || view["queueCount"] != 2 {
		t.Fatalf("browser view = %#v", view)
	}
	if view["profileSizeText"] != "3.0 MB" || view["estimatedProcessText"] != "~840 MB" || view["hostUsedGb"] != 7.5 || view["activeForegroundJobs"] != int64(5) {
		t.Fatalf("browser derived metrics = %#v", view)
	}

	independent := browserPoolViewModel(map[string]any{"enabled": false, "initialized": false}, false, nil, 0)
	independentView := independent["view"].(gin.H)
	if independentView["statusLabel"] != "独立模式" || independentView["modeLabel"] != "独立启动 · 子进程 BROWSER_RUNTIME_MODE=standalone" {
		t.Fatalf("independent browser view = %#v", independentView)
	}
	pool, ok := viewModel["pool"].(map[string]any)
	if !ok {
		t.Fatalf("browser pool type = %T", viewModel["pool"])
	}
	slots, ok := pool["slots"].([]any)
	if !ok || len(slots) != 2 {
		t.Fatalf("browser slots = %#v", pool["slots"])
	}
	firstSlot := slots[0].(map[string]any)
	if firstSlot["statusLabel"] != "空闲" || firstSlot["statusClass"] != "is-idle" || firstSlot["pageSummary"] != "页面数: 0" {
		t.Fatalf("idle slot view = %#v", firstSlot)
	}
}

func TestPoolAssetResponsesOwnStatusLabelsAndTones(t *testing.T) {
	password := poolEmailResponse(models.PoolEmail{ID: "mail_1", Email: "a@example.com", Password: "secret", Registered: true, InUse: true})
	if password["password_label"] != "已配置" || password["password_tone"] != "success" || password["registration_label"] != "已注册" || password["usage_tone"] != "warning" {
		t.Fatalf("pool email view = %#v", password)
	}

	phone := phoneResponse(models.PhoneAsset{ID: "phone_1", Phone: "8613800000000", Status: "封禁", Active: false, UsageCount: 7})
	if phone["status_label"] != "封禁" || phone["status_tone"] != "danger" || phone["active_label"] != "停用" || phone["usage_text"] != "7" {
		t.Fatalf("phone view = %#v", phone)
	}
	options, ok := phone["status_options"].([]gin.H)
	if !ok || len(options) != 3 {
		t.Fatalf("phone status options = %#v", phone["status_options"])
	}

	product := productResponse(models.ProductAsset{ID: "product_1", Email: "a@example.com", Status: "正常", Shipped: true})
	if product["status_label"] != "正常" || product["status_tone"] != "success" || product["shipped_label"] != "是" || product["shipped_tone"] != "success" {
		t.Fatalf("product view = %#v", product)
	}
}

func TestProxyAndAddressResponsesOwnOperationalPresentation(t *testing.T) {
	proxy := proxyResponse(models.ProxyAsset{ID: "proxy_1", ProxyURL: "http://127.0.0.1:8080"})
	if proxy["check_label"] != "未检测" || proxy["check_tone"] != "neutral" || proxy["ip_text"] != "—" || proxy["latency_text"] != "—" {
		t.Fatalf("untested proxy view = %#v", proxy)
	}
	ok := true
	proxy = proxyResponse(models.ProxyAsset{ID: "proxy_2", LastCheckOK: &ok, LastCheckIP: "203.0.113.5", LastCheckLatency: 42})
	if proxy["check_label"] != "活跃" || proxy["check_tone"] != "success" || proxy["ip_text"] != "203.0.113.5" || proxy["latency_text"] != "42ms" {
		t.Fatalf("active proxy view = %#v", proxy)
	}

	address := addressResponse(models.TaxFreeAddress{ID: "address_1", BoundCardID: "card_1"})
	if address["status_label"] != "已绑定" || address["status_tone"] != "success" || address["can_edit"] != false {
		t.Fatalf("bound address view = %#v", address)
	}
}

func TestRenewalViewOwnsSessionActionPermissions(t *testing.T) {
	enabled := renewalView(map[string]any{"hasActiveSubscription": true, "autoRenewRaw": true})
	if enabled["canCancel"] != true || enabled["canEnable"] != false || enabled["label"] != "已开启" || enabled["tone"] != "success" {
		t.Fatalf("enabled renewal view = %#v", enabled)
	}
	disabled := renewalView(map[string]any{"hasActiveSubscription": true, "autoRenewRaw": false})
	if disabled["canCancel"] != false || disabled["canEnable"] != true || disabled["label"] != "已关闭" || disabled["tone"] != "neutral" {
		t.Fatalf("disabled renewal view = %#v", disabled)
	}
	none := renewalView(map[string]any{"hasActiveSubscription": false})
	if none["canCancel"] != false || none["canEnable"] != false || none["label"] != "无订阅" {
		t.Fatalf("empty renewal view = %#v", none)
	}
}

func TestRenewalResultViewIsRenderReady(t *testing.T) {
	view := renewalResultView(map[string]any{
		"email":                "user@example.com",
		"subscriptionChannel":  "Stripe",
		"renewalStatus":        "enabled",
		"renewalStatusLabel":   "已开启",
		"renewalStatusTone":    "success",
		"expiresAtDisplay":     "2026-09-26 12:00:00",
		"remainingDaysDisplay": "31 天",
	}, "订阅状态查询完成")

	checks := map[string]any{
		"message":            "订阅状态查询完成",
		"email":              "user@example.com",
		"subscription_label": "Stripe",
		"status":             "enabled",
		"status_label":       "已开启",
		"status_tone":        "success",
		"expires_at":         "2026-09-26 12:00:00",
		"remaining_days":     "31 天",
	}
	for key, want := range checks {
		if view[key] != want {
			t.Errorf("%s = %#v, want %#v", key, view[key], want)
		}
	}
}

func TestAdminConfigViewProvidesTypedFlagsWithoutDroppingLegacyValues(t *testing.T) {
	view := adminConfigView(map[string]string{
		"maintenance_mode":        "1",
		"maintenance_mode_drain":  "0",
		"browser_pool_enabled":    "true",
		"pool_email_enabled":      "0",
		"pool_email_include_junk": "1",
		"mode":                    "browser",
	}, true, true, false)

	if view["mode"] != "browser" || view["maintenance_mode"] != "1" {
		t.Fatalf("legacy config values were dropped: %#v", view)
	}
	checks := map[string]bool{
		"maintenanceModeEnabled":        true,
		"maintenanceModeDrainEnabled":   false,
		"browserPoolEnabled":            true,
		"poolEmailEnabled":              false,
		"poolEmailIncludeJunk":          true,
		"storeDebugModeEnabled":         true,
		"stripeSecretKeySavedValue":     true,
		"stripeWebhookSecretSavedValue": false,
	}
	for key, want := range checks {
		if got, ok := view[key].(bool); !ok || got != want {
			t.Errorf("%s = %#v (%T), want %v", key, view[key], view[key], want)
		}
	}
}

func TestAutomationViewIncludesRenderReadyStates(t *testing.T) {
	automation, phase := legacyAutomationSummary("订单创建成功\nCheckout 页面已打开\n已预留卡片\n卡号已填写")
	if phase != "Checkout 已打开" || automation["checkout_label"] != "已打开" || automation["checkout_class"] != "ok" {
		t.Fatalf("automation view = %#v", automation)
	}
	stages := automation["stages"].([]gin.H)
	if stages[0]["state"] != "done" || stages[1]["state"] != "done" || stages[5]["state"] != "pending" {
		t.Fatalf("automation stage states = %#v", stages)
	}
	lines := taskInfoLines("银行卡已预留", phase, true)
	if len(lines) != 3 || lines[1]["class_name"] != "task-info-phase" || lines[2]["class_name"] != "task-info-checkout" {
		t.Fatalf("task info lines = %#v", lines)
	}
}

func TestProductGenerationResponseOwnsTerminalAndProgressView(t *testing.T) {
	task := models.ProductGenerationTask{ID: "task_1", JobKey: "product-job-1", Status: models.ProductGenerationRunning, Progress: 37}
	view := productGenerationResponse(task)
	if view["status_label"] != "RUNNING" || view["status_tone"] != "info" || view["terminal"] != false || view["stop_enabled"] != true || view["progress_text"] != "37%" {
		t.Fatalf("running generation view = %#v", view)
	}
	task.Status = models.ProductGenerationSucceeded
	task.Progress = 101
	view = productGenerationResponse(task)
	if view["status"] != "success" || view["terminal"] != true || view["stop_enabled"] != false || view["progress_text"] != "100%" {
		t.Fatalf("finished generation view = %#v", view)
	}
}

func TestToggleProductStatusKeepsTransitionInGo(t *testing.T) {
	if got := toggleProductStatus("正常"); got != "封禁" {
		t.Fatalf("normal product status = %q, want 封禁", got)
	}
	if got := toggleProductStatus("封禁"); got != "正常" {
		t.Fatalf("banned product status = %q, want 正常", got)
	}
}
