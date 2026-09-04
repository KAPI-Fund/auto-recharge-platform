package db

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestMigrateKeepsProviderScopedCardIndexAndRemovesLegacyPANColumn(t *testing.T) {
	databaseURL := os.Getenv("RECHARGE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("RECHARGE_TEST_DATABASE_URL is not set")
	}
	database, err := Open(databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := Migrate(database); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	columns, exists, err := findIndexColumns(database, &models.PaymentCard{}, "ux_payment_cards_provider_card")
	if err != nil {
		t.Fatalf("inspect payment card index: %v", err)
	}
	if !exists || !sameIndexColumns(columns, "provider", "provider_card_id") {
		t.Fatalf("payment card index columns = %v, want provider/provider_card_id", columns)
	}
	if database.Migrator().HasColumn(&models.BillingRecord{}, "card_number") {
		t.Fatal("legacy billing_records.card_number column still exists")
	}
	first := models.PaymentCard{ID: "migration-card-a-" + uuid.NewString(), PoolID: "pool_legacy", Provider: "MIGRATION_A", ProviderCardID: "same-provider-card", UsageType: "MULTI_USE", Status: "ACTIVE"}
	second := models.PaymentCard{ID: "migration-card-b-" + uuid.NewString(), PoolID: "pool_legacy", Provider: "MIGRATION_B", ProviderCardID: "same-provider-card", UsageType: "MULTI_USE", Status: "ACTIVE"}
	if err := database.Create(&first).Error; err != nil {
		t.Fatalf("create first provider card: %v", err)
	}
	defer database.Delete(&models.PaymentCard{}, "id = ?", first.ID)
	if err := database.Create(&second).Error; err != nil {
		t.Fatalf("provider-scoped card index rejected same external id: %v", err)
	}
	defer database.Delete(&models.PaymentCard{}, "id = ?", second.ID)
}

func TestMigrateEnforcesFiniteInventoryForPublishedPlans(t *testing.T) {
	databaseURL := os.Getenv("RECHARGE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("RECHARGE_TEST_DATABASE_URL is not set")
	}
	database, err := Open(databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := Migrate(database); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	planID := "constraint-plan-" + uuid.NewString()
	planCode := "constraint-plan-" + uuid.NewString()
	if err := database.Exec(`INSERT INTO plans (id, code, name, provider_plan_name, country, currency, price, active, sale_limit, sold_count, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		planID, planCode, "Constraint test plan", ProviderPlanNameForCode("plus"), "US", models.PlatformStoreCurrency, 1, false, 0, 0, 9999).Error; err != nil {
		t.Fatalf("create unpublished zero-inventory plan: %v", err)
	}
	defer database.Delete(&models.Plan{}, "id = ?", planID)

	if err := database.Model(&models.Plan{}).Where("id = ?", planID).Update("active", true).Error; err == nil {
		t.Fatal("published zero-inventory plan update unexpectedly succeeded")
	}
}
