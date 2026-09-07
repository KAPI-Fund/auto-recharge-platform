package localtext_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool/providers/localtext"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/security"
	"gorm.io/gorm"
)

func openDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databaseURL := os.Getenv("RECHARGE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("RECHARGE_TEST_DATABASE_URL is not set")
	}
	database, err := db.Open(databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return database
}

func TestOneTimeCardIsUsedAndCannotBeReallocated(t *testing.T) {
	database := openDatabase(t)
	const encryptionKey = "integration-session-encryption-key"
	poolID := db.NewID("pool_local_one_time")
	pool := models.CardPool{ID: poolID, Name: poolID, Type: string(cardpool.PoolTypeLocal), UsageType: string(cardpool.UsageOneTime), Currency: "USD", RoutingStrategy: string(cardpool.RoutingFixed), DefaultProvider: "LOCAL_TEXT", Enabled: true}
	providerRow := models.CardPoolProvider{ID: db.NewID("pp_local_one_time"), PoolID: poolID, Provider: "LOCAL_TEXT", Enabled: true, Priority: 1, Weight: 100}
	if err := database.Create(&pool).Error; err != nil {
		t.Fatalf("create local pool: %v", err)
	}
	if err := database.Create(&providerRow).Error; err != nil {
		t.Fatalf("create local provider row: %v", err)
	}
	numberCipher, err := security.Encrypt("4242424242424242", encryptionKey)
	if err != nil {
		t.Fatalf("encrypt number: %v", err)
	}
	expiryCipher, err := security.Encrypt("12/30", encryptionKey)
	if err != nil {
		t.Fatalf("encrypt expiry: %v", err)
	}
	cvcCipher, err := security.Encrypt("123", encryptionKey)
	if err != nil {
		t.Fatalf("encrypt cvc: %v", err)
	}
	asset := models.CardAsset{ID: db.NewID("local_asset"), PoolID: poolID, Last4: "4242", CardNumberCiphertext: numberCipher, ExpiryCiphertext: expiryCipher, CVVCiphertext: cvcCipher, Status: "正常", Active: true}
	if err := database.Create(&asset).Error; err != nil {
		t.Fatalf("create local asset: %v", err)
	}
	defer func() {
		database.Where("pool_id = ?", poolID).Delete(&models.CardAllocation{})
		database.Where("pool_id = ?", poolID).Delete(&models.PaymentCard{})
		database.Delete(&models.CardAsset{}, "id = ?", asset.ID)
		database.Delete(&models.CardPoolProvider{}, "id = ?", providerRow.ID)
		database.Delete(&models.CardPool{}, "id = ?", poolID)
	}()

	registry := cardpool.NewProviderRegistry()
	registry.Register(localtext.New(database, encryptionKey))
	service := cardpool.NewService(database, registry, nil)
	result, err := service.AcquireCard(context.Background(), cardpool.AcquireCardRequest{PoolID: poolID, PaymentTaskID: "one-time-task", UsageType: cardpool.UsageOneTime})
	if err != nil {
		t.Fatalf("acquire local one-time card: %v", err)
	}
	details, err := service.GetSensitiveCardDetails(context.Background(), result.Card.InternalCardID)
	if err != nil || details.CardNumber != "4242424242424242" || details.CVC != "123" {
		t.Fatalf("sensitive details = %+v, err=%v", details, err)
	}
	if _, err := service.RecordUsageForAllocation(context.Background(), result.Card.InternalCardID, result.AllocationID); err != nil {
		t.Fatalf("record local one-time usage: %v", err)
	}
	if err := service.ReleaseCardForAllocation(context.Background(), result.Card.InternalCardID, result.AllocationID); err != nil {
		t.Fatalf("release local one-time card: %v", err)
	}

	var storedAsset models.CardAsset
	if err := database.First(&storedAsset, "id = ?", asset.ID).Error; err != nil {
		t.Fatalf("read local asset: %v", err)
	}
	if storedAsset.Active || storedAsset.Status != "已报废" || storedAsset.InUse {
		t.Fatalf("local one-time asset = %+v, want inactive and retired", storedAsset)
	}
	var storedCard models.PaymentCard
	if err := database.First(&storedCard, "id = ?", result.Card.InternalCardID).Error; err != nil {
		t.Fatalf("read normalized card: %v", err)
	}
	if storedCard.Status != string(cardpool.CardCancelled) || storedCard.InUse {
		t.Fatalf("normalized one-time card = %+v", storedCard)
	}
	if _, err := service.AcquireCard(context.Background(), cardpool.AcquireCardRequest{PoolID: poolID, PaymentTaskID: "one-time-task-2", UsageType: cardpool.UsageOneTime}); !errors.Is(err, cardpool.ErrNoAvailableCard) {
		t.Fatalf("second one-time acquire error = %v, want ErrNoAvailableCard", err)
	}
}
