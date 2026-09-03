package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestParsePhoneImport(t *testing.T) {
	items := parsePhoneImport("13800138000-API_KEY-with-dash\n13800138001\tTAB_KEY\n13800138002,CSV_KEY\n# ignored\nnot-a-phone")

	if len(items) != 3 {
		t.Fatalf("expected 3 phone rows, got %d: %#v", len(items), items)
	}
	if items[0].Phone != "13800138000" || items[0].Key != "API_KEY-with-dash" {
		t.Fatalf("hyphen format parsed incorrectly: %#v", items[0])
	}
	if items[1].Phone != "13800138001" || items[1].Key != "TAB_KEY" {
		t.Fatalf("tab format parsed incorrectly: %#v", items[1])
	}
	if items[2].Phone != "13800138002" || items[2].Key != "CSV_KEY" {
		t.Fatalf("comma format parsed incorrectly: %#v", items[2])
	}
}

func TestLegacySensitiveRoutesAcceptAdminSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	server := &Server{Cfg: config.Config{AdminAPIToken: "test-admin-token"}}
	router.GET("/test", server.requireSecondarySession, func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/test", nil)
	request.Header.Set("Authorization", "Bearer test-admin-token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestBuildProductProtocolExportMergesLegacyWrappers(t *testing.T) {
	runtimeDir := t.TempDir()
	protocolPath := filepath.Join(runtimeDir, "alice.json")
	protocol := `{"exported_at":"old","proxies":[{"url":"http://proxy"}],"accounts":[{"name":"alice@example.com"}]}`
	if err := os.WriteFile(protocolPath, []byte(protocol), 0o600); err != nil {
		t.Fatal(err)
	}

	server := &Server{Cfg: config.Config{RuntimeDir: runtimeDir}}
	payload, err := server.buildProductProtocolExport([]models.ProductAsset{{Email: "alice@example.com", FilePath: "alice.json"}})
	if err != nil {
		t.Fatal(err)
	}

	var document struct {
		ExportedAt string            `json:"exported_at"`
		Proxies    []json.RawMessage `json:"proxies"`
		Accounts   []json.RawMessage `json:"accounts"`
	}
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	if document.ExportedAt == "old" || len(document.Proxies) != 1 || len(document.Accounts) != 1 {
		t.Fatalf("unexpected merged protocol document: %s", payload)
	}
}

func TestBuildProductProtocolExportRejectsMissingProtocolFile(t *testing.T) {
	server := &Server{Cfg: config.Config{RuntimeDir: t.TempDir()}}
	if _, err := server.buildProductProtocolExport([]models.ProductAsset{{Email: "missing@example.com", FilePath: "missing.json"}}); err == nil {
		t.Fatal("expected missing protocol file to fail export")
	}
}
