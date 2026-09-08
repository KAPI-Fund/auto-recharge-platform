package httpapi

import (
	"database/sql/driver"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newAssetRecoveryMock(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sql mock: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open gorm mock: %v", err)
	}
	return db, mock
}

func expectAssetUpdate(mock sqlmock.Sqlmock, table string, where string, args ...driver.Value) {
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "`+table+`"`) + `.*` + where).
		WithArgs(args...).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestResetAssetLocksReleasesAllUnregisteredReservations(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	mock.ExpectBegin()
	expectAssetUpdate(mock, "phone_assets", `WHERE in_use = \$\d+`, false, nil, "", sqlmock.AnyArg(), true)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "card_assets"`) + `.*WHERE in_use = \$\d+`).
		WithArgs(true).
		WillReturnRows(sqlmock.NewRows([]string{"id", "in_use", "locked_at", "locked_by"}).AddRow("card-reset", true, nil, ""))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "payment_cards"`)+`.*WHERE local_card_asset_id = \$\d+`).
		WithArgs("card-reset", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	expectAssetUpdate(mock, "card_assets", `WHERE id = \$\d+ AND in_use = \$\d+`, false, nil, "", sqlmock.AnyArg(), "card-reset", true)
	expectAssetUpdate(mock, "pool_emails", `WHERE registered = \$\d+ AND in_use = \$\d+`, false, nil, "", sqlmock.AnyArg(), false, true)
	expectAssetUpdate(mock, "proxy_assets", `WHERE in_use = \$\d+`, false, nil, "", sqlmock.AnyArg(), true)
	mock.ExpectCommit()

	server := &Server{DB: db}
	report, err := server.ResetAssetLocks()
	if err != nil {
		t.Fatalf("ResetAssetLocks() error = %v", err)
	}
	if report.PhoneReleased != 1 || report.CardReleased != 1 || report.PoolReleased != 1 {
		t.Fatalf("unexpected reset report: %+v", report)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestReleaseStaleAssetLocksExcludesRegisteredMailboxes(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	mock.ExpectBegin()
	expectAssetUpdate(mock, "phone_assets", `WHERE in_use = \$\d+ AND \(locked_at IS NULL OR locked_at < \$\d+\)`, false, nil, "", sqlmock.AnyArg(), true, sqlmock.AnyArg())
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "card_assets"`)+`.*WHERE in_use = \$\d+ AND \(locked_at IS NULL OR locked_at < \$\d+\)`).
		WithArgs(true, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "in_use", "locked_at", "locked_by"}).AddRow("card-stale", true, time.Now().Add(-assetLockStaleAfter-time.Minute), ""))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "payment_cards"`)+`.*WHERE local_card_asset_id = \$\d+`).
		WithArgs("card-stale", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	expectAssetUpdate(mock, "card_assets", `WHERE id = \$\d+ AND in_use = \$\d+`, false, nil, "", sqlmock.AnyArg(), "card-stale", true)
	expectAssetUpdate(mock, "pool_emails", `WHERE registered = \$\d+ AND in_use = \$\d+ AND \(locked_at IS NULL OR locked_at < \$\d+\)`, false, nil, "", sqlmock.AnyArg(), false, true, sqlmock.AnyArg())
	expectAssetUpdate(mock, "proxy_assets", `WHERE in_use = \$\d+ AND \(locked_at IS NULL OR locked_at < \$\d+\)`, false, nil, "", sqlmock.AnyArg(), true, sqlmock.AnyArg())
	mock.ExpectCommit()

	server := &Server{DB: db}
	report, err := server.ReleaseStaleAssetLocks()
	if err != nil {
		t.Fatalf("ReleaseStaleAssetLocks() error = %v", err)
	}
	if report.PhoneReleased != 1 || report.CardReleased != 1 || report.PoolReleased != 1 {
		t.Fatalf("unexpected stale release report: %+v", report)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestCleanupStaleProductGenerationTasksOnlyUpdatesRunningRows(t *testing.T) {
	db, mock := newAssetRecoveryMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "product_generation_tasks"`)+`.*`+
		`WHERE status = \$\d+ AND updated_at < \$\d+`).
		WithArgs(sqlmock.AnyArg(), "遗留成品任务已自动清理", 100, models.ProductGenerationFailed, sqlmock.AnyArg(), models.ProductGenerationRunning, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	server := &Server{DB: db}
	cleaned, err := server.CleanupStaleProductGenerationTasks()
	if err != nil {
		t.Fatalf("CleanupStaleProductGenerationTasks() error = %v", err)
	}
	if cleaned != 2 {
		t.Fatalf("cleaned = %d, want 2", cleaned)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
