package flow

import (
	"testing"

	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/config"
)

func TestShouldPauseCheckoutDebug(t *testing.T) {
	opts := Options{Config: config.Config{SubmitPayment: true}, Secret: map[string]any{"cdkCode": "[checkout-debug]"}}
	if !ShouldPause(opts) {
		t.Fatal("checkout-debug must pause before Stripe confirm")
	}
	prod := Options{Config: config.Config{SubmitPayment: true}, Secret: map[string]any{"cdkCode": "KC-REAL"}}
	if ShouldPause(prod) {
		t.Fatal("production cdk must not pause")
	}
	pause := Options{Config: config.Config{PauseBeforeSubmit: true, SubmitPayment: false}, Secret: map[string]any{"cdkCode": "KC-REAL"}}
	if !ShouldPause(pause) {
		t.Fatal("simulate checkout must pause")
	}
}

func TestUnusualActivityRotatesProxy(t *testing.T) {
	result := retryOrFail("无法创建官方 Checkout 订单: Our systems have detected unusual activity", "openai_auth_error")
	if result.Status != "retry" {
		t.Fatalf("status=%s", result.Status)
	}
	if result.ErrorCode != "openai_auth_error" {
		t.Fatalf("code=%s", result.ErrorCode)
	}
}
