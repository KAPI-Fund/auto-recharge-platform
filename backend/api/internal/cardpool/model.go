package cardpool

import "time"

type PoolType string

const (
	PoolTypeLocal       PoolType = "LOCAL"
	PoolTypeExternalAPI PoolType = "EXTERNAL_API"
)

type RoutingStrategy string

const (
	RoutingFixed    RoutingStrategy = "FIXED"
	RoutingPriority RoutingStrategy = "PRIORITY"
	RoutingWeighted RoutingStrategy = "WEIGHTED"
	RoutingFailover RoutingStrategy = "FAILOVER"
)

// CardCreationMode controls whether a pool may create a provider card while
// acquiring one for a payment task. POOL_ONLY preserves the legacy behavior
// of consuming a card that already exists in the normalized pool. The
// CREATE_ON_DEMAND mode is useful for Provider APIs that are intended to issue
// a fresh virtual card for each one-time payment.
type CardCreationMode string

const (
	CardCreationPoolOnly CardCreationMode = "POOL_ONLY"
	CardCreationOnDemand CardCreationMode = "CREATE_ON_DEMAND"
)

const (
	ProvisionCreating  = "CREATING"
	ProvisionSucceeded = "SUCCEEDED"
	ProvisionFailed    = "FAILED"
)

type UsageType string

const (
	UsageOneTime   UsageType = "ONE_TIME"
	UsageRecurring UsageType = "RECURRING"
	UsageMultiUse  UsageType = "MULTI_USE"
)

type InternalCardStatus string

const (
	CardCreating  InternalCardStatus = "CREATING"
	CardActive    InternalCardStatus = "ACTIVE"
	CardAssigned  InternalCardStatus = "ASSIGNED"
	CardInUse     InternalCardStatus = "IN_USE"
	CardUsed      InternalCardStatus = "USED"
	CardFrozen    InternalCardStatus = "FROZEN"
	CardCancelled InternalCardStatus = "CANCELLED"
	CardFailed    InternalCardStatus = "FAILED"
)

type AllocationStatus string

const (
	AllocationCreating AllocationStatus = "CREATING"
	AllocationAssigned AllocationStatus = "ASSIGNED"
	AllocationInUse    AllocationStatus = "IN_USE"
	AllocationReleased AllocationStatus = "RELEASED"
	AllocationFailed   AllocationStatus = "FAILED"
)

const (
	ProviderReleaseActionReservation = "RELEASE_RESERVATION"
	ProviderReleaseActionCancel      = "CANCEL"
)

type CreateCardRequest struct {
	PoolID            string
	Provider          string
	BusinessAccountID string
	PaymentTaskID     string
	UsageType         UsageType
	Amount            float64
	Currency          string
	IdempotencyKey    string
	CardholderName    string
	Metadata          map[string]string
}

type AcquireCardRequest struct {
	PoolID            string
	BusinessAccountID string
	PaymentTaskID     string
	AllocationID      string
	UsageType         UsageType
	Amount            float64
	Currency          string
	IdempotencyKey    string
	OwnerKey          string
}

// PaymentCard intentionally contains metadata only. PAN/CVC are returned from
// SensitiveCardDetails and are never part of regular list/get responses.
type PaymentCard struct {
	InternalCardID     string             `json:"internalCardId"`
	PoolID             string             `json:"poolId"`
	Provider           string             `json:"provider"`
	ProviderCardID     string             `json:"providerCardId"`
	LocalCardAssetID   string             `json:"localCardAssetId"`
	BusinessAccountID  string             `json:"businessAccountId"`
	Last4              string             `json:"last4"`
	CardholderName     string             `json:"cardholderName"`
	CardType           string             `json:"cardType"`
	UsageType          UsageType          `json:"usageType"`
	Currency           string             `json:"currency"`
	Status             InternalCardStatus `json:"status"`
	ProviderStatus     string             `json:"providerStatus"`
	InUse              bool               `json:"inUse"`
	UsageCount         int                `json:"usageCount"`
	DailyUsageCount    int                `json:"dailyUsageCount"`
	CooldownUntil      *time.Time         `json:"cooldownUntil,omitempty"`
	LastUsedAt         *time.Time         `json:"lastUsedAt,omitempty"`
	LastAttemptTaskID  string             `json:"lastAttemptTaskId,omitempty"`
	LastFailureCode    string             `json:"lastFailureCode,omitempty"`
	LastFailureMessage string             `json:"lastFailureMessage,omitempty"`
	LastFailureAt      *time.Time         `json:"lastFailureAt,omitempty"`
	CreatedAt          time.Time          `json:"createdAt"`
	UpdatedAt          time.Time          `json:"updatedAt"`
}

type SensitiveCardDetails struct {
	CardNumber     string
	ExpiryMonth    int
	ExpiryYear     int
	CVC            string
	CardholderName string
}

type CardLimits struct {
	Currency       string
	PerTransaction float64
	VelocityAmount float64
	Weekly         float64
	Daily          float64
	Monthly        float64
	AllTime        float64
}

type CardTransaction struct {
	ProviderTransactionID string
	ProviderCardID        string
	Amount                float64
	Currency              string
	Status                string
	Type                  string
	MerchantName          string
	FailureCode           string
	OccurredAt            *time.Time
}

type ProviderHealth struct {
	Provider           string
	Available          bool
	Status             string
	LastSuccessAt      *time.Time
	LastFailureAt      *time.Time
	ConsecutiveFailure int
	RateLimitUntil     *time.Time
}

type AllocationResult struct {
	Card         PaymentCard
	AllocationID string
	Provider     string
}

type UsageStats struct {
	DailyUsageCount int
	CooledDown      bool
}
