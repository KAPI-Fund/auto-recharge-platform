package shared

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

func TestVerifyAirwallexSignatureUsesRawBodyAndMillisecondTimestamp(t *testing.T) {
	secret := "airwallex-secret"
	body := []byte(`{"id":"evt-1","data": {"object": {"amount": 10}}}`)
	now := time.Now().UTC()
	timestamp := fmt.Sprintf("%d", now.UnixMilli())
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write([]byte(timestamp))
	_, _ = digest.Write(body)
	if !VerifyAirwallexSignature(body, timestamp, hex.EncodeToString(digest.Sum(nil)), secret, now, time.Minute) {
		t.Fatal("valid Airwallex signature was rejected")
	}
	if VerifyAirwallexSignature([]byte(`{"id":"tampered"}`), timestamp, hex.EncodeToString(digest.Sum(nil)), secret, now, time.Minute) {
		t.Fatal("tampered Airwallex body was accepted")
	}
}

func TestVerifyStripeSignatureAcceptsOnlyFreshV1Signature(t *testing.T) {
	secret := "stripe-secret"
	body := []byte(`{"id":"evt-1"}`)
	timestamp := time.Now().Unix()
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write([]byte(fmt.Sprintf("%d.", timestamp)))
	_, _ = digest.Write(body)
	header := fmt.Sprintf("t=%d,v1=%s,v0=ignored", timestamp, hex.EncodeToString(digest.Sum(nil)))
	if !VerifyStripeSignature(body, header, secret, time.Now(), 5*time.Minute) {
		t.Fatal("valid Stripe signature was rejected")
	}
	if VerifyStripeSignature([]byte(`{"id":"tampered"}`), header, secret, time.Now(), 5*time.Minute) {
		t.Fatal("tampered Stripe body was accepted")
	}
	staleTimestamp := timestamp - 301
	staleDigest := hmac.New(sha256.New, []byte(secret))
	_, _ = staleDigest.Write([]byte(fmt.Sprintf("%d.", staleTimestamp)))
	_, _ = staleDigest.Write(body)
	if VerifyStripeSignature(body, fmt.Sprintf("t=%d,v1=%s", staleTimestamp, hex.EncodeToString(staleDigest.Sum(nil))), secret, time.Now(), 5*time.Minute) {
		t.Fatal("expired Stripe signature was accepted")
	}
}
