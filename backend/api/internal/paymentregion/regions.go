package paymentregion

import "strings"

// Region is the country/currency contract shared by storefront products,
// checkout debug tasks, and the worker entrypoint. Currency is the target
// ChatGPT billing currency; it is intentionally separate from the platform's
// CNY storefront settlement currency.
type Region struct {
	Code         string
	Currency     string
	Label        string
	CountryLabel string
	Locale       string
	Timezone     string
}

// The list follows OpenAI's published multi-currency billing currencies. For
// currencies shared by multiple countries, the country code is the canonical
// billing-region representative used by the existing checkout flow.
var supported = []Region{
	{Code: "AE", Currency: "AED", Label: "阿联酋（AE）— AED", CountryLabel: "阿联酋", Locale: "en-AE", Timezone: "Asia/Dubai"},
	{Code: "AR", Currency: "ARS", Label: "阿根廷（AR）— ARS", CountryLabel: "阿根廷", Locale: "es-AR", Timezone: "America/Argentina/Buenos_Aires"},
	{Code: "AU", Currency: "AUD", Label: "澳大利亚（AU）— AUD", CountryLabel: "澳大利亚", Locale: "en-AU", Timezone: "Australia/Sydney"},
	{Code: "BR", Currency: "BRL", Label: "巴西（BR）— BRL", CountryLabel: "巴西", Locale: "pt-BR", Timezone: "America/Sao_Paulo"},
	{Code: "CA", Currency: "CAD", Label: "加拿大（CA）— CAD", CountryLabel: "加拿大", Locale: "en-CA", Timezone: "America/Toronto"},
	{Code: "CH", Currency: "CHF", Label: "瑞士（CH）— CHF", CountryLabel: "瑞士", Locale: "de-CH", Timezone: "Europe/Zurich"},
	{Code: "CL", Currency: "CLP", Label: "智利（CL）— CLP", CountryLabel: "智利", Locale: "es-CL", Timezone: "America/Santiago"},
	{Code: "CO", Currency: "COP", Label: "哥伦比亚（CO）— COP", CountryLabel: "哥伦比亚", Locale: "es-CO", Timezone: "America/Bogota"},
	{Code: "CZ", Currency: "CZK", Label: "捷克（CZ）— CZK", CountryLabel: "捷克", Locale: "cs-CZ", Timezone: "Europe/Prague"},
	{Code: "DE", Currency: "EUR", Label: "德国（DE）— EUR", CountryLabel: "德国", Locale: "de-DE", Timezone: "Europe/Berlin"},
	{Code: "DK", Currency: "DKK", Label: "丹麦（DK）— DKK", CountryLabel: "丹麦", Locale: "da-DK", Timezone: "Europe/Copenhagen"},
	{Code: "GB", Currency: "GBP", Label: "英国（GB）— GBP", CountryLabel: "英国", Locale: "en-GB", Timezone: "Europe/London"},
	{Code: "HK", Currency: "HKD", Label: "中国香港（HK）— HKD", CountryLabel: "中国香港", Locale: "zh-HK", Timezone: "Asia/Hong_Kong"},
	{Code: "HU", Currency: "HUF", Label: "匈牙利（HU）— HUF", CountryLabel: "匈牙利", Locale: "hu-HU", Timezone: "Europe/Budapest"},
	{Code: "ID", Currency: "IDR", Label: "印度尼西亚（ID）— IDR", CountryLabel: "印度尼西亚", Locale: "id-ID", Timezone: "Asia/Jakarta"},
	{Code: "IL", Currency: "ILS", Label: "以色列（IL）— ILS", CountryLabel: "以色列", Locale: "he-IL", Timezone: "Asia/Jerusalem"},
	{Code: "IN", Currency: "INR", Label: "印度（IN）— INR", CountryLabel: "印度", Locale: "en-IN", Timezone: "Asia/Kolkata"},
	{Code: "JP", Currency: "JPY", Label: "日本（JP）— JPY", CountryLabel: "日本", Locale: "ja-JP", Timezone: "Asia/Tokyo"},
	{Code: "KR", Currency: "KRW", Label: "韩国（KR）— KRW", CountryLabel: "韩国", Locale: "ko-KR", Timezone: "Asia/Seoul"},
	{Code: "MX", Currency: "MXN", Label: "墨西哥（MX）— MXN", CountryLabel: "墨西哥", Locale: "es-MX", Timezone: "America/Mexico_City"},
	{Code: "MY", Currency: "MYR", Label: "马来西亚（MY）— MYR", CountryLabel: "马来西亚", Locale: "en-MY", Timezone: "Asia/Kuala_Lumpur"},
	{Code: "NG", Currency: "NGN", Label: "尼日利亚（NG）— NGN", CountryLabel: "尼日利亚", Locale: "en-NG", Timezone: "Africa/Lagos"},
	{Code: "NO", Currency: "NOK", Label: "挪威（NO）— NOK", CountryLabel: "挪威", Locale: "nb-NO", Timezone: "Europe/Oslo"},
	{Code: "NZ", Currency: "NZD", Label: "新西兰（NZ）— NZD", CountryLabel: "新西兰", Locale: "en-NZ", Timezone: "Pacific/Auckland"},
	{Code: "PE", Currency: "PEN", Label: "秘鲁（PE）— PEN", CountryLabel: "秘鲁", Locale: "es-PE", Timezone: "America/Lima"},
	{Code: "PH", Currency: "PHP", Label: "菲律宾（PH）— PHP", CountryLabel: "菲律宾", Locale: "en-PH", Timezone: "Asia/Manila"},
	{Code: "PL", Currency: "PLN", Label: "波兰（PL）— PLN", CountryLabel: "波兰", Locale: "pl-PL", Timezone: "Europe/Warsaw"},
	{Code: "QA", Currency: "QAR", Label: "卡塔尔（QA）— QAR", CountryLabel: "卡塔尔", Locale: "en-QA", Timezone: "Asia/Qatar"},
	{Code: "RO", Currency: "RON", Label: "罗马尼亚（RO）— RON", CountryLabel: "罗马尼亚", Locale: "ro-RO", Timezone: "Europe/Bucharest"},
	{Code: "SA", Currency: "SAR", Label: "沙特阿拉伯（SA）— SAR", CountryLabel: "沙特阿拉伯", Locale: "en-SA", Timezone: "Asia/Riyadh"},
	{Code: "SE", Currency: "SEK", Label: "瑞典（SE）— SEK", CountryLabel: "瑞典", Locale: "sv-SE", Timezone: "Europe/Stockholm"},
	{Code: "SG", Currency: "SGD", Label: "新加坡（SG）— SGD", CountryLabel: "新加坡", Locale: "en-SG", Timezone: "Asia/Singapore"},
	{Code: "TH", Currency: "THB", Label: "泰国（TH）— THB", CountryLabel: "泰国", Locale: "th-TH", Timezone: "Asia/Bangkok"},
	{Code: "TR", Currency: "TRY", Label: "土耳其（TR）— TRY", CountryLabel: "土耳其", Locale: "tr-TR", Timezone: "Europe/Istanbul"},
	{Code: "TW", Currency: "TWD", Label: "中国台湾（TW）— TWD", CountryLabel: "中国台湾", Locale: "zh-TW", Timezone: "Asia/Taipei"},
	{Code: "UA", Currency: "UAH", Label: "乌克兰（UA）— UAH", CountryLabel: "乌克兰", Locale: "uk-UA", Timezone: "Europe/Kyiv"},
	{Code: "US", Currency: "USD", Label: "美国（US）— USD", CountryLabel: "美国", Locale: "en-US", Timezone: "America/New_York"},
	{Code: "VN", Currency: "VND", Label: "越南（VN）— VND", CountryLabel: "越南", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh"},
	{Code: "ZA", Currency: "ZAR", Label: "南非（ZA）— ZAR", CountryLabel: "南非", Locale: "en-ZA", Timezone: "Africa/Johannesburg"},
}

var byCode = func() map[string]Region {
	result := make(map[string]Region, len(supported))
	for _, region := range supported {
		result[region.Code] = region
	}
	return result
}()

func Normalize(code string) string {
	value := strings.ToUpper(strings.TrimSpace(code))
	if _, ok := byCode[value]; ok {
		return value
	}
	return ""
}

func IsSupported(code string) bool { return Normalize(code) != "" }

func Get(code string) (Region, bool) {
	region, ok := byCode[Normalize(code)]
	return region, ok
}

func Default() Region {
	return byCode["PH"]
}

func All() []Region {
	result := make([]Region, len(supported))
	copy(result, supported)
	return result
}
