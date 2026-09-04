package models

import "time"

const (
	PlatformStoreCurrency = "CNY"
	// DefaultStoreSaleLimit is used when upgrading legacy published products
	// that previously used sale_limit=0 to mean unlimited inventory.
	DefaultStoreSaleLimit = 100

	CDKAvailable   = "available"
	CDKProcessing  = "processing"
	CDKUsed        = "used"
	CDKDisabled    = "disabled"
	CDKTypeSelf    = "自助"
	CDKTypeProduct = "成品"

	TaskQueued    = "queued"
	TaskRunning   = "running"
	TaskSucceeded = "succeeded"
	TaskFailed    = "failed"
	TaskManual    = "manual"

	ProductGenerationQueued    = "queued"
	ProductGenerationRunning   = "running"
	ProductGenerationSucceeded = "succeeded"
	ProductGenerationFailed    = "failed"
)

type Plan struct {
	ID               string  `gorm:"primaryKey;size:64" json:"id"`
	Code             string  `gorm:"uniqueIndex;size:64;not null" json:"code"`
	Name             string  `gorm:"size:120;not null" json:"name"`
	Description      string  `gorm:"size:500" json:"description"`
	ProviderPlanName string  `gorm:"size:120" json:"providerPlanName"`
	Country          string  `gorm:"size:4;not null;default:US" json:"country"`
	Currency         string  `gorm:"size:8;not null;default:CNY" json:"currency"`
	Price            float64 `json:"price"`
	Active           bool    `gorm:"not null;default:true" json:"active"`
	SortOrder        int     `gorm:"not null;default:0" json:"sortOrder"`
	// SaleLimit is the total number of storefront units that may be sold.
	// Published products must have a positive limit. SoldCount is increased
	// only when a storefront order is successfully fulfilled, never when a
	// pending checkout is made.
	SaleLimit int `gorm:"not null;default:100" json:"saleLimit"`
	SoldCount int `gorm:"not null;default:0" json:"soldCount"`
	// These fields are computed by the API projection and are not persisted.
	RemainingQuantity *int      `gorm:"-" json:"remainingQuantity"`
	SoldOut           bool      `gorm:"-" json:"soldOut"`
	PurchaseEnabled   bool      `gorm:"-" json:"purchaseEnabled"`
	AvailabilityLabel string    `gorm:"-" json:"availabilityLabel"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type StoreOrder struct {
	ID               string     `gorm:"primaryKey;size:64" json:"id"`
	OrderNo          string     `gorm:"uniqueIndex;size:64;not null" json:"orderNo"`
	TraceID          string     `gorm:"index;size:96;not null" json:"traceId"`
	PlanID           string     `gorm:"index;size:64;not null" json:"planId"`
	Email            string     `gorm:"index;size:255" json:"email"`
	PhoneCountryCode string     `gorm:"index;size:8" json:"phoneCountryCode"`
	PhoneNumber      string     `gorm:"size:32" json:"phoneNumber"`
	PhoneE164        string     `gorm:"index;size:24" json:"phoneE164"`
	Status           string     `gorm:"index;size:24;not null" json:"status"`
	Amount           float64    `json:"amount"`
	Currency         string     `gorm:"size:8;not null" json:"currency"`
	StripeSessionID  string     `gorm:"index;size:160" json:"stripeSessionId"`
	StripePaymentID  string     `gorm:"size:160" json:"stripePaymentId"`
	CDKID            string     `gorm:"index;size:64" json:"cdkId"`
	CDKCode          string     `gorm:"size:64" json:"cdkCode"`
	FailureReason    string     `gorm:"size:500" json:"failureReason"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	PaidAt           *time.Time `json:"paidAt"`
	Plan             Plan       `gorm:"foreignKey:PlanID" json:"plan"`
}

