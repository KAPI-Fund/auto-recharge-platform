package chatgpt

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/kc-catk/auto-recharge-platform/backend/protocol-worker/internal/region"
)

type CheckoutPayload struct {
	EntryPoint     string         `json:"entry_point"`
	PlanName       string         `json:"plan_name"`
	BillingDetails map[string]any `json:"billing_details"`
	CheckoutUIMode string         `json:"checkout_ui_mode,omitempty"`
}

func BuildCheckoutPayload(planType, country, currency, planName string) CheckoutPayload {
	planName = region.NormalizePlanName(planType, planName)
	return CheckoutPayload{
		EntryPoint:     "all_plans_pricing_modal",
		PlanName:       planName,
		BillingDetails: map[string]any{"country": strings.ToUpper(country), "currency": strings.ToUpper(currency)},
		CheckoutUIMode: "custom",
	}
}

type CheckoutResult struct {
	OK              bool
	Status          int
	SessionID       string
	StripeSessionID string
	ClientSecret    string
	PublishableKey  string
	CheckoutURL     string
	Processor       string
	Error           string
	Raw             map[string]any
}

var (
	csRe   = regexp.MustCompile(`cs_(?:live|test)_[A-Za-z0-9]+`)
	oaicRe = regexp.MustCompile(`oaics_[a-fA-F0-9]+`)
	pkRe   = regexp.MustCompile(`pk_(?:live|test)_[A-Za-z0-9]+`)
	secRe  = regexp.MustCompile(`cs_(?:live|test)_[A-Za-z0-9]+_secret_[A-Za-z0-9]+`)
)

func ParseCheckout(status int, body []byte) CheckoutResult {
	raw := map[string]any{}
	_ = json.Unmarshal(body, &raw)
	text := string(body)
	result := CheckoutResult{Status: status, Raw: raw, Processor: "openai_llc"}
	extractCheckout(raw, &result)
	MergeCaptured(text, &result)
	if result.CheckoutURL == "" && result.SessionID != "" {
		result.CheckoutURL = "https://chatgpt.com/checkout/openai_llc/" + result.SessionID
	}
	if url, _ := raw["url"].(string); strings.HasPrefix(url, "http") {
		result.CheckoutURL = url
	}
	if status != 200 || result.SessionID == "" {
		result.Error = first(raw, "detail", "message", "error")
		if result.Error == "" {
			result.Error = fmt.Sprintf("HTTP %d", status)
		}
		return result
	}
	result.OK = true
	return result
}

func MergeCaptured(text string, result *CheckoutResult) {
	if result.ClientSecret == "" {
		result.ClientSecret = secRe.FindString(text)
	}
	if result.PublishableKey == "" {
		result.PublishableKey = pkRe.FindString(text)
	}
	if result.StripeSessionID == "" {
		result.StripeSessionID = csRe.FindString(text)
	}
	if result.SessionID == "" {
		if id := oaicRe.FindString(text); id != "" {
			result.SessionID = id
		} else if result.StripeSessionID != "" {
			result.SessionID = result.StripeSessionID
		}
	}
	if result.StripeSessionID == "" && strings.HasPrefix(result.ClientSecret, "cs_") {
		result.StripeSessionID = csRe.FindString(result.ClientSecret)
	}
}

func extractCheckout(value any, result *CheckoutResult) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if text, ok := item.(string); ok {
				assignCheckoutField(key, text, result)
				continue
			}
			extractCheckout(item, result)
		}
	case []any:
		for _, item := range typed {
			extractCheckout(item, result)
		}
	}
}

