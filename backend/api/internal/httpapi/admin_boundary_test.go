package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestRenewalCandidateJobKeysUsesServerEligibilityQuery(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "recharge_tasks" WHERE (status IN ($1,$2) AND session_ciphertext <> '') AND (cdk_code IS NULL OR cdk_code NOT LIKE $3) ORDER BY updated_at DESC, id DESC LIMIT $4`)).
		WithArgs(models.TaskSucceeded, "success", "ADMIN_PRODUCT_GEN:%", 50).
		WillReturnRows(sqlmock.NewRows([]string{"id", "job_key", "status", "session_ciphertext", "cdk_code", "updated_at"}).
			AddRow("task_1", "job_eligible", models.TaskSucceeded, "encrypted", "", now))

	server := &Server{DB: db}
	keys, err := server.renewalCandidateJobKeys(50)
	if err != nil {
		t.Fatalf("renewalCandidateJobKeys() error = %v", err)
	}
	if len(keys) != 1 || keys[0] != "job_eligible" {
		t.Fatalf("candidate keys = %#v, want [job_eligible]", keys)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func boundAddressRows(now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "region", "line1", "city", "state", "postal_code", "country", "active",
		"bound_card_id", "bound_at", "created_at", "updated_at",
	}).AddRow("address_1", "US", "123 Main St", "Portland", "Oregon", "97201", "US", true, "card_1", now, now, now)
}

func TestBoundAddressCannotBeMutatedThroughLegacyHTTPHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	updateDB, updateMock := newAssetRecoveryMock(t)
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	updateMock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "tax_free_addresses" WHERE id = $1 AND active = $2 ORDER BY "tax_free_addresses"."id" LIMIT $3`)).
		WithArgs("address_1", true, 1).
		WillReturnRows(boundAddressRows(now))
	updateRouter := gin.New()
	updateRouter.PUT("/addresses/:id", (&Server{DB: updateDB}).legacyUpdateAddress)
	updateRecorder := httptest.NewRecorder()
	updateRequest := httptest.NewRequest(http.MethodPut, "/addresses/address_1", bytes.NewBufferString(`{"line1":"changed"}`))
	updateRequest.Header.Set("Content-Type", "application/json")
	updateRouter.ServeHTTP(updateRecorder, updateRequest)
	if updateRecorder.Code != http.StatusConflict || !strings.Contains(updateRecorder.Body.String(), "已绑定地址不可编辑") {
		t.Fatalf("bound update status/body = %d/%s", updateRecorder.Code, updateRecorder.Body.String())
	}
	if err := updateMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet update SQL expectations: %v", err)
	}

	deleteDB, deleteMock := newAssetRecoveryMock(t)
	deleteMock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "tax_free_addresses" WHERE id = $1 AND active = $2 ORDER BY "tax_free_addresses"."id" LIMIT $3`)).
		WithArgs("address_1", true, 1).
		WillReturnRows(boundAddressRows(now))
	deleteRouter := gin.New()
	deleteRouter.DELETE("/addresses/:id", (&Server{DB: deleteDB}).legacyDeleteAddress)
	deleteRecorder := httptest.NewRecorder()
	deleteRouter.ServeHTTP(deleteRecorder, httptest.NewRequest(http.MethodDelete, "/addresses/address_1", nil))
	if deleteRecorder.Code != http.StatusConflict || !strings.Contains(deleteRecorder.Body.String(), "已绑定地址不可删除") {
		t.Fatalf("bound delete status/body = %d/%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if err := deleteMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet delete SQL expectations: %v", err)
	}
}
