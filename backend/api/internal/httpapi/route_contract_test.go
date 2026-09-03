package httpapi

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
)

// These are the HTTP contracts consumed by the original KC-PAY-GPT pages.
// Keep this list independent from the Go registration code so a route can be
// removed accidentally without the test changing with it.
func TestLegacyRouteSurfaceMatchesReference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(&Server{Cfg: config.Config{AdminTokenSecret: "test-secret"}})
	routes := make(map[string]bool)
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}

	expected := []string{
		"GET /api/public/admin-paths",
		"GET /api/public/payment-region",
		"GET /api/public/runtime",
		"POST /api/public/subscription/check",
		"POST /api/admin/login",
		"POST /api/admin/login/send-tg-code",
		"POST /api/admin/login/verify-2fa",
		"GET /api/admin/session",
		"GET /api/admin/security/status",
		"GET /api/admin/login-logs",
		"GET /api/admin/secondary/session",
		"POST /api/admin/verify-secondary",
		"POST /api/admin/security/2fa-mode",
		"POST /api/admin/security/paths",
		"POST /api/admin/2fa/setup",
		"POST /api/admin/2fa/confirm",
		"POST /api/admin/2fa/disable",
		"POST /api/admin/change-password",
		"POST /api/admin/change-secondary-password",
		"GET /api/admin/runtime-logs",
		"POST /api/admin/runtime-logs/clear",
		"GET /api/admin/task-logs",
		"DELETE /api/admin/task-logs/:jobKey",
		"GET /api/admin/data",
		"GET /api/admin/config",
		"POST /api/admin/config",
		"GET /api/admin/card-providers/kimoox/bins",
		"POST /api/admin/email/test",
		"GET /api/admin/region",
		"PUT /api/admin/region",
		"GET /api/admin/plans",
		"POST /api/admin/trigger-activation",
		"GET /api/admin/browser-pool",
		"POST /api/admin/browser-pool/mode",
		"POST /api/admin/browser-pool/reload",
		"GET /api/admin/checkout/plans",
		"POST /api/admin/checkout/generate",
		"GET /api/admin/checkout/status/:jobKey",
		"GET /api/admin/screenshots",
		"GET /api/admin/video",
		"GET /api/admin/screenshots/:subdir/:filename",
		"POST /api/admin/telegram",
		"POST /api/admin/telegram/test",
		"GET /api/admin/hcaptcha",
		"POST /api/admin/hcaptcha",
		"POST /api/admin/hcaptcha/test",
		"POST /api/admin/hcaptcha/test-vlm",
		"POST /api/admin/hcaptcha/test-captcha-platform",
		"GET /api/admin/hcaptcha/logs",
		"GET /api/admin/gpt-api",
		"POST /api/admin/gpt-api",
		"POST /api/admin/gpt-api/test",
		"GET /api/admin/gpt-api/status",
		"GET /api/admin/proxies",
		"POST /api/admin/proxies",
		"PUT /api/admin/proxies/:id",
		"POST /api/admin/proxies/:id/toggle",
		"POST /api/admin/proxies/:id/test",
		"POST /api/admin/proxies/test-all",
		"DELETE /api/admin/proxies/:id",
		"GET /api/admin/addresses",
		"POST /api/admin/addresses",
		"POST /api/admin/addresses/generate-random-us",
		"DELETE /api/admin/addresses/unbound",
		"PUT /api/admin/addresses/:id",
		"DELETE /api/admin/addresses/:id",
		"GET /api/admin/billing",
		"GET /api/admin/billing/export",
		"GET /api/admin/billing/summary/:cardLast4",
		"DELETE /api/admin/billing/failed",
		"DELETE /api/admin/billing/:id",
		"GET /api/admin/cdks",
		"POST /api/admin/cdks/generate",
		"POST /api/admin/cdks/import",
		"POST /api/admin/cdks/batch/ship",
		"POST /api/admin/cdks/batch/delete",
		"POST /api/admin/cdks/:cdk/ship",
		"DELETE /api/admin/cdks/:cdk",
		"GET /api/admin/cards",
		"POST /api/admin/cards/import",
		"POST /api/admin/cards/create",
		"DELETE /api/admin/cards/:id",
		"GET /api/admin/card-pools",
		"GET /api/admin/sessions",
		"GET /api/admin/sessions/:jobKey",
		"GET /api/admin/sessions/:jobKey/export",
		"POST /api/admin/sessions/:jobKey/renewal/:action",
		"GET /api/admin/pool-emails",
		"POST /api/admin/pool-emails/import",
		"GET /api/admin/pool-emails/:id/messages",
		"DELETE /api/admin/pool-emails/:id",
		"GET /api/admin/products",
		"POST /api/admin/products/import",
		"PUT /api/admin/products/:id/status",
		"DELETE /api/admin/products/:id",
		"GET /api/admin/products/:id/export",
		"POST /api/admin/products/export",
		"POST /api/admin/products/claim",
		"POST /api/external/cards/push",
		"POST /api/verify-cdk",
		"GET /api/cdk/query",
		"GET /api/cdk/download",
		"POST /api/redeem-product",
		"POST /api/run-process",
	}

	for _, route := range expected {
		if !routes[route] {
			t.Errorf("reference route is missing: %s", route)
		}
	}
	if len(expected) != 104 {
		t.Fatalf("route contract test itself is incomplete: listed %d routes", len(expected))
	}

	// The new platform API is part of the public Next.js purchase/recharge flow.
	for _, route := range []string{
		"GET /healthz",
		"GET /api/v1/plans",
		"GET /api/v1/runtime",
		"GET /api/v1/admin/store/products",
		"POST /api/v1/admin/store/products",
		"PATCH /api/v1/admin/store/products/:code",
		"POST /api/v1/admin/store/products/:code/toggle",
		"POST /api/v1/store/orders",
		"GET /api/v1/store/orders/:id",
		"POST /api/v1/store/orders/query",
		"POST /api/v1/store/webhooks/stripe",
		"POST /api/v1/webhooks/cards/airwallex",
		"POST /api/v1/webhooks/cards/stripe",
		"POST /api/v1/webhooks/cards/photonpay",
		"POST /api/v1/webhooks/cards/dogpay",
		"POST /api/v1/webhooks/cards/kimoox",
		"POST /api/v1/admin/email/test",
		"GET /api/v1/admin/card-providers/kimoox/bins",
		"POST /api/v1/recharge/verify",
		"POST /api/v1/recharge/tasks",
		"GET /api/v1/recharge/tasks/:id",
	} {
		if !routes[route] {
			t.Errorf("new platform route is missing: %s", route)
		}
	}

}
