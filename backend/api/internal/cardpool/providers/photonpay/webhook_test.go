package photonpay

import (
	"context"
	"crypto"
	"crypto/md5" // #nosec G501 -- PhotonPay's documented webhook signing contract.
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
)

func TestParseWebhookAcceptsEventIdentifierAndMapsTransactionDetails(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	publicKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&privateKey.PublicKey)}))
	timestamp := time.Now().UnixMilli()
	body := []byte(fmt.Sprintf(`{"event_identifier":"evt-photon-1","event_type":"vcc.transaction.failed","timestamp":%d,"data":{"object":{"transactionId":"txn-photon-1","cardId":"photon-card-1","amount":12.5,"currency":"usd","status":"failed","type":"authorization","merchantInfo":{"name":"Example Merchant","reasonCode":"INSUFFICIENT_FUNDS"}}}}`, timestamp))
	digest := md5.Sum(body) // #nosec G401 -- required by PhotonPay's documented webhook contract.
	signature := base64.StdEncoding.EncodeToString(signPhotonDigest(t, privateKey, crypto.MD5, digest[:]))
	provider := New(testConfig{"photonpay_webhook_public_key": publicKey}, nil)
	event, err := provider.ParseWebhook(context.Background(), http.Header{"X-PD-SIGN": []string{signature}}, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.ProviderEventID != "evt-photon-1" || event.EventType != "vcc.transaction.failed" || event.ProviderCardID != "photon-card-1" || event.ProviderTxnID != "txn-photon-1" {
		t.Fatalf("unexpected event identity: %#v", event)
	}
	if event.Transaction == nil || event.Transaction.MerchantName != "Example Merchant" || event.Transaction.FailureCode != "INSUFFICIENT_FUNDS" || event.Transaction.Currency != "USD" {
		t.Fatalf("unexpected transaction mapping: %#v", event.Transaction)
	}
	if event.OccurredAt == nil || event.OccurredAt.UnixMilli() != timestamp {
		t.Fatalf("unexpected event time: %#v", event.OccurredAt)
	}
}

func TestParseWebhookAcceptsSHA256Signature(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	publicKey := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: x509MarshalPKIXPublicKeyMust(&privateKey.PublicKey)}))
	body := []byte(`{"id":"evt-photon-2","type":"vcc.card.active","data":{"cardId":"photon-card-2","status":"active"}}`)
	digest := sha256.Sum256(body)
	signature := base64.RawURLEncoding.EncodeToString(signPhotonDigest(t, privateKey, crypto.SHA256, digest[:]))
	provider := New(testConfig{"photonpay_webhook_public_key": publicKey}, nil)
	event, err := provider.ParseWebhook(context.Background(), http.Header{"X-Signature": []string{signature}}, body)
	if err != nil {
		t.Fatalf("ParseWebhook() error = %v", err)
	}
	if event.ProviderEventID != "evt-photon-2" || event.ProviderCardID != "photon-card-2" || event.Status != cardpool.CardActive {
		t.Fatalf("unexpected card event: %#v", event)
	}
}

func TestParseWebhookRejectsInvalidSignature(t *testing.T) {
	provider := New(testConfig{"photonpay_webhook_public_key": "not-a-key"}, nil)
	_, err := provider.ParseWebhook(context.Background(), http.Header{"X-PD-SIGN": []string{"00"}}, []byte(`{"id":"evt-photon-3","type":"vcc.card.active"}`))
	if err == nil || !cardpool.IsWebhookValidationError(err) || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("error = %v, want signature validation error", err)
	}
}

func signPhotonDigest(t *testing.T, privateKey *rsa.PrivateKey, hash crypto.Hash, digest []byte) []byte {
	t.Helper()
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, hash, digest)
	if err != nil {
		t.Fatalf("sign webhook digest: %v", err)
	}
	return signature
}

func x509MarshalPKIXPublicKeyMust(key *rsa.PublicKey) []byte {
	encoded, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		panic(err)
	}
	return encoded
}

func TestPhotonWebhookSignatureDigestUsesRawBody(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	body := []byte(`{"id":"evt-photon-4","type":"vcc.card.active"}`)
	digest := md5.Sum(body) // #nosec G401 -- required by PhotonPay's documented webhook contract.
	signature := signPhotonDigest(t, privateKey, crypto.MD5, digest[:])
	if !verifyPhotonWebhookSignature(body, base64.StdEncoding.EncodeToString(signature), string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: x509MarshalPKIXPublicKeyMust(&privateKey.PublicKey)}))) {
		t.Fatal("valid raw-body PhotonPay signature was rejected")
	}
	if verifyPhotonWebhookSignature([]byte(`{"tampered":true}`), base64.StdEncoding.EncodeToString(signature), string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: x509MarshalPKIXPublicKeyMust(&privateKey.PublicKey)}))) {
		t.Fatal("tampered PhotonPay webhook body was accepted")
	}
}

func TestPhotonWebhookTimeAcceptsUnixSeconds(t *testing.T) {
	value := strconv.FormatInt(time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC).Unix(), 10)
	payload := map[string]any{"timestamp": value}
	parsed := photonWebhookTime(payload)
	if parsed == nil || parsed.Year() != 2026 {
		t.Fatalf("photonWebhookTime(%q) = %v", value, parsed)
	}
}

func TestPhotonWebhookSHA256DigestIsValid(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	body := []byte(`{"id":"evt-photon-5","type":"vcc.card.active"}`)
	digest := sha256.Sum256(body)
	signature := signPhotonDigest(t, privateKey, crypto.SHA256, digest[:])
	publicKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&privateKey.PublicKey)}))
	if !verifyPhotonWebhookSignature(body, hex.EncodeToString(signature), publicKey) {
		t.Fatal("valid SHA-256 PhotonPay webhook signature was rejected")
	}
}
