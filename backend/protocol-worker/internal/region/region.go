package region

import "strings"

type Config struct {
	Currency string
	Label    string
	Locale   string
	Timezone string
}

var table = map[string]Config{
	"PH": {Currency: "PHP", Label: "菲律宾", Locale: "en-PH", Timezone: "Asia/Manila"},
	"US": {Currency: "USD", Label: "美国", Locale: "en-US", Timezone: "America/New_York"},
	"SG": {Currency: "SGD", Label: "新加坡", Locale: "en-SG", Timezone: "Asia/Singapore"},
	"MY": {Currency: "MYR", Label: "马来西亚", Locale: "en-MY", Timezone: "Asia/Kuala_Lumpur"},
}

var planNames = map[string]string{
	"plus":    "chatgptplusplan",
	"pro_5x":  "chatgptprolite",
	"pro_20x": "chatgptpro",
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
