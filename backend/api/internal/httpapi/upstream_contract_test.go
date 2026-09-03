package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
)

func TestUpstreamStatusMappingPreservesTerminalSemantics(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"success", "succeeded"},
		{"completed", "succeeded"},
		{"done", "succeeded"},
		{"declined", "failed"},
		{"cancelled", "failed"},
		{"pending", ""},
	}
	for _, item := range cases {
		if got := upstreamTerminalStatus(item.status); got != item.want {
			t.Errorf("status %q = %q, want %q", item.status, got, item.want)
		}
	}
	if got := extractUpstreamStatus(map[string]any{"status": "done", "result": map[string]any{"ok": false}}); got != "failed" {
		t.Fatalf("failed result status = %q", got)
	}
}

func TestUpstreamPayloadExtractionAndSessionShape(t *testing.T) {
	payload := map[string]any{
		"order_id":   "order-1",
		"task_id":    "task-1",
		"topup_code": "TOPUP-1",
		"result":     map[string]any{"status": "processing"},
	}
	if extractUpstreamOrderID(payload) != "order-1" || extractUpstreamTaskID(payload) != "task-1" || extractUpstreamTopupCode(payload) != "TOPUP-1" {
		t.Fatalf("extracted upstream identifiers are wrong: %#v", payload)
	}
	object := sessionBodyObject(`{"access_token":"token-1"}`)
	if object["access_token"] != "token-1" {
		t.Fatalf("session object = %#v", object)
	}
	plain := sessionBodyObject("token-2")
	if plain["access_token"] != "token-2" {
		t.Fatalf("plain session object = %#v", plain)
	}
	if expiryMonth("09/28") != 9 || expiryYear("09/28") != 2028 || expiryYear("09/2028") != 2028 {
		t.Fatalf("expiry parsing failed")
	}
}

func TestDoUpstreamJSONPreservesHeadersBodyAndErrors(t *testing.T) {
	var observed struct {
		method  string
		headers http.Header
		body    map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observed.method = request.Method
		observed.headers = request.Header.Clone()
		_ = json.NewDecoder(request.Body).Decode(&observed.body)
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/fail" {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"detail":"bad upstream request"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"status":"accepted","order_id":"order-1"}`))
	}))
	defer server.Close()

	client := &http.Client{Timeout: time.Second}
	data, err := doUpstreamJSON(context.Background(), client, http.MethodPost, server.URL+"/pay", map[string]string{"Authorization": "Bearer secret", "Idempotency-Key": "task-1"}, map[string]any{"plan_key": "plus"})
	if err != nil {
		t.Fatalf("upstream request: %v", err)
	}
	if data["order_id"] != "order-1" || observed.method != http.MethodPost || observed.headers.Get("Authorization") != "Bearer secret" || observed.headers.Get("Idempotency-Key") != "task-1" || observed.body["plan_key"] != "plus" {
		t.Fatalf("upstream contract mismatch: data=%#v observed=%#v", data, observed)
	}
	if _, err := doUpstreamJSON(context.Background(), client, http.MethodGet, server.URL+"/fail", nil, nil); err == nil || !strings.Contains(err.Error(), "bad upstream request") {
		t.Fatalf("upstream error = %v", err)
	}
}

func TestUpstreamConfigKeepsGPTSecretsInGo(t *testing.T) {
	server := &Server{Cfg: config.Config{
		SessionEncryptionKey: "test-key",
		GPTAPIPollIntervalMS: 10,
		GPTAPIMaxPolls:       2,
	}}
	serverConfig := server.upstreamConfig()
	if serverConfig.protocol != "generic" || serverConfig.pollAttempts != 40 {
		t.Fatalf("default upstream config = %#v", serverConfig)
	}
}
