package chatgpt

import (
	"testing"
)

func TestBuildCheckoutPayloadSGPlus(t *testing.T) {
	payload := BuildCheckoutPayload("plus", "SG", "SGD", "")
	if payload.EntryPoint != "all_plans_pricing_modal" {
		t.Fatalf("entry_point=%s", payload.EntryPoint)
	}
	if payload.PlanName != "chatgptplusplan" {
		t.Fatalf("plan_name=%s", payload.PlanName)
	}
	if payload.BillingDetails["country"] != "SG" || payload.BillingDetails["currency"] != "SGD" {
		t.Fatalf("billing=%v", payload.BillingDetails)
	}
	if payload.CheckoutUIMode != "custom" {
		t.Fatalf("ui=%s", payload.CheckoutUIMode)
	}
}

func TestParseCheckoutAndPricing(t *testing.T) {
	checkout := ParseCheckout(200, []byte(`{"checkout_session_id":"cs_live_abc123","client_secret":"cs_live_abc123_secret_zzz","publishable_key":"pk_live_example"}`))
	if !checkout.OK || checkout.SessionID != "cs_live_abc123" || checkout.PublishableKey != "pk_live_example" {
		t.Fatalf("%+v", checkout)
	}
	if checkout.CheckoutURL == "" {
		t.Fatal("missing checkout url")
	}
	oaic := ParseCheckout(200, []byte(`{"checkout_session_id":"oaics_0983c9a0b4d54e848367ea3c49af462d","checkout_state":{"client_secret":"cs_live_abc_secret_zzz","publishable_key":"pk_live_nested"}}`))
	if !oaic.OK || oaic.SessionID != "oaics_0983c9a0b4d54e848367ea3c49af462d" || oaic.ClientSecret != "cs_live_abc_secret_zzz" || oaic.PublishableKey != "pk_live_nested" {
		t.Fatalf("oaic parse %+v", oaic)
	}
	if ErrorCode("Our systems have detected unusual activity") != "openai_auth_error" {
		t.Fatal(ErrorCode("unusual activity"))
	}
	account := ParseAccount([]byte(`{"accounts":{"default":{"entitlement":{"has_active_subscription":true,"subscription_plan":"chatgptplusplan"}}}}`))
	if !account.HasActive || account.Plan != "chatgptplusplan" {
		t.Fatalf("%+v", account)
	}
	pricing := ParsePricing([]byte(`{"country_code":"SG","currency_config":{"symbol_code":"SGD","symbol":"S$","tax_percent":9,"plus":{"month":{"amount":30,"psp_override":{"amount":27.52}}}}}`))
	if !pricing.OK || pricing.PlusInclusive != 30 || pricing.PlusExclusive != 27.52 {
		t.Fatalf("%+v", pricing)
	}
}

func TestErrorCode(t *testing.T) {
	if ErrorCode("not_eligible") != "not_eligible" {
		t.Fatal(ErrorCode("not_eligible"))
	}
}

func TestConfirmPathsAndUnusualActivity(t *testing.T) {
	if !UnusualActivity("Our systems have detected unusual activity. Please try again later.") {
		t.Fatal("unusual activity")
	}
	paths := ConfirmPaths("oaics_0983c9a0b4d54e848367ea3c49af462d")
	if len(paths) < 4 {
		t.Fatalf("paths=%v", paths)
	}
	body := ConfirmBody("oaics_abc", "ctoken_1", "openai_llc")
	if body["type"] != "confirmation_token" || body["confirmToken"] != "ctoken_1" {
		t.Fatalf("%v", body)
	}
	hosted := WithoutCustomUI(BuildCheckoutPayload("plus", "SG", "SGD", ""))
	if hosted.CheckoutUIMode != "" {
		t.Fatalf("hosted ui=%s", hosted.CheckoutUIMode)
	}
}

func TestParseCheckoutFromCheckoutURL(t *testing.T) {
	result := ParseCheckout(200, []byte(`{}`))
	MergeCaptured("https://chatgpt.com/checkout/openai_llc/oaics_0983c9a0b4d54e848367ea3c49af462d", &result)
	if result.SessionID != "oaics_0983c9a0b4d54e848367ea3c49af462d" {
		t.Fatalf("%+v", result)
	}
}
