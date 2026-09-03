package cardpool

type Capability string

const (
	CapabilityCreateCard       Capability = "CREATE_CARD"
	CapabilitySensitiveDetails Capability = "SENSITIVE_DETAILS"
	CapabilityFreeze           Capability = "FREEZE"
	CapabilityUnfreeze         Capability = "UNFREEZE"
	CapabilityCancel           Capability = "CANCEL"
	CapabilitySingleUse        Capability = "SINGLE_USE"
	CapabilityMultiUse         Capability = "MULTI_USE"
	CapabilityMerchantLock     Capability = "MERCHANT_LOCK"
	CapabilityTransactionLimit Capability = "TRANSACTION_LIMIT"
	CapabilityCurrencyLimit    Capability = "CURRENCY_LIMIT"
	CapabilityRecurringPayment Capability = "RECURRING_PAYMENT"
	CapabilityWebhook          Capability = "WEBHOOK"
	CapabilityFX               Capability = "FX"
	CapabilityTransactionQuery Capability = "TRANSACTION_QUERY"
)
