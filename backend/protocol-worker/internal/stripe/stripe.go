package stripe

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

func EncodeForm(values map[string]any) string {
	out := url.Values{}
	writeForm("", values, out)
	return out.Encode()
}

func writeForm(prefix string, values map[string]any, out url.Values) {
	for key, value := range values {
		if value == nil {
			continue
		}
		name := key
		if prefix != "" {
			name = prefix + "[" + key + "]"
		}
		switch typed := value.(type) {
		case map[string]any:
			writeForm(name, typed, out)
		case string:
			if typed != "" {
				out.Set(name, typed)
			}
		default:
			text := strings.TrimSpace(fmt.Sprint(typed))
			if text != "" && text != "<nil>" {
				out.Set(name, text)
			}
		}
	}
}

func ParseExpiry(expiry string) (month, year string) {
	raw := strings.ReplaceAll(strings.TrimSpace(expiry), " ", "")
	switch {
	case regexp.MustCompile(`^\d{2}/\d{2}$`).MatchString(raw):
		return raw[:2], "20" + raw[3:]
	case regexp.MustCompile(`^\d{2}/\d{4}$`).MatchString(raw):
		return raw[:2], raw[3:]
	case regexp.MustCompile(`^\d{4}$`).MatchString(raw):
		return raw[:2], "20" + raw[2:]
	case regexp.MustCompile(`^\d{6}$`).MatchString(raw):
		return raw[:2], raw[2:]
	default:
		return "", ""
	}
}

func SessionID(secret string) string {
	return regexp.MustCompile(`cs_(?:live|test)_[A-Za-z0-9]+`).FindString(secret)
}

func Paid(status string, httpStatus int, hasError bool) bool {
	if hasError || httpStatus >= 300 || httpStatus <= 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "paid", "complete", "completed", "processing":
		return true
	default:
		return false
	}
}

type Classified struct {
	Declined     bool
	AuthRequired bool
	Code         string
	Message      string
}

func Classify(message, code, decline string, status int) Classified {
	blob := strings.ToLower(code + " " + decline + " " + message)
	out := Classified{Code: code, Message: message}
	if out.Message == "" {
		out.Message = fmt.Sprintf("HTTP %d", status)
	}
	out.Declined = strings.Contains(blob, "declined") || strings.Contains(blob, "do_not_honor") || strings.Contains(blob, "insufficient_funds") || strings.Contains(blob, "expired_card") || strings.Contains(blob, "incorrect_cvc")
	out.AuthRequired = strings.Contains(blob, "requires_action") || strings.Contains(blob, "3ds") || strings.Contains(blob, "authentication")
	if out.Declined && out.Code == "" {
		out.Code = "card_declined"
	}
	return out
}

func PaymentMethodFields(number, expiry, cvc, name, line1, city, state, postal, country string) map[string]any {
	month, year := ParseExpiry(expiry)
	return map[string]any{
		"type": "card",
		"card": map[string]any{
			"number":    strings.ReplaceAll(number, " ", ""),
			"exp_month": month,
			"exp_year":  year,
			"cvc":       strings.TrimSpace(cvc),
		},
		"billing_details": map[string]any{
			"name": name,
			"address": map[string]any{
				"line1":       line1,
				"city":        city,
				"state":       state,
				"postal_code": postal,
				"country":     country,
			},
		},
	}
}
