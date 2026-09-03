package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
)

// DecodeJSONObjectResponse decodes a provider JSON object without exposing the
// raw response in an error. Provider adapters can then map their own response
// envelope without leaking external DTOs into the card-pool service.
func DecodeJSONObjectResponse(provider, operation string, response *http.Response) (map[string]any, error) {
	var payload map[string]any
	if _, err := DecodeJSONResponse(provider, operation, response, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, cardpool.NewProviderError(provider, operation, cardpool.CategoryTechnicalFailure, true, true, errors.New("provider response is not a JSON object"))
	}
	return payload, nil
}

// BusinessResponseError maps an application-level provider failure returned in
// an otherwise successful HTTP response. Only stable code/category metadata is
// retained in the error; provider messages can contain sensitive card data.
func BusinessResponseError(provider, operation, code string, message string) error {
	code = strings.TrimSpace(code)
	lower := strings.ToLower(strings.TrimSpace(message))
	category := cardpool.CategoryInvalidRequest
	retryable, failover := false, false
	switch {
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "temporar"), strings.Contains(lower, "unavailable"), strings.Contains(lower, "service busy"):
		category, retryable, failover = cardpool.CategoryProviderUnavailable, true, true
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "too many"):
		category, retryable = cardpool.CategoryRateLimit, true
	case strings.Contains(lower, "insufficient"), strings.Contains(lower, "balance"), strings.Contains(lower, "funds"):
		category = cardpool.CategoryInsufficientFunds
	case strings.Contains(lower, "declin"), strings.Contains(lower, "reject"):
		category = cardpool.CategoryBusinessDecline
	case strings.Contains(lower, "compliance"), strings.Contains(lower, "risk"), strings.Contains(lower, "kyc"):
		category = cardpool.CategoryComplianceBlock
	}
	if code == "401" || code == "403" {
		category = cardpool.CategoryInvalidRequest
		retryable, failover = false, false
	}
	stable := "provider returned an application error"
	if code != "" {
		stable = fmt.Sprintf("provider returned application error code %s", code)
	}
	return cardpool.NewProviderError(provider, operation, category, retryable, failover, errors.New(stable))
}

func ObjectField(value map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if nested, ok := value[key].(map[string]any); ok && nested != nil {
			return nested
		}
	}
	return nil
}

func ArrayField(value map[string]any, keys ...string) []any {
	for _, key := range keys {
		if items, ok := value[key].([]any); ok {
			return items
		}
	}
	return nil
}

func StringField(value map[string]any, keys ...string) string {
	for _, key := range keys {
		item, ok := value[key]
		if !ok || item == nil {
			continue
		}
		switch typed := item.(type) {
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				return text
			}
		case json.Number:
			if text := strings.TrimSpace(typed.String()); text != "" {
				return text
			}
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case float32:
			return strconv.FormatFloat(float64(typed), 'f', -1, 32)
		case int:
			return strconv.Itoa(typed)
		case int64:
			return strconv.FormatInt(typed, 10)
		}
	}
	return ""
}

func FloatField(value map[string]any, keys ...string) float64 {
	text := StringField(value, keys...)
	parsed, _ := strconv.ParseFloat(strings.TrimSpace(text), 64)
	return parsed
}

func IntField(value map[string]any, keys ...string) int {
	text := StringField(value, keys...)
	parsed, _ := strconv.Atoi(strings.TrimSpace(text))
	return parsed
}

func BoolField(value map[string]any, keys ...string) bool {
	text := strings.ToLower(StringField(value, keys...))
	return text == "1" || text == "true" || text == "yes" || text == "on"
}
