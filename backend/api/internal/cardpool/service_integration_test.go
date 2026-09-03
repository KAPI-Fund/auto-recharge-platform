package cardpool

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

type integrationProvider struct {
	creates atomic.Int32
}

func (p *integrationProvider) ProviderName() string { return "TEST_PROVIDER" }
func (p *integrationProvider) HealthCheck(context.Context) (ProviderHealth, error) {
	return ProviderHealth{Provider: p.ProviderName(), Available: true}, nil
}
func (p *integrationProvider) AcquireCard(context.Context, AcquireCardRequest) (PaymentCard, error) {
	p.creates.Add(1)
	return PaymentCard{Provider: p.ProviderName(), ProviderCardID: "test-provider-card", Status: CardActive}, nil
}
func (p *integrationProvider) CreateCard(context.Context, CreateCardRequest) (PaymentCard, error) {
	return PaymentCard{}, nil
}
func (p *integrationProvider) GetCard(context.Context, string) (PaymentCard, error) {
	return PaymentCard{}, nil
}
func (p *integrationProvider) GetSensitiveCardDetails(context.Context, string) (SensitiveCardDetails, error) {
	return SensitiveCardDetails{}, nil
}
func (p *integrationProvider) FreezeCard(context.Context, string) error   { return nil }
func (p *integrationProvider) UnfreezeCard(context.Context, string) error { return nil }
func (p *integrationProvider) CancelCard(context.Context, string) error   { return nil }
func (p *integrationProvider) UpdateLimits(context.Context, string, CardLimits) error {
	return nil
}
func (p *integrationProvider) GetTransactions(context.Context, string) ([]CardTransaction, error) {
	return nil, nil
}
func (p *integrationProvider) Supports(Capability) bool { return true }

func TestServiceConcurrentIdempotentAllocationUsesOneProviderCard(t *testing.T) {
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

	poolID := db.NewID("pool_test")
	pool := models.CardPool{ID: poolID, Name: poolID, Type: string(PoolTypeExternalAPI), UsageType: string(UsageOneTime), Currency: "USD", RoutingStrategy: string(RoutingFixed), DefaultProvider: "TEST_PROVIDER", Enabled: true}
	providerRow := models.CardPoolProvider{ID: db.NewID("pool_provider_test"), PoolID: poolID, Provider: "TEST_PROVIDER", Enabled: true, Priority: 1, Weight: 100}
	if err := database.Create(&pool).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if err := database.Create(&providerRow).Error; err != nil {
		t.Fatalf("create provider row: %v", err)
	}
	defer func() {
		database.Where("pool_id = ?", poolID).Delete(&models.CardAllocation{})
		database.Where("pool_id = ?", poolID).Delete(&models.PaymentCard{})
		database.Where("pool_id = ?", poolID).Delete(&models.CardPoolProvider{})
		database.Delete(&models.CardPool{}, "id = ?", poolID)
	}()

	provider := &integrationProvider{}
	registry := NewProviderRegistry()
	registry.Register(provider)
	service := NewService(database, registry, nil)
	const idempotencyKey = "integration-same-task"
	results := make([]AllocationResult, 2)
	errorsOut := make([]error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range results {
		go func(index int) {
			defer wait.Done()
			results[index], errorsOut[index] = service.AcquireCard(context.Background(), AcquireCardRequest{PoolID: poolID, PaymentTaskID: "task-integration", IdempotencyKey: idempotencyKey})
		}(index)
	}
	wait.Wait()
	if provider.creates.Load() != 1 {
		t.Fatalf("provider create count = %d, want 1 (errors: %v)", provider.creates.Load(), errorsOut)
	}
	if (errorsOut[0] != nil && errorsOut[1] != nil) || results[0].Card.InternalCardID == "" && results[1].Card.InternalCardID == "" {
		t.Fatalf("both concurrent allocations failed: results=%#v errors=%#v", results, errorsOut)
	}
	if results[0].Card.InternalCardID != "" && results[1].Card.InternalCardID != "" && results[0].Card.InternalCardID != results[1].Card.InternalCardID {
		t.Fatalf("same idempotency key returned two cards: %#v %#v", results[0].Card, results[1].Card)
	}

	var allocationCount int64
	if err := database.Model(&models.CardAllocation{}).Where("pool_id = ? AND idempotency_key = ?", poolID, idempotencyKey).Count(&allocationCount).Error; err != nil {
		t.Fatalf("count allocations: %v", err)
	}
	if allocationCount != 1 {
		t.Fatalf("allocation count = %d, want 1", allocationCount)
	}
}

var _ CardProvider = (*integrationProvider)(nil)
