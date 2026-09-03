package httpapi

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/cardpool"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

type recoveryProvider struct {
	cancels      atomic.Int32
	cancelErrors atomic.Int32
}

func (p *recoveryProvider) ProviderName() string { return "TEST_RECOVERY" }
func (p *recoveryProvider) HealthCheck(context.Context) (cardpool.ProviderHealth, error) {
	return cardpool.ProviderHealth{Provider: p.ProviderName(), Available: true, Status: "healthy"}, nil
}
func (p *recoveryProvider) AcquireCard(context.Context, cardpool.AcquireCardRequest) (cardpool.PaymentCard, error) {
	return cardpool.PaymentCard{Provider: p.ProviderName(), ProviderCardID: "recovery-card", Status: cardpool.CardActive}, nil
}
func (p *recoveryProvider) CreateCard(context.Context, cardpool.CreateCardRequest) (cardpool.PaymentCard, error) {
	return cardpool.PaymentCard{}, errors.New("not used by recovery test")
}
func (p *recoveryProvider) GetCard(context.Context, string) (cardpool.PaymentCard, error) {
	return cardpool.PaymentCard{}, cardpool.ErrCardNotFound
}
func (p *recoveryProvider) GetSensitiveCardDetails(context.Context, string) (cardpool.SensitiveCardDetails, error) {
	return cardpool.SensitiveCardDetails{}, cardpool.ErrUnsupportedCapability
}
func (p *recoveryProvider) FreezeCard(context.Context, string) error   { return nil }
func (p *recoveryProvider) UnfreezeCard(context.Context, string) error { return nil }
func (p *recoveryProvider) CancelCard(context.Context, string) error {
	if p.cancelErrors.Load() > 0 {
		p.cancelErrors.Add(-1)
		return errors.New("temporary provider cancellation failure")
	}
	p.cancels.Add(1)
	return nil
}
func (p *recoveryProvider) UpdateLimits(context.Context, string, cardpool.CardLimits) error {
	return cardpool.ErrUnsupportedCapability
}
func (p *recoveryProvider) GetTransactions(context.Context, string) ([]cardpool.CardTransaction, error) {
	return nil, nil
}
func (p *recoveryProvider) Supports(capability cardpool.Capability) bool {
	return capability == cardpool.CapabilityCancel
}