// StorePaymentEvent is the Stripe provider-event ledger for storefront orders.
// A unique provider event id makes webhook retries safe while keeping the raw
// payload available for trace and payment troubleshooting.
type StorePaymentEvent struct {
	ID              string     `gorm:"primaryKey;size:64" json:"id"`
	Provider        string     `gorm:"uniqueIndex:ux_store_payment_events_provider_event;size:32;not null" json:"provider"`
	ProviderEventID string     `gorm:"uniqueIndex:ux_store_payment_events_provider_event;size:255;not null" json:"providerEventId"`
	OrderID         string     `gorm:"index;size:64" json:"orderId"`
	OrderNo         string     `gorm:"index;size:64" json:"orderNo"`
	TraceID         string     `gorm:"index;size:96" json:"traceId"`
	EventType       string     `gorm:"size:80;not null" json:"eventType"`
	Status          string     `gorm:"size:24;not null" json:"status"`
	ErrorMessage    string     `gorm:"size:500" json:"errorMessage"`
	Payload         string     `gorm:"type:text" json:"-"`
	ProcessedAt     *time.Time `json:"processedAt"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// EmailDelivery is the durable idempotency record for customer notifications.
// The event key is stable across Stripe retries and repeated Worker heartbeats.
type EmailDelivery struct {
	ID           string     `gorm:"primaryKey;size:64" json:"id"`
	EventKey     string     `gorm:"uniqueIndex;size:180;not null" json:"eventKey"`
	EventType    string     `gorm:"index;size:40;not null" json:"eventType"`
	OrderID      string     `gorm:"index;size:64" json:"orderId"`
	TaskID       string     `gorm:"index;size:64" json:"taskId"`
	To           string     `gorm:"size:255;not null" json:"to"`
	TraceID      string     `gorm:"index;size:96;not null" json:"traceId"`
	Status       string     `gorm:"index;size:24;not null" json:"status"`
	AttemptCount int        `gorm:"not null;default:0" json:"attemptCount"`
	ErrorMessage string     `gorm:"size:1000" json:"errorMessage"`
	SentAt       *time.Time `json:"sentAt"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

type CDK struct {
	ID            string     `gorm:"primaryKey;size:64" json:"id"`
	Code          string     `gorm:"uniqueIndex;size:64;not null" json:"code"`
	PlanID        string     `gorm:"index;size:64;not null" json:"planId"`
	PlanType      string     `gorm:"index;size:24;not null;default:plus" json:"planType"`
	Type          string     `gorm:"index;size:24;not null;default:自助" json:"type"`
	Status        string     `gorm:"index;size:24;not null" json:"status"`
	ShippedAt     *time.Time `json:"shippedAt"`
	UsedByTaskID  string     `gorm:"index;size:64" json:"usedByTaskId"`
	UsedAt        *time.Time `json:"usedAt"`
	FailCount     int        `gorm:"not null;default:0" json:"failCount"`
	CooldownUntil *time.Time `json:"cooldownUntil"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	Plan          Plan       `gorm:"foreignKey:PlanID" json:"plan"`
}

type RechargeTask struct {
	ID      string `gorm:"primaryKey;size:64" json:"id"`
	JobKey  string `gorm:"uniqueIndex;size:96;not null" json:"jobKey"`
	TraceID string `gorm:"index;size:96;not null;default:''" json:"traceId"`
	// CDKID is nil for admin-only checkout debugging tasks. Customer
	// redemption tasks keep a concrete CDK foreign-key value.
	CDKID  *string `gorm:"index;size:64" json:"cdkId"`
	PlanID string  `gorm:"index;size:64;not null" json:"planId"`
	PoolID string  `gorm:"index;size:64" json:"poolId"`
	// PaymentRegion is captured when the task is created. It prevents a later
	// global payment-region change from changing the checkout country/currency
	// of an already queued task. Empty values are retained for legacy rows and
	// resolved from the linked plan at read time.
	PaymentRegion      string     `gorm:"index;size:4" json:"paymentRegion"`
	BusinessAccountID  string     `gorm:"index;size:160" json:"businessAccountId"`
	UsageType          string     `gorm:"index;size:24" json:"usageType"`
	PaymentCardID      string     `gorm:"index;size:64" json:"paymentCardId"`
	CardAllocationID   string     `gorm:"index;size:64" json:"cardAllocationId"`
	CardProvider       string     `gorm:"index;size:40" json:"cardProvider"`
	CardProviderCardID string     `gorm:"size:160" json:"cardProviderCardId"`
	CardFailureCode    string     `gorm:"size:80" json:"cardFailureCode"`
	CardFailureMessage string     `gorm:"size:500" json:"cardFailureMessage"`
	CardFailureAt      *time.Time `json:"cardFailureAt"`
	Mode               string     `gorm:"size:24;not null" json:"mode"`
	TokenPreview       string     `gorm:"size:160" json:"tokenPreview"`
	SessionPreview     string     `gorm:"size:160" json:"sessionPreview"`
	SessionPayload     string     `gorm:"type:text" json:"-"`
	SessionCiphertext  string     `gorm:"type:text" json:"-"`
	// PlanNameOverride preserves the optional plan_name override used by the
	// original checkout debug form. An empty value means use the canonical
	// plan_type mapping from the linked Plan.
	PlanNameOverride   string     `gorm:"size:120" json:"-"`
	CDKCode            string     `gorm:"index;size:64" json:"cdkCode"`
	Phone              string     `gorm:"size:32" json:"phone"`
	CardLast4          string     `gorm:"size:4" json:"cardLast4"`
	Status             string     `gorm:"index;size:24;not null" json:"status"`
	Progress           int        `gorm:"not null;default:0" json:"progress"`
	Message            string     `gorm:"size:500" json:"message"`
	DisplayTime        string     `gorm:"size:64" json:"displayTime"`
	RawOutput          string     `gorm:"type:text" json:"rawOutput"`
	FailureScreenshots string     `gorm:"type:text" json:"failureScreenshots"`
	UpstreamOrderID    string     `gorm:"size:160" json:"upstreamOrderId"`
	GPTAPIOrderID      string     `gorm:"size:160" json:"gptApiOrderId"`
	GPTAPITaskID       string     `gorm:"size:160" json:"gptApiTaskId"`
	GPTAPIRaw          string     `gorm:"type:text" json:"gptApiRaw"`
	GPTAPITopupCode    string     `gorm:"size:160" json:"gptApiTopupCode"`
	WorkerID           string     `gorm:"size:120" json:"workerId"`
	ClientIP           string     `gorm:"index;size:45" json:"clientIp"`
	Attempt            int        `gorm:"not null;default:0" json:"attempt"`
	ErrorCode          string     `gorm:"size:80" json:"errorCode"`
	ErrorMessage       string     `gorm:"size:500" json:"errorMessage"`
	WorkerLeaseToken   string     `gorm:"index;size:96" json:"-"`
	AdmissionToken     string     `gorm:"index;size:96" json:"-"`
	HeartbeatAt        *time.Time `gorm:"index" json:"-"`
	LeaseExpiresAt     *time.Time `gorm:"index" json:"-"`
	QueueDeadlineAt    *time.Time `gorm:"index" json:"-"`
	StartedAt          *time.Time `json:"startedAt"`
	FinishedAt         *time.Time `json:"finishedAt"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	CDK                CDK        `gorm:"foreignKey:CDKID" json:"cdk"`
	Plan               Plan       `gorm:"foreignKey:PlanID" json:"plan"`
}

// ProductGenerationTask tracks the admin-only account production batch. It is
// separate from RechargeTask because production has no customer CDK or
// session payload; the worker receives only this task ID through Redis.
type ProductGenerationTask struct {
	ID                string     `gorm:"primaryKey;size:64" json:"id"`
	JobKey            string     `gorm:"uniqueIndex;size:96;not null" json:"jobKey"`
	TraceID           string     `gorm:"index;size:96;not null;default:''" json:"traceId"`
	TargetCount       int        `gorm:"not null" json:"targetCount"`
	CompletedCount    int        `gorm:"not null;default:0" json:"completedCount"`
	SuccessCount      int        `gorm:"not null;default:0" json:"successCount"`
	FailedCount       int        `gorm:"not null;default:0" json:"failedCount"`
	WorkerCount       int        `gorm:"not null;default:1" json:"workerCount"`
	Status            string     `gorm:"index;size:24;not null" json:"status"`
	Progress          int        `gorm:"not null;default:0" json:"progress"`
	Message           string     `gorm:"size:500" json:"message"`
	RawOutput         string     `gorm:"type:text" json:"rawOutput"`
	Aborted           bool       `gorm:"index;not null;default:false" json:"aborted"`
	ResumedFromJobKey string     `gorm:"size:96" json:"resumedFromJobKey"`
	StartedAt         *time.Time `json:"startedAt"`
	FinishedAt        *time.Time `json:"finishedAt"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type PhoneAsset struct {
	ID         string     `gorm:"primaryKey;size:64" json:"id"`
	Phone      string     `gorm:"uniqueIndex;size:32;not null" json:"phone"`
	APIKey     string     `gorm:"type:text" json:"-"`
	UsageCount int        `gorm:"not null;default:0" json:"usageCount"`
	SortOrder  int        `gorm:"not null;default:0" json:"sortOrder"`
	Active     bool       `gorm:"index;not null;default:true" json:"active"`
	Status     string     `gorm:"size:32;not null;default:正常" json:"status"`
	InUse      bool       `gorm:"index;not null;default:false" json:"inUse"`
	LockedAt   *time.Time `json:"lockedAt"`
	LockedBy   string     `gorm:"size:96" json:"lockedBy"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

type CardAsset struct {
	ID                   string     `gorm:"primaryKey;size:64" json:"id"`
	PoolID               string     `gorm:"index;size:64" json:"poolId"`
	Provider             string     `gorm:"index;size:40;not null;default:LOCAL_TEXT" json:"provider"`
	Last4                string     `gorm:"size:4;index;not null" json:"last4"`
	CardNumberCiphertext string     `gorm:"type:text;not null" json:"-"`
	ExpiryCiphertext     string     `gorm:"type:text" json:"-"`
	CVVCiphertext        string     `gorm:"type:text" json:"-"`
	Holder               string     `gorm:"size:120" json:"holder"`
	PaymentHolderName    string     `gorm:"size:120" json:"paymentHolderName"`
	PaymentAddressLine1  string     `gorm:"size:200" json:"paymentAddressLine1"`
	PaymentAddressCity   string     `gorm:"size:100" json:"paymentAddressCity"`
	PaymentAddressState  string     `gorm:"size:100" json:"paymentAddressState"`
	PaymentAddressPostal string     `gorm:"size:20" json:"paymentAddressPostal"`
	PaymentAddressID     string     `gorm:"size:96" json:"paymentAddressId"`
	SortOrder            int        `gorm:"not null;default:0" json:"sortOrder"`
	Status               string     `gorm:"index;size:32;not null" json:"status"`
	Active               bool       `gorm:"index;not null;default:true" json:"active"`
	InUse                bool       `gorm:"index;not null;default:false" json:"inUse"`
	LockedAt             *time.Time `json:"lockedAt"`
	LockedBy             string     `gorm:"size:96" json:"lockedBy"`
	UsageCount           int        `gorm:"not null;default:0" json:"usageCount"`
	DailyUsageCount      int        `gorm:"not null;default:0" json:"dailyUsageCount"`
	DailyUsageResetAt    *time.Time `json:"dailyUsageResetAt"`
	LastUsedAt           *time.Time `json:"lastUsedAt"`
	CooldownUntil        *time.Time `json:"cooldownUntil"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

// CardPool is the business-facing card source. A pool owns routing policy,
// while the provider rows below decide which adapters may serve new work.
// Existing local text cards remain in CardAsset and are linked through PoolID.
type CardPool struct {
	ID               string             `gorm:"primaryKey;size:64" json:"id"`
	Name             string             `gorm:"uniqueIndex;size:96;not null" json:"name"`
	Type             string             `gorm:"size:24;not null" json:"type"`
	UsageType        string             `gorm:"size:24;not null" json:"usageType"`
	Currency         string             `gorm:"size:8" json:"currency"`
	FundingCurrency  string             `gorm:"size:8" json:"fundingCurrency"`
	MerchantCurrency string             `gorm:"size:8" json:"merchantCurrency"`
	RoutingStrategy  string             `gorm:"size:24;not null" json:"routingStrategy"`
	DefaultProvider  string             `gorm:"size:40" json:"defaultProvider"`
	CardCreationMode string             `gorm:"size:32;not null;default:POOL_ONLY" json:"cardCreationMode"`
	Enabled          bool               `gorm:"index;not null;default:true" json:"enabled"`
	CreatedAt        time.Time          `json:"createdAt"`
	UpdatedAt        time.Time          `json:"updatedAt"`
	Providers        []CardPoolProvider `gorm:"foreignKey:PoolID" json:"providers"`
}

type CardPoolProvider struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	PoolID    string    `gorm:"uniqueIndex:ux_card_pool_provider;size:64;not null" json:"poolId"`
	Provider  string    `gorm:"uniqueIndex:ux_card_pool_provider;size:40;not null" json:"provider"`
	Enabled   bool      `gorm:"index;not null;default:true" json:"enabled"`
	Priority  int       `gorm:"not null;default:1" json:"priority"`
	Weight    int       `gorm:"not null;default:100" json:"weight"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// PaymentCard is the provider-neutral card metadata used by business code.
// PAN/CVC are deliberately absent. A provider loads sensitive details only
// through GetSensitiveCardDetails immediately before the payment attempt.
type PaymentCard struct {
	ID                 string     `gorm:"primaryKey;size:64" json:"id"`
	PoolID             string     `gorm:"index;size:64;not null" json:"poolId"`
	Provider           string     `gorm:"index;uniqueIndex:ux_payment_cards_provider_card;size:40;not null" json:"provider"`
	ProviderCardID     string     `gorm:"uniqueIndex:ux_payment_cards_provider_card;size:160;not null" json:"providerCardId"`
	LocalCardAssetID   string     `gorm:"index;size:64" json:"localCardAssetId"`
	BusinessAccountID  string     `gorm:"index;size:160" json:"businessAccountId"`
	Last4              string     `gorm:"size:4;index" json:"last4"`
	CardholderName     string     `gorm:"size:120" json:"cardholderName"`
	CardType           string     `gorm:"size:32" json:"cardType"`
	UsageType          string     `gorm:"index;size:24;not null" json:"usageType"`
	Currency           string     `gorm:"size:8" json:"currency"`
	Status             string     `gorm:"index;size:24;not null" json:"status"`
	ProviderStatus     string     `gorm:"size:64" json:"providerStatus"`
	ProviderStatusAt   *time.Time `json:"providerStatusAt"`
	InUse              bool       `gorm:"index;not null;default:false" json:"inUse"`
	UsageCount         int        `gorm:"not null;default:0" json:"usageCount"`
	DailyUsageCount    int        `gorm:"not null;default:0" json:"dailyUsageCount"`
	DailyUsageResetAt  *time.Time `json:"dailyUsageResetAt"`
	LastUsedAt         *time.Time `json:"lastUsedAt"`
	CooldownUntil      *time.Time `json:"cooldownUntil"`
	LastAttemptTaskID  string     `gorm:"index;size:64" json:"lastAttemptTaskId"`
	LastFailureCode    string     `gorm:"size:80" json:"lastFailureCode"`
	LastFailureMessage string     `gorm:"size:500" json:"lastFailureMessage"`
	LastFailureAt      *time.Time `json:"lastFailureAt"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

// CardProvisionRequest makes manual/provider card creation idempotent. A
// provider call can time out after the remote card was created; retaining the
// request row lets a retry return the already persisted normalized card
// instead of issuing another card.
type CardProvisionRequest struct {
	ID             string    `gorm:"primaryKey;size:64" json:"id"`
	IdempotencyKey string    `gorm:"uniqueIndex;size:180;not null" json:"idempotencyKey"`
	PoolID         string    `gorm:"index;size:64;not null" json:"poolId"`
	Provider       string    `gorm:"index;size:40;not null" json:"provider"`
	PaymentCardID  string    `gorm:"index;size:64" json:"paymentCardId"`
	Status         string    `gorm:"index;size:24;not null" json:"status"`
	FailureCode    string    `gorm:"size:80" json:"failureCode"`
	FailureMessage string    `gorm:"size:500" json:"failureMessage"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type CardAllocation struct {
	ID                     string     `gorm:"primaryKey;size:64" json:"id"`
	IdempotencyKey         string     `gorm:"uniqueIndex;size:180;not null" json:"idempotencyKey"`
	PaymentTaskID          string     `gorm:"index;size:64" json:"paymentTaskId"`
	PaymentCardID          string     `gorm:"index;size:64" json:"paymentCardId"`
	PoolID                 string     `gorm:"index;size:64;not null" json:"poolId"`
	Provider               string     `gorm:"index;size:40;not null" json:"provider"`
	ProviderCardID         string     `gorm:"size:160" json:"providerCardId"`
	BusinessAccountID      string     `gorm:"index;size:160" json:"businessAccountId"`
	UsageType              string     `gorm:"index;size:24;not null" json:"usageType"`
	OwnerKey               string     `gorm:"size:180" json:"ownerKey"`
	Status                 string     `gorm:"index;size:24;not null" json:"status"`
	FailureCode            string     `gorm:"size:80" json:"failureCode"`
	FailureMessage         string     `gorm:"size:500" json:"failureMessage"`
	AllocatedAt            time.Time  `json:"allocatedAt"`
	ReleasedAt             *time.Time `json:"releasedAt"`
	UsageRecordedAt        *time.Time `json:"usageRecordedAt"`
	ProviderReleasedAt     *time.Time `json:"providerReleasedAt,omitempty"`
	ProviderReleaseError   string     `gorm:"size:500" json:"-"`
	ProviderReleasePending bool       `gorm:"index;not null;default:false" json:"-"`
	ProviderReleaseAction  string     `gorm:"size:32" json:"-"`
	// Provider release is performed outside the database transaction because it
	// is an external API call. These fields form a short-lived database claim so
	// duplicate Worker callbacks/recovery loops cannot call CancelCard twice at
	// the same time. They are deliberately hidden from API responses.
	ProviderReleaseClaimToken string     `gorm:"size:96" json:"-"`
	ProviderReleaseClaimedAt  *time.Time `gorm:"index" json:"-"`
	CreatedAt                 time.Time  `json:"createdAt"`
	UpdatedAt                 time.Time  `json:"updatedAt"`
}

type CardProviderEvent struct {
	ID              string     `gorm:"primaryKey;size:64" json:"id"`
	Provider        string     `gorm:"uniqueIndex:ux_card_provider_event;size:40;not null" json:"provider"`
	ProviderEventID string     `gorm:"uniqueIndex:ux_card_provider_event;size:180;not null" json:"providerEventId"`
	EventType       string     `gorm:"index;size:100;not null" json:"eventType"`
	PaymentCardID   string     `gorm:"index;size:64" json:"paymentCardId"`
	ProviderCardID  string     `gorm:"index;size:160" json:"providerCardId"`
	Status          string     `gorm:"size:24;not null" json:"status"`
	TraceID         string     `gorm:"index;size:96" json:"traceId"`
	Payload         string     `gorm:"type:text" json:"-"`
	OccurredAt      *time.Time `json:"occurredAt"`
	ProcessedAt     *time.Time `json:"processedAt"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type CardTransaction struct {
	ID                    string     `gorm:"primaryKey;size:64" json:"id"`
	Provider              string     `gorm:"uniqueIndex:ux_card_transactions_provider_id;size:40;not null" json:"provider"`
	ProviderTransactionID string     `gorm:"uniqueIndex:ux_card_transactions_provider_id;size:180;not null" json:"providerTransactionId"`
	PaymentCardID         string     `gorm:"index;size:64" json:"paymentCardId"`
	ProviderCardID        string     `gorm:"index;size:160" json:"providerCardId"`
	Amount                float64    `json:"amount"`
	Currency              string     `gorm:"size:8" json:"currency"`
	Status                string     `gorm:"index;size:32" json:"status"`
	Type                  string     `gorm:"size:32" json:"type"`
	MerchantName          string     `gorm:"size:255" json:"merchantName"`
	FailureCode           string     `gorm:"size:80" json:"failureCode"`
	OccurredAt            *time.Time `json:"occurredAt"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

type BillingRecord struct {
	ID               string    `gorm:"primaryKey;size:64" json:"id"`
	TaskID           string    `gorm:"index;size:64" json:"taskId"`
	TraceID          string    `gorm:"index;size:96" json:"traceId"`
	PaymentCardID    string    `gorm:"index;size:64" json:"paymentCardId"`
	CardAllocationID string    `gorm:"index;size:64" json:"cardAllocationId"`
	Provider         string    `gorm:"index;size:40" json:"provider"`
	PoolID           string    `gorm:"index;size:64" json:"poolId"`
	PaymentTime      time.Time `gorm:"index" json:"paymentTime"`
	CardLast4        string    `gorm:"index;size:4" json:"cardLast4"`
	// Card PAN is intentionally no longer part of the billing record. Keep the
	// Go field ignored so old callers compile while the legacy column can be
	// purged during migration without being selected or written again.
	CardNumber      string    `gorm:"-" json:"-"`
	Amount          float64   `json:"amount"`
	Currency        string    `gorm:"size:8" json:"currency"`
	PlanID          string    `gorm:"index;size:64" json:"planId"`
	PlanType        string    `gorm:"index;size:24" json:"planType"`
	StripeSessionID string    `gorm:"size:160" json:"stripeSessionId"`
	CDKCode         string    `gorm:"index;size:64" json:"cdkCode"`
	Email           string    `gorm:"size:255" json:"email"`
	Status          string    `gorm:"index;size:24" json:"status"`
	ErrorCode       string    `gorm:"size:80" json:"errorCode"`
	ErrorMessage    string    `gorm:"size:500" json:"errorMessage"`
	UpstreamOrderID string    `gorm:"size:160" json:"upstreamOrderId"`
	CreatedAt       time.Time `json:"createdAt"`
}

type AppConfig struct {
	Key       string    `gorm:"primaryKey;size:80" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	IsSecret  bool      `gorm:"index;not null;default:false" json:"-"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ActivationAttemptLimit struct {
	ID            string     `gorm:"primaryKey;size:64" json:"id"`
	ScopeType     string     `gorm:"uniqueIndex:ux_attempt_scope;size:24;not null" json:"scopeType"`
	ScopeKey      string     `gorm:"uniqueIndex:ux_attempt_scope;size:160;not null" json:"scopeKey"`
	FailCount     int        `gorm:"not null;default:0" json:"failCount"`
	CooldownUntil *time.Time `json:"cooldownUntil"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

type ProductAsset struct {
	ID         string    `gorm:"primaryKey;size:64" json:"id"`
	Email      string    `gorm:"uniqueIndex;size:255;not null" json:"email"`
	IMAPKey    string    `gorm:"size:64" json:"imapKey"`
	ClaimedCDK string    `gorm:"size:64" json:"claimedCdk"`
	Password   string    `gorm:"type:text" json:"-"`
	Token      string    `gorm:"type:text" json:"-"`
	FilePath   string    `gorm:"size:512" json:"filePath"`
	Status     string    `gorm:"index;size:32;not null;default:正常" json:"status"`
	Active     bool      `gorm:"index;not null;default:true" json:"active"`
	Shipped    bool      `gorm:"not null;default:false" json:"shipped"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type PoolEmail struct {
	ID           string     `gorm:"primaryKey;size:64" json:"id"`
	Email        string     `gorm:"uniqueIndex;size:255;not null" json:"email"`
	Password     string     `gorm:"type:text" json:"-"`
	ClientID     string     `gorm:"size:128" json:"clientId"`
	RefreshToken string     `gorm:"type:text" json:"-"`
	Registered   bool       `gorm:"index;not null;default:false" json:"registered"`
	RegisteredAt *time.Time `json:"registeredAt"`
	InUse        bool       `gorm:"index;not null;default:false" json:"inUse"`
	LockedAt     *time.Time `json:"lockedAt"`
	LockedBy     string     `gorm:"size:96" json:"lockedBy"`
	SortOrder    int        `gorm:"not null;default:0" json:"sortOrder"`
	Active       bool       `gorm:"index;not null;default:true" json:"active"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

type TaxFreeAddress struct {
	ID          string     `gorm:"primaryKey;size:64" json:"id"`
	Region      string     `gorm:"index;size:4;not null" json:"region"`
	Line1       string     `gorm:"size:200;not null" json:"line1"`
	City        string     `gorm:"size:100;not null" json:"city"`
	State       string     `gorm:"size:100;not null" json:"state"`
	PostalCode  string     `gorm:"size:20;not null" json:"postalCode"`
	Country     string     `gorm:"size:2;not null" json:"country"`
	Active      bool       `gorm:"index;not null;default:true" json:"active"`
	BoundCardID string     `gorm:"index;size:64" json:"boundCardId"`
	BoundAt     *time.Time `json:"boundAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type ProxyAsset struct {
	ID               string     `gorm:"primaryKey;size:64" json:"id"`
	ProxyURL         string     `gorm:"type:text;not null" json:"proxyUrl"`
	ProxyURLHash     string     `gorm:"uniqueIndex;size:64;not null" json:"proxyUrlHash"`
	Label            string     `gorm:"size:128" json:"label"`
	Protocol         string     `gorm:"size:16" json:"protocol"`
	Host             string     `gorm:"size:255" json:"host"`
	Active           bool       `gorm:"index;not null;default:true" json:"active"`
	LastCheckAt      *time.Time `json:"lastCheckAt"`
	LastCheckOK      *bool      `json:"lastCheckOk"`
	LastCheckIP      string     `gorm:"size:64" json:"lastCheckIp"`
	LastCheckLatency int        `json:"lastCheckLatencyMs"`
	LastCheckError   string     `gorm:"size:512" json:"lastCheckError"`
	UsageCount       int        `gorm:"not null;default:0" json:"usageCount"`
	SortOrder        int        `gorm:"not null;default:0" json:"sortOrder"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type AdminLoginLog struct {
	ID          string    `gorm:"primaryKey;size:64" json:"id"`
	TraceID     string    `gorm:"index;size:96" json:"traceId"`
	Event       string    `gorm:"index;size:32;not null" json:"event"`
	AdminEmail  string    `gorm:"size:128" json:"adminEmail"`
	IP          string    `gorm:"size:45" json:"ip"`
	UserAgent   string    `gorm:"size:512" json:"userAgent"`
	Fingerprint string    `gorm:"size:128" json:"fingerprint"`
	Detail      string    `gorm:"size:512" json:"detail"`
	CreatedAt   time.Time `json:"createdAt"`
}

type RuntimeLog struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	JobKey    string    `gorm:"index;size:96" json:"jobKey"`
	TraceID   string    `gorm:"index;size:96" json:"traceId"`
	Level     string    `gorm:"size:16;not null" json:"level"`
	Source    string    `gorm:"size:64" json:"source"`
	Text      string    `gorm:"type:text" json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}
