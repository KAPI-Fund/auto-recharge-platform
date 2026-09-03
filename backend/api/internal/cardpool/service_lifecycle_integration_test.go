package cardpool

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
)

type lifecycleIntegrationProvider struct {
	creates       atomic.Int32
	cancels       atomic.Int32
	cancelCalls   atomic.Int32
	cancelErrors  atomic.Int32
	fixedCardID   string
	cancelStarted chan struct{}
	releaseCancel chan struct{}
	cancelOnce    sync.Once
}

func (p *lifecycleIntegrationProvider) ProviderName() string { return "TEST_LIFECYCLE" }
func (p *lifecycleIntegrationProvider) HealthCheck(context.Context) (ProviderHealth, error) {
	return ProviderHealth{Provider: p.ProviderName(), Available: true, Status: "healthy"}, nil
}
func (p *lifecycleIntegrationProvider) AcquireCard(context.Context, AcquireCardRequest) (PaymentCard, error) {
	sequence := p.creates.Add(1)
	providerCardID := p.fixedCardID
	if providerCardID == "" {
		providerCardID = "remote-card-" + stringID(sequence)
	}
	return PaymentCard{Provider: p.ProviderName(), ProviderCardID: providerCardID, Last4: "4242", Status: CardActive}, nil
}
func (p *lifecycleIntegrationProvider) CreateCard(ctx context.Context, request CreateCardRequest) (PaymentCard, error) {
	return p.AcquireCard(ctx, AcquireCardRequest{UsageType: request.UsageType})
}
func (p *lifecycleIntegrationProvider) GetCard(context.Context, string) (PaymentCard, error) {
	return PaymentCard{}, ErrCardNotFound
}
func (p *lifecycleIntegrationProvider) GetSensitiveCardDetails(context.Context, string) (SensitiveCardDetails, error) {
	return SensitiveCardDetails{CardNumber: "4242424242424242", ExpiryMonth: 12, ExpiryYear: 2030, CVC: "123"}, nil
}
func (p *lifecycleIntegrationProvider) FreezeCard(context.Context, string) error   { return nil }
func (p *lifecycleIntegrationProvider) UnfreezeCard(context.Context, string) error { return nil }
func (p *lifecycleIntegrationProvider) CancelCard(context.Context, string) error {
	p.cancelCalls.Add(1)
	if p.cancelStarted != nil && p.releaseCancel != nil {
		p.cancelOnce.Do(func() { close(p.cancelStarted) })
		<-p.releaseCancel
	}
	if p.cancelErrors.Load() > 0 {
		p.cancelErrors.Add(-1)
		return errors.New("remote cancellation temporarily failed")
	}
	p.cancels.Add(1)
	return nil
}

func waitForProviderCancel(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for provider cancellation call")
	}
}
func (p *lifecycleIntegrationProvider) UpdateLimits(context.Context, string, CardLimits) error {
	return ErrUnsupportedCapability
}
func (p *lifecycleIntegrationProvider) GetTransactions(context.Context, string) ([]CardTransaction, error) {
	return nil, nil
}
func (p *lifecycleIntegrationProvider) Supports(capability Capability) bool {
	return capability == CapabilityCancel || capability == CapabilitySensitiveDetails
}

func stringID(value int32) string {
	return strconv.Itoa(int(value))
}