func TestRecoverStaleTaskCompensatesRemoteOneTimeCard(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("RECHARGE_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("set RECHARGE_TEST_DATABASE_URL to run the real PostgreSQL recovery flow")
	}
	database, err := db.Open(databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate integration database: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	defer sqlDB.Close()

	provider := &recoveryProvider{}
	provider.cancelErrors.Store(1)
	registry := cardpool.NewProviderRegistry()
	registry.Register(provider)
	service := cardpool.NewService(database, registry, nil)
	poolID := db.NewID("pool_recovery")
	pool := models.CardPool{ID: poolID, Name: poolID, Type: string(cardpool.PoolTypeExternalAPI), UsageType: string(cardpool.UsageOneTime), Currency: "USD", RoutingStrategy: string(cardpool.RoutingFixed), DefaultProvider: provider.ProviderName(), Enabled: true}
	providerRow := models.CardPoolProvider{ID: db.NewID("pool_provider_recovery"), PoolID: poolID, Provider: provider.ProviderName(), Enabled: true, Priority: 1, Weight: 100}
	if err := database.Create(&pool).Error; err != nil {
		t.Fatalf("create recovery pool: %v", err)
	}
	if err := database.Create(&providerRow).Error; err != nil {
		t.Fatalf("create recovery provider row: %v", err)
	}
	var plan models.Plan
	if err := database.Where("code = ?", "plus").First(&plan).Error; err != nil {
		t.Fatalf("load seeded plus plan: %v", err)
	}
	taskID := db.NewID("task_recovery")
	cardID := db.NewID("payment_card_recovery")
	allocationID := db.NewID("allocation_recovery")
	defer func() {
		database.Where("job_key = ?", "recovery-job").Delete(&models.RuntimeLog{})
		database.Delete(&models.RechargeTask{}, "id = ?", taskID)
		database.Delete(&models.CardAllocation{}, "id = ?", allocationID)
		database.Delete(&models.PaymentCard{}, "id = ?", cardID)
		database.Delete(&models.CardPoolProvider{}, "pool_id = ?", poolID)
		database.Delete(&models.CardPool{}, "id = ?", poolID)
	}()

	expired := time.Now().Add(-time.Minute)
	card := models.PaymentCard{
		ID: cardID, PoolID: poolID, Provider: provider.ProviderName(), ProviderCardID: "recovery-card",
		UsageType: string(cardpool.UsageOneTime), Currency: "USD", Status: string(cardpool.CardInUse), InUse: true,
	}
	allocation := models.CardAllocation{
		ID: allocationID, IdempotencyKey: "recovery-idempotency", PaymentTaskID: taskID, PaymentCardID: cardID,
		PoolID: poolID, Provider: provider.ProviderName(), ProviderCardID: card.ProviderCardID,
		UsageType: string(cardpool.UsageOneTime), Status: string(cardpool.AllocationInUse), AllocatedAt: expired,
	}
	task := models.RechargeTask{
		ID: taskID, JobKey: "recovery-job", TraceID: "trace-recovery", PoolID: poolID,
		PlanID: plan.ID, UsageType: string(cardpool.UsageOneTime), PaymentCardID: cardID, CardAllocationID: allocationID,
		Mode: "browser", Status: models.TaskRunning, LeaseExpiresAt: &expired,
	}
	if err := database.Create(&card).Error; err != nil {
		t.Fatalf("create recovery card: %v", err)
	}
	if err := database.Create(&allocation).Error; err != nil {
		t.Fatalf("create recovery allocation: %v", err)
	}
	if err := database.Create(&task).Error; err != nil {
		t.Fatalf("create recovery task: %v", err)
	}

	server := &Server{DB: database, CardPools: service, Cfg: config.Config{TaskLeaseTimeoutSeconds: 60}}
	report, err := server.RecoverStaleRechargeTasks()
	if err != nil {
		t.Fatalf("first recovery: %v", err)
	}
	if report.RunningExpired != 1 || report.ProviderReleaseAttempted != 1 || report.ProviderReleaseFailed != 1 || provider.cancels.Load() != 0 {
		t.Fatalf("first recovery report=%+v cancels=%d", report, provider.cancels.Load())
	}
	var failedCard models.PaymentCard
	if err := database.First(&failedCard, "id = ?", cardID).Error; err != nil {
		t.Fatalf("read failed recovery card: %v", err)
	}
	if failedCard.Status != string(cardpool.CardFailed) || failedCard.InUse {
		t.Fatalf("remote card became reusable after failed cancellation: %+v", failedCard)
	}
	var pending models.CardAllocation
	if err := database.First(&pending, "id = ?", allocationID).Error; err != nil {
		t.Fatalf("read pending recovery allocation: %v", err)
	}
	if !pending.ProviderReleasePending || pending.ProviderReleaseAction != cardpool.ProviderReleaseActionCancel {
		t.Fatalf("pending release state = %+v", pending)
	}

	report, err = server.RecoverStaleRechargeTasks()
	if err != nil {
		t.Fatalf("retry recovery: %v", err)
	}
	if report.ProviderReleaseAttempted != 1 || report.ProviderReleaseSucceeded != 1 || provider.cancels.Load() != 1 {
		t.Fatalf("retry recovery report=%+v cancels=%d", report, provider.cancels.Load())
	}
	var releasedCard models.PaymentCard
	if err := database.First(&releasedCard, "id = ?", cardID).Error; err != nil {
		t.Fatalf("read released recovery card: %v", err)
	}
	if releasedCard.Status != string(cardpool.CardCancelled) || releasedCard.InUse {
		t.Fatalf("released remote card state = %+v", releasedCard)
	}
	if err := database.First(&pending, "id = ?", allocationID).Error; err != nil {
		t.Fatalf("read completed recovery allocation: %v", err)
	}
	if pending.ProviderReleasePending || pending.ProviderReleasedAt == nil || pending.ProviderReleaseError != "" {
		t.Fatalf("completed release state = %+v", pending)
	}
}

var _ cardpool.CardProvider = (*recoveryProvider)(nil)
