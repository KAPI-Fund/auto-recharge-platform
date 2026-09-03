package cardpool

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/db"
	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

type webhookIntegrationProvider struct {
	providerCardID string
}

func (webhookIntegrationProvider) ProviderName() string { return "TEST_WEBHOOK" }
func (webhookIntegrationProvider) HealthCheck(context.Context) (ProviderHealth, error) {
	return ProviderHealth{Provider: "TEST_WEBHOOK", Available: true, Status: "healthy"}, nil
}
func (webhookIntegrationProvider) AcquireCard(context.Context, AcquireCardRequest) (PaymentCard, error) {
	return PaymentCard{}, ErrUnsupportedCapability
}
func (webhookIntegrationProvider) CreateCard(context.Context, CreateCardRequest) (PaymentCard, error) {
	return PaymentCard{}, ErrUnsupportedCapability
}
func (webhookIntegrationProvider) GetCard(context.Context, string) (PaymentCard, error) {
	return PaymentCard{}, ErrCardNotFound
}
func (webhookIntegrationProvider) GetSensitiveCardDetails(context.Context, string) (SensitiveCardDetails, error) {
	return SensitiveCardDetails{}, ErrUnsupportedCapability
}
func (webhookIntegrationProvider) FreezeCard(context.Context, string) error {
	return ErrUnsupportedCapability
}
func (webhookIntegrationProvider) UnfreezeCard(context.Context, string) error {
	return ErrUnsupportedCapability
}
func (webhookIntegrationProvider) CancelCard(context.Context, string) error {
	return ErrUnsupportedCapability
}
func (webhookIntegrationProvider) UpdateLimits(context.Context, string, CardLimits) error {
	return ErrUnsupportedCapability
}
func (webhookIntegrationProvider) GetTransactions(context.Context, string) ([]CardTransaction, error) {
	return nil, ErrUnsupportedCapability
}
func (webhookIntegrationProvider) Supports(capability Capability) bool {
	return capability == CapabilityWebhook
}
func (p webhookIntegrationProvider) ParseWebhook(context.Context, http.Header, []byte) (InternalEvent, error) {
	occurred := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	return InternalEvent{
		ProviderEventID: "evt-webhook-1", EventType: "card.updated", ProviderCardID: p.providerCardID,
		ProviderStatus: "inactive", Status: CardFrozen, OccurredAt: &occurred,
		Transaction: &CardTransaction{ProviderTransactionID: "txn-webhook-1", ProviderCardID: p.providerCardID, Amount: 10, Currency: "USD", Status: "PENDING", Type: "card_payment", OccurredAt: &occurred},
	}, nil
}

func TestServiceProcessWebhookIsIdempotentAndUpdatesInternalState(t *testing.T) {
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

	cardID := db.NewID("webhook_card")
	providerCardID := "remote-card-" + cardID
	if err := database.Create(&models.PaymentCard{ID: cardID, PoolID: "pool_legacy", Provider: "TEST_WEBHOOK", ProviderCardID: providerCardID, UsageType: string(UsageRecurring), Currency: "USD", Status: string(CardActive)}).Error; err != nil {
		t.Fatalf("create test card: %v", err)
	}
	defer func() {
		database.Where("provider = ? AND provider_event_id = ?", "TEST_WEBHOOK", "evt-webhook-1").Delete(&models.CardProviderEvent{})
		database.Where("provider = ? AND provider_transaction_id = ?", "TEST_WEBHOOK", "txn-webhook-1").Delete(&models.CardTransaction{})
		database.Delete(&models.PaymentCard{}, "id = ?", cardID)
	}()

	provider := webhookIntegrationProvider{providerCardID: providerCardID}
	registry := NewProviderRegistry()
	registry.Register(provider)
	service := NewService(database, registry, nil)
	first, err := service.ProcessWebhook(context.Background(), provider.ProviderName(), http.Header{}, []byte(`{"id":"ignored-by-test-parser"}`), "trace-webhook-1")
	if err != nil {
		t.Fatalf("first ProcessWebhook() error = %v", err)
	}
	if first.Duplicate || first.Ignored || !first.CardUpdated || !first.TransactionSaved {
		t.Fatalf("first webhook result = %+v", first)
	}
	second, err := service.ProcessWebhook(context.Background(), provider.ProviderName(), http.Header{}, []byte(`{"id":"ignored-by-test-parser"}`), "trace-webhook-2")
	if err != nil {
		t.Fatalf("duplicate ProcessWebhook() error = %v", err)
	}
	if !second.Duplicate || second.Ignored {
		t.Fatalf("duplicate webhook result = %+v", second)
	}

	var card models.PaymentCard
	if err := database.First(&card, "id = ?", cardID).Error; err != nil {
		t.Fatalf("read card: %v", err)
	}
	if card.Status != string(CardFrozen) || card.ProviderStatus != "inactive" {
		t.Fatalf("card state = %+v", card)
	}
	var eventCount, transactionCount int64
	database.Model(&models.CardProviderEvent{}).Where("provider = ? AND provider_event_id = ?", "TEST_WEBHOOK", "evt-webhook-1").Count(&eventCount)
	database.Model(&models.CardTransaction{}).Where("provider = ? AND provider_transaction_id = ?", "TEST_WEBHOOK", "txn-webhook-1").Count(&transactionCount)
	if eventCount != 1 || transactionCount != 1 {
		t.Fatalf("event/transaction counts = %d/%d, want 1/1", eventCount, transactionCount)
	}
}

var _ CardProvider = webhookIntegrationProvider{}
var _ WebhookProvider = webhookIntegrationProvider{}