func assignCheckoutField(key, value string, result *CheckoutResult) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	lower := strings.ToLower(key)
	switch {
	case strings.Contains(lower, "client_secret"):
		if result.ClientSecret == "" {
			result.ClientSecret = value
		}
	case strings.Contains(lower, "publishable") || strings.Contains(lower, "publisher_key"):
		if strings.HasPrefix(value, "pk_") {
			result.PublishableKey = value
		}
	case lower == "checkout_session_id" || lower == "session_id" || lower == "id":
		if result.SessionID == "" || strings.HasPrefix(value, "oaics_") {
			result.SessionID = value
		}
	case strings.Contains(lower, "processor"):
		result.Processor = value
	}
	if strings.HasPrefix(value, "oaics_") {
		result.SessionID = value
	}
	if strings.HasPrefix(value, "cs_live_") || strings.HasPrefix(value, "cs_test_") {
		if strings.Contains(value, "_secret_") && result.ClientSecret == "" {
			result.ClientSecret = value
		}
		if result.StripeSessionID == "" {
			result.StripeSessionID = csRe.FindString(value)
		}
	}
	if strings.HasPrefix(value, "pk_") && result.PublishableKey == "" {
		result.PublishableKey = value
	}
	if strings.HasPrefix(value, "http") && strings.Contains(value, "checkout") && result.CheckoutURL == "" {
		result.CheckoutURL = value
	}
}

type Pricing struct {
	OK            bool
	Country       string
	Currency      string
	Symbol        string
	PlusInclusive float64
	PlusExclusive float64
	TaxPercent    float64
}

func ParsePricing(body []byte) Pricing {
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil {
		return Pricing{}
	}
	cfg, _ := raw["currency_config"].(map[string]any)
	plus, _ := cfg["plus"].(map[string]any)
	month, _ := plus["month"].(map[string]any)
	override, _ := month["psp_override"].(map[string]any)
	return Pricing{
		OK:            true,
		Country:       asString(raw["country_code"]),
		Currency:      asString(cfg["symbol_code"]),
		Symbol:        asString(cfg["symbol"]),
		PlusInclusive: asFloat(month["amount"]),
		PlusExclusive: asFloat(override["amount"]),
		TaxPercent:    asFloat(cfg["tax_percent"]),
	}
}

type Account struct {
	HasActive bool
	Plan      string
}

func ParseAccount(body []byte) Account {
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil {
		return Account{}
	}
	accounts, _ := raw["accounts"].(map[string]any)
	def, _ := accounts["default"].(map[string]any)
	ent, _ := def["entitlement"].(map[string]any)
	plan, _ := ent["subscription_plan"].(string)
	active, _ := ent["has_active_subscription"].(bool)
	return Account{HasActive: active, Plan: plan}
}

func WithoutCustomUI(payload CheckoutPayload) CheckoutPayload {
	payload.CheckoutUIMode = ""
	return payload
}

func UnusualActivity(errText string) bool {
	return strings.Contains(strings.ToLower(errText), "unusual activity")
}

func ConfirmPaths(sessionID string) []string {
	paths := []string{
		"/backend-api/payments/checkout/confirm",
		"/backend-api/payments/confirm_checkout",
		"/backend-api/payments/checkout_session/confirm",
	}
	if strings.TrimSpace(sessionID) != "" {
		paths = append(paths,
			"/backend-api/payments/checkout/"+sessionID+"/confirm",
			"/backend-api/payments/checkout_session/"+sessionID+"/confirm",
		)
	}
	return paths
}

func ConfirmBody(sessionID, confirmToken, processor string) map[string]any {
	return map[string]any{
		"checkout_session_id": sessionID,
		"type":                "confirmation_token",
		"confirmation_token":  confirmToken,
		"confirm_token":       confirmToken,
		"confirmToken":        confirmToken,
		"processor_entity":    firstNonEmpty(processor, "openai_llc"),
	}
}

func ErrorCode(errText string) string {
	text := strings.ToLower(errText)
	switch {
	case strings.Contains(text, "unusual activity"):
		return "openai_auth_error"
	case strings.Contains(text, "not_eligible"), strings.Contains(text, "offer not found"), strings.Contains(text, "already_subscribed"):
		return "not_eligible"
	case strings.Contains(text, "session"), strings.Contains(text, "unauthorized"), strings.Contains(text, "401"):
		return "session_invalid"
	default:
		return "checkout_create_failed"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func first(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return strings.TrimSpace(typed)
				}
			}
		}
	}
	return ""
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}

func asFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		n, _ := typed.Float64()
		return n
	default:
		return 0
	}
}
