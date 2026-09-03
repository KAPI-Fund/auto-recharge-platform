package cardpool

import (
	"context"
	"net/http"
	"time"

	"gorm.io/gorm"
)

type ConfigReader interface {
	Value(key, fallback string) string
	Secret(key, fallback string) string
}

// ConfigValidator is implemented by providers that can validate their
// startup configuration without making a network request. This keeps missing
// credentials from surfacing only after a customer starts a payment.
type ConfigValidator interface {
	ValidateConfiguration() error
}

type CardProvider interface {
	ProviderName() string
	HealthCheck(ctx context.Context) (ProviderHealth, error)
	AcquireCard(ctx context.Context, request AcquireCardRequest) (PaymentCard, error)
	CreateCard(ctx context.Context, request CreateCardRequest) (PaymentCard, error)
	GetCard(ctx context.Context, providerCardID string) (PaymentCard, error)
	GetSensitiveCardDetails(ctx context.Context, providerCardID string) (SensitiveCardDetails, error)
	FreezeCard(ctx context.Context, providerCardID string) error
	UnfreezeCard(ctx context.Context, providerCardID string) error
	CancelCard(ctx context.Context, providerCardID string) error
	UpdateLimits(ctx context.Context, providerCardID string, limits CardLimits) error
	GetTransactions(ctx context.Context, providerCardID string) ([]CardTransaction, error)
	Supports(capability Capability) bool
}

// ReservationProvider is an optional extension for providers that maintain a
// local reservation lock. It keeps reservation mechanics out of business code.
type ReservationProvider interface {
	ReleaseReservation(ctx context.Context, providerCardID string) error
	RecordUsage(ctx context.Context, providerCardID string) (UsageStats, error)
}

// AllocationReservationProvider is the allocation-aware extension used by
// local providers. The legacy ReservationProvider methods remain available so
// older adapters can still be registered without changing the core contract.
type AllocationReservationProvider interface {
	ReleaseReservationForAllocation(ctx context.Context, providerCardID, allocationID string) error
	RecordUsageForAllocation(ctx context.Context, providerCardID, allocationID string) (UsageStats, error)
}

// TransactionalAllocationReservationProvider lets a provider update its
// local reservation/accounting row on the same database transaction that marks
// the normalized allocation as used. This is optional because remote issuing
// providers generally do not maintain a local usage counter.
type TransactionalAllocationReservationProvider interface {
	RecordUsageForAllocationTx(ctx context.Context, tx *gorm.DB, providerCardID, allocationID string) (UsageStats, error)
}

type WebhookProvider interface {
	ParseWebhook(ctx context.Context, headers http.Header, body []byte) (InternalEvent, error)
}

type InternalEvent struct {
	ProviderEventID string
	EventType       string
	ProviderCardID  string
	ProviderTxnID   string
	ProviderStatus  string
	Status          InternalCardStatus
	Transaction     *CardTransaction
	OccurredAt      *time.Time
	Ignored         bool
}

type WebhookResult struct {
	Provider         string
	ProviderEventID  string
	EventType        string
	Duplicate        bool
	Ignored          bool
	CardUpdated      bool
	TransactionSaved bool
}