func openCardPoolIntegrationDatabase(t *testing.T) *gorm.DB {
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

func createIntegrationPool(t *testing.T, database *gorm.DB, usage UsageType, provider string) string {
	t.Helper()
	poolID := db.NewID("pool_lifecycle")
	pool := models.CardPool{ID: poolID, Name: poolID, Type: string(PoolTypeExternalAPI), UsageType: string(usage), CardCreationMode: string(CardCreationOnDemand), Currency: "USD", RoutingStrategy: string(RoutingFixed), DefaultProvider: provider, Enabled: true}
	providerRow := models.CardPoolProvider{ID: db.NewID("pool_provider_lifecycle"), PoolID: poolID, Provider: provider, Enabled: true, Priority: 1, Weight: 100}
	if err := database.Create(&pool).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if err := database.Create(&providerRow).Error; err != nil {
		t.Fatalf("create provider row: %v", err)
	}
	return poolID
}

func cleanupIntegrationPool(database *gorm.DB, poolID string) {
	database.Where("pool_id = ?", poolID).Delete(&models.CardAllocation{})
	database.Where("pool_id = ?", poolID).Delete(&models.PaymentCard{})
	database.Where("pool_id = ?", poolID).Delete(&models.CardAsset{})
	database.Where("pool_id = ?", poolID).Delete(&models.CardPoolProvider{})
	database.Delete(&models.CardPool{}, "id = ?", poolID)
}

func TestServiceOnDemandAllocationCreatesAndDestroysOneCardPerAttempt(t *testing.T) {
	database := openCardPoolIntegrationDatabase(t)
	provider := &lifecycleIntegrationProvider{}
	registry := NewProviderRegistry()
	registry.Register(provider)
	service := NewService(database, registry, nil)
	poolID := db.NewID("pool_lifecycle_on_demand")
	pool := models.CardPool{ID: poolID, Name: poolID, Type: string(PoolTypeExternalAPI), UsageType: string(UsageRecurring), CardCreationMode: string(CardCreationOnDemand), Currency: "USD", RoutingStrategy: string(RoutingFixed), DefaultProvider: provider.ProviderName(), Enabled: true}
	providerRow := models.CardPoolProvider{ID: db.NewID("pool_provider"), PoolID: poolID, Provider: provider.ProviderName(), Enabled: true, Priority: 1, Weight: 100}
	if err := database.Create(&pool).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if err := database.Create(&providerRow).Error; err != nil {
		t.Fatalf("create provider row: %v", err)
	}
	defer cleanupIntegrationPool(database, poolID)

	first, err := service.AcquireCard(context.Background(), AcquireCardRequest{
		PoolID: poolID, BusinessAccountID: "business-account-1", UsageType: UsageRecurring, IdempotencyKey: "single-use-first",
	})
	if err != nil {
		t.Fatalf("first on-demand acquire: %v", err)
	}
	second, err := service.AcquireCard(context.Background(), AcquireCardRequest{
		PoolID: poolID, BusinessAccountID: "business-account-1", UsageType: UsageRecurring, IdempotencyKey: "single-use-second",
	})
	if err != nil {
		t.Fatalf("second on-demand acquire: %v", err)
	}
	if provider.creates.Load() != 2 || first.Card.InternalCardID == second.Card.InternalCardID {
		t.Fatalf("on-demand allocation did not create two distinct cards: creates=%d first=%+v second=%+v", provider.creates.Load(), first.Card, second.Card)
	}
	if first.Card.UsageType != UsageOneTime || second.Card.UsageType != UsageOneTime {
		t.Fatalf("recharge allocations were not forced to one-time: first=%q second=%q", first.Card.UsageType, second.Card.UsageType)
	}

	if _, err := service.SettleCardForAllocation(context.Background(), first.Card.InternalCardID, first.AllocationID, "task-1", "", ""); err != nil {
		t.Fatalf("settle successful one-time card: %v", err)
	}
	if _, err := service.SettleCardForAllocation(context.Background(), second.Card.InternalCardID, second.AllocationID, "task-2", "card_declined", "declined"); err != nil {
		t.Fatalf("settle failed one-time card: %v", err)
	}
	if provider.cancels.Load() != 2 {
		t.Fatalf("provider cancellation count = %d, want 2", provider.cancels.Load())
	}
	for _, cardID := range []string{first.Card.InternalCardID, second.Card.InternalCardID} {
		var card models.PaymentCard
		if err := database.First(&card, "id = ?", cardID).Error; err != nil {
			t.Fatalf("read settled card %s: %v", cardID, err)
		}
		if card.Status != string(CardCancelled) || card.InUse || card.UsageCount != 1 {
			t.Fatalf("settled one-time card = %+v, want cancelled, unused and used once", card)
		}
	}
}

func TestExternalOneTimeProviderCancellationIsRetryable(t *testing.T) {
	database := openCardPoolIntegrationDatabase(t)
	provider := &lifecycleIntegrationProvider{}
	provider.cancelErrors.Store(1)
	registry := NewProviderRegistry()
	registry.Register(provider)
	service := NewService(database, registry, nil)
	poolID := createIntegrationPool(t, database, UsageOneTime, provider.ProviderName())
	defer cleanupIntegrationPool(database, poolID)

	result, err := service.AcquireCard(context.Background(), AcquireCardRequest{PoolID: poolID, PaymentTaskID: "external-one-time", UsageType: UsageOneTime})
	if err != nil {
		t.Fatalf("external one-time acquire: %v", err)
	}
	if _, err := service.SettleCardForAllocation(context.Background(), result.Card.InternalCardID, result.AllocationID, "external-one-time", "provider_timeout", "provider timeout"); err == nil {
		t.Fatal("first provider cancellation unexpectedly succeeded")
	}
	var allocation models.CardAllocation
	if err := database.First(&allocation, "id = ?", result.AllocationID).Error; err != nil {
		t.Fatalf("read failed release allocation: %v", err)
	}
	if allocation.Status != string(AllocationReleased) || allocation.ProviderReleasedAt != nil || allocation.ProviderReleaseError == "" {
		t.Fatalf("failed provider release state = %+v", allocation)
	}
	if err := service.ReleaseCardForAllocation(context.Background(), result.Card.InternalCardID, result.AllocationID); err != nil {
		t.Fatalf("retry provider cancellation: %v", err)
	}
	if provider.cancels.Load() != 1 {
		t.Fatalf("provider cancellation count = %d, want 1 successful cancellation", provider.cancels.Load())
	}
	if err := database.First(&allocation, "id = ?", result.AllocationID).Error; err != nil {
		t.Fatalf("read retried release allocation: %v", err)
	}
	if allocation.ProviderReleasedAt == nil || allocation.ProviderReleaseError != "" {
		t.Fatalf("retried provider release state = %+v", allocation)
	}
}

func TestConcurrentSettlementCallsShareOneProviderReleaseClaim(t *testing.T) {
	database := openCardPoolIntegrationDatabase(t)
	provider := &lifecycleIntegrationProvider{
		cancelStarted: make(chan struct{}),
		releaseCancel: make(chan struct{}),
	}
	registry := NewProviderRegistry()
	registry.Register(provider)
	service := NewService(database, registry, nil)
	poolID := createIntegrationPool(t, database, UsageOneTime, provider.ProviderName())
	defer cleanupIntegrationPool(database, poolID)

	result, err := service.AcquireCard(context.Background(), AcquireCardRequest{
		PoolID: poolID, PaymentTaskID: "concurrent-settlement", UsageType: UsageOneTime,
	})
	if err != nil {
		t.Fatalf("acquire card: %v", err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, settleErr := service.SettleCardForAllocation(context.Background(), result.Card.InternalCardID, result.AllocationID, "concurrent-settlement", "", "")
		firstDone <- settleErr
	}()
	waitForProviderCancel(t, provider.cancelStarted)

	secondDone := make(chan error, 1)
	go func() {
		_, settleErr := service.SettleCardForAllocation(context.Background(), result.Card.InternalCardID, result.AllocationID, "concurrent-settlement", "", "")
		secondDone <- settleErr
	}()
	select {
	case settleErr := <-secondDone:
		if settleErr != nil {
			t.Fatalf("duplicate settlement returned error while release was claimed: %v", settleErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("duplicate settlement did not finish while the first provider call was blocked")
	}
	if got := provider.cancelCalls.Load(); got != 1 {
		t.Fatalf("provider cancellation calls before release = %d, want 1", got)
	}

	close(provider.releaseCancel)
	select {
	case settleErr := <-firstDone:
		if settleErr != nil {
			t.Fatalf("first settlement: %v", settleErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first settlement did not finish after provider release was unblocked")
	}
	if got := provider.cancels.Load(); got != 1 {
		t.Fatalf("successful provider cancellations = %d, want 1", got)
	}

	var allocation models.CardAllocation
	if err := database.First(&allocation, "id = ?", result.AllocationID).Error; err != nil {
		t.Fatalf("read allocation: %v", err)
	}
	if allocation.ProviderReleasePending || allocation.ProviderReleaseClaimToken != "" || allocation.ProviderReleaseClaimedAt != nil {
		t.Fatalf("release claim was not finalized: %+v", allocation)
	}
	var card models.PaymentCard
	if err := database.First(&card, "id = ?", result.Card.InternalCardID).Error; err != nil {
		t.Fatalf("read card: %v", err)
	}
	if card.Status != string(CardCancelled) || card.UsageCount != 1 || card.InUse {
		t.Fatalf("settled card = %+v, want cancelled/used once/not in use", card)
	}
}

func TestConcurrentRecoveryRetriesClaimOnePendingProviderRelease(t *testing.T) {
	database := openCardPoolIntegrationDatabase(t)
	provider := &lifecycleIntegrationProvider{cancelErrors: atomic.Int32{}}
	provider.cancelErrors.Store(1)
	registry := NewProviderRegistry()
	registry.Register(provider)
	service := NewService(database, registry, nil)
	poolID := createIntegrationPool(t, database, UsageOneTime, provider.ProviderName())
	defer cleanupIntegrationPool(database, poolID)

	result, err := service.AcquireCard(context.Background(), AcquireCardRequest{
		PoolID: poolID, PaymentTaskID: "concurrent-recovery", UsageType: UsageOneTime,
	})
	if err != nil {
		t.Fatalf("acquire card: %v", err)
	}
	if _, err := service.SettleCardForAllocation(context.Background(), result.Card.InternalCardID, result.AllocationID, "concurrent-recovery", "provider_timeout", "temporary provider failure"); err == nil {
		t.Fatal("initial settlement unexpectedly succeeded")
	}

	provider.cancelStarted = make(chan struct{})
	provider.releaseCancel = make(chan struct{})
	provider.cancelOnce = sync.Once{}
	firstDone := make(chan ProviderReleaseRetryReport, 1)
	firstErr := make(chan error, 1)
	go func() {
		report, retryErr := service.RetryPendingProviderReleases(context.Background(), 10)
		firstDone <- report
		firstErr <- retryErr
	}()
	waitForProviderCancel(t, provider.cancelStarted)

	secondDone := make(chan ProviderReleaseRetryReport, 1)
	secondErr := make(chan error, 1)
	go func() {
		report, retryErr := service.RetryPendingProviderReleases(context.Background(), 10)
		secondDone <- report
		secondErr <- retryErr
	}()
	select {
	case report := <-secondDone:
		if retryErr := <-secondErr; retryErr != nil {
			t.Fatalf("duplicate recovery retry: %v", retryErr)
		}
		if report.Attempted != 0 || report.Succeeded != 0 || report.Failed != 0 {
			t.Fatalf("duplicate recovery retry report = %+v, want no remote attempt", report)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("duplicate recovery retry did not skip the active release claim")
	}

	close(provider.releaseCancel)
	select {
	case report := <-firstDone:
		if retryErr := <-firstErr; retryErr != nil {
			t.Fatalf("first recovery retry: %v", retryErr)
		}
		if report.Attempted != 1 || report.Succeeded != 1 || report.Failed != 0 {
			t.Fatalf("first recovery retry report = %+v", report)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first recovery retry did not finish after provider release was unblocked")
	}
	if provider.cancelCalls.Load() != 2 || provider.cancels.Load() != 1 {
		t.Fatalf("provider cancellation calls=%d successful=%d, want 2 total/1 successful", provider.cancelCalls.Load(), provider.cancels.Load())
	}
}

func TestOnDemandAcquireDoesNotResurrectConsumedProviderCard(t *testing.T) {
	database := openCardPoolIntegrationDatabase(t)
	provider := &lifecycleIntegrationProvider{fixedCardID: "remote-terminal-card"}
	registry := NewProviderRegistry()
	registry.Register(provider)
	service := NewService(database, registry, nil)
	poolID := createIntegrationPool(t, database, UsageOneTime, provider.ProviderName())
	defer cleanupIntegrationPool(database, poolID)

	cardID := db.NewID("terminal_card")
	card := models.PaymentCard{
		ID: cardID, PoolID: poolID, Provider: provider.ProviderName(), ProviderCardID: provider.fixedCardID,
		Last4: "4242", UsageType: string(UsageOneTime), Currency: "USD", Status: string(CardCancelled), UsageCount: 1,
	}
	if err := database.Create(&card).Error; err != nil {
		t.Fatalf("create terminal card: %v", err)
	}

	_, err := service.AcquireCard(context.Background(), AcquireCardRequest{PoolID: poolID, PaymentTaskID: "terminal-task", IdempotencyKey: "terminal-acquire"})
	if !errors.Is(err, ErrCardConsumed) {
		t.Fatalf("acquire terminal provider card error = %v, want ErrCardConsumed", err)
	}
	var stored models.PaymentCard
	if err := database.First(&stored, "id = ?", cardID).Error; err != nil {
		t.Fatalf("read terminal card: %v", err)
	}
	if stored.Status != string(CardCancelled) || stored.UsageCount != 1 || stored.InUse {
		t.Fatalf("terminal card was resurrected: %+v", stored)
	}
	if provider.cancels.Load() != 0 {
		t.Fatalf("reconciliation conflict unexpectedly cancelled existing card %d times", provider.cancels.Load())
	}
	var allocation models.CardAllocation
	if err := database.Where("idempotency_key = ?", "terminal-acquire").First(&allocation).Error; err != nil {
		t.Fatalf("read failed allocation: %v", err)
	}
	if allocation.Status != string(AllocationFailed) || allocation.FailureCode != "card_consumed" {
		t.Fatalf("terminal allocation = %+v, want failed/card_consumed", allocation)
	}
}

func TestManualCreateDoesNotResurrectConsumedProviderCard(t *testing.T) {
	database := openCardPoolIntegrationDatabase(t)
	provider := &lifecycleIntegrationProvider{fixedCardID: "remote-manual-terminal-card"}
	registry := NewProviderRegistry()
	registry.Register(provider)
	service := NewService(database, registry, nil)
	poolID := createIntegrationPool(t, database, UsageOneTime, provider.ProviderName())
	defer cleanupIntegrationPool(database, poolID)

	cardID := db.NewID("manual_terminal_card")
	card := models.PaymentCard{
		ID: cardID, PoolID: poolID, Provider: provider.ProviderName(), ProviderCardID: provider.fixedCardID,
		Last4: "4242", UsageType: string(UsageOneTime), Currency: "USD", Status: string(CardUsed), UsageCount: 1,
	}
	if err := database.Create(&card).Error; err != nil {
		t.Fatalf("create manual terminal card: %v", err)
	}

	_, err := service.CreateCard(context.Background(), CreateCardRequest{PoolID: poolID, Provider: provider.ProviderName(), IdempotencyKey: "manual-terminal-create"})
	if !errors.Is(err, ErrCardConsumed) {
		t.Fatalf("manual create terminal provider card error = %v, want ErrCardConsumed", err)
	}
	var stored models.PaymentCard
	if err := database.First(&stored, "id = ?", cardID).Error; err != nil {
		t.Fatalf("read terminal card: %v", err)
	}
	if stored.Status != string(CardUsed) || stored.UsageCount != 1 || stored.InUse {
		t.Fatalf("manual create resurrected terminal card: %+v", stored)
	}
	if provider.cancels.Load() != 0 {
		t.Fatalf("manual reconciliation conflict unexpectedly cancelled existing card %d times", provider.cancels.Load())
	}
}

var _ CardProvider = (*lifecycleIntegrationProvider)(nil)
