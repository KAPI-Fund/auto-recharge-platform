package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
)

const maxResponseBody = 2 << 20

var sensitiveBodyKey = regexp.MustCompile(`(?i)(card_number|number|cvc|cvv|api_key|secret|token|authorization)`)
var sensitiveAssignment = regexp.MustCompile(`(?i)((?:card[_-]?number|pan|number|cvc|cvv|security[_-]?code|api[_-]?key|secret|token|authorization)\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`)
var cardNumberValue = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)

// DecodeJSONResponse consumes and closes the provider response. Error text is
// deliberately bounded and redacted because provider error payloads may echo
// request fields.
func DecodeJSONResponse(provider, operation string, response *http.Response, target any) ([]byte, error) {
	if response == nil {
		return nil, cardpool.NewProviderError(provider, operation, cardpool.CategoryTechnicalFailure, true, true, errors.New("empty provider response"))
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBody))
	_ = response.Body.Close()
	if readErr != nil {
		return body, RequestError(provider, operation, readErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return body, HTTPError(provider, operation, response.StatusCode, body)
	}
	if target == nil || len(strings.TrimSpace(string(body))) == 0 {
		return body, nil
	}
	if err := json.Unmarshal(body, target); err != nil {
		return body, cardpool.NewProviderError(provider, operation, cardpool.CategoryTechnicalFailure, true, true, fmt.Errorf("decode provider response: %w", err))
	}
	return body, nil
}

func RequestError(provider, operation string, err error) error {
	if err == nil {
		return nil
	}
	var providerErr *cardpool.ProviderError
	if errors.As(err, &providerErr) {
		return err
	}
	category := cardpool.CategoryProviderUnavailable
	failover := true
	if errors.Is(err, context.Canceled) {
		category = cardpool.CategoryTechnicalFailure
		failover = false
	}
	return cardpool.NewProviderError(provider, operation, category, true, failover, err)
}

func HTTPError(provider, operation string, status int, body []byte) error {
	text := strings.ToLower(string(body))
	category := cardpool.CategoryInvalidRequest
	retryable := false
	failover := false
	switch {
	case status == http.StatusTooManyRequests:
		category = cardpool.CategoryRateLimit
		retryable = true
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		category = cardpool.CategoryInvalidRequest
	case status == http.StatusNotFound:
		category = cardpool.CategoryNotFound
	case status == http.StatusRequestTimeout || status == http.StatusTooEarly || status >= 500:
		category = cardpool.CategoryProviderUnavailable
		retryable = true
		failover = true
	case strings.Contains(text, "insufficient") || strings.Contains(text, "balance") || strings.Contains(text, "funds"):
		category = cardpool.CategoryInsufficientFunds
	case strings.Contains(text, "declin") || strings.Contains(text, "merchant") && strings.Contains(text, "reject"):
		category = cardpool.CategoryBusinessDecline
	case strings.Contains(text, "compliance") || strings.Contains(text, "risk") || strings.Contains(text, "kyc"):
		category = cardpool.CategoryComplianceBlock
	}
	return cardpool.NewProviderError(provider, operation, category, retryable, failover, fmt.Errorf("HTTP %d: %s", status, summarize(body)))
}

func summarize(body []byte) string {
	value := strings.TrimSpace(string(body))
	if value == "" {
		return "empty response"
	}
	var object any
	if json.Unmarshal(body, &object) == nil {
		redactJSON(object)
		if encoded, err := json.Marshal(object); err == nil {
			value = string(encoded)
		}
	}
	value = sensitiveAssignment.ReplaceAllString(value, "$1[REDACTED]")
	value = cardNumberValue.ReplaceAllString(value, "[REDACTED_PAN]")
	if len(value) > 800 {
		value = value[:800]
	}
	return value
}

func redactJSON(value any) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if sensitiveBodyKey.MatchString(key) {
				item[key] = "[REDACTED]"
				continue
			}
			redactJSON(child)
		}
	case []any:
		for _, child := range item {
			redactJSON(child)
		}
	}
}
