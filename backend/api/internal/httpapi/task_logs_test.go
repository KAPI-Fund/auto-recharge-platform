package httpapi

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestTaskLogQueryCountUsesRechargeTaskModel(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "recharge_tasks"`)).
		WithArgs("ADMIN_PRODUCT_GEN:%", "", models.CDKTypeSelf).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	var total int64
	if err := taskLogQuery(db).Count(&total).Error; err != nil {
		t.Fatalf("taskLogQuery().Count() error = %v", err)
	}
	if total != 1 {
		t.Fatalf("task log total = %d, want 1", total)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
