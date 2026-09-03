package httpapi

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm"
)

func expectActivationCapacityQuery(mock sqlmock.Sqlmock, active int64) {
	mock.ExpectExec(regexp.QuoteMeta(activationAdmissionLockSQL)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "recharge_tasks"`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(active))
}

func TestCheckActivationCapacityTxRejectsAtConfiguredDefaultLimit(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	mock.ExpectBegin()
	expectActivationCapacityQuery(mock, 1)
	mock.ExpectRollback()

	err := database.Transaction(func(tx *gorm.DB) error {
		return (&Server{}).checkActivationCapacityTx(tx)
	})
	if !errors.Is(err, errActivationCapacity) {
		t.Fatalf("capacity error = %v, want %v", err, errActivationCapacity)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestCheckActivationCapacityTxAllowsBelowLimitAndKeepsDecisionTransactional(t *testing.T) {
	database, mock := newAssetRecoveryMock(t)
	mock.ExpectBegin()
	expectActivationCapacityQuery(mock, 0)
	mock.ExpectCommit()

	err := database.Transaction(func(tx *gorm.DB) error {
		return (&Server{}).checkActivationCapacityTx(tx)
	})
	if err != nil {
		t.Fatalf("capacity check error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
