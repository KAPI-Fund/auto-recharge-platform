package shared

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// VerifyAirwallexSignature follows Airwallex's documented signing contract:
// HMAC-SHA256(x-timestamp + raw request body), with the timestamp in
// milliseconds. The raw body must be passed unchanged.
func VerifyAirwallexSignature(body []byte, timestamp, signature, secret string, now time.Time, tolerance time.Duration) bool {
	timestamp = strings.TrimSpace(timestamp)
	signature = strings.TrimSpace(signature)
	secret = strings.TrimSpace(secret)
	if timestamp == "" || signature == "" || secret == "" {
		return false
	}
	parsed, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if tolerance > 0 {
		received := time.UnixMilli(parsed)
		if received.IsZero() || now.Sub(received) > tolerance || received.Sub(now) > tolerance {
			return false
		}
	}
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write([]byte(timestamp))
	_, _ = digest.Write(body)
	expected, err := hex.DecodeString(signature)
	return err == nil && hmac.Equal(digest.Sum(nil), expected)
}

// VerifyStripeSignature verifies Stripe's Stripe-Signature header using the
// documented v1 scheme. The timestamp is in seconds and the signed payload is
// "timestamp.raw_body".
func VerifyStripeSignature(body []byte, header, secret string, now time.Time, tolerance time.Duration) bool {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return false
	}
	var timestamp string
	var signatures []string
	for _, item := range strings.Split(header, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "t":
			timestamp = strings.TrimSpace(parts[1])
		case "v1":
			signatures = append(signatures, strings.TrimSpace(parts[1]))
		}
	}
	parsed, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || len(signatures) == 0 {
		return false
	}
	if tolerance > 0 {
		received := time.Unix(parsed, 0)
		if now.Sub(received) > tolerance || received.Sub(now) > tolerance {
			return false
		}
	}
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write([]byte(timestamp))
	_, _ = digest.Write([]byte("."))
	_, _ = digest.Write(body)
	actual := digest.Sum(nil)
	for _, signature := range signatures {
		expected, decodeErr := hex.DecodeString(signature)
		if decodeErr == nil && hmac.Equal(actual, expected) {
			return true
		}
	}
	return false
}

// VerifyDogPaySignature verifies the signature used by DogPay card webhooks.
// DogPay signs the raw request body with HMAC-SHA512. The provider has used
// both hexadecimal and base64 encodings in different environments, so accept
// either representation while always comparing the decoded bytes in constant
// time.
func VerifyDogPaySignature(body []byte, signature, secret string) bool {
	secret = strings.TrimSpace(secret)
	signature = strings.TrimSpace(signature)
	if secret == "" || signature == "" {
		return false
	}
	digest := hmac.New(sha512.New, []byte(secret))
	_, _ = digest.Write(body)
	actual := digest.Sum(nil)
	if expected, err := hex.DecodeString(signature); err == nil && hmac.Equal(actual, expected) {
		return true
	}
	decoded, err := base64.StdEncoding.DecodeString(signature)
	return err == nil && hmac.Equal(actual, decoded)
}
