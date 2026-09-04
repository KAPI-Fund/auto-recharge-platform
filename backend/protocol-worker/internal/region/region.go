package region

import "strings"

type Config struct {
	Currency string
	Label    string
	Locale   string
	Timezone string
}

var table = map[string]Config{
	"IN": {Currency: "INR", Label: "印度", Locale: "en-IN", Timezone: "Asia/Kolkata"},
	"PH": {Currency: "PHP", Label: "菲律宾", Locale: "en-PH", Timezone: "Asia/Manila"},
	"US": {Currency: "USD", Label: "美国", Locale: "en-US", Timezone: "America/New_York"},
	"SG": {Currency: "SGD", Label: "新加坡", Locale: "en-SG", Timezone: "Asia/Singapore"},
	"MY": {Currency: "MYR", Label: "马来西亚", Locale: "en-MY", Timezone: "Asia/Kuala_Lumpur"},
}

var planNames = map[string]string{
	"plus":            "chatgptplusplan",
	"chatgptplusplan": "chatgptplusplan",
	"pro_5x":          "chatgptprolite",
	"pro5x":           "chatgptprolite",
	"chatgptprolite":  "chatgptprolite",
	"pro_20x":         "chatgptpro",
	"pro20x":          "chatgptpro",
	"chatgptpro":      "chatgptpro",
	"go":              "chatgptgoplan",
	"chatgpt_go":      "chatgptgoplan",
	"chatgptgoplan":   "chatgptgoplan",
}

func Get(code string) Config {
	cfg, ok := table[strings.ToUpper(strings.TrimSpace(code))]
	if !ok {
		return table["SG"]
	}
	return cfg
}

func PlanName(planType string) string {
	if name, ok := planNames[strings.ToLower(strings.TrimSpace(planType))]; ok {
		return name
	}
	return planNames["plus"]
}

// NormalizePlanType keeps the worker contract bounded to the business plan
// keys while accepting provider plan names and historical aliases at the
// boundary. Unknown values retain the legacy Plus fallback.
func NormalizePlanType(planType string) string {
	switch strings.ToLower(strings.TrimSpace(planType)) {
	case "go", "chatgpt_go", "chatgptgoplan":
		return "go"
	case "pro_5x", "pro5x", "chatgptprolite":
		return "pro_5x"
	case "pro_20x", "pro20x", "chatgptpro":
		return "pro_20x"
	case "plus", "chatgptplusplan":
		return "plus"
	default:
		return "plus"
	}
}

// NormalizePlanName applies the same compatibility rule to an optional
// override. Custom provider names are preserved, while known aliases resolve
// to the canonical value expected by the Checkout API.
func NormalizePlanName(planType, override string) string {
	if value := strings.TrimSpace(override); value != "" {
		if name, ok := planNames[strings.ToLower(value)]; ok {
			return name
		}
		return value
	}
	return PlanName(NormalizePlanType(planType))
}
